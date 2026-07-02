package editor

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The /artifacts management view: every installed artifact with its state,
// space to enable/disable, ctrl+d to uninstall, enter to page through the JS
// source. Picking a type stays in /type — this view is for looking after the
// collection ("create it now, forget it later" needs a place to remember).

// handleArtifactsKey drives the list.
func (m *Model) handleArtifactsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(loadedArtifacts)
	switch k.String() {
	case "esc", "q":
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
		if m.artSel < n {
			a := loadedArtifacts[m.artSel]
			if err := database.SetArtifactEnabled(m.db, a.Key, !a.Enabled); err == nil {
				loadArtifacts(m.db)
				m.flash = "artifact · " + a.Key + " " + onOff(!a.Enabled)
			}
		}
		return m, nil
	case "ctrl+d":
		if m.artSel < n {
			a := loadedArtifacts[m.artSel]
			if err := database.DeleteArtifact(m.db, a.Key); err == nil {
				loadArtifacts(m.db)
				m.flash = "artifact · " + a.Key + " uninstalled - its nodes fall back to bullets"
				if m.artSel >= len(loadedArtifacts) && m.artSel > 0 {
					m.artSel--
				}
			}
		}
		return m, nil
	case "enter":
		if m.artSel < n {
			m.artKey = loadedArtifacts[m.artSel].Key
			m.artSrcScroll = 0
			m.mode = modeArtifactSrc
		}
		return m, nil
	}
	return m, nil
}

// handleArtifactSrcKey scrolls the source pager.
func (m *Model) handleArtifactSrcKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "enter":
		m.mode = modeArtifacts
		return m, nil
	case "down", "j":
		m.artSrcScroll++
	case "up", "k":
		if m.artSrcScroll > 0 {
			m.artSrcScroll--
		}
	case "pgdown":
		m.artSrcScroll += artifactSrcRows
	case "pgup":
		m.artSrcScroll -= artifactSrcRows
		if m.artSrcScroll < 0 {
			m.artSrcScroll = 0
		}
	}
	return m, nil
}

const artifactSrcRows = 12 // source pager window height

// artifactListLines renders the management list above the status bar.
func (m *Model) artifactListLines(maxLine int) []string {
	lines := []string{clip(" "+cDim+"artifacts · space enable/disable · enter source · ctrl+d uninstall"+cReset, maxLine)}
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

// artifactSrcLines renders the JS source pager for the inspected artifact.
func (m *Model) artifactSrcLines(maxLine int) []string {
	var a *loadedArtifact
	for i := range loadedArtifacts {
		if loadedArtifacts[i].Key == m.artKey {
			a = &loadedArtifacts[i]
		}
	}
	if a == nil {
		m.mode = modeArtifacts
		return nil
	}
	src := strings.Split(strings.TrimRight(a.Source, "\n"), "\n")
	if m.artSrcScroll > len(src)-1 {
		m.artSrcScroll = len(src) - 1
	}
	if m.artSrcScroll < 0 {
		m.artSrcScroll = 0
	}
	hdr := fmt.Sprintf(" artifact %s · %d lines · ↑↓ scroll · esc back", a.Key, len(src))
	lines := []string{clip(cDim+hdr+cReset, maxLine)}
	e := m.artSrcScroll + artifactSrcRows
	if e > len(src) {
		e = len(src)
	}
	for _, l := range src[m.artSrcScroll:e] {
		lines = append(lines, clip("   "+cFG+strings.ReplaceAll(l, "\t", "    ")+cReset, maxLine))
	}
	return lines
}

func onOff(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}
