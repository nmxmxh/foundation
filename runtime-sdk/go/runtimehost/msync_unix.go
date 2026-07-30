//go:build linux || darwin

package runtimehost

import (
	"syscall"
	"unsafe"
)

// unix_msync flushes a mapping synchronously.
//
// Go's syscall package exposes Msync on some platforms and not others, so this
// issues the syscall directly to keep one implementation across linux and
// darwin. MS_SYNC rather than MS_ASYNC: the caller is about to hand a descriptor
// to another process and must not race the writeback.
func unix_msync(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	const msSync = 0x10 // MS_SYNC on both linux and darwin
	_, _, errno := syscall.Syscall(
		syscall.SYS_MSYNC,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		uintptr(msSync),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
