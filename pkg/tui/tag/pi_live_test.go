package tag

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestPiClientLive drives the @Pi → local pi bridge end-to-end against the real
// CLI. Gated behind LFLOW_LIVE=1 (and skips when pi is not on PATH) so the normal
// suite never shells out or spends tokens. Run with:
//
//	LFLOW_LIVE=1 go test --tags fts5 -run TestPiClientLive -v ./pkg/tui/tag
func TestPiClientLive(t *testing.T) {
	if os.Getenv("LFLOW_LIVE") != "1" {
		t.Skip("set LFLOW_LIVE=1 to run the live pi bridge test")
	}
	if !piAvailable() {
		t.Skip("pi CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c := &PiClient{}
	thread := []ThreadNode{
		{UUID: "live-root", Depth: 0, Name: "@Pi reply with exactly the single word: pong", Type: "bullets", Role: "user", Asked: true},
	}

	ch, err := c.Send(ctx, "Pi", thread)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var gotMessage, gotDone, gotError bool
	var message, errText string
	for ev := range ch {
		switch ev.Op {
		case "message":
			gotMessage = true
			message = ev.Text
			t.Logf("message = %q", ev.Text)
		case "error":
			gotError = true
			errText = ev.Text
			t.Logf("ERROR = %q", ev.Text)
		case "done":
			gotDone = true
		}
	}

	t.Logf("summary: message=%v done=%v error=%v", gotMessage, gotDone, gotError)
	if gotError {
		t.Fatalf("pi bridge returned an error: %s", errText)
	}
	if !gotMessage || message == "" {
		t.Error("expected a non-empty message reply from pi")
	}
	if !gotDone {
		t.Error("expected a done event closing the turn")
	}
}
