package database

import (
	"database/sql"

	"github.com/pkg/errors"
)

// AgentSession binds one agent conversation to one thread node — the
// Claude-Tag model: the @mentioned node is the thread root, its subtree is the
// conversation, and the session id lets a later mention in the same thread
// resume the remote context. The bridge lives inside the editor process, so a
// session left running when the editor closes is marked paused and picked
// back up by id on the next send.
type AgentSession struct {
	ID        string `json:"id"`        // remote session id
	NodeUUID  string `json:"node_uuid"` // thread root node
	Agent     string `json:"agent"`     // e.g. "Pi"
	State     string `json:"state"`     // idle | running | paused
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

const sessionColumns = "id, node_uuid, agent, state, created_at, updated_at"

func scanSession(row interface{ Scan(...interface{}) error }) (AgentSession, error) {
	var s AgentSession
	err := row.Scan(&s.ID, &s.NodeUUID, &s.Agent, &s.State, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// Upsert saves the session, updating state/updated_at for an existing id.
func (s AgentSession) Upsert(db *DB) error {
	_, err := db.Exec(`INSERT INTO agent_sessions (`+sessionColumns+`) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
		s.ID, s.NodeUUID, s.Agent, s.State, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return errors.Wrapf(err, "upserting agent session %s", s.ID)
	}
	return nil
}

// GetThreadSession returns the session bound to the given thread node and
// agent, or ok=false — the lookup that makes a later @mention in a thread
// continue the same conversation instead of forking a new one.
func GetThreadSession(db *DB, nodeUUID, agent string) (AgentSession, bool, error) {
	s, err := scanSession(db.QueryRow(
		"SELECT "+sessionColumns+" FROM agent_sessions WHERE node_uuid = ? AND agent = ? ORDER BY created_at DESC LIMIT 1",
		nodeUUID, agent))
	if err == sql.ErrNoRows {
		return s, false, nil
	} else if err != nil {
		return s, false, errors.Wrapf(err, "querying session for node %s", nodeUUID)
	}
	return s, true, nil
}

// DeleteThreadSession drops the session bound to a thread node + agent — used
// to self-heal stale bindings (e.g. the node no longer mentions the agent).
func DeleteThreadSession(db *DB, nodeUUID, agent string) error {
	if _, err := db.Exec("DELETE FROM agent_sessions WHERE node_uuid = ? AND agent = ?", nodeUUID, agent); err != nil {
		return errors.Wrapf(err, "deleting session for node %s", nodeUUID)
	}
	return nil
}

// PauseRunningSessions marks every running session paused — called when the
// editor closes, since the bridge (and any in-flight work) dies with it.
func PauseRunningSessions(db *DB) error {
	if _, err := db.Exec("UPDATE agent_sessions SET state = 'paused' WHERE state = 'running'"); err != nil {
		return errors.Wrap(err, "pausing running sessions")
	}
	return nil
}
