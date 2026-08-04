package editor

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reading the CLI agents' OWN session stores. lflow never keeps a copy of a
// conversation: a session chip stores only which CLI and which session id it
// points at, and what it shows about that session — its name, whether it is
// running, the color the CLI gave it — is read back out of the CLI's store on
// demand (see agent.go for the chip side). The conversation itself is never read:
// it belongs to the CLI.
//
// Every reader here is deliberately TOLERANT. Each CLI writes its own record
// shape, and those shapes move between releases; a record we cannot read is
// skipped, a store we cannot find degrades to "no session found", and nothing
// here ever fails a render.
//
// One writer lives here and only one: opencodeRename, which edits the "title" of
// an opencode session record because that store is a single small JSON object and
// that field is exactly what its own UI edits. Nothing else writes into a CLI's
// store, and no writer ever touches a conversation.

const (
	agentScanCap   = 400                 // most session files one discovery scan reads
	agentScanDepth = 7                   // how deep a store walk goes below its root
	agentMetaCap   = 64 << 10            // bytes read from a record file: its head names the session
	agentScanAge   = 90 * 24 * time.Hour // sessions older than this are not offered
	agentTitleCap  = 200                 // sanity bound on a session name, not a display width
)

// agentStoreSession is one session discovered in a CLI's own store: what lflow
// needs to offer it in the "attach an existing session" picker and to keep a chip
// labelled with the session's live name.
type agentStoreSession struct {
	variant string
	id      string
	title   string    // what the session is CALLED; "" = nothing to call it
	named   bool      // the CLI declared that name, rather than it being a first prompt
	color   string    // the color the CLI gave the session; "" = it records none
	cwd     string    // the directory the session ran in, when the store records it
	updated time.Time // when the session itself last moved (the store's clock, else the file's)
	modAt   time.Time // the record file's mtime — what a cache checks to skip a re-read
	path    string    // the file (or directory) the session was read from
}

// agentReg is one entry of a CLI's LIVE session registry (Claude Code writes
// ~/.claude/sessions/<pid>.json while a session runs): the session's own name and
// whether it is running right now.
type agentReg struct {
	id   string
	name string
	cwd  string
	live bool
}

// agentIdent is what a CLI records ABOUT a session, as distinct from what the
// conversation contains: the name it goes by, and the color it was given.
//
// The three CLIs keep this in three different places, and none of them is the
// transcript — which is why a session read only from its transcript came back
// nameless and got labelled with its first prompt instead:
//
//	claude    ~/.claude/jobs/<short>/state.json — name AND color, keyed by
//	          sessionId. The only one of the three that records a color.
//	pi        a {"type":"session_info","name":…} record appended to the
//	          session's own JSONL; the last one written is the current name.
//	opencode  the "title" field of the session record itself.
type agentIdent struct{ name, color string }

// registryIdents reads session names out of a CLI's live-session registry. The
// files outlive the processes that wrote them, so this names sessions the job
// store has no job for — a plain interactive run rather than a dispatched one.
func registryIdents(roots []string) map[string]agentIdent {
	out := map[string]agentIdent{}
	for id, reg := range agentRegistry(roots) {
		if reg.name != "" {
			out[id] = agentIdent{name: clipStr(oneLine(reg.name), agentTitleCap)}
		}
	}
	return out
}

// mergeIdents folds several indexes into one, earlier arguments winning: a
// session named in the job store keeps that name even when a registry entry also
// has one, and a later index only ever fills a gap or a missing field.
func mergeIdents(idx ...map[string]agentIdent) map[string]agentIdent {
	out := map[string]agentIdent{}
	for i := len(idx) - 1; i >= 0; i-- {
		for id, v := range idx[i] {
			cur := out[id]
			if v.name != "" {
				cur.name = v.name
			}
			if v.color != "" {
				cur.color = v.color
			}
			out[id] = cur
		}
	}
	return out
}

