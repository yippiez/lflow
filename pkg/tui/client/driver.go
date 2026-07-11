// Package client connects lflow commands and the editor to the daemon. It
// exposes the daemon as a database/sql driver speaking the wire protocol, so
// every existing database.* helper works unchanged against a remote handle —
// plus Ensure (dial-or-spawn) and Subscribe (the live change feed).
package client

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/lflow/lflow/pkg/tui/wire"
	"github.com/pkg/errors"
)

// connector dials one daemon connection per pooled driver.Conn. All conns of
// one process share the instance id so the editor can drop its own echoes.
type connector struct {
	sock     string
	name     string
	instance string
	version  string
}

func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	nc, err := dialHello(c.sock, c.name, c.instance, c.version)
	if err != nil {
		return nil, err
	}
	return nc, nil
}

func (c connector) Driver() driver.Driver { return drv{} }

type drv struct{}

func (drv) Open(string) (driver.Conn, error) {
	return nil, errors.New("lflow client: use sql.OpenDB")
}

// conn is one socket to the daemon: requests go out, responses come back, in
// lockstep (database/sql guarantees single-goroutine use per conn).
type conn struct {
	nc     net.Conn
	dec    *json.Decoder
	enc    *json.Encoder
	nextID int64
}

// dialHello opens a socket and performs the handshake. A version mismatch is
// a hard error — Ensure resolves skew by respawning before handing out conns.
func dialHello(sock, name, instance, version string) (*conn, error) {
	nc, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return nil, errors.Wrap(err, "dialing daemon")
	}
	c := &conn{nc: nc, dec: json.NewDecoder(nc), enc: json.NewEncoder(nc)}
	resp, err := c.call(wire.Req{Op: wire.OpHello, Name: name, Instance: instance, Version: version})
	if err != nil {
		nc.Close()
		return nil, err
	}
	if version != "" && resp.Version != version {
		nc.Close()
		return nil, errors.Errorf("daemon version %s, client %s", resp.Version, version)
	}
	return c, nil
}

func (c *conn) call(req wire.Req) (*wire.Resp, error) {
	c.nextID++
	req.ID = c.nextID
	if err := c.enc.Encode(req); err != nil {
		return nil, driver.ErrBadConn
	}
	var msg wire.Msg
	if err := c.dec.Decode(&msg); err != nil {
		return nil, driver.ErrBadConn
	}
	if msg.Resp == nil {
		return nil, driver.ErrBadConn
	}
	if msg.Resp.Err != "" {
		return nil, errors.New(msg.Resp.Err)
	}
	return msg.Resp, nil
}

func (c *conn) Prepare(query string) (driver.Stmt, error) { return &stmt{c: c, query: query}, nil }
func (c *conn) Close() error                              { return c.nc.Close() }

func (c *conn) Begin() (driver.Tx, error) {
	if _, err := c.call(wire.Req{Op: wire.OpBegin}); err != nil {
		return nil, err
	}
	return &tx{c: c}, nil
}

type tx struct{ c *conn }

func (t *tx) Commit() error {
	_, err := t.c.call(wire.Req{Op: wire.OpCommit})
	return err
}

func (t *tx) Rollback() error {
	_, err := t.c.call(wire.Req{Op: wire.OpRollback})
	return err
}

// stmt carries the SQL string; the daemon has no prepared-statement state.
type stmt struct {
	c     *conn
	query string
}

func (s *stmt) Close() error  { return nil }
func (s *stmt) NumInput() int { return -1 }

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	enc, err := encodeArgs(args)
	if err != nil {
		return nil, err
	}
	resp, err := s.c.call(wire.Req{Op: wire.OpExec, SQL: s.query, Args: enc})
	if err != nil {
		return nil, err
	}
	return result{affected: resp.Affected, lastID: resp.LastID}, nil
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	enc, err := encodeArgs(args)
	if err != nil {
		return nil, err
	}
	resp, err := s.c.call(wire.Req{Op: wire.OpQuery, SQL: s.query, Args: enc})
	if err != nil {
		return nil, err
	}
	return &rows{cols: resp.Cols, data: resp.Rows}, nil
}

func encodeArgs(args []driver.Value) ([]any, error) {
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a
	}
	return wire.EncodeValues(vals)
}

type result struct{ affected, lastID int64 }

func (r result) LastInsertId() (int64, error) { return r.lastID, nil }
func (r result) RowsAffected() (int64, error) { return r.affected, nil }

// rows is a fully-buffered result set.
type rows struct {
	cols []string
	data [][]any
	pos  int
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return nil }

func (r *rows) Next(dest []driver.Value) error {
	if r.pos >= len(r.data) {
		return io.EOF
	}
	row, err := wire.DecodeValues(r.data[r.pos])
	if err != nil {
		return err
	}
	r.pos++
	for i := range dest {
		if i < len(row) {
			dest[i] = row[i]
		} else {
			dest[i] = nil
		}
	}
	return nil
}
