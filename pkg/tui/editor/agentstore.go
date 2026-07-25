package editor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reading the CLI agents' OWN session stores. lflow never keeps a copy of a
// conversation: a session node/chip stores only which CLI and which session id it
// points at, and everything shown about that session — its title, its transcript,
// the color the CLI itself gave it — is read back out of the CLI's store on
// demand (see agent.go for the node/chip side).
//
// Every reader here is deliberately TOLERANT. Each CLI writes its own transcript
// shape, and those shapes move between releases; a record we cannot read is
// skipped, a store we cannot find degrades to "no transcript", and nothing here
// ever fails a render. The one thing we never do is write into a CLI's store.

const (
	agentScanCap   = 400                 // most session files one discovery scan reads
	agentScanDepth = 7                   // how deep a store walk goes below its root
	agentMetaCap   = 64 << 10            // bytes read from a file when only meta is wanted
	agentEntryCap  = 4000                // most transcript entries held for one session
	agentScanAge   = 90 * 24 * time.Hour // sessions older than this are not offered
)

// agentStoreSession is one session discovered in a CLI's own store: what lflow
// needs to offer it in the "attach an existing session" picker and to keep a
// node/chip labelled with the session's live title.
type agentStoreSession struct {
	variant string
	id      string
	title   string    // the CLI's own title/summary for the session; "" = untitled
	cwd     string    // the directory the session ran in, when the store records it
	color   string    // an SGR color the CLI assigned the session; "" = none
	updated time.Time // when the session itself last moved (the store's clock, else the file's)
	modAt   time.Time // the record file's mtime — what a cache checks to skip a re-read
	path    string    // the file (or directory) the session was read from
}

// agentEntry is one line of a session transcript, normalized across CLIs.
type agentEntry struct {
	kind string // "user" | "assistant" | "thinking" | "tool" | "result" | "meta"
	name string // tool name, for kind "tool"
	text string
	at   time.Time
}

// homeStores resolves store paths written relative to the user's home dir,
// dropping the ones that do not exist. XDG_DATA_HOME wins for "<data>/…" paths.
func homeStores(paths ...string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	var out []string
	for _, p := range paths {
		abs := filepath.Join(home, p)
		if rest, ok := strings.CutPrefix(p, "<data>/"); ok {
			abs = filepath.Join(data, rest)
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			out = append(out, abs)
		}
	}
	return out
}

// agentStoreFiles walks a variant's store roots and returns the session record
// paths, newest first. mustContain, when set, is a path fragment a session record
// must sit under ("/session/") — a store that keeps sessions and their messages
// as sibling trees would otherwise offer every message file as a session. The
// walk is bounded (depth, count, age) so a store with years of history cannot
// stall a picker.
func agentStoreFiles(roots []string, exts []string, mustContain string) []string {
	type hit struct {
		path string
		mod  time.Time
	}
	var hits []hit
	cutoff := time.Now().Add(-agentScanAge)
	for _, root := range roots {
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable corner of a store is skipped, never fatal
			}
			if d.IsDir() {
				if strings.Count(filepath.Clean(path), string(filepath.Separator))-rootDepth > agentScanDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if !hasExt(path, exts) {
				return nil
			}
			if mustContain != "" && !strings.Contains(filepath.ToSlash(path), mustContain) {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				return nil
			}
			hits = append(hits, hit{path: path, mod: info.ModTime()})
			return nil
		})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })
	if len(hits) > agentScanCap {
		hits = hits[:agentScanCap]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.path)
	}
	return out
}

func hasExt(path string, exts []string) bool {
	for _, e := range exts {
		if strings.HasSuffix(path, e) {
			return true
		}
	}
	return len(exts) == 0
}

