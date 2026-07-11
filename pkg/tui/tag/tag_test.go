package tag

import (
	"testing"

	"github.com/lflow/lflow/pkg/agent"
)

// renderThread draws the context as nested XML — element per node, children
// nested inside their parent, role in the element name (parent/asked/answer).
func TestRenderThreadXML(t *testing.T) {
	thread := []ThreadNode{
		{UUID: "p", Depth: 0, Name: "importer notes", Role: "user", Parent: true},
		{UUID: "n1", Depth: 1, Name: "@Pi make retries safe?", Role: "user", Asked: true},
		{UUID: "k1", Depth: 2, Name: "importer is in packages/importer", Role: "user"},
		{UUID: "k1a", Depth: 3, Name: "uses curl", Role: "user"},
		{UUID: "k2", Depth: 2, Name: "cap the attempts", Role: "agent"},
	}
	want := "<parent>importer notes\n" +
		"  <asked>@Pi make retries safe?\n" +
		"    <node>importer is in packages/importer\n" +
		"      <node>uses curl</node>\n" +
		"    </node>\n" +
		"    <answer>cap the attempts</answer>\n" +
		"  </asked>\n" +
		"</parent>\n"
	if got := renderThread(thread); got != want {
		t.Errorf("renderThread mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Typed nodes wear their type as the element (XMLTag/XMLAttrs from the
// registry's toContext hook), a multi-line XMLBody owns the element content
// with children nesting after it, and the role tags still win the element
// name — an asked todo reads <asked done="false">, never <todo>.
func TestRenderThreadTypedElements(t *testing.T) {
	thread := []ThreadNode{
		{UUID: "n1", Depth: 0, Name: "@Pi plan the release", Role: "user", Asked: true,
			XMLTag: "todo", XMLAttrs: `done="false"`},
		{UUID: "k1", Depth: 1, Name: "ship it", Role: "user",
			XMLTag: "todo", XMLAttrs: `done="true"`},
		{UUID: "k2", Depth: 1, Name: "cut at 14:00", Role: "user",
			XMLTag: "log", XMLAttrs: `time="2026-07-11 14:00"`},
		{UUID: "k3", Depth: 1, Name: `{"env": "prod"}`, Role: "user",
			XMLTag: "json", XMLBody: "{\n  \"env\": \"prod\"\n}"},
		{UUID: "k3a", Depth: 2, Name: "the deploy config", Role: "user"},
	}
	want := `<asked done="false">@Pi plan the release` + "\n" +
		`  <todo done="true">ship it</todo>` + "\n" +
		`  <log time="2026-07-11 14:00">cut at 14:00</log>` + "\n" +
		"  <json>\n" +
		"    {\n" +
		"      \"env\": \"prod\"\n" +
		"    }\n" +
		"    <node>the deploy config</node>\n" +
		"  </json>\n" +
		"</asked>\n"
	if got := renderThread(thread); got != want {
		t.Errorf("renderThread mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The per-turn prompt is full XML — <instructions> then the rendered thread in
// <NodeContext> — so the outline never mixes with the framing.
func TestTurnPromptWrapsNodeContext(t *testing.T) {
	thread := []ThreadNode{
		{UUID: "n1", Depth: 0, Name: "@Pi what is this?", Role: "user", Asked: true},
	}
	want := "<instructions>\n" +
		"Answer the <asked> node in NodeContext, as one short chat message.\n" +
		"</instructions>\n" +
		"\n" +
		"<NodeContext>\n" +
		"<asked>@Pi what is this?</asked>\n" +
		"</NodeContext>"
	if got := turnPrompt(thread); got != want {
		t.Errorf("turnPrompt mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The built-in agents register out of the box with no config file, so @Pi and
// @Grok both complete and fire without any setup.
func TestLoadAgentsDefaultsToPiAndGrok(t *testing.T) {
	agents := LoadAgents(t.TempDir()) // empty dir → no agents.json → fallback
	got := map[string]bool{}
	for _, a := range agents {
		got[a.Name] = true
	}
	for _, name := range []string{"Pi", "Grok"} {
		if !got[name] {
			t.Errorf("built-in agent %q not registered; got %v", name, agents)
		}
	}
}

// Each built-in agent is hardcoded to exactly one backend — @Pi → pi, @Grok →
// grok — and nothing else: @Grok must never resolve to pi. An unrecognized name
// is an error (no custom agents, no mock fallback).
func TestProviderFor(t *testing.T) {
	cases := []struct {
		name string
		want agent.Provider
		ok   bool
	}{
		{"Pi", agent.ProviderPi, true},
		{"Grok", agent.ProviderGrok, true},
		{"grok", "", false},    // exact match only — not a generic lookup
		{"Zamboni", "", false}, // no custom agents
	}
	for _, c := range cases {
		got, err := providerFor(c.name)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("providerFor(%q) = (%q, err=%v), want (%q, ok=%v)",
				c.name, got, err, c.want, c.ok)
		}
	}
	// the safety net the user asked for: Grok is never pi.
	if p, _ := providerFor("Grok"); p == agent.ProviderPi {
		t.Fatal("@Grok resolved to the pi backend")
	}
}
