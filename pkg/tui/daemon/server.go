package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wire"
	"github.com/pkg/errors"
)

// ErrAlreadyServing reports that a live daemon already owns the socket.
var ErrAlreadyServing = errors.New("a daemon is already serving this database")

// Options configures Serve.
type Options struct {
	Sock    string
	Version string
	Idle    time.Duration // exit after this long with no clients; 0 = never
	Log     io.Writer     // event log sink (`lflow serve`); nil = quiet
}

// session is one connected client.
type session struct {
	id       int64
	name     string // human label from hello: editor, cli, serve
	instance string // per-process id for echo suppression
}

type server struct {
	store *Store
	opts  Options
	ln    net.Listener

	mu        sync.Mutex
	conns     map[net.Conn]bool
	lastEmpty time.Time
	closing   atomic.Bool
	nextSess  atomic.Int64
}

// mirror the CLI palette (see editor/render.go): muted gray prose, yellow names
const (
	logReset  = "\x1b[0m"
	logDim    = "\x1b[38;2;122;122;122m"
	logYellow = "\x1b[38;2;255;215;95m"
)

// Serve runs the daemon on an already-opened (and migrated) store. It returns
// when a client asks for shutdown, the idle deadline passes with no clients,
// or the listener fails. ErrAlreadyServing means another daemon owns the sock.
func Serve(store *Store, opts Options) error {
	ln, err := listen(opts.Sock)
	if err != nil {
		return err
	}
	sv := &server{
		store:     store,
		opts:      opts,
		ln:        ln,
		conns:     map[net.Conn]bool{},
		lastEmpty: time.Now(),
	}
	if opts.Log != nil {
		store.onEvent = sv.logEvent
	}
	sv.logServing()

	if opts.Idle > 0 {
		go sv.idleWatch()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if sv.closing.Load() {
				return nil
			}
			return errors.Wrap(err, "accepting connection")
		}
		sv.track(conn, true)
		go sv.session(conn)
	}
}

// listen binds the unix socket, reclaiming a stale one left by a dead daemon.
func listen(sock string) (net.Listener, error) {
	ln, err := net.Listen("unix", sock)
	if err == nil {
		_ = os.Chmod(sock, 0o600)
		return ln, nil
	}
	// bound already: a live daemon answers a dial, a dead one left a stale file
	if c, derr := net.DialTimeout("unix", sock, 300*time.Millisecond); derr == nil {
		c.Close()
		return nil, ErrAlreadyServing
	}
	_ = os.Remove(sock)
	ln, err = net.Listen("unix", sock)
	if err != nil {
		return nil, errors.Wrap(err, "binding socket")
	}
	_ = os.Chmod(sock, 0o600)
	return ln, nil
}

func (sv *server) track(conn net.Conn, add bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if add {
		sv.conns[conn] = true
	} else {
		delete(sv.conns, conn)
		if len(sv.conns) == 0 {
			sv.lastEmpty = time.Now()
		}
	}
}

func (sv *server) idleWatch() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		if sv.closing.Load() {
			return
		}
		sv.mu.Lock()
		idle := len(sv.conns) == 0 && time.Since(sv.lastEmpty) > sv.opts.Idle
		sv.mu.Unlock()
		if idle {
			sv.logf("→ idle exit · no clients for %s", sv.opts.Idle)
			sv.stop()
			return
		}
	}
}

// stop closes the listener and every connection; Serve then returns.
func (sv *server) stop() {
	if !sv.closing.CompareAndSwap(false, true) {
		return
	}
	sv.ln.Close()
	sv.mu.Lock()
	for c := range sv.conns {
		c.Close()
	}
	sv.mu.Unlock()
}

