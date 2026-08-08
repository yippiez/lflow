package multiplexer

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens /dev/ptmx and readies its slave the Darwin way: grant, unlock,
// and read the slave's name back through TIOCPTYGNAME (a 128-byte path buffer).
func openPTY() (ptmx *os.File, slave string, err error) {
	ptmx, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, "", err
	}
	fd := ptmx.Fd()
	if err := ioctlPty(fd, unix.TIOCPTYGRANT, 0); err != nil {
		ptmx.Close()
		return nil, "", err
	}
	if err := ioctlPty(fd, unix.TIOCPTYUNLK, 0); err != nil {
		ptmx.Close()
		return nil, "", err
	}
	var buf [128]byte // TIOCPTYGNAME fills a 128-byte path
	if err := ioctlPty(fd, unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0]))); err != nil {
		ptmx.Close()
		return nil, "", err
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return ptmx, string(buf[:n]), nil
}

func ioctlPty(fd uintptr, req uint, arg uintptr) error {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, fd, uintptr(req), arg)
	if e != 0 {
		return e
	}
	return nil
}
