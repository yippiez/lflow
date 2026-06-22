package editor

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Ultraloop: an agent whose query contains the magic word "ultraloop" (optionally
// with an interval, e.g. "ultraloop 10m") re-prompts itself forever. Every interval
// (default 1m) the original query is re-sent; if the agent is still working that
// tick is skipped. The word animates (see anim.go); after a prompt the node shows a
// ↻ curved arrow + a countdown to the next prompt.

// loopState is one agent's recurring schedule.
type loopState struct {
	interval time.Duration
	next     time.Time // when the next prompt fires
}

const loopTickEvery = 1 * time.Second

type loopTickMsg time.Time

func loopTick() tea.Cmd {
	return tea.Tick(loopTickEvery, func(t time.Time) tea.Msg { return loopTickMsg(t) })
}

var ultraloopRe = regexp.MustCompile(`(?i)ultraloop(?:\s+(\d+)\s*([smh]))?`)

// ultraloopParse reports whether a query enables ultraloop and at what interval
// (default 1m). "ultraloop 30s" / "ultraloop 10m" / "ultraloop 2h" set the interval.
func ultraloopParse(name string) (time.Duration, bool) {
	mch := ultraloopRe.FindStringSubmatch(name)
	if mch == nil {
		return 0, false
	}
	if mch[1] == "" {
		return time.Minute, true
	}
	n, _ := strconv.Atoi(mch[1])
	switch strings.ToLower(mch[2]) {
	case "s":
		return time.Duration(n) * time.Second, true
	case "h":
		return time.Duration(n) * time.Hour, true
	default:
		return time.Duration(n) * time.Minute, true
	}
}

// ultraloopStrip removes the "ultraloop [interval]" directive from a query so the
// agent is prompted with the task itself, not the loop control word.
func ultraloopStrip(name string) string {
	return strings.TrimSpace(ultraloopRe.ReplaceAllString(name, ""))
}

// registerLoop sets up (or refreshes) an agent's ultraloop schedule when its query
// asks for it. Returns true if a loop is active for this agent.
func (m *Model) registerLoop(uuid, name string) bool {
	dur, ok := ultraloopParse(name)
	if !ok {
		if m.loops != nil {
			delete(m.loops, uuid)
		}
		return false
	}
	if m.loops == nil {
		m.loops = map[string]*loopState{}
	}
	ls := m.loops[uuid]
	if ls == nil {
		ls = &loopState{}
		m.loops[uuid] = ls
	}
	ls.interval = dur
	ls.next = time.Now().Add(dur)
	return true
}

// advanceLoops fires every due ultraloop agent (skipping any still working) and
// returns commands to run for agents whose process had exited. Prunes loops for
// agents that no longer exist or no longer ask to loop.
func (m *Model) advanceLoops() []tea.Cmd {
	now := time.Now()
	var cmds []tea.Cmd
	for uuid, ls := range m.loops {
		w := m.tempTree.byUUID[uuid]
		if w == nil || !ultraloopRe.MatchString(w.name) {
			delete(m.loops, uuid) // agent gone or no longer an ultraloop
			continue
		}
		if m.workerStatus[uuid] == "running" {
			continue // already working — skip this tick
		}
		if now.Before(ls.next) {
			continue // not due yet
		}
		ls.next = now.Add(ls.interval)
		query := ultraloopStrip(w.name)
		if s := m.liveSteer(uuid); s != nil {
			_ = s.Steer(query) // same conversation, re-prompt
			m.workerStatus[uuid] = "running"
			if m.workerAction != nil {
				m.workerAction[uuid] = workerActivity{text: "thinking…"}
			}
		} else {
			cmds = append(cmds, runWorker(m, w)) // process exited — fresh turn
		}
	}
	return cmds
}

// loopCountdown is the "↻ m:ss" chip for an agent's next scheduled prompt (empty
// when not looping).
func (m *Model) loopCountdown(uuid string) string {
	ls := m.loops[uuid]
	if ls == nil {
		return ""
	}
	d := time.Until(ls.next)
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	return cMagenta + "↻ " + cReset + cDim + fmtMSS(s) + cReset
}

func fmtMSS(s int) string {
	return strconv.Itoa(s/60) + ":" + zero2(s%60)
}

func zero2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
