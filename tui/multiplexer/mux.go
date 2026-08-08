package multiplexer

// Manager is the editor's registry of sessions, keyed the way the run bands
// are: by node uuid or chip id. It is owner-goroutine only — every method runs
// on bubbletea's event loop.
type Manager struct {
	// ScrollbackCap bounds each session's screen history, in rows.
	ScrollbackCap int

	sessions map[string]*Session
}

// NewManager builds an empty registry.
func NewManager(scrollbackCap int) *Manager {
	return &Manager{ScrollbackCap: scrollbackCap, sessions: map[string]*Session{}}
}

// Start launches argv on a fresh PTY session under id, replacing any dead
// session filed there. A live session under the same id is returned as-is —
// one id is one process.
func (mx *Manager) Start(id, agent, label, dir string, argv []string, cols, rows int) *Session {
	if s := mx.sessions[id]; s != nil && s.Live() {
		return s
	}
	s := &Session{ID: id, Agent: agent, Label: label, Dir: dir}
	mx.start(s, argv, cols, rows)
	mx.sessions[id] = s
	return s
}

// Get returns the session filed under id, live or exited, nil when none.
func (mx *Manager) Get(id string) *Session { return mx.sessions[id] }

// Remove drops a session's record. It does not kill — callers kill first when
// they mean to.
func (mx *Manager) Remove(id string) { delete(mx.sessions, id) }

// Drop kills (if needed) and removes a session's record — the "a fresh run
// replaces whatever was here" path.
func (mx *Manager) Drop(id string) {
	if s := mx.sessions[id]; s != nil {
		if s.Live() {
			s.Kill()
		}
		delete(mx.sessions, id)
	}
}

// LiveCount is how many sessions are still running.
func (mx *Manager) LiveCount() int {
	n := 0
	for _, s := range mx.sessions {
		if s.Live() {
			n++
		}
	}
	return n
}

// Live returns the running sessions, unordered.
func (mx *Manager) Live() []*Session {
	var out []*Session
	for _, s := range mx.sessions {
		if s.Live() {
			out = append(out, s)
		}
	}
	return out
}

// KillAll stops every live session — the editor's quit path.
func (mx *Manager) KillAll() {
	for _, s := range mx.sessions {
		if s.Live() {
			s.Kill()
		}
	}
}
