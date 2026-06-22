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

// FlagValue is the value to pass to the backend's --model flag. pi and opencode
// take "upstream/model"; grok takes the bare model id.
func (m Model) FlagValue() string {
	if m.CLI == ProviderGrok {
		return m.Name
	}
	return m.ID()
}

// String is the canonical, round-trippable form used in the model picker and in
// persisted config. The default pi backend keeps the bare "upstream/model" form
// (so existing config and the status bar read unchanged); other backends are
// prefixed with "<cli>:". The ":" vs "/" split disambiguates a CLI prefix from an
// upstream provider, so a pi model whose upstream is literally "opencode-go" never
// collides with the opencode backend.
func (m Model) String() string {
	if m.Empty() {
		return ""
	}
	if m.CLI == ProviderPi || m.CLI == "" {
		return m.ID()
	}
	return string(m.CLI) + ":" + m.ID()
}

// ParseModel is the inverse of Model.String. A missing CLI prefix defaults to pi.
func ParseModel(s string) Model {
	cli := ProviderPi
	if i := strings.IndexByte(s, ':'); i >= 0 {
		switch p := Provider(s[:i]); p {
		case ProviderPi, ProviderOpencode, ProviderGrok:
			cli = p
			s = s[i+1:]
		}
	}
	up, name := "", s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		up, name = s[:i], s[i+1:]
	}
	return Model{CLI: cli, Upstream: up, Name: name}
}