// applyIdent lays a CLI's own record of a session over what the transcript said.
// A declared name wins, because a first prompt is only ever a stand-in for one;
// an empty ident changes nothing, so a session the index does not know keeps
// whatever reading it produced.
func (s *agentStoreSession) applyIdent(id agentIdent) {
	if id.name != "" {
		s.title, s.named = id.name, true
	}
	if id.color != "" {
		s.color = id.color
	}
}

// claudeIdents reads Claude Code's job store into a sessionId-keyed index. Each
// job is one small JSON object, so the whole index costs a directory walk and a
// few hundred short reads — cheap enough to build per picker scan, unlike the
// transcripts, which are megabytes each.
func claudeIdents(roots []string) map[string]agentIdent {
	out := map[string]agentIdent{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			for _, rec := range agentReadRecords(filepath.Join(root, e.Name(), "state.json"), agentMetaCap) {
				id := agentString(rec, "sessionId", "resumeSessionId")
				if id == "" {
					continue
				}
				out[id] = agentIdent{
					name:  clipStr(oneLine(agentString(rec, "name")), agentTitleCap),
					color: agentString(rec, "color"),
				}
			}
		}
	}
	return out
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
	// Every record in a JSONL transcript carries its OWN id — pi stamps one on
	// each message — so the session's id is taken once and never overwritten,
	// and an explicit session-id key always beats a bare "id".
	named, anyID := false, false
	for _, rec := range append(agentReadRecords(path, agentMetaCap), agentReadTail(path, agentMetaCap)...) {
		// a store that keeps its own clock (opencode's session index) is more
		// truthful than the file's mtime, which a copy or a sync would reset
		if t := agentTime(rec); !t.IsZero() {
			s.updated = t
		}
		if id := agentString(rec, "sessionId", "session_id", "sessionID"); id != "" && looksLikeSessionID(id) && !named {
			s.id, named, anyID = id, true, true
		}
		if id := agentString(rec, "id"); id != "" && looksLikeSessionID(id) && !anyID {
			s.id, anyID = id, true
		}
		if s.cwd == "" {
			s.cwd = agentString(rec, "cwd", "directory", "workingDirectory", "worktree", "path")
		}
		// A name the CLI DECLARED always beats a first prompt, wherever in the
		// file it sits — pi appends its {"type":"session_info","name":…} record
		// when you rename, which is long after the prompt that would otherwise
		// have named the session, and the last one written is the current name.
		//
		// The name is kept WHOLE here and clipped only where it is drawn: the
		// panel shows all of it, wrapped, and only the pill and the picker row
		// shorten it. agentTitleCap is a sanity bound, not a display width — a
		// first prompt can be an entire pasted file.
		// name and color are taken independently: Claude Code records a recolor as
		// its own record carrying no name at all, so requiring one dropped it
		if d := agentDeclaredIdent(rec); d.name != "" || d.color != "" {
			if d.name != "" {
				s.title, s.named = clipStr(oneLine(d.name), agentTitleCap), true
			}
			if d.color != "" {
				s.color = d.color
			}
		}
		if s.title == "" {
			// nothing named it: the first thing asked of it does instead
			if p := agentFirstPrompt(rec); p != "" {
				s.title = clipStr(oneLine(p), agentTitleCap)
			}
		}
	}
	return s
}

// agentDeclaredName returns the name a record gives THE SESSION, or "" when the
// record is about anything else.
//
// The gate matters more than it looks. A transcript is full of records that
// carry a "name" or a "summary" without naming the session: pi's outline
// extension writes {"type":"node","name":…} for every node it touches, and a
// compaction writes a summary of what it dropped. Reading any of those as the
// session's name labelled sessions with a stray node's title.
//
// So only these shapes count — a record whose TYPE says it is about the session,
// and a whole-document session record whose own id is the session's (opencode,
// whose file IS the session object and whose "title" is what its UI edits).
//
// Note it is the type that is read, never the field: pi's compaction record
// carries a "summary" of what it dropped, while a record whose type IS "summary"
// is a summary OF THE SESSION. Same field, opposite meanings.
func agentDeclaredName(rec map[string]any) string { return agentDeclaredIdent(rec).name }

