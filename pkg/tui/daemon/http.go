package daemon

// The HTTP face of the daemon: `lflow serve --http <addr>` exposes the same
// store to non-terminal clients (the mobile app, scripts) as a small JSON API
// plus an SSE event stream, and serves the embedded mobile web app at /.
// Writes route through Store.Exec so they broadcast to every subscriber
// exactly like a unix-socket client's; the TUI editor sees mobile edits live
// and vice versa.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lflow/lflow/pkg/tui/daemon/webui"
	"github.com/lflow/lflow/pkg/tui/database"
)

// httpServer bridges HTTP requests onto the daemon's store.
type httpServer struct {
	sv    *server
	token string
}

// serveHTTP starts the HTTP listener. It returns the bound address or an error.
func (sv *server) serveHTTP(addr, token string) (string, error) {
	// HTTP clients navigate from the root: make sure the fixed roots exist
	// before the first outline fetch (idempotent, same as the CLI commands do)
	if err := database.EnsureRoot(sv.store.DB()); err != nil {
		return "", err
	}
	if err := database.EnsureTemp(sv.store.DB()); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	hs := &httpServer{sv: sv, token: token}
	srv := &http.Server{Handler: hs.mux()}
	go func() {
		<-sv.httpDone
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

func (hs *httpServer) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", hs.wrap(hs.info))
	mux.HandleFunc("/api/outline", hs.wrap(hs.outline))
	mux.HandleFunc("/api/nodes", hs.wrap(hs.nodes))
	mux.HandleFunc("/api/nodes/", hs.wrap(hs.node))
	mux.HandleFunc("/api/search", hs.wrap(hs.search))
	mux.HandleFunc("/api/events", hs.wrap(hs.events))
	mux.Handle("/", webui.Handler())
	return mux
}

// wrap applies CORS, auth and the busy counter (an active HTTP client holds
// off the idle exit exactly like a socket client).
func (hs *httpServer) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Lflow-Instance")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if hs.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				got = r.URL.Query().Get("token")
			}
			if got != hs.token {
				httpErr(w, http.StatusUnauthorized, "bad token")
				return
			}
		}
		hs.sv.httpBusy.Add(1)
		defer hs.sv.httpBusy.Add(-1)
		h(w, r)
	}
}

// httpSession builds the synthetic session HTTP writes run under, so change
// events carry the writing client's instance id (echo suppression) and label.
func (hs *httpServer) httpSession(r *http.Request) *session {
	inst := r.Header.Get("X-Lflow-Instance")
	if inst == "" {
		inst = r.URL.Query().Get("instance")
	}
	return &session{id: hs.sv.nextSess.Add(1), name: "mobile", instance: inst}
}

func httpJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (hs *httpServer) info(w http.ResponseWriter, r *http.Request) {
	var count int
	_ = hs.sv.store.DB().QueryRow("SELECT COUNT(*) FROM nodes WHERE deleted = 0").Scan(&count)
	httpJSON(w, map[string]any{
		"version": hs.sv.opts.Version,
		"db":      hs.sv.store.path,
		"nodes":   count,
		"root":    database.RootUUID,
	})
}

// outline returns every live node outside the Temporary Domain — the whole
// tree in one shot. Outlines are small; one fetch plus the event stream keeps
// the client model trivial (same trade the wire protocol makes).
func (hs *httpServer) outline(w http.ResponseWriter, r *http.Request) {
	db := hs.sv.store.DB()
	tempUUIDs, err := database.TempSubtreeUUIDs(db)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	all, err := database.GetNodesWhere(db, "deleted = 0")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	nodes := make([]database.Node, 0, len(all))
	for _, n := range all {
		if tempUUIDs[n.UUID] {
			continue
		}
		nodes = append(nodes, n)
	}
	httpJSON(w, map[string]any{"root": database.RootUUID, "nodes": nodes})
}

type createReq struct {
	ParentUUID string `json:"parent_uuid"`
	Name       string `json:"name"`
	Note       string `json:"note"`
	Type       string `json:"type"`
	MirrorOf   string `json:"mirror_of"` // uuid of an original: create a live mirror of it
	After      string `json:"after"`     // sibling uuid to land after
	Position   string `json:"position"`  // "top" | "bottom" | "" (parent priority)
}

