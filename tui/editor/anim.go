package editor

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
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
	r, g, b := lerpRGB3(base, peak, t)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// magicKeywordRow reports whether a row is one the magic keywords animate on.
// They live on the Pir node and nowhere else: a shine that fires wherever the
// word "ultracode" appears turns every note ABOUT the keyword into a light show,
// and the animation stops meaning "this row is an instruction".
func magicKeywordRow(it *item) bool {
	return it != nil && it.typ == database.TypePir
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

// ShineText applies the ultraloop red sliding-shine across an entire string at the
// current animation frame — a plugin's "generating…" indicator (see nlpcompute).
// It colors every rune; callers pass plain text (no existing SGR).
func ShineText(s string) string {
	kw := magicKeywords[1] // ultraloop — red
	r := []rune(s)
	var b strings.Builder
	for j, c := range r {
		b.WriteString(shineColorAt(len(r), j, animFrame, kw.speed, kw.base, kw.peak))
		b.WriteRune(c)
	}
	b.WriteString(cReset)
	return b.String()
}

// ── run shimmer ─────────────────────────────────────────────────────────────

// A command in flight animates what is already on screen instead of adding
// chrome: a running cmd chip's code cell gets a soft lighter band sliding across
// its BACKGROUND (the text never moves, so the row stays readable), and the
// "running…" line under the node shimmers on its FOREGROUND in the same rhythm.
// Both read the shared animFrame, so every busy surface pulses together — and
// the tick keeps running while any command is live (see animActive), which is
// what lets a silent `sleep 30` still look alive.
const (
	shimmerSpeed = 0.55 // cells per animation frame (~10 cells/s at animEvery)
	shimmerHalf  = 5.0  // half-width of the highlight band, in cells
	shimmerGap   = 6    // cells of travel past the ends, so sweeps arrive in waves
)

var (
	shimmerBase = [3]int{31, 31, 31}  // bgCode — the resting code cell
	shimmerPeak = [3]int{74, 90, 116} // cool blue-gray lift at the highlight's centre
)

// shimmerAt returns how strongly cell j of an n-cell span is lit at the given
// frame, 0 (resting) to 1 (the centre of the band). The centre is fractional and
// wraps over n+shimmerGap cells, so the sweep is smooth and comes in waves.
func shimmerAt(n, j, frame int) float64 {
	if n <= 0 {
		return 0
	}
	span := float64(n + shimmerGap)
	center := math.Mod(float64(frame)*shimmerSpeed, span) - shimmerGap/2
	t := 1 - math.Abs(float64(j)-center)/shimmerHalf
	if t < 0 {
		return 0
	}
	return t
}

// lerpRGB mixes base→peak by t (0..1).
func lerpRGB3(base, peak [3]int, t float64) (int, int, int) {
	return base[0] + int(float64(peak[0]-base[0])*t),
		base[1] + int(float64(peak[1]-base[1])*t),
		base[2] + int(float64(peak[2]-base[2])*t)
}

// shimmerBG is the background SGR for cell j of an n-cell code cell at the given
// frame — bgCode at rest, lifted where the band passes.
func shimmerBG(n, j, frame int) string {
	r, g, b := lerpRGB3(shimmerBase, shimmerPeak, shimmerAt(n, j, frame))
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// shimmerLabel paints a short indicator ("running…") with the same sliding
// highlight on its foreground: muted gray that brightens to a cool near-white as
// the band passes. Callers pass plain text (no existing SGR).
func shimmerLabel(s string) string {
	base, peak := [3]int{122, 122, 122}, [3]int{216, 229, 245} // cDim → blue-white
	runes := []rune(s)
	var b strings.Builder
	for j, c := range runes {
		r, g, bl := lerpRGB3(base, peak, shimmerAt(len(runes), j, animFrame))
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", r, g, bl)
		b.WriteRune(c)
	}
	b.WriteString(cReset)
	return b.String()
}

// animActive reports whether anything on screen needs the animation tick — a
// magic keyword, an in-flight image paste (its spinner), a plugin node marked
// animating (nlpcompute while generating), a shell command still running (its
// shimmer and elapsed clock), or a picker still filling from disk (its spinner).
func (m *Model) animActive() bool {
	return m.hasMagicKeyword() || m.anyImagePasting() || m.anyNodeAnimating() ||
		m.queryLoad != nil || m.anyRunning() || m.anyPickerFilling()
}

// anyPickerFilling reports whether a picker is still reading its rows off the
// disk, which is what turns the spinner in its header.
func (m *Model) anyPickerFilling() bool {
	return m.zoteroFill.running() || m.agentFill.running()
}

// anyRunning reports whether any run band has a command in flight — a cmd chip
// or a runnable node. Keeps the shimmer sliding and the elapsed clock ticking
// even for a command that prints nothing for minutes.
func (m *Model) anyRunning() bool {
	for _, r := range m.runs {
		if r.cancel != nil {
			return true
		}
	}
	return false
}

// runningCount is how many commands are in flight right now (bash nodes and cmd
// chips together) — the status bar's red "N running" tally.
func (m *Model) runningCount() int {
	n := 0
	for _, r := range m.runs {
		if r.cancel != nil {
			n++
		}
	}
	return n
}

// runningQueryCount is the number of query runs in flight. Starting a run
// cancels the previous one (see startQueryLoad), so today this is 0 or 1 — it is
// a count rather than a bool so the toolbar reads the same if that ever changes.
func (m *Model) runningQueryCount() int {
	if m.queryLoad == nil {
		return 0
	}
	return 1
}

// anyNodeAnimating reports whether any node set the generic "animating" flag in
// its ephemeral store — the shine indicator a plugin raises while it works.
func (m *Model) anyNodeAnimating() bool {
	return m.computingNodeCount() > 0
}

// computingNodeCount counts nodes mid-compute through the generic "animating"
// flag a plugin raises while NLPCompute is generating.
func (m *Model) computingNodeCount() int {
	n := 0
	for _, d := range m.nodeData {
		if a, _ := d["animating"].(bool); a {
			n++
		}
	}
	return n
}

// hasMagicKeyword reports whether any currently visible row contains an animated
// keyword. The animation tick runs only while one is on screen.
func (m *Model) hasMagicKeyword() bool {
	for _, r := range m.rows {
		if !magicKeywordRow(r.it) {
			continue
		}
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
	if !m.animTicking && m.animActive() {
		m.animTicking = true
		return tea.Batch(cmd, animTick())
	}
	return cmd
}
