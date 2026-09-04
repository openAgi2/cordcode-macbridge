package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// transcript_shapes_test.go —— 文件面边界层 fixture 回归（设计 §6 Phase 4.1）。
//
// transcript 是 Claude CLI 的**无合同**存储（官方无 schema 承诺）。fixture 包
// （testdata/transcript-shapes/fixture.jsonl）来自本机真实会话的脱敏样本
// （2026-09-04，CLI 2.1.229-2.1.234 混合代际），锁定：
//   - 观测到的顶层 type 枚举（新类型出现 = 有意识的清单更新，不是静默漂移）
//   - 解析器对每一类记录的行为（已知类型正确消费；未知类型无 panic 跳过）
//
// CLI 大版本升级后应重跑本测试 + 重新采样生成 fixture（diff 即漂移报告）。

// observedTranscriptTypes 是 2026-09-04 真实普查（最近 6 个会话文件）锁定的
// 顶层 type 全集。attachment/atis-latch/last-prompt/mode/queue-operation/
// relocated 等均非消息记录（Message==nil 路径跳过）。
var observedTranscriptTypes = map[string]bool{
	"assistant":       true,
	"atis-latch":      true,
	"attachment":      true,
	"custom-title":    true,
	"last-prompt":     true,
	"mode":            true,
	"queue-operation": true,
	"relocated":       true,
	"system":          true,
	"user":            true,
}

func loadShapeFixture(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "transcript-shapes", "fixture.jsonl"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var out []map[string]any
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("fixture line not JSON: %v", err)
		}
		out = append(out, obj)
	}
	if len(out) == 0 {
		t.Fatalf("fixture empty")
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// 每条 fixture 记录的 type 必须在锁定枚举内；枚举外的 = CLI 代际漂移，
// 需人工核对新形状后更新清单（fail closed，不静默放行）。
func TestTranscriptShapeFixture_TypeEnumLocked(t *testing.T) {
	seen := map[string]bool{}
	for _, obj := range loadShapeFixture(t) {
		typ, _ := obj["type"].(string)
		if !observedTranscriptTypes[typ] {
			t.Errorf("fixture type %q not in locked enum — CLI shape drift requires conscious review", typ)
		}
		seen[typ] = true
	}
	// 锁定全集必须在 fixture 中全部出现（fixture 完整性）
	for typ := range observedTranscriptTypes {
		if !seen[typ] {
			t.Errorf("locked enum type %q missing from fixture (fixture incomplete)", typ)
		}
	}
}

// 解析器必须对每一类真实记录形状无 panic 地解出信封；消息类记录的
// transcriptHistoryEnvelope 关键字段（uuid/timestamp/message.id/message.role）
// 形状保持。
func TestTranscriptShapeFixture_EnvelopeDecodes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "transcript-shapes", "fixture.jsonl"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	messageRecords := 0
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var env transcriptHistoryEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("envelope decode failed on fixture record: %v", err)
		}
		if env.Message != nil {
			messageRecords++
			if env.Message.Role == "" {
				t.Errorf("message record missing role: type=%q", env.Type)
			}
		}
	}
	if messageRecords < 2 {
		t.Fatalf("fixture must contain user/assistant message records, got %d", messageRecords)
	}
}

// custom-title 命名空间（Phase 4.2）：写入用 cordcode: 前缀，读取双接受。
func TestCustomTitleNamespace_RoundTrip(t *testing.T) {
	if !isClaudeCustomTitleRecord("cordcode:custom-title") {
		t.Errorf("namespaced type must be accepted")
	}
	if !isClaudeCustomTitleRecord("custom-title") {
		t.Errorf("legacy type must stay accepted (存量会话)")
	}
	for _, foreign := range []string{"custom-titles", "cordcode:other", "", "title"} {
		if isClaudeCustomTitleRecord(foreign) {
			t.Errorf("foreign type %q must not match", foreign)
		}
	}
}