// agentDeclaredIdent is agentDeclaredName plus any color the same record carries
// — a store that names a session in its own records is also where it would
// record a color for one, so both are read through the same gate.
func agentDeclaredIdent(rec map[string]any) agentIdent {
	switch strings.ToLower(agentString(rec, "type")) {
	// Claude Code renames and recolors an INTERACTIVE session by appending these
	// to the transcript itself — the job store only ever knew about dispatched
	// ones, which is why a named, colored session still read as its first prompt
	// in the agent's own default color. Each is written afresh on every change,
	// so the last one in the file is the current one.
	case "custom-title":
		return agentIdent{name: agentString(rec, "customTitle", "title")}
	case "agent-name":
		return agentIdent{name: agentString(rec, "agentName", "name")}
	case "agent-color":
		return agentIdent{color: agentString(rec, "agentColor", "color")}

	case "session_info", "session":
		return named(agentString(rec, "name", "title"), rec)
	case "summary":
		return named(agentString(rec, "summary", "title", "name"), rec)
	case "":
		if id := agentString(rec, "id"); looksLikeSessionID(id) {
			return named(agentString(rec, "title", "name"), rec)
		}
	}
	return agentIdent{}
}

// named pairs a name with any color the same record carries — a store that names
// a session in its own records is also where it would record a color for one.
func named(name string, rec map[string]any) agentIdent {
	if name == "" {
		return agentIdent{}
	}
	return agentIdent{name: name, color: agentString(rec, "color")}
}

// agentInjectedTags are the wrapper blocks a CLI's own harness writes INTO a user
// turn: caveats, hook output, slash-command echoes, task notifications, IDE
// context. They arrive as user records but they are not what the user typed, so
// they must never name a session — "<system-reminder> The user named this s…" is
// not a name anyone can search for.
var agentInjectedTags = []string{
	"system-reminder", "local-command-caveat", "local-command-stdout",
	"command-name", "command-message", "command-args", "command-contents",
	"task-notification", "user-prompt-submit-hook", "session-start-hook",
	"ide-selection", "ide-opened-file",
}

// agentPromptText strips those wrappers and returns what the user actually wrote,
// or "" when the record was entirely machine-inserted — in which case the caller
// keeps looking at later records for a real prompt.
func agentPromptText(text string) string {
	s := strings.TrimSpace(text)
	for range agentInjectedTags { // bounded: one turn carries a handful of wrappers
		tag := ""
		for _, t := range agentInjectedTags {
			if strings.HasPrefix(s, "<"+t+">") {
				tag = t
				break
			}
		}
		if tag == "" {
			break
		}
		j := strings.Index(s, "</"+tag+">")
		if j < 0 {
			return "" // unterminated: the wrapper IS the whole record
		}
		s = strings.TrimSpace(s[j+len(tag)+3:])
	}
	// a wrapper this list has not learned yet: same machinery, newer tag. The
	// convention is a bare lowercase-with-hyphens element, which prose does not
	// open with; a session named for one would be unsearchable anyway.
	if strings.HasPrefix(s, "<") {
		if i := strings.Index(s, ">"); i > 1 {
			if head := s[1:i]; head == strings.ToLower(head) && !strings.ContainsAny(head, " \t=\"/") {
				return ""
			}
		}
	}
	return s
}