// session serves one client connection until it disconnects.
func (sv *server) session(conn net.Conn) {
	sess := &session{id: sv.nextSess.Add(1)}
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	greeted := false

	defer func() {
		conn.Close()
		sv.store.Abort(sess) // a dying client never strands the write lock
		sv.track(conn, false)
		if greeted {
			sv.logf("→ gone %q · %s", sess.name, shortID(sess.instance))
		}
	}()

	for {
		var req wire.Req
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := wire.Resp{ID: req.ID}

		switch req.Op {
		case wire.OpHello:
			// a bare re-hello (version read) keeps the original identity
			if req.Name != "" {
				sess.name = req.Name
			}
			if req.Instance != "" {
				sess.instance = req.Instance
			}
			resp.Version = sv.opts.Version
			// probes (Ensure's liveness checks) stay out of the log
			if !greeted && sess.name != "probe" {
				greeted = true
				sv.logf("→ connected %q · %s", sess.name, shortID(sess.instance))
			}

		case wire.OpExec:
			args, err := wire.DecodeValues(req.Args)
			if err == nil {
				resp.Affected, resp.LastID, err = sv.store.Exec(sess, req.SQL, args)
			}
			setErr(&resp, err)

		case wire.OpQuery:
			args, err := wire.DecodeValues(req.Args)
			if err == nil {
				resp.Cols, resp.Rows, err = sv.store.Query(sess, req.SQL, args)
			}
			setErr(&resp, err)

		case wire.OpBegin:
			setErr(&resp, sv.store.Begin(sess))
		case wire.OpCommit:
			setErr(&resp, sv.store.Commit(sess))
		case wire.OpRollback:
			setErr(&resp, sv.store.Rollback(sess))

		case wire.OpSubscribe:
			if err := enc.Encode(wire.Msg{Resp: &resp}); err != nil {
				return
			}
			sv.push(conn, dec, enc)
			return

		case wire.OpDeps:
			// dependency truth lives on the daemon: the process that would exec
			// a CLI is the one that says whether it can
			resp.Bins = probeBins(req.Bins)

		case wire.OpAgent:
			cl, thread, err := prepAgent(req)
			if err != nil {
				setErr(&resp, err)
				break // failed turns ack the error; the conn stays a request conn
			}
			if err := enc.Encode(wire.Msg{Resp: &resp}); err != nil {
				return
			}
			sv.agentTurn(conn, dec, enc, sess, req, cl, thread)
			return

		case wire.OpShutdown:
			_ = enc.Encode(wire.Msg{Resp: &resp})
			sv.logf("→ shutdown · asked by %q", sess.name)
			sv.stop()
			return

		default:
			resp.Err = "unknown op: " + req.Op
		}

		if err := enc.Encode(wire.Msg{Resp: &resp}); err != nil {
			return
		}
	}
}

// push streams events to a subscribed connection until the client hangs up or
// falls behind (the store closes a lagging channel; the dropped client
// reconnects and resyncs).
func (sv *server) push(conn net.Conn, dec *json.Decoder, enc *json.Encoder) {
	ch, cancel := sv.store.Subscribe()
	defer cancel()

	dead := make(chan struct{})
	go func() { // the only reads left are EOF/close detection
		var x json.RawMessage
		for {
			if err := dec.Decode(&x); err != nil {
				close(dead)
				return
			}
		}
	}()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := enc.Encode(wire.Msg{Event: &ev}); err != nil {
				return
			}
		case <-dead:
			return
		}
	}
}

func setErr(resp *wire.Resp, err error) {
	if err != nil {
		resp.Err = err.Error()
	}
}

func (sv *server) logf(format string, args ...any) {
	if sv.opts.Log == nil {
		return
	}
	fmt.Fprintf(sv.opts.Log, logDim+format+logReset+"\n", args...)
}

func (sv *server) logServing() {
	if sv.opts.Log == nil {
		return
	}
	var count int
	_ = sv.store.DB().QueryRow("SELECT COUNT(*) FROM nodes WHERE deleted = 0").Scan(&count)
	sv.logf("→ serving %s%s%s · %d nodes · sock %s",
		logYellow, sv.store.path, logDim, count, sv.opts.Sock)
}

// logEvent renders one applied change as a serve log line, resolving chip
// anchors so node names read clean.
func (sv *server) logEvent(ev wire.Event) {
	from := ev.Name
	if from == "" {
		from = "unknown"
	}
	if len(ev.Nodes) == 0 {
		sv.logf("→ applied chips · seq %d · %s", ev.Seq, from)
		return
	}
	name := ev.Nodes[0].Name
	if database.HasAnchor(name) {
		if chips, err := database.LoadChips(sv.store.DB()); err == nil {
			name = database.DisplayAnchors(name, chips)
		}
	}
	name = truncate(strings.TrimSpace(name), 40)
	if name == "" {
		name = "untitled"
	}
	sv.logf("→ applied %s%q%s · %s · seq %d · %s",
		logYellow, name, logDim, nodeNoun(len(ev.Nodes)), ev.Seq, from)
}

func nodeNoun(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func shortID(s string) string {
	if len(s) > 6 {
		return s[:6]
	}
	if s == "" {
		return "anon"
	}
	return s
}