// agentReadMeta reads one session record file's identity: id, title, cwd and any
// color the CLI stamped on it. Only the head of the file is read — a transcript
// carries its identity in its first records, and a multi-megabyte conversation
// must not be paged in to fill a picker row.
func agentReadMeta(variant, path string) agentStoreSession {
	s := agentStoreSession{variant: variant, path: path, id: agentIDFromPath(path)}
	if st, err := os.Stat(path); err == nil {
		s.modAt, s.updated = st.ModTime(), st.ModTime()
	}
	for _, rec := range agentReadRecords(path, agentMetaCap) {
		// a store that keeps its own clock (opencode's session index) is more
		// truthful than the file's mtime, which a copy or a sync would reset
		if t := agentTime(rec); !t.IsZero() {
			s.updated = t
		}
		if id := agentString(rec, "sessionId", "session_id", "sessionID", "id"); id != "" && looksLikeSessionID(id) {
			s.id = id
		}
		if s.cwd == "" {
			s.cwd = agentString(rec, "cwd", "directory", "workingDirectory", "worktree", "path")
		}
		if s.color == "" {
			s.color = agentColorSGR(agentString(rec, "color", "sessionColor", "labelColor"))
		}
		if s.title == "" {
			s.title = clipStr(oneLine(agentString(rec, "summary", "title", "name", "description")), 60)
		}
		if s.title == "" {
			// no titled record: the first user message is the session's own name
			for _, e := range agentDecodeRecord(rec) {
				if e.kind == "user" && strings.TrimSpace(e.text) != "" {
					s.title = clipStr(oneLine(e.text), 60)
					break
				}
			}
		}
	}
	return s
}

// agentIDFromPath derives a session id from its record file name — the shape
// every one of these CLIs uses ("<id>.jsonl", "<id>/…", "<id>.json").
func agentIDFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}

// looksLikeSessionID keeps a stray "id" field (a message id, a tool id) from
// hijacking a session's identity: a session id is one long opaque token.
func looksLikeSessionID(s string) bool {
	if len(s) < 8 || len(s) > 128 || strings.ContainsAny(s, " \t/\\") {
		return false
	}
	return true
}

