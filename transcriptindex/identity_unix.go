//go:build darwin || linux

package transcriptindex

import (
	"os"
	"syscall"
)

// statDevice and statInode extract file identity for the append-identity check
// (design §6.5.1). On Unix, os.FileInfo.Sys() is *syscall.Stat_t.
func statDevice(stat os.FileInfo) uint64 {
	if sys, ok := stat.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Dev)
	}
	return 0
}

func statInode(stat os.FileInfo) uint64 {
	if sys, ok := stat.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Ino)
	}
	return 0
}