// agentFirstPrompt returns a record's user text, when it holds one — the fallback
// name for a session its CLI never titled. Deliberately shallow: it reads the
// shapes these stores actually write and gives up on anything else.
func agentFirstPrompt(rec map[string]any) string {
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
	if role != "user" && role != "human" {
		return ""
	}
	switch c := content.(type) {
	case string:
		return agentPromptText(c)
	case []any:
		for _, item := range c {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.EqualFold(agentString(part, "type"), "text") {
				if t := agentPromptText(agentString(part, "text")); t != "" {
					return t
				}
			}
		}
	}
	return ""
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

// agentReadTail decodes the LAST limit bytes of a JSONL record file. A rename is
// appended, not written into the head — pi's {"type":"session_info","name":…}
// lands after however much conversation came before it — so the head read alone
// always reported the old name, or a first prompt where a name existed.
//
// Only the tail is read, never the middle: a transcript runs to megabytes and a
// picker cannot page one in per row. The first line is dropped because a seek
// lands mid-record, and a file that fits inside limit yields nothing here since
// the head read already covered all of it.
func agentReadTail(path string, limit int) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() || st.Size() <= int64(limit) {
		return nil // already read whole by the head pass
	}
	if _, err := f.Seek(-int64(limit), io.SeekEnd); err != nil {
		return nil
	}

	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first { // the seek cut this one in half
			first = false
			continue
		}
		if line == "" || line[0] != '{' {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

// agentInt reads the first numeric field among keys.
func agentInt(rec map[string]any, keys ...string) int {
	for _, k := range keys {
		switch v := rec[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		}
	}
	return 0
}

// agentRegistry reads a CLI's live-session registry: which sessions are running
// right now and the names the CLI gave them. Claude Code writes one file per
// running session, and that file is the only place its session names live; a CLI
// without a registry contributes nothing and its sessions are named from the
// store instead.
func agentRegistry(roots []string) map[string]agentReg {
	out := map[string]agentReg{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			for _, rec := range agentReadRecords(filepath.Join(root, e.Name()), agentMetaCap) {
				id := agentString(rec, "sessionId", "session_id", "id")
				if id == "" {
					continue
				}
				out[id] = agentReg{
					id:   id,
					name: agentString(rec, "name", "title"),
					cwd:  agentString(rec, "cwd", "directory"),
					live: agentPidAlive(agentInt(rec, "pid")),
				}
			}
		}
	}
	return out
}

// agentPidAlive reports whether a registry entry's process is still running —
// a stale file from a crashed session must not read as live.
func agentPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM is not "gone": the process is THERE and simply not ours to signal.
	// A session running as another user is still a running session, and reading
	// it as dead would quietly mark a live chip idle.
	return errors.Is(err, syscall.EPERM)
}

