package editor

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The @mention agent surface (see pkg/tui/tag and AGENTS.md).
//
// Two trigger rules, nothing else:
//  1. alt+r on the node that mentions a configured agent is the manual fire —
//     always; it starts the session or re-sends the thread as it reads now.
//  2. any local change to a DESCENDANT of a session-bound mention arms a
//     debounce (agentThinkEvery); when it settles the agent re-reads the
//     thread and decides whether to reply (PASS is fine). Cursor-leave and
//     Enter do not ship on their own.
// The mention node itself is the thread root: the session binds to it, so
// siblings and ancestors never trigger a turn or receive replies. Context per
// turn = the mention's parent (one ambient line, marked Parent) + the mention
// + everything beneath it — nothing else; the agent searches the rest of the
// outline itself via the lflow CLI.

// agentGlyph is the agent reply marker: a red ✦ — the general AI sparkle mark for the
// default agent Pi. (When a second agent exists, the author will need to be
// recorded on the node so each agent can wear its own mark.)
func agentGlyph(it *item) (string, string) {
	return "✦", cRed
}

var mentionRe = regexp.MustCompile(`@([A-Za-z][A-Za-z0-9_-]*)`)

// mentionedAgent returns the configured agent mentioned in the text.
func (m *Model) mentionedAgent(text string) (tag.Agent, bool) {
	for _, match := range mentionRe.FindAllStringSubmatch(text, -1) {
		for _, a := range m.agents {
			if a.Name == match[1] {
				return a, true
			}
		}
	}
	return tag.Agent{}, false
}

// mentionsName reports whether the text mentions exactly this agent name.
func mentionsName(text, name string) bool {
	for _, match := range mentionRe.FindAllStringSubmatch(text, -1) {
		if match[1] == name {
			return true
		}
	}
	return false
}

// threadSessionAt reports whether a VALID session binds this node to the
// agent. Valid means the node still mentions the agent — a session lives on
// the @-chipped node and nowhere else, so rows left by the earlier
// parent-binding scheme (or a mention edited away) must not keep an invisible
// thread alive, silently swallowing every new node beneath them. Stale rows
// are deleted on sight: the DB self-heals as it is read.
func (m *Model) threadSessionAt(p *item, agentName string) bool {
	if _, ok, _ := database.GetThreadSession(m.db, p.uuid, agentName); !ok {
		return false
	}
	if mentionsName(expandAnchors(p.name, m.chips), agentName) {
		return true
	}
	_ = database.DeleteThreadSession(m.db, p.uuid, agentName)
	return false
}

// threadRootFor resolves the conversation a message belongs to: an existing
// session on the node or any ancestor wins (follow-ups continue it), and a
// fresh mention roots ITS OWN thread — the session binds to the mention node,
// so only the mention's descendants are ever in-thread. Siblings and parents
// stay outside: they neither trigger turns nor receive replies.
func (m *Model) threadRootFor(it *item, ag tag.Agent) *item {
	for p := it; p != nil; p = p.parent {
		if p.uuid == "" {
			break
		}
		if m.threadSessionAt(p, ag.Name) {
			return p
		}
	}
	return it
}

