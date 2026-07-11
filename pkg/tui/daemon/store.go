// Package daemon is the lflow server: one process owns the SQLite file and
// every client — the CLI, the editor, later remote apps — speaks the wire
// protocol to it over a unix socket. Being the single writer lets it run WAL,
// serialize transactions in one place, and fan out every committed change to
// subscribers, which is what makes open editors update live.
package daemon

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wire"
	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
)

// changeLog collects what one statement (or transaction) touched, filled in
// by the sqlite update hook while the write runs.
type changeLog struct {
	nodeRowIDs map[int64]bool
	aux        bool // a render-support table (chips, tag_colors, …) changed
}

// collector is the changeLog the update hook currently feeds. One daemon per
// process and Store.mu serializes all SQL, so a single slot suffices; atomic
// because migrations run before the server loop and hooks fire on the sqlite
// thread of whichever goroutine is executing.
var collector atomic.Pointer[changeLog]

var registerOnce sync.Once

const driverName = "sqlite3_lflowd"

func registerDriver() {
	sql.Register(driverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			conn.RegisterUpdateHook(func(op int, dbName, table string, rowid int64) {
				cl := collector.Load()
				if cl == nil {
					return
				}
				switch {
				case table == "nodes":
					cl.nodeRowIDs[rowid] = true
				case wire.AuxTables[table]:
					cl.aux = true
				}
			})
			return nil
		},
	})
}

// Store owns the daemon's single SQLite connection. mu serializes every
// statement; a client transaction holds it from Begin to Commit/Rollback so
// its writes are atomic with respect to every other client. state guards the
// tx fields so exactly one of {owner, watchdog, disconnect} finishes a tx.
type Store struct {
	sqldb *sql.DB
	path  string

	mu sync.Mutex // the write lock: one statement or one open tx at a time

	state   sync.Mutex // guards the four tx fields below
	tx      *sql.Tx
	txOwner *session
	txLog   *changeLog
	txTimer *time.Timer

	seq  atomic.Int64
	subs struct {
		sync.Mutex
		m    map[int64]chan wire.Event
		next int64
	}

	// onEvent, when set, receives every broadcast event (the `lflow serve`
	// log sink). Called outside mu.
	onEvent func(wire.Event)
}

// txMaxAge bounds how long a client transaction may hold the write lock; a
// hung or dead client must not freeze every other client forever.
const txMaxAge = 30 * time.Second

// OpenStore opens the database with the hooked driver, WAL, and a single
// connection. The caller runs migrations through DB() before serving.
func OpenStore(dbPath string) (*Store, error) {
	registerOnce.Do(registerDriver)
	dsn := dbPath
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_busy_timeout=5000&_journal_mode=WAL"

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, errors.Wrap(err, "opening db")
	}
	db.SetMaxOpenConns(1) // the single writer: one sqlite connection, period
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, errors.Wrap(err, "connecting to db")
	}
	s := &Store{sqldb: db, path: dbPath}
	s.subs.m = map[int64]chan wire.Event{}
	return s, nil
}

// DB wraps the store's connection for code that speaks *database.DB
// (migrations, init). Changes made through it fan out like any client's.
func (s *Store) DB() *database.DB {
	return &database.DB{Conn: s.sqldb, Filepath: s.path}
}

func (s *Store) Close() error { return s.sqldb.Close() }

// beginCollect arms the update hook. Callers hold mu.
func (s *Store) beginCollect() *changeLog {
	cl := &changeLog{nodeRowIDs: map[int64]bool{}}
	collector.Store(cl)
	return cl
}

// sessionTx returns the session's open transaction, or nil. The returned tx
// may be finished concurrently by the watchdog; database/sql then returns
// ErrTxDone, which flows back to the client as a plain error.
func (s *Store) sessionTx(sess *session) *sql.Tx {
	s.state.Lock()
	defer s.state.Unlock()
	if s.txOwner == sess {
		return s.tx
	}
	return nil
}

// claimTx atomically takes ownership of the session's tx away from every
// other finisher (owner call, watchdog, disconnect). Exactly one caller gets
// a non-nil result and with it the duty to settle the tx and release mu.
func (s *Store) claimTx(sess *session) (*sql.Tx, *changeLog) {
	s.state.Lock()
	defer s.state.Unlock()
	if s.txOwner != sess || s.tx == nil {
		return nil, nil
	}
	tx, cl := s.tx, s.txLog
	if s.txTimer != nil {
		s.txTimer.Stop()
	}
	s.tx, s.txOwner, s.txLog, s.txTimer = nil, nil, nil, nil
	return tx, cl
}

