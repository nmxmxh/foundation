//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package kernellane

import (
	"syscall"

	"golang.org/x/sys/unix"
)

const reusePortAvailable = true

// reusePortControl sets SO_REUSEPORT on the socket before it is bound. It runs
// inside net.ListenConfig.Control, which is the only point where the option can
// still be applied.
func reusePortControl(_, _ string, c syscall.RawConn) error {
	var setErr error
	if err := c.Control(func(fd uintptr) {
		setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	}); err != nil {
		return err
	}
	return setErr
}