// buildThread flattens the thread context: the mention's parent first (one
// Parent-marked line — where the thread sits in the outline), then the root
// (the mention node) and its subtree depth-first. Nothing else is sent;
// anything else in the outline the agent fetches itself through the lflow CLI
// (see cliSystemPrompt). askedUUID marks the node this turn is about, so
// replies can target it. Every node's children are emitted at most ONCE —
// the expanded set is global to the walk, not per-path — so a mirror pointing
// back at an ancestor can't loop, and two mirrors of the same source can't
// duplicate its subtree. A priority-up node shows its newest children on TOP,
// so the walk inverts them: the agent always reads the conversation
// chronologically, oldest first (see cliSystemPrompt).
func (m *Model) buildThread(root *item, askedUUID string) []tag.ThreadNode {
	var out []tag.ThreadNode

	roleOf := func(it *item) string {
		if it.typ == database.TypeAgent {
			return "agent"
		}
		return "user"
	}
	// typeXML folds the type's toContext hook (registry) into the wire node,
	// so a todo carries done="…", a log its time, a json its multi-line body.
	// The Model-aware toContextM wins when set (a canvas body loads from its
	// blob).
	typeXML := func(tn tag.ThreadNode, it *item) tag.ThreadNode {
		nt := typeOf(it.typ)
		switch {
		case nt.toContextM != nil:
			x := nt.toContextM(m, it)
			tn.XMLTag, tn.XMLAttrs, tn.XMLBody = x.tag, x.attrs, x.body
		case nt.toContext != nil:
			x := nt.toContext(it)
			tn.XMLTag, tn.XMLAttrs, tn.XMLBody = x.tag, x.attrs, x.body
		}
		return tn
	}
	rootDepth := 0
	if p := root.parent; p != nil && p.uuid != "" {
		out = append(out, typeXML(tag.ThreadNode{
			UUID: p.uuid, Depth: 0, Name: expandAnchors(m.tree.displayName(p), m.chips),
			Type: p.typ, Role: roleOf(p), Parent: true,
		}, p))
		rootDepth = 1
	}
	expanded := map[*item]bool{}
	var walk func(it *item, depth int)
	walk = func(it *item, depth int) {
		out = append(out, typeXML(tag.ThreadNode{
			UUID: it.uuid, Depth: depth, Name: expandAnchors(m.tree.displayName(it), m.chips),
			Type: it.typ, Role: roleOf(it), Asked: it.uuid == askedUUID,
		}, it))
		tgt := m.tree.expandTarget(it)
		if tgt == nil || expanded[tgt] {
			return
		}
		expanded[tgt] = true
		kids := m.tree.childItems(it)
		if tgt.priority == database.PriorityUp {
			// newest-on-top in the outline → oldest-first for the agent
			for i := len(kids) - 1; i >= 0; i-- {
				walk(kids[i], depth+1)
			}
		} else {
			for _, c := range kids {
				walk(c, depth+1)
			}
		}
	}
	walk(root, rootDepth)
	return out
}

// forceThreadPriorityDown pins an agent-chipped node to priority down: an
// agent conversation always reads top-down chronological, so a mention node
// never inverts — /priority:up refuses it, and a node that was up before the
// chip landed converts to down on contact (chip completion, thread send).
func (m *Model) forceThreadPriorityDown(it *item) {
	if it == nil || it.priority == database.PriorityDown {
		return
	}
	it.priority = database.PriorityDown
	if m.db != nil {
		_ = database.SetPriority(m.db, it.uuid, database.PriorityDown)
	}
}

// agentEvMsg carries one streamed event into the update loop.
type agentEvMsg struct {
	thread string // thread root uuid
	asked  string // the node this turn is about — replies target it
	agent  string
	ev     tag.Event
	ch     <-chan tag.Event
}

// agentStreamEndMsg fires when the event channel closes.
type agentStreamEndMsg struct{ thread string }

func waitAgentCmd(thread, asked, agent string, ch <-chan tag.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentStreamEndMsg{thread: thread}
		}
		return agentEvMsg{thread: thread, asked: asked, agent: agent, ev: ev, ch: ch}
	}
}

// sendThread ships the thread to the agent, launch-and-forget: every turn is
// a fresh agent fed the whole thread as it reads NOW (no remote session to
// drift from edited nodes). asked is the node this turn is about (the
// mention, or a detected follow-up question).
func (m *Model) sendThread(asked *item, ag tag.Agent) tea.Cmd {
	root := m.threadRootFor(asked, ag)
	if t := m.thread(root.uuid); t != nil && t.busy {
		return nil
	}
	// the thread root is an agent-chipped node: forced down, always chronological
	m.forceThreadPriorityDown(root)
	m.agentErr = "" // a fresh attempt clears the last failure

	if m.tagClients == nil {
		m.tagClients = map[string]tag.Client{}
	}
	// NodeCLIDeps gate: a missing backend fails fast with the same error the
	// daemon would return, before any conn or process is attempted
	if bin, missing := m.agentDepMissing(ag); missing {
		m.agentErr = "Missing dependency: " + bin
		return nil
	}
	client, ok := m.tagClients[ag.Name]
	if !ok {
		// the turn runs on the daemon when one is connected — the editor is
		// only a client; mock/websocket agents and daemon-less runs keep their
		// own transport (see tagClientFor)
		c, err := m.tagClientFor(ag)
		if err != nil {
			m.agentErr = err.Error() // no backend for @Name — surfaced in the bar
			return nil
		}
		client = c
		m.tagClients[ag.Name] = client
	}

	// a cancelable context per turn so flash "stop" (or a re-send) can kill the
	// in-flight CLI — the process is scoped to this ctx all the way down.
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := client.Send(ctx, ag.Name, m.buildThread(root, asked.uuid))
	if err != nil {
		cancel()
		m.agentErr = err.Error()
		return nil
	}
	t := m.ensureThread(root.uuid)
	t.cancel = cancel
	t.busy = true
	// record the LOCAL thread binding (node ↔ agent) — what makes follow-ups
	// inside this subtree reach the agent, across editor restarts too
	m.touchThread(root.uuid, ag.Name, "running")
	return waitAgentCmd(root.uuid, asked.uuid, ag.Name, ch)
}

