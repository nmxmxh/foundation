//go:build unix

package kernellane

import (
	"os"
	"syscall"
)

func fileStatBlocks(st os.FileInfo) int64 {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys != nil {
		return int64(sys.Blocks)
	}
	return 0
}
