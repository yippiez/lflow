// Package live implements `lflow serve`: a long-lived process that owns the
// local SQLite outline and serves connected clients (the mobile app today, the
// desktop TUI later) over WebSocket. On connect a client receives a full
// snapshot; it then streams edit ops back, which the server applies to the DB
// and re-broadcasts so every client converges on the same tree.
//
// WARNING (invariant): the serve process is the single owner of the DB. No
// other process should write lflow.db while serve runs (the interim desktop TUI
// still does, and will be migrated onto this server in a later phase).
package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	lflowctx "github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/log"
	"github.com/pkg/errors"
)

// Server holds the shared outline DB, the connection hub and the access token.
type Server struct {
	db    *database.DB
	token string

	mu  sync.Mutex // serializes apply()+broadcast so clients can't race the DB
	hub *hub
}

// client is one connected WebSocket. Outbound frames go through send so the
// single writer goroutine owns all writes to the conn.
type client struct {
	conn *websocket.Conn
	send chan []byte
}

type hub struct {
	mu      sync.Mutex
	clients map[*client]bool
}

func newHub() *hub { return &hub{clients: map[*client]bool{}} }

func (h *hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	if h.clients[c] {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// broadcast pushes the same pre-marshaled frame to every client. A client whose
// buffer is full is dropped rather than blocking the whole hub.
func (h *hub) broadcast(frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- frame:
		default:
			// slow client: drop it; it will reconnect and re-snapshot.
		}
	}
}

// Serve starts the live server on the given port and blocks.
func Serve(ctx lflowctx.DnoteCtx, port int) error {
	token, err := newToken()
	if err != nil {
		return err
	}

	s := &Server{db: ctx.DB, token: token, hub: newHub()}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", port)
	printConnectionInfo(port, token)

	srv := &http.Server{Addr: addr, Handler: mux}
	return srv.ListenAndServe()
}

// handleWS authenticates with the shared token, upgrades the connection, sends
// an initial snapshot and then loops reading ops.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// token is the auth boundary, so any origin (Expo web/native) is allowed.
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Errorf("ws accept: %s\n", err)
		return
	}
	defer conn.CloseNow()

	c := &client{conn: conn, send: make(chan []byte, 8)}
	s.hub.add(c)
	defer s.hub.remove(c)

	go c.writeLoop()

	if frame, err := s.snapshotFrame(); err == nil {
		c.send <- frame
	}

	for {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return // client gone
		}
		var o op
		if err := json.Unmarshal(data, &o); err != nil {
			log.Errorf("ws bad op: %s\n", err)
			continue
		}
		s.handleOp(o)
	}
}

// handleOp applies one op under the server lock and broadcasts the new state.
func (s *Server) handleOp(o op) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.apply(o); err != nil {
		log.Errorf("applying op %q: %s\n", o.Op, err)
		return
	}
	frame, err := s.snapshotFrame()
	if err != nil {
		log.Errorf("snapshot: %s\n", err)
		return
	}
	s.hub.broadcast(frame)
}

// snapshotFrame builds the current snapshot and marshals it once for reuse.
func (s *Server) snapshotFrame() ([]byte, error) {
	snap, err := buildSnapshot(s.db)
	if err != nil {
		return nil, err
	}
	return json.Marshal(snap)
}

// writeLoop owns every write to the conn, draining the send channel until it is
// closed by hub.remove.
func (c *client) writeLoop() {
	for frame := range c.send {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := c.conn.Write(ctx, websocket.MessageText, frame)
		cancel()
		if err != nil {
			c.conn.CloseNow()
			return
		}
	}
}

// newToken returns a random 128-bit hex token used as the LAN access secret.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "generating token")
	}
	return hex.EncodeToString(b), nil
}

// lanIP returns the first non-loopback IPv4 address, for the printed connect URL.
func lanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func printConnectionInfo(port int, token string) {
	ip := lanIP()
	fmt.Printf("\n  lflow serve · live outline server\n\n")
	fmt.Printf("  → connect the app to:  ws://%s:%d/ws\n", ip, port)
	fmt.Printf("  → token:               %s\n\n", token)
	fmt.Printf("  on the same Wi-Fi. ctrl+c to stop.\n\n")
}
