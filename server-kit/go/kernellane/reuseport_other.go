//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package kernellane

import "syscall"

const reusePortAvailable = false

// reusePortControl is a no-op where the platform has no SO_REUSEPORT. The
// listener still binds normally with a single acceptor, preserving behaviour;
// ReusePortSupported reports false so callers do not start a second one.
func reusePortControl(_, _ string, _ syscall.RawConn) error {
	return nil
}