// nodes handles POST /api/nodes (create).
func (hs *httpServer) nodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if req.ParentUUID == "" {
		req.ParentUUID = database.RootUUID
	}
	typ := req.Type
	if typ == "" {
		typ = database.TypeBullets
	}
	if !validTypeString(typ) {
		httpErr(w, 400, "bad type: "+typ)
		return
	}
	if req.MirrorOf != "" {
		if _, err := database.GetNode(hs.sv.store.DB(), req.MirrorOf); err != nil {
			httpErr(w, 400, "mirror target does not exist: "+req.MirrorOf)
			return
		}
	}
	sess := hs.httpSession(r)
	rank, err := hs.placeRank(sess, req.ParentUUID, req.After, req.Position)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	now := time.Now().UnixNano()
	n := database.Node{
		UUID:       uuid.New().String(),
		ParentUUID: req.ParentUUID,
		Rank:       rank,
		Name:       req.Name,
		Note:       req.Note,
		Type:       typ,
		MirrorOf:   req.MirrorOf,
		AddedOn:    now,
		EditedOn:   now,
		Priority:   database.PriorityDown,
	}
	_, _, err = hs.sv.store.Exec(sess,
		"INSERT INTO nodes (uuid, parent_uuid, rank, name, note, type, style, mirror_of, completed_at, added_on, edited_on, deleted, collapsed, readonly, starred, priority) VALUES (?, ?, ?, ?, ?, ?, '', ?, 0, ?, ?, 0, 0, 0, 0, ?)",
		[]any{n.UUID, n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.MirrorOf, n.AddedOn, n.EditedOn, n.Priority})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	httpJSON(w, n)
}