// agentToolLine is the last tool call seen this turn — rendered as a muted band
// beneath the running mention (tool name colored, detail gray). Ephemeral: it
// never enters the outline or the DB, and clears when the turn ends.
type agentToolLine struct {
	name   string
	detail string
}

// agentThinkEvery is how long after a descendant edit settles before the
// agent re-reads the thread and decides whether to reply.
const agentThinkEvery = time.Second

// agentThinkMsg fires when a debounced descendant-change timer settles.
// gen must match Model.agentThinkGen — a newer edit supersedes older ticks.
type agentThinkMsg struct{ gen int }

// agentThread is the per-uuid turn state for the @mention agent: all keyed by
// ONE uuid so a lifecycle event (finish/stop/stream-end) clears it together.
// busy/cancel/tool key on the thread ROOT uuid.
type agentThread struct {
	busy   bool          // a turn is in flight for this thread root
	cancel func()        // cancels the in-flight turn (flash "stop"); nil when idle
	tool   agentToolLine // the last tool call (live band under the running mention)
}

// thread returns the existing thread state for a uuid, or nil — nil-safe for
// read/comma-ok sites (an absent uuid reads as "not busy, nothing sent").
func (m *Model) thread(uuid string) *agentThread { return m.threads[uuid] }

// ensureThread returns the thread state for a uuid, lazily creating m.threads
// and the entry (mirrors nodeStore). Every write site goes through here.
func (m *Model) ensureThread(uuid string) *agentThread {
	if m.threads == nil {
		m.threads = map[string]*agentThread{}
	}
	t := m.threads[uuid]
	if t == nil {
		t = &agentThread{}
		m.threads[uuid] = t
	}
	return t
}

// busyThreadCount reports how many threads have a turn in flight. NOT
// len(m.threads): a thread entry survives idle (it may only hold sent), so the
// bar must count busy==true.
func (m *Model) busyThreadCount() int {
	n := 0
	for _, t := range m.threads {
		if t.busy {
			n++
		}
	}
	return n
}

// handleAgentEvent lands one event in the outline and re-arms the stream.
func (m *Model) handleAgentEvent(msg agentEvMsg) (tea.Model, tea.Cmd) {
	rearm := waitAgentCmd(msg.thread, msg.asked, msg.agent, msg.ch)
	switch msg.ev.Op {
	case "tool":
		// live progress under the running mention — keep only the latest call.
		if msg.ev.Tool != "" {
			m.ensureThread(msg.thread).tool = agentToolLine{name: msg.ev.Tool, detail: msg.ev.Text}
		}
		return m, rearm
	case "thinking":
		// the model moved past its last tool (reasoning / answering) — drop the
		// tool line so the band falls back to "Thinking…" instead of freezing.
		if t := m.thread(msg.thread); t != nil {
			t.tool = agentToolLine{}
		}
		return m, rearm
	case "message":
		m.placeAgentNode(msg.thread, msg.asked, msg.ev.Text, msg.ev.Placement)
	case "error":
		// an agent failure shows in the status bar (like thinking), not the
		// outline — it is transient state, not a note the user wrote or wants kept
		m.agentErr = msg.ev.Text
		return m, m.finishThread(msg.thread, msg.agent)
	case "done":
		return m, m.finishThread(msg.thread, msg.agent)
	}
	return m, rearm
}

