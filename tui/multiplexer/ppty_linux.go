package multiplexer

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openPTY opens /dev/ptmx and unlocks its slave: the Linux (and WSL2) half of
// posix_openpt + grantpt + unlockpt + ptsname. grantpt is a no-op on Linux —
// devpts mounts the slave with sane ownership already.
func openPTY() (ptmx *os.File, slave string, err error) {
	ptmx, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, "", err
	}
	fd := int(ptmx.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		ptmx.Close()
		return nil, "", fmt.Errorf("unlockpt: %w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		ptmx.Close()
		return nil, "", fmt.Errorf("ptsname: %w", err)
	}
	return ptmx, fmt.Sprintf("/dev/pts/%d", n), nil
}
