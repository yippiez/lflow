# Voice node: record/play voice note with waveform

| | |
|---|---|
| **Status** | Shipped |
| **Date** | 2026-06-18 |
| **Owner** | eren |
| **Related commits** | 1bcb449, 42b2a7f, e8831ed |
| **Related ADRs** | — |

## Problem / Context

From the consolidated runnable-nodes spec: "record a voice message; play it back,"
with simple non-emoji icons and a waveform of block bars. Binary audio must not touch
the synced DB.

## Goals

- A voice node: alt+r records (toggle), alt+e plays.
- Inline render: a `▸` play sign + a waveform of vertical bars + duration; a red `●` + live animated bars while recording.
- Store audio as a local `.wav` file under `~/.local/share/lflow/voice/<uuid>.wav`; the amplitude envelope is recomputed from the `.wav` on demand and cached in memory, not stored in any per-node blob.

## Non-goals

- Putting binary audio in the synced DB.
- Emoji or media-control glyphs (`▶⏸⏹⏺`).

## Approach / Design

- `pkg/tui/editor/voice.go`: `runVoice` toggles recording via `ffmpeg` (PulseAudio mono 16kHz) to `~/.local/share/lflow/voice/<uuid>.wav`; a second alt+r stops gracefully (`q` to ffmpeg) and computes the waveform. `playVoice` plays via `ffplay` (detached). If no audio device, it flashes `voice: no audio device (need PulseAudio/WSLg)`.
- `voiceRender` shows recording state (`● recording · ⌥r stop`), an empty state (`▸ empty · ⌥r record`), or `▸` + a waveform of `▁▂▃▄▅▆▇█` bars + `M:SS · ⌥e play`. `parseWavEnvelope` downsamples the wav into a 28-bucket max-amplitude envelope (chunk-aware: reads sampleRate from `fmt ` and PCM from `data`).
- Registered in `registry.go` with `inlineEditable: false`, `renderM: voiceRender`, `run: runVoice`, `expand: playVoice`. Nil maps guarded on disk load (`e8831ed`).

## Decisions

- Binary audio never touches the synced DB — the `.wav` is a local file and the envelope is recomputed from it on demand and cached in memory ("big/binary content lives in local files, never the synced row").
- Simple non-emoji icons; waveform of block bars.
- Compact `⌥e`/`⌥r` shortcut labels (`42b2a7f`).

## UX / Behavior

- alt+r records (toggle); while recording shows red `●` + `recording · ⌥r stop`.
- alt+e plays the recording.
- Idle render: `▸` + waveform bars + `M:SS · ⌥e play`; empty render: `▸ empty · ⌥r record`.
- Waveform loads lazily from disk on reopen.

## Status & History

- 2026-06-18 `1bcb449` voice note — record/play with a waveform display.
- 2026-06-18 `42b2a7f` compact `⌥`-style shortcut labels (`⌥e` play / `⌥r` record).
- 2026-06-18 `e8831ed` guard nil maps in voiceRender on disk load.
