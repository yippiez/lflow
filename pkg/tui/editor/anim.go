package editor

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// animFrame is the render-time animation clock for the magic keywords (ultracode /
// ultraloop). The animation tick increments it and rendering reads it; both run on
// bubbletea's single event-loop goroutine, so no synchronization is needed. Nothing
// about the animation is stored — detection is purely render-time, so it never
// writes a marker into the node name and never violates the per-node-style rule.
var animFrame int

const animEvery = 55 * time.Millisecond

type animTickMsg time.Time

func animTick() tea.Cmd {
	return tea.Tick(animEvery, func(t time.Time) tea.Msg { return animTickMsg(t) })
}

// magicKeyword is an animated word and its sliding-shine palette (base color → a
// lighter tint at the highlight, never white, so the word keeps its color).
type magicKeyword struct {
	word       string
	speed      float64
	base, peak [3]int
}

var magicKeywords = []magicKeyword{
	{"ultracode", 0.32, [3]int{178, 102, 230}, [3]int{223, 190, 250}}, // purple
	{"ultraloop", 0.40, [3]int{235, 72, 72}, [3]int{255, 172, 172}},   // red
}

// shineColorAt returns the SGR foreground for rune j of an n-rune keyword at the given
// frame: a soft highlight band that slides continuously across the word. The center is
// fractional, so motion is smooth.
func shineColorAt(n, j, frame int, speed float64, base, peak [3]int) string {
	center := math.Mod(float64(frame)*speed, float64(n)+4) - 2
	t := 1 - math.Abs(float64(j)-center)/2.5
	if t < 0 {
		t = 0
	}
	r := base[0] + int(float64(peak[0]-base[0])*t)
	g := base[1] + int(float64(peak[1]-base[1])*t)
	b := base[2] + int(float64(peak[2]-base[2])*t)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// markKeywords sets flags[i].kwColor for every rune that falls inside a magic keyword,
// so renderBody paints those runes with the animated color.
func markKeywords(runes []rune, flags []spanFlags, frame int) {
	low := []rune(strings.ToLower(string(runes)))
	for _, k := range magicKeywords {
		w := []rune(k.word)
		for i := 0; i+len(w) <= len(low); i++ {
			match := true
			for j := range w {
				if low[i+j] != w[j] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			for j := range w {
				flags[i+j].kwColor = shineColorAt(len(w), j, frame, k.speed, k.base, k.peak)
			}
			i += len(w) - 1
		}
	}
}

// hasMagicKeyword reports whether any currently visible row contains an animated
// keyword. The animation tick runs only while one is on screen.
func (m *Model) hasMagicKeyword() bool {
	for _, r := range m.rows {
		low := strings.ToLower(r.it.name)
		for _, k := range magicKeywords {
			if strings.Contains(low, k.word) {
				return true
			}
		}
	}
	return false
}

// startAnim batches an animation tick onto cmd when a keyword is on screen and the
// tick is not already running.
func (m *Model) startAnim(cmd tea.Cmd) tea.Cmd {
	if !m.animTicking && m.hasMagicKeyword() {
		m.animTicking = true
		return tea.Batch(cmd, animTick())
	}
	return cmd
}
