//go:build !js && (linux || darwin || freebsd || openbsd || netbsd)

package healthcheck

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

// DiskSpaceCheck returns a check for available disk space.
func DiskSpaceCheck(path string, minFreeBytes uint64) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
			result.Duration = time.Since(start)
			return result
		}

		freeBytes := stat.Bavail * uint64(stat.Bsize)
		totalBytes := stat.Blocks * uint64(stat.Bsize)

		result.Details = extension.Object{
			"path":        extension.String(path),
			"free_bytes":  extension.Uint(freeBytes),
			"total_bytes": extension.Uint(totalBytes),
			"free_pct":    extension.Float(float64(freeBytes) / float64(totalBytes) * 100),
		}

		if freeBytes < minFreeBytes {
			result.Status = StatusUnhealthy
			result.Message = fmt.Sprintf("disk space low: %d bytes free", freeBytes)
		} else {
			result.Status = StatusHealthy
			result.Message = fmt.Sprintf("disk space OK: %d bytes free", freeBytes)
		}

		result.Duration = time.Since(start)
		return result
	}
}