// placeAgentNode lands one agent reply relative to the asked node — the two
// Claude-Tag surfaces: "thread" nests it as the asked node's child, "below"
// posts it message-board style as the next sibling. A missing asked node
// (e.g. deleted mid-turn) falls back to the thread root's children.
//
// The reply text may carry attachment tokens (see peelAgentAttachments): the
// remaining prose becomes the agent comment, and each attach lands as a typed
// child under it — special nodes (code, image, bash-as-cmd, json, …), not
// conversation bullets.
func (m *Model) placeAgentNode(threadUUID, askedUUID, text, placement string) {
	asked := m.tree.byUUID[askedUUID]
	if asked == nil {
		asked = m.tree.byUUID[threadUUID]
		placement = "thread"
	}
	if asked == nil {
		return
	}
	comment, atts := peelAgentAttachments(text)
	it, err := m.tree.newItem()
	if err != nil {
		return
	}
	it.typ = database.TypeAgent
	it.name = m.chipifyAgentText(comment)
	// replies are born locked so they can't drift under the thread — /lock
	// unlocks one for reshaping like any other node
	it.readonly = true
	// a reply hosts its own sub-thread: born down so the conversation under it
	// reads top-down chronological (the new-node up default would invert it)
	it.priority = database.PriorityDown

	// a reply to the thread root itself always nests — "below" would land it
	// outside the mention's subtree, beyond the session's reach
	if asked.uuid == threadUUID {
		placement = "thread"
	}
	if placement == "below" && asked.parent != nil {
		it.parent = asked.parent
		idx := indexOf(asked)
		// message-board placement follows the surrounding order: under a
		// priority-up parent the conversation stacks newest first, so the
		// reply posts ABOVE the asked node instead of after it
		if asked.parent.priority != database.PriorityUp {
			idx++
		}
		m.tree.insertChildAt(asked.parent, idx, it)
	} else {
		it.parent = asked
		// a priority-up node keeps its newest children on top — the reply
		// lands first; down appends at the bottom as before
		if asked.priority == database.PriorityUp {
			asked.children = append([]*item{it}, asked.children...)
		} else {
			asked.children = append(asked.children, it)
		}
		asked.collapsed = false
	}
	// agent replies carry chips like any node: spoken {{…}} tokens converted
	// above, and #tags / canonical dates via the same detection the backfill
	// uses. Skip the backfill once anchors exist — its span detection is not
	// anchor-aware; a skipped plain #tag still renders via inlineSpans.
	if !hasAnchor(it.name) {
		m.backfillName(it)
	}
	// attachments hang under the reply as typed children (order of appearance)
	for _, a := range atts {
		m.addAgentAttachment(it, a.typ, a.body)
	}
	if len(atts) > 0 {
		it.collapsed = false
	}
	m.unsaved = true

	// a reply arrives asynchronously: re-anchor the cursor to the ITEM it was
	// on, or an insertion above it shifts every row and typing lands on the
	// wrong node
	var curIt, curCtx *item
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		curIt, curCtx = m.rows[m.cursor].it, m.rows[m.cursor].ctx
	}
	m.refreshRows()
	if curIt != nil {
		if r := m.findRow(curIt, curCtx); r >= 0 {
			m.cursor = r
		}
	}
}

// agentAttach is one typed node the agent wants as a child of its reply —
// special content (image, code, json, …), not a chat turn.
type agentAttach struct {
	typ  string
	body string
}

// agentAttachBlockRe matches a multi-line attachment fence:
//
//	{{attach:code}}
//	body…
//	{{/attach}}
//
// Body may contain braces and chip tokens; the fence is the only delimiter.
var agentAttachBlockRe = regexp.MustCompile(`(?s)\{\{attach:([a-zA-Z][a-zA-Z0-9_-]*)\}\}\r?\n?(.*?)\r?\n?\{\{/attach\}\}`)

// agentAttachInlineRe matches a single-line attachment token:
//
//	{{attach:type|body}}
//
// Body cannot contain `}` (use the block form for multi-line / braced content).
var agentAttachInlineRe = regexp.MustCompile(`\{\{attach:([a-zA-Z][a-zA-Z0-9_-]*)\|([^{}]*)\}\}`)

