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

// FlagValue is the value to pass to pi's --model flag ("upstream/model").
func (m Model) FlagValue() string { return m.ID() }

// String is the canonical "upstream/model" form (pi is the only backend now).
func (m Model) String() string {
	if m.Empty() {
		return ""
	}
	return m.ID()
}

// ParseModel is the inverse of Model.String. pi is the only backend; a leading
// "pi:" prefix is tolerated for backward compatibility.
func ParseModel(s string) Model {
	s = strings.TrimPrefix(s, "pi:")
	up, name := "", s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		up, name = s[:i], s[i+1:]
	}
	return Model{CLI: ProviderPi, Upstream: up, Name: name}
}
