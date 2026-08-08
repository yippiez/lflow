package multiplexer

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// OpenTerminalWindow opens a new terminal window on the HOST running argv in
// dir — the "give this session a window of its own" path. The window is a
// sibling, not a child: it is detached from lflow's session so closing the
// editor does not take it down.
//
// WSL gets Windows Terminal (wt.exe) or a cmd.exe start as the fallback, both
// re-entering the distro via wsl.exe; Linux walks $TERMINAL and the common
// emulators; macOS scripts Terminal.app. Returns what it launched, for the
// flash line.
func OpenTerminalWindow(dir string, argv []string) (string, error) {
	launch := func(name string, args ...string) (string, error) {
		c := exec.Command(name, args...)
		c.Dir = dir
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			return "", err
		}
		_ = c.Process.Release()
		return name, nil
	}

	if isWSL() {
		// wsl.exe re-enters this distro; --cd takes the Linux path as-is
		inner := append([]string{"--cd", dir, "-e"}, argv...)
		if _, err := exec.LookPath("wt.exe"); err == nil {
			return launch("wt.exe", append([]string{"wsl.exe"}, inner...)...)
		}
		if cmd, err := exec.LookPath("cmd.exe"); err == nil {
			return launch(cmd, append([]string{"/c", "start", "wsl.exe"}, inner...)...)
		}
		return "", fmt.Errorf("no wt.exe or cmd.exe on PATH")
	}

	if runtime.GOOS == "darwin" {
		// Terminal.app takes a script, not an argv; quote for the shell it runs
		script := fmt.Sprintf("cd %s && %s", shellQuote(dir), shellQuoteAll(argv))
		return launch("osascript",
			"-e", fmt.Sprintf(`tell application "Terminal" to do script %q`, script),
			"-e", `tell application "Terminal" to activate`)
	}

	// Linux desktops: the user's stated terminal first, then the usual suspects.
	// The -e/-x forms inherit our cwd (launch sets c.Dir), so no per-emulator
	// cwd flag is needed.
	if t := os.Getenv("TERMINAL"); t != "" {
		if _, err := exec.LookPath(t); err == nil {
			return launch(t, append([]string{"-e"}, argv...)...)
		}
	}
	type emu struct {
		bin  string
		args []string
	}
	candidates := []emu{
		{"x-terminal-emulator", append([]string{"-e"}, argv...)},
		{"gnome-terminal", append([]string{"--"}, argv...)},
		{"konsole", append([]string{"-e"}, argv...)},
		{"xfce4-terminal", []string{"-x"}},
		{"kitty", argv},
		{"alacritty", append([]string{"-e"}, argv...)},
		{"wezterm", append([]string{"start", "--cwd", dir, "--"}, argv...)},
		{"foot", argv},
		{"xterm", append([]string{"-e"}, argv...)},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c.bin); err != nil {
			continue
		}
		args := c.args
		if c.bin == "xfce4-terminal" {
			args = append(args, argv...)
		}
		return launch(c.bin, args...)
	}
	return "", fmt.Errorf("no terminal emulator found · set $TERMINAL")
}

// isWSL sniffs Windows Subsystem for Linux the way herdr does: the interop
// env vars first, /proc/version's fingerprint as the fallback.
func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(b))
	return strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuoteAll(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = shellQuote(a)
	}
	return strings.Join(out, " ")
}
