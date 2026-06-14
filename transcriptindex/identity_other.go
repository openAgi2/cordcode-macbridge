//go:build !darwin && !linux

package transcriptindex

import "os"

// statDevice and statInode return zero on platforms without a Unix stat_t. The
// continuity anchor and size checks still detect rewrites/truncation; identity
// is best-effort defense-in-depth (design §6.5.1).
func statDevice(os.FileInfo) uint64 { return 0 }
func statInode(os.FileInfo) uint64  { return 0 }
