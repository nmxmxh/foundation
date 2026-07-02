package hermessnapshot

import (
	"fmt"
	"io"
	"os"
)

// userspaceCopy is the portable clone lane shared by every platform: a plain
// buffered stream through userspace. It is the semantics baseline the kernel
// lanes must refine.
func userspaceCopy(dst, src *os.File) (string, error) {
	if _, err := src.Seek(0, 0); err != nil {
		return "", err
	}
	if _, err := dst.Seek(0, 0); err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("userspace clone copy: %w", err)
	}
	return "userspace", nil
}

func closeQuiet(f *os.File) {
	_ = f.Close()
}