// validTypeString accepts any plausible type name, not just ValidTypes:
// nodes.type is a free string by design — custom client-side note extensions
// mint their own types, and unknown types everywhere fall back to bullets.
func validTypeString(t string) bool {
	if t == "" || len(t) > 32 {
		return false
	}
	for _, r := range t {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// placeRank computes where an incoming node lands and opens a gap when needed.
func (hs *httpServer) placeRank(sess *session, parent, after, position string) (int, error) {
	db := hs.sv.store.DB()
	if after != "" {
		sib, err := database.GetNode(db, after)
		if err == nil && sib.ParentUUID == parent {
			_, _, err = hs.sv.store.Exec(sess,
				"UPDATE nodes SET rank = rank + 1 WHERE parent_uuid = ? AND rank > ? AND deleted = 0",
				[]any{parent, sib.Rank})
			return sib.Rank + 1, err
		}
	}
	switch position {
	case "top":
		return database.FirstRank(db, parent)
	case "bottom":
		return database.NextRank(db, parent)
	}
	return database.PlaceRank(db, parent)
}

type patchReq struct {
	Name      *string `json:"name"`
	Note      *string `json:"note"`
	Type      *string `json:"type"`
	Style     *string `json:"style"`
	Collapsed *bool   `json:"collapsed"`
	Starred   *bool   `json:"starred"`
	Completed *bool   `json:"completed"`
	Priority  *string `json:"priority"`
}

type moveReq struct {
	ParentUUID string `json:"parent_uuid"`
	After      string `json:"after"`
	Position   string `json:"position"`
}

// node handles /api/nodes/{uuid}[...]: GET, PATCH, DELETE and POST .../move.
func (hs *httpServer) node(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		httpErr(w, 400, "missing node uuid")
		return
	}
	db := hs.sv.store.DB()
	n, err := database.GetNode(db, id)
	if err != nil {
		httpErr(w, 404, "no such node: "+id)
		return
	}

	switch {
	case action == "move" && r.Method == http.MethodPost:
		hs.move(w, r, n)
	case action == "" && r.Method == http.MethodGet:
		httpJSON(w, n)
	case action == "" && r.Method == http.MethodPatch:
		hs.patch(w, r, n)
	case action == "" && r.Method == http.MethodDelete:
		hs.delete(w, r, n)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func (hs *httpServer) patch(w http.ResponseWriter, r *http.Request, n database.Node) {
	var req patchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if n.Readonly && (req.Name != nil || req.Note != nil || req.Type != nil) {
		httpErr(w, 403, "node is locked")
		return
	}
	var sets []string
	var args []any
	content := false // content edits stamp edited_on; view toggles do not
	set := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if req.Name != nil {
		set("name", *req.Name)
		content = true
	}
	if req.Note != nil {
		set("note", *req.Note)
		content = true
	}
	if req.Type != nil {
		if !validTypeString(*req.Type) {
			httpErr(w, 400, "bad type: "+*req.Type)
			return
		}
		set("type", *req.Type)
		content = true
	}
	if req.Style != nil {
		set("style", *req.Style)
		content = true
	}
	if req.Collapsed != nil {
		set("collapsed", *req.Collapsed)
	}
	if req.Starred != nil {
		set("starred", *req.Starred)
	}
	if req.Priority != nil {
		if *req.Priority != database.PriorityUp && *req.Priority != database.PriorityDown {
			httpErr(w, 400, "priority must be up or down")
			return
		}
		set("priority", *req.Priority)
	}
	if req.Completed != nil {
		if *req.Completed {
			set("completed_at", time.Now().UnixNano())
		} else {
			set("completed_at", 0)
		}
		content = true
	}
	if len(sets) == 0 {
		httpJSON(w, n)
		return
	}
	if content {
		set("edited_on", time.Now().UnixNano())
	}
	args = append(args, n.UUID)
	sess := hs.httpSession(r)
	_, _, err := hs.sv.store.Exec(sess,
		"UPDATE nodes SET "+strings.Join(sets, ", ")+" WHERE uuid = ?", args)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	fresh, err := database.GetNode(hs.sv.store.DB(), n.UUID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	httpJSON(w, fresh)
}

func (hs *httpServer) move(w http.ResponseWriter, r *http.Request, n database.Node) {
	var req moveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if req.ParentUUID == "" {
		req.ParentUUID = n.ParentUUID
	}
	db := hs.sv.store.DB()
	// a node must never become its own descendant
	for cur := req.ParentUUID; cur != "" && cur != database.RootUUID; {
		if cur == n.UUID {
			httpErr(w, 400, "cannot move a node into its own subtree")
			return
		}
		p, err := database.GetNode(db, cur)
		if err != nil {
			break
		}
		cur = p.ParentUUID
	}
	sess := hs.httpSession(r)
	rank, err := hs.placeRank(sess, req.ParentUUID, req.After, req.Position)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// structural change: parent and rank move, edited_on stays (Reparent semantics)
	_, _, err = hs.sv.store.Exec(sess,
		"UPDATE nodes SET parent_uuid = ?, rank = ? WHERE uuid = ?",
		[]any{req.ParentUUID, rank, n.UUID})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	fresh, _ := database.GetNode(db, n.UUID)
	httpJSON(w, fresh)
}

func (hs *httpServer) delete(w http.ResponseWriter, r *http.Request, n database.Node) {
	subtree, err := database.GetSubtree(hs.sv.store.DB(), n.UUID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	ph := make([]string, 0, len(subtree))
	args := make([]any, 0, len(subtree))
	for _, s := range subtree {
		ph = append(ph, "?")
		args = append(args, s.UUID)
	}
	sess := hs.httpSession(r)
	_, _, err = hs.sv.store.Exec(sess,
		fmt.Sprintf("UPDATE nodes SET deleted = 1 WHERE uuid IN (%s)", strings.Join(ph, ",")), args)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	httpJSON(w, map[string]any{"deleted": len(subtree)})
}

func (hs *httpServer) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	includeCompleted := r.URL.Query().Get("completed") == "1"
	nodes, err := database.SearchNodes(hs.sv.store.DB(), q, includeCompleted)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if nodes == nil {
		nodes = []database.Node{}
	}
	httpJSON(w, map[string]any{"nodes": nodes})
}

// events streams committed changes as server-sent events: one `data:` line per
// wire.Event, a comment heartbeat while quiet. EventSource reconnects and the
// client refetches the outline, mirroring the socket subscribe contract.
func (hs *httpServer) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := hs.sv.store.Subscribe()
	defer cancel()

	fmt.Fprintf(w, ": connected\n\n")
	fl.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok { // lagged and cut loose: the client reconnects and resyncs
				return
			}
			buf, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", buf); err != nil {
				return
			}
			fl.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