// peelAgentAttachments strips attach tokens from a reply, returning the
// comment prose (chip tokens intact for chipifyAgentText) and the attachments
// in document order. Scans left-to-right so block fences and inline tokens
// interleave as written; at each index a block open wins over an inline token.
func peelAgentAttachments(text string) (string, []agentAttach) {
	var atts []agentAttach
	var b strings.Builder
	for i := 0; i < len(text); {
		// prefer a block fence at this offset
		if loc := agentAttachBlockRe.FindStringSubmatchIndex(text[i:]); loc != nil && loc[0] == 0 {
			typ := strings.ToLower(text[i+loc[2] : i+loc[3]])
			body := text[i+loc[4] : i+loc[5]]
			body = strings.TrimRight(body, "\r\n")
			body = strings.TrimPrefix(body, "\n")
			atts = append(atts, agentAttach{typ: typ, body: body})
			i += loc[1]
			continue
		}
		// else an inline token at this offset
		if loc := agentAttachInlineRe.FindStringSubmatchIndex(text[i:]); loc != nil && loc[0] == 0 {
			typ := strings.ToLower(text[i+loc[2] : i+loc[3]])
			body := strings.TrimSpace(text[i+loc[4] : i+loc[5]])
			body = strings.ReplaceAll(body, `\n`, "\n")
			atts = append(atts, agentAttach{typ: typ, body: body})
			i += loc[1]
			continue
		}
		// skip to the next "{{attach:" candidate, copying prose as we go
		next := strings.Index(text[i:], "{{attach:")
		if next < 0 {
			b.WriteString(text[i:])
			break
		}
		if next == 0 {
			// "{{attach:" that matched neither form — emit the open literally
			// and advance one byte so we don't loop forever
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteString(text[i : i+next])
		i += next
	}
	return strings.TrimSpace(collapseBlankLines(b.String())), atts
}

// collapseBlankLines turns 3+ newlines into a double newline so peeled attach
// fences don't leave holes in the comment.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// addAgentAttachment appends one typed, locked child under the agent reply.
// Free-string types are accepted (registry types and any former-mod key); the
// agent type itself is refused (no nested agent replies). "bash" maps to a
// bullet whose name is a single runnable cmd chip — the bash node type was
// removed in favor of chips. Image bodies are caption, or path, or path|caption
// when the agent has a local image file to load.
func (m *Model) addAgentAttachment(parent *item, typ, body string) {
	if parent == nil {
		return
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" || typ == database.TypeAgent {
		return
	}
	child, err := m.tree.newItem()
	if err != nil {
		return
	}
	child.readonly = true // agent-authored, same lock as the reply comment
	child.parent = parent
	parent.children = append(parent.children, child)

	switch typ {
	case "bash": // legacy bash node type → runnable $ chip (bash type was removed)
		child.typ = database.TypeBullets
		if body != "" {
			child.name = m.createChip(chipKindCmd, body)
		}
	case database.TypeImage:
		child.typ = database.TypeImage
		path, caption := splitImageAttach(body)
		child.name = caption
		if path != "" {
			m.loadImageAttach(child, path)
		}
	default:
		// free string: code, json, quote, log, todo, query, h1…, or any other key
		child.typ = typ
		child.name = m.chipifyAgentText(body)
		if child.name != "" && !hasAnchor(child.name) {
			m.backfillName(child)
		}
	}
}

// splitImageAttach parses an image attachment body:
//
//	"caption"            → no path, caption
//	"/path/to.png"       → path (existing image file), empty caption
//	"/path/to.png|cap"   → path + caption
//
// A path is recognized only when the left side (or whole body) is an existing
// regular file — so a plain caption never gets mistaken for a path.
func splitImageAttach(body string) (path, caption string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}
	left, right, hasBar := strings.Cut(body, "|")
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if hasBar {
		if fileLooksLikeImage(left) {
			return left, right
		}
		// not a loadable path — treat the whole thing as caption
		return "", body
	}
	if fileLooksLikeImage(left) {
		return left, ""
	}
	return "", body
}

