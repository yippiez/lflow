package editor

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The /artifacts management view: every installed artifact with its state,
// space to enable/disable, ctrl+d to uninstall. Picking a type stays in /type;
// reading an artifact's JS is a CLI job (lflow artifact show <key>) — the TUI
// deliberately has no code viewer.

// handleArtifactsKey drives the list.
func (m *Model) handleArtifactsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(loadedArtifacts)
	switch k.String() {
	case "esc", "q", "enter":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.artSel > 0 {
			m.artSel--
		}
		return m, nil
	case "down":
		if m.artSel < n-1 {
			m.artSel++
		}
		return m, nil
	case " ", "space":
		// no flash — the row's state column is the feedback
		if m.artSel < n {
			a := loadedArtifacts[m.artSel]
			if err := database.SetArtifactEnabled(m.db, a.Key, !a.Enabled); err == nil {
				loadArtifacts(m.db)
			}
		}
		return m, nil
	case "ctrl+d":
		if m.artSel < n {
			a := loadedArtifacts[m.artSel]
			if err := database.DeleteArtifact(m.db, a.Key); err == nil {
				loadArtifacts(m.db)
				if m.artSel >= len(loadedArtifacts) && m.artSel > 0 {
					m.artSel--
				}
			}
		}
		return m, nil
	}
	return m, nil
}

// artifactListLines renders the management list above the status bar.
func (m *Model) artifactListLines(maxLine int) []string {
	lines := []string{clip(" "+cDim+"artifacts · space enable/disable · ctrl+d uninstall"+cReset, maxLine)}
	if len(loadedArtifacts) == 0 {
		return append(lines, clip(cDim+"   none installed · ask an agent for one (@Pi create an artifact …)"+cReset, maxLine))
	}
	if m.artSel >= len(loadedArtifacts) {
		m.artSel = len(loadedArtifacts) - 1
	}
	win := pickerMaxRowsArtifacts
	s := scrollStart(m.artSel, len(loadedArtifacts), win)
	e := s + win
	if e > len(loadedArtifacts) {
		e = len(loadedArtifacts)
	}
	for i := s; i < e; i++ {
		a := loadedArtifacts[i]
		mark := "  "
		if i == m.artSel {
			mark = cAccent + "▸ " + cReset
		}
		// just the name and whether it's on — provenance/version bookkeeping is
		// deliberately not shown; it's useless while note-taking
		state := cGreen + "enabled" + cReset
		if !a.Enabled {
			state = cDim + "disabled" + cReset
		}
		if a.loadErr != "" {
			state = cRed + "error" + cReset
		}
		line := fmt.Sprintf(" %s%s%-16s%s %s", mark, cFG, a.Label, cReset, state)
		if a.loadErr != "" {
			line += cDim + " · " + a.loadErr + cReset
		}
		lines = append(lines, clip(line, maxLine))
	}
	return lines
}

const pickerMaxRowsArtifacts = 8
