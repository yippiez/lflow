package editor

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/agent"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// Global editor preferences, edited via /settings and persisted in the DB
// settings table (see database/settings.go). Each preference is one settingDef;
// adding a preference is one entry in settingDefs plus reading m.setting(key)
// where it matters — no scattered flags.

// settingOption is one selectable value of a setting.
type settingOption struct{ value, label string }

// settingDef declares a preference: its stored key, label, allowed options,
// default, and an optional apply hook run when the value is loaded or changed
// (e.g. theme reseeds the live palette). Values not in options fall back to def.
type settingDef struct {
	key     string
	label   string
	options []settingOption
	def     string
	apply   func(m *Model, value string)
}

var settingDefs = []settingDef{
	{
		key: "theme", label: "Theme",
		options: themeOptions(),
		def:     "system",
		apply: func(m *Model, v string) {
			if t, ok := themeByName(v); ok {
				applyTheme(t)
			}
		},
	},
	{
		key: "link.color", label: "Link color",
		options: []settingOption{{"gray", "gray"}, {"blue", "blue"}},
		def:     "gray",
		apply:   func(m *Model, v string) { linkColorMode = v },
	},
	{
		key: "image.preview", label: "Image preview",
		options: []settingOption{
			{"compact", "compact · one line"},
			{"true", "true · thumbnail"},
		},
		def: "compact",
	},
	// The @Pi agent's model and thinking level, passed on every turn. "default"
	// leaves both to pi's own config (~/.pi/agent/settings.json). The model
	// options are filled from `pi --list-models` when /settings opens — see
	// seedAgentModelOptions; never on the editor start path.
	{
		key: "agent.model", label: "Agent model",
		options: []settingOption{{"default", "default · pi config"}},
		def:     "default",
		apply:   func(m *Model, v string) { tag.SetModelPref(v) },
	},
	{
		key: "agent.thinking", label: "Agent thinking",
		options: []settingOption{
			{"default", "default · pi config"},
			{"off", "off"}, {"low", "low"}, {"medium", "medium"}, {"high", "high"},
		},
		def:   "default",
		apply: func(m *Model, v string) { tag.SetThinkingPref(v) },
	},
}

// agentDefaultModelLabel resolves what "default" actually IS — pi's own
// configured model — so the row shows the real value, not a pointer to it.
func agentDefaultModelLabel() string {
	if mo, _ := agent.DefaultModel(); !mo.Empty() {
		return "default · " + mo.String()
	}
	return "default · pi config"
}

// agentDefaultThinkingLabel is the same for the thinking level.
func agentDefaultThinkingLabel() string {
	if _, th := agent.DefaultModel(); th != "" {
		return "default · " + th
	}
	return "default · pi config"
}

// refreshAgentDefaultLabels rewrites the two "default" rows with the values
// pi's config resolves to. Called from loadSettings (one cached file read).
func refreshAgentDefaultLabels() {
	for i := range settingDefs {
		switch settingDefs[i].key {
		case "agent.model":
			settingDefs[i].options[0].label = agentDefaultModelLabel()
		case "agent.thinking":
			settingDefs[i].options[0].label = agentDefaultThinkingLabel()
		}
	}
}

// The pi model list is fetched in the BACKGROUND when /settings first opens —
// `pi --list-models` execs a CLI and must never block the UI thread (it froze
// /settings when it ran synchronously). The row offers "default" until the
// message lands.
var agentModelsSeeded bool

// agentModelsMsg delivers the fetched model options into the update loop.
type agentModelsMsg struct{ options []settingOption }

func fetchAgentModelsCmd() tea.Cmd {
	return func() tea.Msg {
		opts := []settingOption{{"default", agentDefaultModelLabel()}}
		for _, mo := range agent.ListModels() {
			opts = append(opts, settingOption{mo.String(), mo.String()})
		}
		return agentModelsMsg{options: opts}
	}
}

// themeOptions derives the theme setting's options from the theme registry so
// there is a single source of truth.
func themeOptions() []settingOption {
	out := make([]settingOption, len(themes))
	for i, t := range themes {
		out[i] = settingOption{t.name, t.name}
	}
	return out
}

func settingByKey(key string) (settingDef, bool) {
	for _, d := range settingDefs {
		if d.key == key {
			return d, true
		}
	}
	return settingDef{}, false
}

// setting returns the current value of a preference (the stored value, or its
// default when unset).
func (m *Model) setting(key string) string {
	if v, ok := m.settings[key]; ok {
		return v
	}
	if d, ok := settingByKey(key); ok {
		return d.def
	}
	return ""
}

// setSetting updates a preference in memory, persists it to the DB, and runs its
// apply hook (so e.g. a theme change takes effect immediately).
func (m *Model) setSetting(key, value string) {
	if m.settings == nil {
		m.settings = map[string]string{}
	}
	m.settings[key] = value
	if m.db != nil {
		_ = database.SetSetting(m.db, key, value)
	}
	if d, ok := settingByKey(key); ok && d.apply != nil {
		d.apply(m, value)
	}
}

// loadSettings hydrates preferences from the DB at startup and runs every apply
// hook (with defaults for unset keys). It also migrates a legacy file-based theme
// choice into the DB once, so an existing /theme selection is preserved.
func (m *Model) loadSettings() {
	if m.db != nil {
		if s, err := database.LoadSettings(m.db); err == nil {
			m.settings = s
		}
	}
	if m.settings == nil {
		m.settings = map[string]string{}
	}
	if _, ok := m.settings["theme"]; !ok {
		if b, err := os.ReadFile(m.themeConfigPath()); err == nil {
			if name := strings.TrimSpace(string(b)); name != "" {
				if _, ok := themeByName(name); ok {
					m.setSetting("theme", name) // migrate the legacy file into the DB
				}
			}
		}
	}
	refreshAgentDefaultLabels() // "default" rows show what pi actually resolves to
	for _, d := range settingDefs {
		if d.apply != nil {
			d.apply(m, m.setting(d.key))
		}
	}
}

// cycleSetting returns the value dir steps from cur (wrapping) in d's options.
func cycleSetting(d settingDef, cur string, dir int) string {
	idx := 0
	for i, o := range d.options {
		if o.value == cur {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(d.options)) % len(d.options)
	return d.options[idx].value
}

// settingValueLabel is the human label for a setting's current value.
func settingValueLabel(d settingDef, value string) string {
	for _, o := range d.options {
		if o.value == value {
			return o.label
		}
	}
	return value
}

// settingValueColor picks the value's color for the settings rows: negative
// values (off/false/disabled/none) read red, everything else green — a chosen
// value is an affirmative statement.
func settingValueColor(value string) string {
	switch value {
	case "off", "false", "disabled", "none", "no":
		return cRed
	}
	return cGreen
}
