package editor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// A voice note: alt+r records (toggle) via ffmpeg, alt+e plays via ffplay. The
// audio is a local wav (~/.local/share/lflow/voice/<uuid>.wav) — never in the DB
// or sync. Inline it shows a ▸ waveform of varying-height bars + duration.

const voiceBuckets = 28

type voiceRecording struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

type voiceDoneMsg struct {
	uuid string
	env  []int
	dur  float64
}

func (m *Model) voicePath(uuid string) string {
	return filepath.Join(m.ctx.Paths.Data, "lflow", "voice", uuid+".wav")
}

// voice state lives in the generic per-node store (no per-type Model maps).
func (m *Model) voiceRecOf(uuid string) (*voiceRecording, bool) {
	r, ok := m.nodeStore(uuid)["voiceRec"].(*voiceRecording)
	return r, ok
}
func (m *Model) voiceWave(uuid string) ([]int, float64) {
	d := m.nodeStore(uuid)
	env, _ := d["voiceEnv"].([]int)
	dur, _ := d["voiceDur"].(float64)
	return env, dur
}
func (m *Model) setVoiceWave(uuid string, env []int, dur float64) {
	d := m.nodeStore(uuid)
	d["voiceEnv"] = env
	d["voiceDur"] = dur
}

// runVoice toggles recording: alt+r starts ffmpeg (PulseAudio mono 16kHz), a
// second alt+r stops it gracefully and computes the waveform.
func runVoice(m *Model, it *item) tea.Cmd {
	if rec, ok := m.voiceRecOf(it.uuid); ok { // stop
		io.WriteString(rec.stdin, "q\n") // ffmpeg stops gracefully on q
		rec.stdin.Close()
		delete(m.nodeStore(it.uuid), "voiceRec")
		path := m.voicePath(it.uuid)
		return func() tea.Msg {
			rec.cmd.Wait()
			env, dur := parseWavEnvelope(path, voiceBuckets)
			return voiceDoneMsg{it.uuid, env, dur}
		}
	}
	// start
	path := m.voicePath(it.uuid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.err = err
		return nil
	}
	cmd := exec.Command("ffmpeg", "-y", "-f", "pulse", "-i", "default", "-ac", "1", "-ar", "16000", path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		m.err = err
		return nil
	}
	if err := cmd.Start(); err != nil {
		m.flash = "voice: no audio device (need PulseAudio/WSLg)"
		return nil
	}
	m.nodeStore(it.uuid)["voiceRec"] = &voiceRecording{cmd: cmd, stdin: stdin}
	return nil
}

// voiceFlashActions names a voice node's flash actions properly: alt+r records
// (a toggle — the verb tracks the state) and alt+e plays. Without this hook flash
// would infer the generic "run"/"expand" from the registry; the hook is the
// per-type discovery mechanism that lets a type say what its actions are called.
func voiceFlashActions(m *Model, it *item) []flashAction {
	record := "record"
	if _, recording := m.voiceRecOf(it.uuid); recording {
		record = "stop"
	}
	acts := []flashAction{{verb: record, color: cGreen, do: runVoice}}
	if fileExists(m.voicePath(it.uuid)) {
		acts = append(acts, flashAction{verb: "play", color: cCyan, do: playVoice})
	}
	return acts
}

// playVoice plays the recording via ffplay (detached, fire-and-forget).
func playVoice(m *Model, it *item) tea.Cmd {
	path := m.voicePath(it.uuid)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	_ = exec.Command("ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet", path).Start()
	return nil
}

// voiceRender is the inline display: recording state, or a ▸ waveform + duration.
func (m *Model) voiceRender(it *item) string {
	if _, recording := m.voiceRecOf(it.uuid); recording {
		return cRed + "●" + cReset + " " + cDim + "recording · ⌥r stop" + cReset
	}
	env, dur := m.voiceWave(it.uuid)
	if len(env) == 0 { // lazily load from disk (e.g. after reopen)
		if p := m.voicePath(it.uuid); fileExists(p) {
			env, dur = parseWavEnvelope(p, voiceBuckets)
			m.setVoiceWave(it.uuid, env, dur)
		}
	}
	if len(env) == 0 {
		return cDim + "▸ empty · ⌥r record" + cReset
	}
	return cDim + "▸ " + cReset + cAccent + envBars(env) + cReset +
		cDim + fmt.Sprintf("  %d:%02d · ⌥e play", int(dur)/60, int(dur)%60) + cReset
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// envBars maps an amplitude envelope to a row of block bars of varying heights.
func envBars(env []int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	var max int
	for _, v := range env {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		max = 1
	}
	var b []rune
	for _, v := range env {
		lvl := v * (len(bars) - 1) / max
		if lvl < 0 {
			lvl = 0
		}
		b = append(b, bars[lvl])
	}
	return string(b)
}

// parseWavEnvelope reads a 16-bit PCM wav and returns a downsampled max-amplitude
// envelope plus the duration in seconds. Best-effort; returns nil on any problem.
func parseWavEnvelope(path string, buckets int) ([]int, float64) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 44 {
		return nil, 0
	}
	// chunk-aware: read sampleRate from the "fmt " chunk and the PCM from "data",
	// since ffmpeg may insert chunks that shift fixed offsets.
	sampleRate := 16000
	if fi := bytes.Index(data, []byte("fmt ")); fi >= 0 && fi+16 <= len(data) {
		sampleRate = int(binary.LittleEndian.Uint32(data[fi+12 : fi+16]))
	}
	idx := bytes.Index(data, []byte("data"))
	if idx < 0 || idx+8 >= len(data) || sampleRate == 0 {
		return nil, 0
	}
	pcm := data[idx+8:]
	if sz := int(binary.LittleEndian.Uint32(data[idx+4 : idx+8])); sz > 0 && sz <= len(pcm) {
		pcm = pcm[:sz]
	}
	n := len(pcm) / 2
	if n == 0 {
		return nil, 0
	}
	dur := float64(n) / float64(sampleRate)
	per := n / buckets
	if per < 1 {
		per = 1
	}
	env := make([]int, 0, buckets)
	for bkt := 0; bkt < buckets; bkt++ {
		max := 0
		for i := 0; i < per; i++ {
			off := (bkt*per + i) * 2
			if off+1 >= len(pcm) {
				break
			}
			s := int(int16(binary.LittleEndian.Uint16(pcm[off : off+2])))
			if s < 0 {
				s = -s
			}
			if s > max {
				max = s
			}
		}
		env = append(env, max)
	}
	return env, dur
}
