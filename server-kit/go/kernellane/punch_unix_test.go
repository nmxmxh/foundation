//go:build unix

package kernellane

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnsupportedHolePunchErrors(t *testing.T) {
	for _, expectedErr := range []error{
		unix.ENOTSUP, unix.EOPNOTSUPP, unix.EINVAL, unix.ENOSYS, unix.EPERM,
	} {
		if !isUnsupportedHolePunch(expectedErr) {
			t.Fatalf("expected %v to be recognized as unsupported hole punch", expectedErr)
		}
	}
	if isUnsupportedHolePunch(unix.EBUSY) {
		t.Fatalf("expected EBUSY not to be classified as unsupported hole punch")
	}
}
