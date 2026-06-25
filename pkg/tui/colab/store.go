// Package colab wires lflow's local database to the runtime package's Colab
// session, persisting the Google OAuth token in the `system` table.
package colab

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lflow/lflow/pkg/runtime"
	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

// Store implements runtime.TokenStore over the lflow `system` key/value table.
type Store struct {
	db *database.DB
}

// NewStore returns a token store backed by db.
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// Load returns the stored Colab token, or (nil, nil) if none is saved.
func (s *Store) Load(ctx context.Context) (*runtime.Token, error) {
	var raw string
	if err := database.GetSystem(s.db, consts.SystemColabAuth, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "loading colab token")
	}
	if raw == "" {
		return nil, nil
	}
	var tok runtime.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, errors.Wrap(err, "parsing stored colab token")
	}
	return &tok, nil
}

// Save persists the Colab token as JSON.
func (s *Store) Save(ctx context.Context, t *runtime.Token) error {
	data, err := json.Marshal(t)
	if err != nil {
		return errors.Wrap(err, "marshaling colab token")
	}
	if err := database.UpsertSystem(s.db, consts.SystemColabAuth, string(data)); err != nil {
		return errors.Wrap(err, "saving colab token")
	}
	return nil
}
