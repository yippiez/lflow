package tag

import (
	"context"
	"encoding/json"

	"github.com/coder/websocket"
	"github.com/pkg/errors"
)

// WSClient speaks the tag protocol to a Pi coding-agent service over a
// websocket: one JSON object per message, a send request out, then a stream
// of events back until done/error (shapes documented in AGENTS.md).
type WSClient struct {
	URL string
}

// sendReq is the one request shape.
type sendReq struct {
	Op     string       `json:"op"` // always "send"
	Agent  string       `json:"agent"`
	Thread []ThreadNode `json:"thread"`
}

// Send dials per turn and ships the WHOLE thread — the request is stateless,
// so the transport stays connectionless and the service needs no memory
// between turns (a service that wants continuity anyway can key off
// thread[0].UUID).
func (c *WSClient) Send(ctx context.Context, agent string, thread []ThreadNode) (<-chan Event, error) {
	conn, _, err := websocket.Dial(ctx, c.URL, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "dialing agent service %s", c.URL)
	}

	req, err := json.Marshal(sendReq{Op: "send", Agent: agent, Thread: thread})
	if err != nil {
		conn.Close(websocket.StatusInternalError, "marshal")
		return nil, errors.Wrap(err, "marshalling send request")
	}
	if err := conn.Write(ctx, websocket.MessageText, req); err != nil {
		conn.Close(websocket.StatusInternalError, "write")
		return nil, errors.Wrap(err, "sending thread")
	}

	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				ch <- Event{Op: "error", Text: "agent connection lost: " + err.Error()}
				return
			}
			var ev Event
			if err := json.Unmarshal(data, &ev); err != nil {
				continue // skip malformed frames; the stream ends on done/error
			}
			ch <- ev
			if ev.Op == "done" || ev.Op == "error" {
				return
			}
		}
	}()
	return ch, nil
}
