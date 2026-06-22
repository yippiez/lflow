package editor

import (
	"github.com/lflow/lflow/pkg/agent"
	"github.com/lflow/lflow/pkg/tui/config"
)

// curModel resolves the model + thinking a new agent uses, as canonical
// agent.Model strings ("upstream/model", or "<cli>:upstream/model" for non-pi
// backends). Resolution order: the persisted lflow default set by /model, else
// pi's configured default, overlaid with the session's /model (ctrl+p) and ctrl+t
// overrides. Replaces the old editor/pi.go curModel now that model handling lives
// in pkg/agent.
func (m *Model) curModel() (model, thinking string) {
	dm, dth := agent.DefaultModel()
	model, thinking = dm.String(), dth
	if c, err := config.Read(m.ctx); err == nil && c.AgentModel != "" {
		model = c.AgentModel // persisted /model default wins over pi's config
	}
	if m.piModel != "" {
		model = m.piModel
	}
	if m.piThinking != "" {
		thinking = m.piThinking
	}
	return model, thinking
}

// persistDefaultModel saves the /model choice as lflow's default so it survives
// restarts (answer: persist as default). The value is a canonical agent.Model
// string; a missing settings file is created.
func (m *Model) persistDefaultModel(model string) {
	c, err := config.Read(m.ctx)
	if err != nil {
		return
	}
	c.AgentModel = model
	_ = config.Write(m.ctx, c)
}