// agentSessionPath locates the record file (or directory) for a session id under
// the given roots — how a chip finds the session it points at. The walk is
// bounded like agentStoreFiles.
func agentSessionPath(roots []string, exts []string, id string) string {
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
			base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			// Pi prefixes transcript filenames with an ISO timestamp
			// (timestamp_<session-id>.jsonl). The picker learns the real id from
			// the session header, so later trace reads must recognize that suffix
			// rather than looking only for <session-id>.jsonl.
			if base == id || strings.HasSuffix(base, "_"+id) {
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

// ── writing back ───────────────────────────────────────────────────────────
//
// Renaming or recoloring a chip pushes the change down into the CLI's own store,
// so the two agree rather than lflow keeping a private label. Each CLI takes it
// where its own format allows, and no writer here ever touches a conversation:
//
//	claude    the job record's "name" and "color" — the only CLI of the three
//	          that keeps a color. Refused while the session is RUNNING, because
//	          that file belongs to the process writing it.
//	pi        an appended session_info record, which is exactly what pi writes
//	          when you rename in it. Names only; pi records no color.
//	opencode  the session record's "title". Names only.
//
// Every rewrite round-trips the whole object and replaces it atomically, so a
// field lflow does not know about survives untouched.

// opencodeWriteIdent writes a new title into an opencode session record: a
// single small JSON object whose "title" is exactly what its own UI edits.
func opencodeWriteIdent(_, path string, want agentIdent) error {
	if want.name == "" {
		return nil // opencode records no color, so there is nothing else to write
	}
	return rewriteJSON(path, func(rec map[string]any) error {
		if _, ok := rec["title"]; !ok {
			return fmt.Errorf("no title to rename")
		}
		rec["title"] = want.name
		return nil
	})
}

// piWriteIdent appends a session_info record naming the session — the same
// record pi itself appends on a rename, and the last one written is the name
// that counts. Appending is why this is safe: nothing already in the transcript
// is rewritten, and a session pi has open sees the record the way it sees its
// own.
func piWriteIdent(_, path string, want agentIdent) error {
	if want.name == "" {
		return nil // pi records no color
	}
	rec := map[string]any{
		"type":      "session_info",
		"id":        randomHex(8),
		"parentId":  nil,
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"name":      want.name,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Close()
}

// claudeWriteIdent writes a name and/or color into Claude Code's job record —
// the place its own UI reads both from.
//
// It REFUSES while the session is running. That file is live state belonging to
// the process writing it (its status, its in-flight counts, its clock all move
// during a run), and replacing it under that process could drop an update it
// made a moment ago. A finished session's record is nobody's to lose.
func claudeWriteIdent(sessionID, path string, want agentIdent) error {
	if want.name == "" && want.color == "" {
		return nil
	}
	wrote := false

	// The transcript first, because that is where an INTERACTIVE session's name
	// and color live and most sessions are that. Appending is what Claude Code
	// itself does on every rename, and these records carry no parent link, so one
	// more of them changes nothing about the conversation's shape.
	if path != "" {
		if err := claudeAppendIdent(path, sessionID, want); err != nil {
			return err
		}
		wrote = true
	}

	// then the job record, for a session that was DISPATCHED as one — that is
	// where Claude Code's own agent list reads from, so the two must agree.
	if job := claudeJobPath(homeStores(".claude/jobs"), sessionID); job != "" {
		// the registry is read here rather than passed in: a write-back is
		// something you do by hand, not something a frame does
		if reg, ok := agentRegistry(homeStores(".claude/sessions"))[sessionID]; ok && reg.live {
			if !wrote {
				return fmt.Errorf("the session is running — Claude Code owns its job record while it does")
			}
			return nil // the transcript took it; the job record is the running one's
		}
		if err := rewriteJSON(job, func(rec map[string]any) error {
			if want.name != "" {
				rec["name"] = want.name
				rec["nameSource"] = "user" // what Claude Code stamps on a name you chose
			}
			if want.color != "" {
				rec["color"] = want.color
			}
			return nil
		}); err != nil {
			return err
		}
		wrote = true
	}

	if !wrote {
		return fmt.Errorf("no record found for this session")
	}
	return nil
}

// claudeAppendIdent appends the records Claude Code writes when you rename or
// recolor a session: a name goes in as both custom-title and agent-name, which
// is the pair it writes together, and a color as agent-color. Each is written
// afresh on every change and the last in the file wins, so appending IS the
// edit — nothing already in the transcript is touched.
func claudeAppendIdent(path, sessionID string, want agentIdent) error {
	var recs []map[string]any
	if want.name != "" {
		recs = append(recs,
			map[string]any{"type": "custom-title", "customTitle": want.name, "sessionId": sessionID},
			map[string]any{"type": "agent-name", "agentName": want.name, "sessionId": sessionID},
		)
	}
	if want.color != "" {
		recs = append(recs,
			map[string]any{"type": "agent-color", "agentColor": want.color, "sessionId": sessionID},
		)
	}
	if len(recs) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, rec := range recs {
		line, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return f.Close()
}

// claudeJobPath finds the job record for a session id. The directory is named
// for a short form of the id, but that is read as a hint rather than a rule: the
// sessionId inside each record is what actually answers.
func claudeJobPath(roots []string, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(root, e.Name(), "state.json")
			for _, rec := range agentReadRecords(p, agentMetaCap) {
				if agentString(rec, "sessionId") == sessionID || agentString(rec, "resumeSessionId") == sessionID {
					return p
				}
			}
		}
	}
	return ""
}

// rewriteJSON applies edit to a JSON object file and replaces it atomically,
// keeping every key the edit did not touch.
func rewriteJSON(path string, edit func(map[string]any) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		return fmt.Errorf("unreadable session record")
	}
	if err := edit(rec); err != nil {
		return err
	}
	out, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := path + ".lflow.tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// randomHex is the id shape pi stamps on a record it appends.
func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)[:n]
	}
	return hex.EncodeToString(b)[:n]
}
