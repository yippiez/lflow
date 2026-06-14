package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// settleAfterLaunch is the single fixed pause we allow: the inline bubbletea
// editor needs a moment to paint its first frame after the pane spawns. Every
// other synchronization uses waitFor against the rendered pane.
const settleAfterLaunch = 1200 * time.Millisecond

// session drives one isolated lflow editor instance through a private tmux
// session. Each test gets its own HOME + XDG dirs (so its own sqlite db) and
// its own uniquely named tmux session, so tests never collide.
type session struct {
	t       *testing.T
	name    string   // tmux session name, unique per test
	home    string   // isolated HOME / XDG root
	env     []string // env passed to every lflow invocation
	width   int
	height  int
	openCmd []string // the `node open ...` args used to (re)launch
}

// seedFn runs lflow subcommands against the isolated env before the editor is
// launched, to build the starting tree.
type seedFn func(s *session)

// newSession seeds a tree via seed and launches `lflow <openArgs...>` in a tmux
// pane sized w x h. openArgs defaults to {"node", "open", "scratch"}.
func newSession(t *testing.T, w, h int, seed seedFn, openArgs ...string) *session {
	home := t.TempDir()
	env := []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + home,
		"XDG_DATA_HOME=" + home,
		"XDG_CACHE_HOME=" + home,
		// a stable TERM so tmux renders predictably
		"TERM=xterm-256color",
		// keep PATH so the binary can find anything it shells out to
		"PATH=" + os.Getenv("PATH"),
	}

	if len(openArgs) == 0 {
		openArgs = []string{"node", "open", "scratch"}
	}

	// a tmux-legal unique name: letters/digits/underscores only
	name := "lflow_" + sanitize(t.Name()) + fmt.Sprintf("_%d", time.Now().UnixNano())

	s := &session{
		t:       t,
		name:    name,
		home:    home,
		env:     env,
		width:   w,
		height:  h,
		openCmd: openArgs,
	}

	if seed != nil {
		seed(s)
	}

	s.launch()
	return s
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// seedCmd runs a single lflow subcommand against the isolated env, failing the
// test on a non-zero exit. Used to build the starting tree.
func (s *session) seedCmd(args ...string) string {
	cmd := exec.Command(lflowBin, args...)
	cmd.Env = s.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("seed %q failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// launchScript writes a tiny wrapper that execs lflow with the isolated env,
// then starts a detached tmux session running it. Going through a script keeps
// the env handling robust regardless of the user's login shell.
func (s *session) launch() {
	s.t.Helper()

	script := filepath.Join(s.home, "run-"+s.name+".sh")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, e := range s.env {
		b.WriteString("export " + shellQuoteEnv(e) + "\n")
	}
	b.WriteString("exec " + shellQuote(lflowBin))
	for _, a := range s.openCmd {
		b.WriteString(" " + shellQuote(a))
	}
	b.WriteString("\n")
	if err := os.WriteFile(script, []byte(b.String()), 0755); err != nil {
		s.t.Fatalf("writing launch script: %v", err)
	}

	out, err := exec.Command("tmux",
		"new-session", "-d",
		"-s", s.name,
		"-x", fmt.Sprint(s.width),
		"-y", fmt.Sprint(s.height),
		"/bin/sh", script,
	).CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux new-session failed: %v\n%s", err, out)
	}

	s.t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", s.name).Run()
	})

	time.Sleep(settleAfterLaunch)
}

// reopen kills the current tmux pane and relaunches lflow on the same db, to
// assert persistence across editor restarts.
func (s *session) reopen() {
	s.t.Helper()
	exec.Command("tmux", "kill-session", "-t", s.name).Run()
	// a fresh name avoids any lingering-server race on the old name
	s.name = s.name + "_r"
	s.launch()
}

// send dispatches named tmux keys (Enter, Tab, BSpace, C-d, M-Right, ...).
func (s *session) send(keys ...string) {
	s.t.Helper()
	args := append([]string{"send-keys", "-t", s.name}, keys...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		s.t.Fatalf("tmux send-keys %v failed: %v\n%s", keys, err, out)
	}
	// a tiny gap lets the editor process the key before the next one; this is
	// not a synchronization wait — assertions still go through waitFor.
	time.Sleep(40 * time.Millisecond)
}

// sendText types a literal string (no key interpretation).
func (s *session) sendText(text string) {
	s.t.Helper()
	if out, err := exec.Command("tmux", "send-keys", "-t", s.name, "-l", text).CombinedOutput(); err != nil {
		s.t.Fatalf("tmux send-keys -l %q failed: %v\n%s", text, err, out)
	}
	time.Sleep(60 * time.Millisecond)
}

// snapshot captures the live pane as plain text (no SGR escapes).
func (s *session) snapshot() string {
	out, err := exec.Command("tmux", "capture-pane", "-t", s.name, "-p").CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux capture-pane failed: %v\n%s", err, out)
	}
	return string(out)
}

// waitFor polls the pane until it contains substr or the timeout elapses,
// returning the snapshot it matched on. On timeout it fails with the last seen
// snapshot so the failure shows what actually rendered.
func (s *session) waitFor(substr string, timeout time.Duration) string {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		last = s.snapshot()
		if strings.Contains(last, substr) {
			return last
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %q. last pane:\n%s", substr, last)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

// waitForFunc polls until pred(snapshot) is true.
func (s *session) waitForFunc(desc string, pred func(string) bool, timeout time.Duration) string {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		last = s.snapshot()
		if pred(last) {
			return last
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %s. last pane:\n%s", desc, last)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

const waitTimeout = 4 * time.Second

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteEnv quotes the value half of a KEY=VALUE assignment.
func shellQuoteEnv(kv string) string {
	i := strings.IndexByte(kv, '=')
	if i < 0 {
		return shellQuote(kv)
	}
	return kv[:i] + "=" + shellQuote(kv[i+1:])
}