// fileLooksLikeImage reports whether p is a regular file whose suffix looks
// like an image the decoder can handle (png/jpg/jpeg/gif/webp).
func fileLooksLikeImage(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(p)
	ok := false
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp"} {
		if strings.HasSuffix(low, ext) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// loadImageAttach reads a local image file into the node's blob so the
// attachment renders with pixels, not just a caption. Failures are silent —
// the image node still lands (empty + caption); the agent can't fix a missing
// file mid-place.
func (m *Model) loadImageAttach(it *item, path string) {
	if it == nil || path == "" || m.db == nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > imageMaxBytes {
		return
	}
	// normalize via decode+encode so the cache and half-block path always see PNG
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return
	}
	b := img.Bounds()
	_ = database.PutBlob(m.db, database.Blob{
		UUID: it.uuid, Mime: "image/png", Bytes: buf.Bytes(),
		W: b.Dx(), H: b.Dy(),
	})
	m.imageInvalidate(it.uuid)
}

// agentChipRe matches the {{kind:value}} chip tokens an agent may speak — see
// cliSystemPrompt in pkg/tui/tag. A link value may carry a "label|" prefix.
var agentChipRe = regexp.MustCompile(`\{\{(cmd|path|link|tag|date):([^{}]+)\}\}`)

// chipifyAgentText converts a reply's chip tokens into real chips (anchor in
// the text + chip record), so a spoken {{cmd:…}} lands as the same runnable
// chip the user would have typed. Unknown kinds stay literal text.
func (m *Model) chipifyAgentText(text string) string {
	return agentChipRe.ReplaceAllStringFunc(text, func(tok string) string {
		sub := agentChipRe.FindStringSubmatch(tok)
		kind, val := sub[1], strings.TrimSpace(sub[2])
		if val == "" {
			return ""
		}
		switch kind {
		case chipKindLink:
			label, target := "", val
			if i := strings.IndexByte(val, '|'); i >= 0 {
				label, target = strings.TrimSpace(val[:i]), strings.TrimSpace(val[i+1:])
			}
			if target == "" {
				return ""
			}
			return m.createLabeledChip(chipKindLink, target, label)
		case chipKindTag:
			val = strings.TrimPrefix(val, "#")
		}
		if val == "" {
			return ""
		}
		return m.createChip(kind, val)
	})
}

// finishThread clears the busy flag and parks the thread. If edits landed
// under the thread while it was busy, re-arm the debounced think so they are
// considered once the in-flight turn is done.
func (m *Model) finishThread(threadUUID, agent string) tea.Cmd {
	if t := m.thread(threadUUID); t != nil {
		t.busy = false
		t.tool = agentToolLine{} // the live tool band is gone once the turn ends
		if t.cancel != nil {
			t.cancel() // release the ctx (a no-op if the turn already ended)
			t.cancel = nil
		}
	}
	m.touchThread(threadUUID, agent, "idle")
	if m.agentRethinkUUID == "" {
		return nil
	}
	it := m.tree.byUUID[m.agentRethinkUUID]
	m.agentRethinkUUID = ""
	return m.noteAgentChange(it)
}

// stopThread cancels an in-flight turn: killing the CLI closes the stream,
// which flows back as a "done" event and finishThread cleans up. Safe to call
// when nothing is running (no cancel recorded → no-op).
func (m *Model) stopThread(threadUUID, agentName string) {
	if t := m.thread(threadUUID); t != nil && t.cancel != nil {
		t.cancel()
		m.flash = "@" + agentName + " stopped"
	}
}

