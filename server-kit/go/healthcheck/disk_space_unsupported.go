//go:build js || (!linux && !darwin && !freebsd && !openbsd && !netbsd)

package healthcheck

import (
	"context"
	"fmt"
	"time"
)

// DiskSpaceCheck reports unsupported disk-space probing on platforms without statfs.
func DiskSpaceCheck(path string, minFreeBytes uint64) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		return CheckResult{
			Status:    StatusUnknown,
			Message:   fmt.Sprintf("disk space check is unsupported on this platform for %s; required free bytes: %d", path, minFreeBytes),
			Duration:  time.Since(start),
			Timestamp: start,
		}
	}
}
