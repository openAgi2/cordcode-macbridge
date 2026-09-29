package codexremote

import "testing"

// S-5（断线韧性方案 §3.5）：重组上限镜像官方 REMOTE_CONTROL_REASSEMBLED_MAX_BYTES
// （segment.rs:21 = 100MB）。曾漂移为 1GB；入站方向官方不会发超限帧，常量对齐消除
// 与官方语义的歧义。
func TestReassembledMessageMaxBytesMirrorsOfficial(t *testing.T) {
	if ReassembledMessageMaxBytes != 100*1024*1024 {
		t.Fatalf("ReassembledMessageMaxBytes = %d, want 100MB (official segment.rs REMOTE_CONTROL_REASSEMBLED_MAX_BYTES)",
			ReassembledMessageMaxBytes)
	}
}
