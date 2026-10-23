//go:build !unix

package kernellane

import "os"

func fileStatBlocks(_ os.FileInfo) int64 {
	return 0
}
