package gobridge

// projection_webperf_fixture_gen_test.go — PERF-S0B（iOS 仓
// docs/2026-08-23-message-web-gpuix-borrowing-realistic-assessment.md §13）：
// 用真实官方样本 → 各 backend 真实冷 hydrate 映射 → Projection Kernel 真实 ingest，
// 生成 codex-web / opencode-web 的 canonical projection fixture（1:1，无放大；
// turn 数量放大在 iOS Web 层 fixture 生成器做并记录 shapeAmplification）。
//
// 采集器即测试：首次运行写出 fixture 并 skip；commit 后复跑必须 byte-equal
//（映射/内核漂移即失败）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	codexweb "github.com/openAgi2/cordcode-macbridge/agent/codex-web"
	opencodeweb "github.com/openAgi2/cordcode-macbridge/agent/opencode-web"
)

func webperfFixturePath(name string) string {
	return filepath.Join("..", "docs", "protocol", "samples", "session-projection-v2", "fixtures", "web-perf", name)
}

// ingestHydrateEvents 走与 runProjectionHydrateTransaction 相同的 kernel 私有 ingest
// 事务（Begin → ApplyHydrateEvent×N → Commit），返回 committed projection。
func ingestHydrateEvents(t *testing.T, backendID, sessionID string, events []projectionHydrateEvent) SessionProjection {
	t.Helper()
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	source := ProjectionSourceDescriptor{Identity: sessionID, Cursor: 0}
	if _, err := kernel.BeginHydrateTransaction(backendID, sessionID, source, false, false, false); err != nil {
		t.Fatalf("begin hydrate: %v", err)
	}
	for _, ev := range events {
		kernel.ApplyHydrateEvent(backendID, sessionID, "epoch-webperf-fixture", ev.Event, ev.Data)
	}
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatalf("commit hydrate: %v", err)
	}
	return normalizeWebperfTimestamps(commit.Projection)
}

// normalizeWebperfTimestamps 把 reducer 写入的墙钟时间戳归一为固定值（保留非零性），
// 使 fixture 生成跨运行确定性（canonical fixtures 脱敏规则 3 同义）。
func normalizeWebperfTimestamps(p SessionProjection) SessionProjection {
	const fixed = int64(1750000000)
	if p.UpdatedAt != 0 {
		p.UpdatedAt = fixed
	}
	for i := range p.Turns {
		if p.Turns[i].StartedAt != 0 {
			p.Turns[i].StartedAt = fixed
		}
		if p.Turns[i].CompletedAt != 0 {
			p.Turns[i].CompletedAt = fixed
		}
	}
	return p
}

func writeOrVerifyFixture(t *testing.T, path string, projection SessionProjection) {
	t.Helper()
	data, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(data) {
			t.Fatalf("committed fixture %s diverged from real-pipeline regeneration — mapping/kernel drift", path)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Skipf("collected new fixture %s — commit it and re-run to verify parity", path)
}

// TestGenerateCodexWebProjectionFixture：official-0.149.0-alpha.4 catalog dump 的
// thread/read id=19（真实官方 thread，2 turns，官方 turn/item identity）。
func TestGenerateCodexWebProjectionFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "agent", "codex-web", "testdata",
		"official-0.149.0-alpha.4", "dumps", "catalog", "raw.jsonl"))
	if err != nil {
		t.Fatalf("read catalog dump: %v", err)
	}
	responses := map[string]json.RawMessage{}
	for _, line := range splitLines(string(raw)) {
		var e struct {
			Dir string          `json:"dir"`
			Msg json.RawMessage `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Dir != "server" {
			continue
		}
		var m struct {
			ID     *json.Number    `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(e.Msg, &m); err != nil || m.ID == nil {
			continue
		}
		responses[m.ID.String()] = m.Result
	}
	result, ok := responses["19"]
	if !ok {
		t.Fatal("catalog dump missing thread/read response id=19")
	}
	var threadWrap struct {
		Thread codexweb.ThreadInfo `json:"thread"`
	}
	if err := json.Unmarshal(result, &threadWrap); err != nil {
		t.Fatalf("decode thread/read result: %v", err)
	}
	turns := codexweb.MapThreadInfoToTurnScopedHistory(&threadWrap.Thread, 0)
	if len(turns) != 2 {
		t.Fatalf("expected 2 official turns, got %d", len(turns))
	}
	events := turnScopedHistoryTurnToProjectionEvents(turns)
	if len(events) == 0 {
		t.Fatal("no hydrate events produced")
	}
	projection := ingestHydrateEvents(t, "codex-web", "perf-fixture-codex-web", events)
	if len(projection.Turns) != 2 {
		t.Fatalf("projection turns = %d, want 2", len(projection.Turns))
	}
	writeOrVerifyFixture(t, webperfFixturePath("codex-web-catalog.json"), projection)
}

// TestGenerateOpencodeWebProjectionFixture：official-1.18.18 a8-todos 样本的
// GET /session/:id/message 响应（真实脱敏列表，6 msgs，todo/tool 语义）。
func TestGenerateOpencodeWebProjectionFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "agent", "opencode-web", "testdata",
		"official-1.18.18", "samples", "a8-todos.sanitized.json"))
	if err != nil {
		t.Fatalf("read a8-todos sample: %v", err)
	}
	var doc struct {
		HTTP []struct {
			Path     string          `json:"path"`
			Response json.RawMessage `json:"response"`
		} `json:"http"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse a8 sample: %v", err)
	}
	var messageList json.RawMessage
	for _, entry := range doc.HTTP {
		if filepath.Base(entry.Path) == "message" && len(entry.Response) > 0 {
			messageList = entry.Response // 取最后一次（最新列表）
		}
	}
	if messageList == nil {
		t.Fatal("a8 sample missing GET /session/:id/message response")
	}
	entries, err := opencodeweb.MapMessageListToRichEntries(messageList, 0)
	if err != nil {
		t.Fatalf("map message list: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("expected 6 rich entries, got %d", len(entries))
	}
	var events []projectionHydrateEvent
	err = streamRichHistoryProjectionEntries(context.Background(), entries, true, func(ev projectionHydrateEvent) bool {
		events = append(events, ev)
		return true
	})
	if err != nil {
		t.Fatalf("stream entries: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no hydrate events produced")
	}
	projection := ingestHydrateEvents(t, "opencode-web", "perf-fixture-opencode-web", events)
	if len(projection.Turns) == 0 {
		t.Fatal("projection has no turns")
	}
	writeOrVerifyFixture(t, webperfFixturePath("opencode-web-todos.json"), projection)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
