package editor

import (
	"encoding/json"
	"os"
	"strings"
)

// The read-only trace readers: the CLI's own transcript, parsed tolerantly
// into one-line events. The quick-reply box reads the LAST assistant line
// here; nothing is ever copied into the outline or sync state. (The alt+e
// trace band these once rendered is gone — the chip's surfaces are ⌥r, ⌥m,
// ⌥o and the ⌥e edit page.)

const agentTraceCap = 4 << 20

type agentHandle struct {
	id   string
	v    agentVariant
	sess agentSession
}

type agentTrace struct {
	kind string
	text string
}

func (m *Model) refreshAgentTrace(h agentHandle) {
	m.nodeStore(h.id)["agentTrace"] = readAgentTrace(h.v, h.sess)
}

func (m *Model) agentTraces(h agentHandle) []agentTrace {
	if traces, ok := m.nodeStore(h.id)["agentTrace"].([]agentTrace); ok {
		return traces
	}
	m.refreshAgentTrace(h)
	traces, _ := m.nodeStore(h.id)["agentTrace"].([]agentTrace)
	return traces
}

func readAgentTrace(v agentVariant, s agentSession) []agentTrace {
	if db := v.openAgentDB(); db != nil {
		defer db.Close()
		return db.trace(s.SessionID)
	}
	path := agentSessionPath(v.sessionDirs(), v.exts, s.SessionID)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil
	}
	var records []map[string]any
	if st.Size() <= agentTraceCap {
		records = agentReadRecords(path, 0)
	} else {
		records = agentReadTail(path, agentTraceCap)
	}
	var out []agentTrace
	for _, rec := range records {
		out = append(out, agentRecordTrace(rec)...)
	}
	return out
}

func agentRecordTrace(rec map[string]any) []agentTrace {
	role := strings.ToLower(agentString(rec, "role", "type"))
	content := rec["content"]
	if msg, ok := rec["message"].(map[string]any); ok {
		if r := strings.ToLower(agentString(msg, "role")); r != "" {
			role = r
		}
		content = msg["content"]
	}
	if content == nil {
		content = rec["parts"]
	}
	kind := role
	switch role {
	case "human":
		kind = "user"
	case "message":
		kind = strings.ToLower(agentString(rec, "role"))
	case "summary", "session", "session_info", "custom-title", "agent-name", "agent-color":
		return nil
	}
	if kind != "user" && kind != "assistant" {
		if strings.Contains(role, "tool") {
			out := agentContentTrace("result", content)
			if len(out) > 0 {
				return out
			}
			text := agentString(rec, "name", "tool", "toolName", "command", "output", "result")
			if text != "" {
				return []agentTrace{{kind: "tool", text: oneLine(text)}}
			}
		}
		return nil
	}
	return agentContentTrace(kind, content)
}

func agentContentTrace(role string, content any) []agentTrace {
	switch value := content.(type) {
	case string:
		text := strings.TrimSpace(value)
		if role == "user" {
			text = agentPromptText(text)
		}
		if text == "" {
			return nil
		}
		return []agentTrace{{kind: role, text: oneLine(text)}}
	case []any:
		var out []agentTrace
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ := strings.ToLower(agentString(part, "type"))
			switch typ {
			case "text", "input_text", "output_text":
				out = append(out, agentContentTrace(role, agentString(part, "text"))...)
			case "thinking", "reasoning":
				if text := oneLine(agentString(part, "thinking", "text")); text != "" {
					out = append(out, agentTrace{kind: "thinking", text: text})
				}
			case "tool_use", "tool_call", "toolcall", "function_call", "functioncall":
				text := agentString(part, "name", "tool")
				if args := part["input"]; args != nil {
					text = strings.TrimSpace(text + " " + compactAgentValue(args))
				} else if args := part["arguments"]; args != nil {
					text = strings.TrimSpace(text + " " + compactAgentValue(args))
				}
				out = append(out, agentTrace{kind: "tool", text: text})
			case "tool_result", "toolresult", "function_result", "functionresult":
				result := part["content"]
				if result == nil {
					result = part["result"]
				}
				text := compactAgentValue(result)
				if text != "" {
					out = append(out, agentTrace{kind: "result", text: text})
				}
			}
		}
		return out
	}
	return nil
}

func compactAgentValue(v any) string {
	if s, ok := v.(string); ok {
		return clipStr(oneLine(s), 400)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return clipStr(string(b), 400)
}
