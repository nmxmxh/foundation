//go:build !cgo || (!linux && !darwin)

package runtimehost

func ffiSupported() bool {
	return false
}