// agentReadRecords decodes a record file into generic maps. It accepts JSONL
// (one record per line — pi and Claude Code), a single JSON object, or a JSON
// array, so one reader serves every store. limit caps the bytes read (0 = all).
func agentReadRecords(path string, limit int) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, int64(limit))
	}
	br := bufio.NewReaderSize(r, 64<<10)
	// every file is read as JSONL first, since that is what the transcript stores
	// write; a pretty-printed object or an array yields no per-line records and is
	// retried below as one whole document.
	var out []map[string]any
	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	var whole strings.Builder
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if whole.Len() < agentMetaCap {
			whole.WriteString(line)
		}
		if line == "" || (line[0] != '{' && line[0] != '[') {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			out = append(out, rec)
			if len(out) >= agentEntryCap {
				break
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	// not JSONL: one JSON document (an object, or an array of them)
	doc := whole.String()
	var one map[string]any
	if err := json.Unmarshal([]byte(doc), &one); err == nil {
		return []map[string]any{one}
	}
	var many []map[string]any
	if err := json.Unmarshal([]byte(doc), &many); err == nil {
		return many
	}
	return nil
}

// agentTranscript reads a session's transcript: the located path plus its
// entries, oldest first. A missing store yields no entries and an empty path —
// the view says so rather than pretending the session is empty.
func agentTranscript(roots []string, exts []string, id string) ([]agentEntry, string) {
	path := agentTranscriptPath(roots, exts, id)
	if path == "" {
		return nil, ""
	}
	var files []string
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		// a per-session DIRECTORY of message records (opencode): read them in name
		// order, which is creation order for every store that uses this shape.
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if !e.IsDir() && hasExt(e.Name(), exts) {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}
	var out []agentEntry
	for _, f := range files {
		for _, rec := range agentReadRecords(f, 0) {
			out = append(out, agentDecodeRecord(rec)...)
			if len(out) >= agentEntryCap {
				return out, path
			}
		}
	}
	return out, path
}

// agentTranscriptPath locates the record file (or directory) for a session id
// under the given roots. The walk is bounded like agentStoreFiles.
func agentTranscriptPath(roots []string, exts []string, id string) string {
	if id == "" {
		return ""
	}
	for _, root := range roots {
		for _, ext := range append(append([]string{}, exts...), "") {
			if p := filepath.Join(root, id+ext); fileExists(p) {
				return p
			}
		}
		found := ""
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || found != "" {
				return nil //nolint:nilerr // unreadable corners are skipped
			}
			if d.IsDir() {
				if strings.Count(filepath.Clean(path), string(filepath.Separator))-rootDepth > agentScanDepth {
					return filepath.SkipDir
				}
				if d.Name() == id {
					found = path
				}
				return nil
			}
			if agentIDFromPath(path) == id {
				found = path
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// agentNewestSince returns the session id of the newest record touched at or
// after `since` under roots — how lflow ADOPTS the id of a session a CLI minted
// itself (opencode names its own sessions; see agentVariant.assignsID).
func agentNewestSince(roots []string, exts []string, since time.Time) string {
	best, bestMod := "", since.Add(-time.Second)
	for _, root := range roots {
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // unreadable corners are skipped
			}
			if d.IsDir() {
				if strings.Count(filepath.Clean(path), string(filepath.Separator))-rootDepth > agentScanDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if !hasExt(path, exts) {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.ModTime().After(bestMod) {
				return nil
			}
			if id := agentIDFromPath(path); looksLikeSessionID(id) {
				best, bestMod = id, info.ModTime()
			}
			return nil
		})
	}
	return best
}

// ── record decoding ────────────────────────────────────────────────────────

// agentDecodeRecord normalizes ONE transcript record into entries. The shapes it
// knows, in the order they are tried:
//
//   - Claude Code: {"type":"user|assistant","message":{"role","content":[…]},"timestamp"}
//     with content items text / thinking / tool_use / tool_result.
//   - pi (rpc-shaped records): {"type":"tool","toolName":…,"args":{…}} and
//     {"message":{"role","content":[{"type":"text","text":…}]}}.
//   - opencode: {"role":…,"parts":[{"type":"text"|"tool","tool":…,"state":{"input"}}]}.
//   - a summary/title record, which becomes a "meta" entry.
//
// Anything else yields nothing — an unknown record is skipped, never guessed at.
func agentDecodeRecord(rec map[string]any) []agentEntry {
	at := agentTime(rec)
	typ := strings.ToLower(agentString(rec, "type"))
	if typ == "summary" {
		if s := agentString(rec, "summary", "title"); s != "" {
			return []agentEntry{{kind: "meta", text: s, at: at}}
		}
	}

	role := strings.ToLower(agentString(rec, "role"))
	var content any
	if msg, ok := rec["message"].(map[string]any); ok {
		if r := strings.ToLower(agentString(msg, "role")); r != "" {
			role = r
		}
		content = msg["content"]
	}
	if content == nil {
		content = rec["parts"]
	}
	if content == nil {
		content = rec["content"]
	}
	if role == "" && (typ == "user" || typ == "assistant") {
		role = typ
	}

	var out []agentEntry
	// a top-level tool record (pi's rpc stream)
	if name := agentString(rec, "toolName", "tool_name"); name != "" {
		out = append(out, agentEntry{kind: "tool", name: name, text: agentToolDetail(rec["args"]), at: at})
	}

	switch c := content.(type) {
	case string:
		if strings.TrimSpace(c) != "" {
			out = append(out, agentEntry{kind: agentRoleKind(role), text: c, at: at})
		}
	case []any:
		for _, item := range c {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, agentDecodePart(part, role, at)...)
		}
	}
	return out
}

// agentDecodePart normalizes one content/parts item.
func agentDecodePart(part map[string]any, role string, at time.Time) []agentEntry {
	if t := agentTime(part); !t.IsZero() {
		at = t
	}
	switch strings.ToLower(agentString(part, "type")) {
	case "text":
		if s := agentString(part, "text"); strings.TrimSpace(s) != "" {
			return []agentEntry{{kind: agentRoleKind(role), text: s, at: at}}
		}
	case "thinking", "reasoning":
		if s := agentString(part, "thinking", "text"); strings.TrimSpace(s) != "" {
			return []agentEntry{{kind: "thinking", text: s, at: at}}
		}
	case "tool_use", "tool", "tool-call", "tool_call":
		name := agentString(part, "name", "tool", "toolName")
		input := part["input"]
		if input == nil {
			input = part["args"]
		}
		if state, ok := part["state"].(map[string]any); ok && input == nil {
			input = state["input"]
		}
		return []agentEntry{{kind: "tool", name: name, text: agentToolDetail(input), at: at}}
	case "tool_result", "tool-result":
		return []agentEntry{{kind: "result", text: agentToolDetail(part["content"]), at: at}}
	}
	return nil
}

func agentRoleKind(role string) string {
	switch role {
	case "user", "human":
		return "user"
	case "assistant", "model", "ai":
		return "assistant"
	case "system":
		return "meta"
	}
	return "assistant"
}

// agentToolDetail renders a tool's arguments as one short human line — the
// command, path or pattern that says what the call actually did.
func agentToolDetail(v any) string {
	switch t := v.(type) {
	case string:
		return clipStr(oneLine(t), 90)
	case []any:
		var parts []string
		for _, item := range t {
			if s := agentToolDetail(item); s != "" {
				parts = append(parts, s)
			}
		}
		return clipStr(strings.Join(parts, " "), 90)
	case map[string]any:
		// order matters: a search call carries both a pattern and the path it
		// searched, and the pattern is what says what the call was for
		for _, key := range []string{"command", "cmd", "pattern", "query", "file_path", "filePath", "path", "url", "description", "prompt", "text", "content"} {
			if s := agentString(t, key); strings.TrimSpace(s) != "" {
				return clipStr(oneLine(s), 90)
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			if s, ok := t[k].(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, k+"="+oneLine(s))
			}
			if len(parts) == 2 {
				break
			}
		}
		return clipStr(strings.Join(parts, " "), 90)
	}
	return ""
}

// agentString returns the first non-empty string field among keys.
func agentString(rec map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := rec[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// agentTime reads a record's timestamp: RFC3339 text, unix seconds/millis, or a
// nested {"created"/"updated"} clock (opencode). Zero when absent.
func agentTime(rec map[string]any) time.Time {
	for _, k := range []string{"timestamp", "time", "createdAt", "created_at", "date"} {
		switch v := rec[k].(type) {
		case string:
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05"} {
				if t, err := time.Parse(layout, v); err == nil {
					return t
				}
			}
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return agentEpoch(float64(n))
			}
		case float64:
			return agentEpoch(v)
		case map[string]any:
			for _, kk := range []string{"created", "updated", "start"} {
				if n, ok := v[kk].(float64); ok {
					return agentEpoch(n)
				}
			}
		}
	}
	return time.Time{}
}

// agentEpoch reads a numeric timestamp whose unit the store does not declare:
// seconds, milliseconds and microseconds are all in the wild, and their
// magnitudes tell them apart.
func agentEpoch(n float64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n > 1e17:
		return time.Unix(0, int64(n))
	case n > 1e14:
		return time.UnixMicro(int64(n))
	case n > 1e11:
		return time.UnixMilli(int64(n))
	default:
		return time.Unix(int64(n), 0)
	}
}

// agentColorSGR turns a color a CLI recorded for a session into an SGR sequence:
// a /style color name, a #rrggbb hex, or an "r,g,b" triple. Unrecognized values
// yield "" and the variant's own color stands.
func agentColorSGR(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	if c, ok := styleColorCode[v]; ok {
		return c
	}
	switch v { // common aliases the palette does not name
	case "magenta", "pink", "violet":
		return styleColorCode["purple"]
	case "teal", "aqua":
		return styleColorCode["cyan"]
	case "grey", "white":
		return styleColorCode["gray"]
	}
	if r, g, b, ok := parseHexColor(v); ok {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	}
	return ""
}

// parseHexColor accepts "#rgb", "#rrggbb" and "r,g,b".
func parseHexColor(v string) (int, int, int, bool) {
	if h, ok := strings.CutPrefix(v, "#"); ok {
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 {
			return 0, 0, 0, false
		}
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return 0, 0, 0, false
		}
		return int(n >> 16 & 0xff), int(n >> 8 & 0xff), int(n & 0xff), true
	}
	parts := strings.Split(v, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var rgb [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			return 0, 0, 0, false
		}
		rgb[i] = n
	}
	return rgb[0], rgb[1], rgb[2], true
}