// Exec runs one autocommit statement for a session, or routes it into the
// session's open transaction.
func (s *Store) Exec(sess *session, query string, args []any) (affected, lastID int64, err error) {
	if tx := s.sessionTx(sess); tx != nil {
		res, err := tx.Exec(query, args...)
		return execResult(res, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cl := s.beginCollect()
	res, err := s.sqldb.Exec(query, args...)
	collector.Store(nil)
	s.broadcast(cl, sess)
	return execResult(res, err)
}

func execResult(res sql.Result, err error) (int64, int64, error) {
	if err != nil {
		return 0, 0, err
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return affected, lastID, nil
}

// Query runs a read for a session (inside its tx when one is open) and
// returns the fully-buffered result. Outlines are small; buffering keeps the
// protocol one-shot.
func (s *Store) Query(sess *session, query string, args []any) (cols []string, rows [][]any, err error) {
	var raw *sql.Rows
	if tx := s.sessionTx(sess); tx != nil {
		raw, err = tx.Query(query, args...)
	} else {
		s.mu.Lock()
		defer s.mu.Unlock()
		raw, err = s.sqldb.Query(query, args...)
	}
	if err != nil {
		return nil, nil, err
	}
	defer raw.Close()

	cols, err = raw.Columns()
	if err != nil {
		return nil, nil, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for raw.Next() {
		if err := raw.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		enc, err := wire.EncodeValues(vals)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, enc)
	}
	return cols, rows, raw.Err()
}

// Begin opens a transaction for the session. It takes mu and keeps it until
// the tx settles — the write lock IS the transaction. A watchdog rolls back
// a transaction its client abandons.
func (s *Store) Begin(sess *session) error {
	if s.sessionTx(sess) != nil {
		return errors.New("transaction already open")
	}
	s.mu.Lock()
	tx, err := s.sqldb.Begin()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.state.Lock()
	s.tx = tx
	s.txOwner = sess
	s.txLog = s.beginCollect()
	s.txTimer = time.AfterFunc(txMaxAge, func() { s.Abort(sess) })
	s.state.Unlock()
	return nil
}

// Commit settles the session's transaction and broadcasts its changes.
func (s *Store) Commit(sess *session) error {
	tx, cl := s.claimTx(sess)
	if tx == nil {
		return errors.New("no open transaction")
	}
	err := tx.Commit()
	collector.Store(nil)
	if err == nil {
		s.broadcast(cl, sess)
	}
	s.mu.Unlock()
	return err
}

// Rollback discards the session's transaction.
func (s *Store) Rollback(sess *session) error {
	tx, _ := s.claimTx(sess)
	if tx == nil {
		return errors.New("no open transaction")
	}
	err := tx.Rollback()
	collector.Store(nil)
	s.mu.Unlock()
	return err
}

// Abort force-rolls-back the session's transaction if it still owns one —
// the watchdog and session-disconnect path. A no-op when the owner already
// settled it; the claim makes exactly one finisher win.
func (s *Store) Abort(sess *session) {
	tx, _ := s.claimTx(sess)
	if tx == nil {
		return
	}
	_ = tx.Rollback()
	collector.Store(nil)
	s.mu.Unlock()
}

// Subscribe registers an event channel. The returned cancel removes it.
func (s *Store) Subscribe() (<-chan wire.Event, func()) {
	ch := make(chan wire.Event, 1024)
	s.subs.Lock()
	id := s.subs.next
	s.subs.next++
	s.subs.m[id] = ch
	s.subs.Unlock()
	return ch, func() {
		s.subs.Lock()
		if _, ok := s.subs.m[id]; ok {
			delete(s.subs.m, id)
			close(ch)
		}
		s.subs.Unlock()
	}
}

// broadcast turns a changeLog into an Event and pushes it to every
// subscriber. A subscriber that cannot keep up is closed — on close the
// client reconnects and resyncs in full, so nothing is silently missed.
func (s *Store) broadcast(cl *changeLog, sess *session) {
	if cl == nil || (len(cl.nodeRowIDs) == 0 && !cl.aux) {
		return
	}
	nodes, err := s.nodesByRowID(cl.nodeRowIDs)
	if err != nil {
		nodes = nil // the event still ships for its aux flag; rows just absent
	}
	ev := wire.Event{
		Seq:   s.seq.Add(1),
		Nodes: nodes,
		Aux:   cl.aux,
	}
	if sess != nil {
		ev.Instance = sess.instance
		ev.Name = sess.name
	}

	s.subs.Lock()
	for id, ch := range s.subs.m {
		select {
		case ch <- ev:
		default: // too far behind: cut it loose, the client resyncs
			delete(s.subs.m, id)
			close(ch)
		}
	}
	s.subs.Unlock()

	if s.onEvent != nil {
		s.onEvent(ev)
	}
}

// nodesByRowID fetches fresh post-commit rows for the touched rowids.
// Tombstoned rows are included (clients remove them); hard-deleted rowids
// simply do not come back.
func (s *Store) nodesByRowID(ids map[int64]bool) ([]database.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for id := range ids {
		ph = append(ph, "?")
		args = append(args, id)
	}
	cond := fmt.Sprintf("rowid IN (%s)", strings.Join(ph, ","))
	return database.GetNodesWhere(s.DB(), cond, args...)
}
