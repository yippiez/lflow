package agent

import "strings"

// Model is a selectable model surfaced by one CLI backend, e.g.
// {CLI: "pi", Upstream: "anthropic", Name: "claude-opus"}. grok models have no
// upstream (Upstream == "").
type Model struct {
	CLI      Provider // backend that exposes the model
	Upstream string   // upstream provider id ("anthropic"), "" for grok
	Name     string   // model id ("claude-opus")
}

// ID is the "upstream/model" identifier (or bare "model" when there is no
// upstream, as with grok).
func (m Model) ID() string {
	if m.Upstream == "" {
		return m.Name
	}
	return m.Upstream + "/" + m.Name
}

// Empty reports whether the model is unset.
func (m Model) Empty() bool { return m.Name == "" }

// FlagValue is the value to pass to the backend's model flag: pi wants
// "upstream/model", grok a bare model id — both are ID().
func (m Model) FlagValue() string { return m.ID() }

// String is the canonical stored form: "cli:upstream/model". pi models stay
// unprefixed ("upstream/model") so existing stored settings keep parsing.
func (m Model) String() string {
	if m.Empty() {
		return ""
	}
	if m.CLI == "" || m.CLI == ProviderPi {
		return m.ID()
	}
	return string(m.CLI) + ":" + m.ID()
}

// ParseModel is the inverse of Model.String: an optional "cli:" prefix names
// the backend (any registered provider; missing or "pi" means pi), the rest is
// "upstream/model" (or a bare model id for backends without upstreams).
func ParseModel(s string) Model {
	cli := ProviderPi
	if i := strings.IndexByte(s, ':'); i >= 0 {
		if _, ok := Get(Provider(s[:i])); ok {
			cli = Provider(s[:i])
			s = s[i+1:]
		}
	}
	up, name := "", s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		up, name = s[:i], s[i+1:]
	}
	return Model{CLI: cli, Upstream: up, Name: name}
}
