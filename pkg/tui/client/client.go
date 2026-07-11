package client

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wire"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/pkg/errors"
)

// Client is a live connection to the daemon: a *database.DB whose driver
// speaks the wire protocol, plus the subscribe feed.
type Client struct {
	db       *database.DB
	sock     string
	name     string
	instance string
	version  string
}

// DB returns the remote database handle; every database.* helper works on it.
func (c *Client) DB() *database.DB { return c.db }

// Instance is this process's id — events carrying it are the client's own
// writes echoed back.
func (c *Client) Instance() string { return c.instance }

func (c *Client) Close() error { return c.db.Close() }

// SockPath returns the daemon socket for a database: next to the DB file.
func SockPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "daemon.sock")
}

// Ensure returns a client for the database, spawning the daemon when none is
// running. A running daemon built from a different binary (the dev loop: the
// user reinstalls lflow constantly) is asked to shut down and replaced.
func Ensure(dbPath, name, version string) (*Client, error) {
	sock := SockPath(dbPath)
	instance, err := utils.GenerateUUID()
	if err != nil {
		return nil, errors.Wrap(err, "generating instance id")
	}

	if c, err := dialHello(sock, name, instance, ""); err == nil {
		v := c.serverVersion()
		if v == version {
			c.Close()
			return open(sock, name, instance, version), nil
		}
		// version skew: replace the daemon before anything talks to it
		_, _ = c.call(wire.Req{Op: wire.OpShutdown})
		c.Close()
		waitGone(sock, 3*time.Second)
	}

	if err := spawn(dbPath, sock); err != nil {
		return nil, err
	}
	return open(sock, name, instance, version), nil
}

// serverVersion re-runs hello to read the daemon's version (the first hello
// in dialHello passed "" to skip enforcement).
func (c *conn) serverVersion() string {
	resp, err := c.call(wire.Req{Op: wire.OpHello})
	if err != nil {
		return ""
	}
	return resp.Version
}

func open(sock, name, instance, version string) *Client {
	db := sql.OpenDB(connector{sock: sock, name: name, instance: instance, version: version})
	return &Client{
		db:       &database.DB{Conn: db, Filepath: sock},
		sock:     sock,
		name:     name,
		instance: instance,
		version:  version,
	}
}

// spawn starts `lflow serve --quiet --idle` detached and waits for the socket
// to answer. A lock file keeps two racing clients from double-spawning.
func spawn(dbPath, sock string) error {
	lock, err := os.OpenFile(sock+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.Wrap(err, "opening spawn lock")
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return errors.Wrap(err, "locking spawn lock")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	// the race loser finds the winner's daemon already up
	if pingable(sock) {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "resolving lflow binary")
	}

	logf, err := os.OpenFile(filepath.Join(filepath.Dir(dbPath), "daemon.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return errors.Wrap(err, "opening daemon log")
	}
	defer logf.Close()

	cmd := exec.Command(exe, "serve", "--quiet", "--idle", "--db", dbPath, "--sock", sock)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive the client
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "starting daemon")
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pingable(sock) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("daemon did not come up · see daemon.log next to the database")
}

func pingable(sock string) bool {
	c, err := dialHello(sock, "ping", "", "")
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func waitGone(sock string, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if !pingable(sock) {
			// the old daemon may leave its socket file behind
			_ = os.Remove(sock)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Shutdown asks the daemon to exit — version replacement and tests.
func (c *Client) Shutdown() {
	if nc, err := dialHello(c.sock, c.name, c.instance, ""); err == nil {
		_, _ = nc.call(wire.Req{Op: wire.OpShutdown})
		nc.Close()
	}
}

// Subscribe opens the live change feed. The channel closes when the daemon
// drops the subscriber (lagging, shutdown) — the caller reconnects and does a
// full resync. cancel closes the feed.
func (c *Client) Subscribe() (<-chan wire.Event, func(), error) {
	nc, err := dialHello(c.sock, c.name, c.instance, c.version)
	if err != nil {
		return nil, nil, err
	}
	if _, err := nc.call(wire.Req{Op: wire.OpSubscribe}); err != nil {
		nc.Close()
		return nil, nil, err
	}

	ch := make(chan wire.Event, 256)
	go func() {
		defer close(ch)
		for {
			var msg wire.Msg
			if err := nc.dec.Decode(&msg); err != nil {
				return
			}
			if msg.Event != nil {
				ch <- *msg.Event
			}
		}
	}()
	return ch, func() { nc.Close() }, nil
}
