// Package wire defines the JSON message protocol spoken between the lflow
// daemon (the single owner of the SQLite file) and its clients (the CLI, the
// editor, and later remote apps). Messages are newline-delimited JSON over a
// unix socket. SQL travels as-is; values travel as tagged strings so int64
// timestamps (UnixNano exceeds float64 precision) and blobs survive JSON.
package wire

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Ops a client can send.
const (
	OpHello     = "hello"     // handshake: name, instance id, version
	OpExec      = "exec"      // run a statement, return affected/lastID
	OpQuery     = "query"     // run a query, return cols+rows
	OpBegin     = "begin"     // open a transaction (holds the write lock)
	OpCommit    = "commit"    // commit the open transaction
	OpRollback  = "rollback"  // roll back the open transaction
	OpSubscribe = "subscribe" // switch this connection to event-push mode
	OpShutdown  = "shutdown"  // ask the daemon to exit (version respawn)
)

// Req is a client request.
type Req struct {
	ID   int64  `json:"id"`
	Op   string `json:"op"`
	SQL  string `json:"sql,omitempty"`
	Args []any  `json:"args,omitempty"` // encoded values, see EncodeValue

	// hello fields
	Name     string `json:"name,omitempty"`     // human label: editor, cli, serve
	Instance string `json:"instance,omitempty"` // per-process id for echo suppression
	Version  string `json:"version,omitempty"`
}

// Resp is the daemon's reply to a Req with the same ID.
type Resp struct {
	ID       int64    `json:"id"`
	Err      string   `json:"err,omitempty"`
	Cols     []string `json:"cols,omitempty"`
	Rows     [][]any  `json:"rows,omitempty"` // encoded values
	Affected int64    `json:"affected,omitempty"`
	LastID   int64    `json:"lastId,omitempty"`
	Version  string   `json:"version,omitempty"` // hello reply
}

// Event is one committed change, fanned out to every subscriber. Nodes carry
// the fresh post-commit rows (Deleted=true rows are tombstones the client
// removes). Aux signals a change to a render-support table (chips, tag_colors,
// node_spans, settings) — clients reload those wholesale, they are tiny.
// Resync tells a client it missed events and must reload everything.
type Event struct {
	Seq      int64           `json:"seq"`
	Instance string          `json:"instance,omitempty"` // writer's instance id
	Name     string          `json:"name,omitempty"`     // writer's human label
	Nodes    []database.Node `json:"nodes,omitempty"`
	Aux      bool            `json:"aux,omitempty"`
	Resync   bool            `json:"resync,omitempty"`
}

// Msg is the top-level frame: exactly one of Resp or Event.
type Msg struct {
	Resp  *Resp  `json:"resp,omitempty"`
	Event *Event `json:"event,omitempty"`
}

// AuxTables are the render-support tables whose changes flag Event.Aux.
var AuxTables = map[string]bool{
	"chips":      true,
	"tag_colors": true,
	"node_spans": true,
	"settings":   true,
}

// EncodeValue converts a driver-level value into its wire form: nil stays
// null, everything else becomes a tagged string ("i:", "f:", "s:", "b:",
// "d:") so types round-trip through JSON exactly.
func EncodeValue(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case int64:
		return "i:" + strconv.FormatInt(x, 10), nil
	case int:
		return "i:" + strconv.FormatInt(int64(x), 10), nil
	case bool:
		if x {
			return "i:1", nil
		}
		return "i:0", nil
	case float64:
		return "f:" + strconv.FormatFloat(x, 'g', -1, 64), nil
	case string:
		return "s:" + x, nil
	case []byte:
		return "b:" + base64.StdEncoding.EncodeToString(x), nil
	case time.Time:
		return "d:" + x.Format(time.RFC3339Nano), nil
	default:
		return nil, fmt.Errorf("wire: unsupported value type %T", v)
	}
}

// DecodeValue reverses EncodeValue.
func DecodeValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok || len(s) < 2 || s[1] != ':' {
		return nil, fmt.Errorf("wire: malformed value %v", v)
	}
	body := s[2:]
	switch s[0] {
	case 'i':
		return strconv.ParseInt(body, 10, 64)
	case 'f':
		return strconv.ParseFloat(body, 64)
	case 's':
		return body, nil
	case 'b':
		return base64.StdEncoding.DecodeString(body)
	case 'd':
		return time.Parse(time.RFC3339Nano, body)
	default:
		return nil, fmt.Errorf("wire: unknown value tag %q", s[0])
	}
}

// EncodeValues encodes a slice of driver values.
func EncodeValues(in []any) ([]any, error) {
	out := make([]any, len(in))
	for i, v := range in {
		e, err := EncodeValue(v)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

// DecodeValues decodes a slice of wire values.
func DecodeValues(in []any) ([]any, error) {
	out := make([]any, len(in))
	for i, v := range in {
		d, err := DecodeValue(v)
		if err != nil {
			return nil, err
		}
		out[i] = d
	}
	return out, nil
}
