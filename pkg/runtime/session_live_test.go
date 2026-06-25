package runtime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// memStore is an in-memory TokenStore seeded from a JSON token, used by the
// live test so it doesn't touch the real lflow database.
type memStore struct{ tok *Token }

func (m *memStore) Load(ctx context.Context) (*Token, error) { return m.tok, nil }
func (m *memStore) Save(ctx context.Context, t *Token) error { m.tok = t; return nil }

// TestSessionPersistsVariables proves the core property: a single session keeps
// one kernel open so Python variables survive across Execute calls.
//
// It is gated on a real Colab token to avoid hitting the network in CI. To run:
//
//	LFLOW_COLAB_TOKEN_JSON="$(cat ~/.config/compute/token.json)" \
//	  go test -tags fts5 -run TestSessionPersistsVariables -v ./pkg/runtime
func TestSessionPersistsVariables(t *testing.T) {
	raw := os.Getenv("LFLOW_COLAB_TOKEN_JSON")
	if raw == "" {
		t.Skip("set LFLOW_COLAB_TOKEN_JSON to run the live Colab session test")
	}
	var tok Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		t.Fatalf("parse LFLOW_COLAB_TOKEN_JSON: %v", err)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sess, err := SessionStart(startCtx, Opts{Store: &memStore{tok: &tok}, CPU: true})
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		if err := sess.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	if _, err := sess.Execute(startCtx, "x = 41"); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	out, err := sess.Execute(startCtx, "print(x + 1)")
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("variable did not persist across Execute calls; output = %q", out)
	}
}
