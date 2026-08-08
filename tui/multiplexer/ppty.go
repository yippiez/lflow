//go:build linux || darwin

package multiplexer

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// The PTY layer, hand-rolled: what creack/pty did for lflow was one call, and
// on Unix that call is small — open the master, unlock the slave, size it, and
// start the child as the session leader with the slave as its controlling
// terminal. WSL2 runs a real Linux kernel, so the linux path IS the WSL path.
// Native Windows (ConPTY) is not supported, exactly as before.

// startPTY starts c on a fresh PTY of the given size and returns the master.
// The child gets the slave as stdin/stdout/stderr and as its controlling
// terminal; the parent keeps only the master.
func startPTY(c *exec.Cmd, cols, rows int) (*os.File, error) {
	ptmx, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	_ = resizePTY(ptmx, cols, rows)
	tty, err := os.OpenFile(slave, os.O_RDWR, 0)
	if err != nil {
		ptmx.Close()
		return nil, err
	}
	defer tty.Close()
	c.Stdin, c.Stdout, c.Stderr = tty, tty, tty
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Setsid makes the child a session leader; Setctty adopts the slave (fd 0,
	// its stdin) as the controlling terminal — what delivers SIGWINCH on resize
	// and lets the program see itself as "on a terminal".
	c.SysProcAttr.Setsid = true
	c.SysProcAttr.Setctty = true
	if err := c.Start(); err != nil {
		ptmx.Close()
		return nil, err
	}
	return ptmx, nil
}

// resizePTY sets the PTY's window size on the master; the kernel raises
// SIGWINCH in the child's process group.
func resizePTY(f *os.File, cols, rows int) error {
	return unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ,
		&unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
}