// stopAgentsUnder cancels every in-flight agent turn whose thread root sits
// inside it's subtree (including it). Called just before those nodes leave the
// tree — local delete, empty-node remove, or an external tombstone — so the
// CLI process is not orphaned the way a deleted bash run would be without
// deleteRunOut. Silent: no flash (the node is already gone).
func (m *Model) stopAgentsUnder(it *item) {
	if it == nil {
		return
	}
	var walk func(x *item)
	walk = func(x *item) {
		if t := m.thread(x.uuid); t != nil && t.cancel != nil {
			t.cancel()
			// clear immediately so the bar's busy count drops now; stream-end
			// still arrives and is a no-op on a nil cancel
			t.cancel = nil
			t.busy = false
			t.tool = agentToolLine{}
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(it)
}

// touchThread upserts the LOCAL thread binding (agent_sessions row, id =
// thread root uuid). This is editor bookkeeping only — which subtree talks to
// which agent — never a remote session; the agent itself is launch-and-forget.
func (m *Model) touchThread(nodeUUID, agent, state string) {
	now := time.Now().UnixNano()
	s := database.AgentSession{
		ID: nodeUUID, NodeUUID: nodeUUID, Agent: agent, State: state,
		CreatedAt: now, UpdatedAt: now,
	}
	_ = s.Upsert(m.db)
}

// markAgentTouch records that it was locally edited this keystroke. Update
// drains the uuid into noteAgentChange after handleKey so edit sites only set
// a field — no tea.Cmd plumbing through every return.
func (m *Model) markAgentTouch(it *item) {
	if it != nil && it.uuid != "" {
		m.agentTouched = it.uuid
	}
}

// noteAgentChange arms a debounced think for a changed descendant of a
// session-bound @mention. When the timer settles the agent re-reads the
// whole thread and decides whether to reply. The mention root itself never
// auto-starts (alt+r only); empty and agent-typed nodes are skipped.
//
// >>> m.noteAgentChange(followUp)  // followUp under a session-bound @Pi
// ... m.agentChangeUUID = followUp.uuid
// ... m.agentThinkGen++            // e.g. 3
// ... → tea.Tick(1s) → agentThinkMsg{gen:3}
// >>> m.noteAgentChange(followUp)  // another keystroke 200ms later
// ... m.agentThinkGen++            // 4; prior tick discarded on fire
// >>> handle(agentThinkMsg{gen:4})
// ... m.fireAgentThink() → sendThread(followUp, Pi)
func (m *Model) noteAgentChange(it *item) tea.Cmd {
	if it == nil || it.name == "" || it.typ == database.TypeAgent {
		return nil
	}
	// only strict descendants of a session-bound mention — never the root
	ag, root, ok := m.activeThreadAgentAbove(it)
	if !ok {
		return nil
	}
	_ = ag
	// a turn already in flight: remember the latest edit and re-arm on finish
	if t := m.thread(root.uuid); t != nil && t.busy {
		m.agentRethinkUUID = it.uuid
		return nil
	}
	m.agentChangeUUID = it.uuid
	m.agentThinkGen++
	gen := m.agentThinkGen
	return tea.Tick(agentThinkEvery, func(time.Time) tea.Msg {
		return agentThinkMsg{gen: gen}
	})
}

// fireAgentThink ships the debounced change if it is still a valid descendant
// under an active session. Called only when agentThinkMsg.gen is current.
func (m *Model) fireAgentThink() tea.Cmd {
	uuid := m.agentChangeUUID
	m.agentChangeUUID = ""
	if uuid == "" {
		return nil
	}
	it := m.tree.byUUID[uuid]
	if it == nil || it.name == "" || it.typ == database.TypeAgent {
		return nil
	}
	ag, _, ok := m.activeThreadAgentAbove(it)
	if !ok {
		return nil
	}
	return m.sendThread(it, ag)
}

// activeThreadAgent finds the agent whose session covers this node — the
// nearest ancestor (or the node itself) that is a session-bound @mention
// node. Nodes outside active threads reach no agent, ever.
func (m *Model) activeThreadAgent(it *item) (tag.Agent, bool) {
	for p := it; p != nil; p = p.parent {
		for _, a := range m.agents {
			if m.threadSessionAt(p, a.Name) {
				return a, true
			}
		}
	}
	return tag.Agent{}, false
}

// activeThreadAgentAbove is activeThreadAgent but only for STRICT descendants:
// the session must sit on an ancestor, never on it itself. Auto-think is for
// children of the agent chip, not re-fires of the mention line.
func (m *Model) activeThreadAgentAbove(it *item) (tag.Agent, *item, bool) {
	if it == nil {
		return tag.Agent{}, nil, false
	}
	for p := it.parent; p != nil; p = p.parent {
		for _, a := range m.agents {
			if m.threadSessionAt(p, a.Name) {
				return a, p, true
			}
		}
	}
	return tag.Agent{}, nil, false
}
