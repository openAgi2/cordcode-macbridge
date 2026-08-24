package gobridge

// catalog_forensics_test.go —— v5 §3 取证契约的定向测试。
// 覆盖：fetch 调用数不变、同源、失败不影响 discovery、disabled 无 I/O、
// 双向 fingerprint 一致、schema 无 raw 字段、HMAC run 间不可关联、field mask、
// 上限/截断/dropped、error 枚举、head 与 authoritative 不同 corpus。

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newForensicsBuf(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logs
}

// forensicsEvents 从 buffer 解析 catalog_forensics 事件的 event JSON。
func forensicsEvents(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log: %v\n%s", err, line)
		}
		if record["msg"] != forensicsLogMessage {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(record["event"].(string)), &event); err != nil {
			t.Fatalf("decode forensics event: %v", err)
		}
		out = append(out, event)
	}
	return out
}

func forensicsTestRun(maxSamples int) *forensicsRun {
	cfg := forensicsConfig{enabled: true, maxSamples: maxSamples, maxBytes: forensicsMaxBytes, now: time.Now}
	return newForensicsRun(cfg)
}

// codexWireN 构造与 fingerprint 同源的 wire maps（字段名与 sessionsToWire 对齐）。
func codexWireN(ids ...string) []map[string]interface{} {
	wire := make([]map[string]interface{}, 0, len(ids))
	for i, id := range ids {
		wire = append(wire, map[string]interface{}{
			"id":              id,
			"updatedAtMillis": int64(1000 + i),
			"title":           "title-" + id,
			"directory":       "/ws/" + id,
			"projectId":       "proj-" + id,
		})
	}
	return wire
}

func forensicsHintAgent(t *testing.T, state *discoveryHintState) *discoveryHintCodexAgent {
	t.Helper()
	withCodexRootsDisabled(t)
	base := &fakeCodexCatalogAgent{fakeAgent: &fakeAgent{name: "codex"}}
	base.fetchFn = func(context.Context, string) ([]core.AgentSessionInfo, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.fullCalls++
		infos := []core.AgentSessionInfo{{ID: "s1", Summary: "one", Directory: state.workspace}}
		if state.expanded {
			infos = append([]core.AgentSessionInfo{{ID: "s2", Summary: "two", Directory: state.workspace}}, infos...)
		}
		return infos, nil
	}
	return &discoveryHintCodexAgent{fakeCodexCatalogAgent: base, state: state}
}

func runForensicsProbeSequence(t *testing.T, h *Handlers, agent *discoveryHintCodexAgent, run *forensicsRun) {
	t.Helper()
	h.forensics = run
	ctx := context.Background()
	seen := map[string]string{}
	// seed
	h.snapshotBackendSessionT(ctx, seen, true, "codex", agent, string(forensicsTriggerSeed), "")
	// 稳定 head ×2（不得触发 full）
	if _, _, err := h.codexDiscoveryHintFingerprint(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.codexDiscoveryHintFingerprint(ctx, agent); err != nil {
		t.Fatal(err)
	}
	// head 变化 → full refresh（head_changed）
	agent.state.mu.Lock()
	agent.state.expanded = true
	agent.state.mu.Unlock()
	fp, headSampleID, err := h.codexDiscoveryHintFingerprint(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if run != nil && headSampleID == "" {
		t.Fatal("head sample id empty with observer enabled")
	}
	h.snapshotBackendSessionT(ctx, seen, false, "codex", agent, string(forensicsTriggerHeadChanged), headSampleID)
	// periodic tick full
	h.snapshotBackendSessionT(ctx, seen, false, "codex", agent, string(forensicsTriggerPeriodicTick), "")
	_ = fp
}

// TestForensicsObserverDoesNotAddFetches：开/关 observer 两次跑同一序列，
// FetchThreadListHead 与 FetchThreadList 调用数完全一致（同源、无二次 fetch）。
func TestForensicsObserverDoesNotAddFetches(t *testing.T) {
	var baselineHead, baselineFull int
	for _, withObserver := range []bool{false, true} {
		state := &discoveryHintState{headSeeded: make(chan struct{}), workspace: t.TempDir()}
		agent := forensicsHintAgent(t, state)
		handlers := newTestHandlers(t)
		handlers.RegisterAgent("codex", agent)
		var run *forensicsRun
		if withObserver {
			run = forensicsTestRun(256)
		}
		runForensicsProbeSequence(t, handlers, agent, run)
		state.mu.Lock()
		headCalls, fullCalls := state.headCalls, state.fullCalls
		state.mu.Unlock()
		if !withObserver {
			baselineHead, baselineFull = headCalls, fullCalls
			continue
		}
		if headCalls != baselineHead || fullCalls != baselineFull {
			t.Fatalf("observer changed fetch counts: head %d→%d full %d→%d",
				baselineHead, headCalls, baselineFull, fullCalls)
		}
	}
}

// TestForensicsSameWireAsFingerprint：样本与 fingerprint 同源——rowCount 等于
// fingerprint 输入行数，fingerprint 字段等于对同一 wire 重算的 listSemanticFingerprint。
func TestForensicsSameWireAsFingerprint(t *testing.T) {
	logs := newForensicsBuf(t)
	state := &discoveryHintState{headSeeded: make(chan struct{}), workspace: t.TempDir()}
	agent := forensicsHintAgent(t, state)
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	handlers.forensics = forensicsTestRun(256)
	ctx := context.Background()
	seen := map[string]string{}
	handlers.snapshotBackendSessionT(ctx, seen, true, "codex", agent, string(forensicsTriggerSeed), "")

	events := forensicsEvents(t, logs)
	var summary map[string]any
	for _, e := range events {
		if e["recordKind"] == "sample_summary" && e["corpusKind"] == "authoritative" {
			summary = e
		}
	}
	if summary == nil {
		t.Fatalf("no authoritative sample: %#v", events)
	}
	// 同源基准：同一 agent 状态的 wire 经与指纹相同的本地转换（无第二次 fetch）。
	base, err := agent.FetchThreadList(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	wire := filterCodexCatalogSessions(sessionsToWire(base))
	if summary["rowCount"] != float64(len(wire)) {
		t.Fatalf("rowCount=%v want %d", summary["rowCount"], len(wire))
	}
	if summary["fingerprint"] != listSemanticFingerprint(wire) {
		t.Fatalf("fingerprint mismatch vs same-wire semantic fingerprint: %v vs %v",
			summary["fingerprint"], listSemanticFingerprint(wire))
	}
	if summary["triggerKind"] != string(forensicsTriggerSeed) {
		t.Fatalf("seed trigger=%v", summary["triggerKind"])
	}
}

// TestForensicsSchemaNoRawFields：样本 JSON 不含 raw id/title/workspace 原文，
// 且字段集合符合冻结 schema（无 raw 泄漏）。
func TestForensicsSchemaNoRawFields(t *testing.T) {
	logs := newForensicsBuf(t)
	state := &discoveryHintState{headSeeded: make(chan struct{}), workspace: t.TempDir()}
	agent := forensicsHintAgent(t, state)
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	handlers.forensics = forensicsTestRun(256)
	ctx := context.Background()
	seen := map[string]string{}
	handlers.snapshotBackendSessionT(ctx, seen, true, "codex", agent, string(forensicsTriggerSeed), "")
	// 下一轮：s1 updatedAt 变化（通过全量返回不同 ModifiedAt）→ row_diff 输出。
	agent.state.mu.Lock()
	agent.state.expanded = true
	agent.state.mu.Unlock()
	handlers.snapshotBackendSessionT(ctx, seen, false, "codex", agent, string(forensicsTriggerPeriodicTick), "")

	events := forensicsEvents(t, logs)
	if len(events) == 0 {
		t.Fatal("no forensics events emitted")
	}
	raw := logs.String()
	for _, forbidden := range []string{"s1", "s2", "title-s", "/ws/", "proj-"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("raw value leaked into forensics events: %q in %s", forbidden, raw)
		}
	}
	for _, e := range events {
		if e["schemaVersion"] != forensicsSchemaVersion {
			t.Fatalf("schemaVersion=%v", e["schemaVersion"])
		}
		if _, ok := e["rowKeyHmac"]; !ok {
			t.Fatalf("missing rowKeyHmac: %#v", e)
		}
		if e["rowKeyHmac"] != nil {
			raw, ok := e["rowKeyHmac"].(string)
			if !ok || len(raw) != 64 {
				t.Fatalf("rowKeyHmac not 64-hex: %#v", e["rowKeyHmac"])
			}
			if _, err := hex.DecodeString(raw); err != nil {
				t.Fatalf("rowKeyHmac not hex: %v", err)
			}
		}
		if err := e["observerError"]; err == nil || e["observerError"] != string(forensicsErrorNone) {
			t.Fatalf("observerError invalid on normal sample: %#v", e["observerError"])
		}
	}
}

// TestForensicsHMACRunNotLinkable：同一 raw id 在两个 run 中生成不同的
// rowKeyHmac（key 只在内存，跨 run 不可关联）。
func TestForensicsHMACRunNotLinkable(t *testing.T) {
	r1 := forensicsTestRun(16)
	r2 := forensicsTestRun(16)
	if r1.rowKeyHMAC("s1") == r2.rowKeyHMAC("s1") {
		t.Fatal("same raw id produced same rowKeyHmac across runs")
	}
	if r1.rowKeyHMAC("a") == r1.rowKeyHMAC("b") {
		t.Fatal("distinct ids collide in one run")
	}
}

// TestForensicsFieldMask：相邻 authoritative 样本 diff 的 fieldChangeMask 各 bit 独立。
func TestForensicsFieldMask(t *testing.T) {
	logs := newForensicsBuf(t)
	run := forensicsTestRun(64)
	// 手工 drive：wire → sample → commit（绕过 discovery，聚焦 diff 计算）。
	pre := codexWireN("A", "B")
	s1 := run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerSeed, pre, 2)
	run.commit(s1, listSemanticFingerprint(pre), 0, 0, "")

	next := []map[string]interface{}{
		{"id": "B", "updatedAtMillis": int64(1000), "title": "title-B", "directory": "/ws/B", "projectId": "proj-B"},
		{"id": "A", "updatedAtMillis": int64(7000), "title": "title-A-renamed", "directory": "/ws/A", "projectId": "proj-A"},
		{"id": "C", "updatedAtMillis": int64(9000), "title": "title-C", "directory": "/ws/C", "projectId": "proj-C"},
	}
	s2 := run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerPeriodicTick, next, 3)
	run.commit(s2, listSemanticFingerprint(next), 0, 0, "")

	events := forensicsEvents(t, logs)
	type diffRow struct {
		key   string
		mask  uint8
		delta int64
	}
	var diffs []diffRow
	for _, e := range events {
		if e["recordKind"] != "row_diff" {
			continue
		}
		diffs = append(diffs, diffRow{
			key:  e["rowKeyHmac"].(string),
			mask: uint8(e["fieldChangeMask"].(float64)),
			delta: func() int64 {
				if v, ok := e["updatedAtDeltaMs"]; ok && v != nil {
					return int64(v.(float64))
				}
				return 0
			}(),
		})
	}
	var maskOf = func(id string) uint8 {
		// 同一 id 在前置样本（added）与最新样本都可能出现；取最后匹配（最新样本的 diff）。
		got := uint8(0)
		found := false
		for _, d := range diffs {
			if d.key == run.rowKeyHMAC(id) {
				got = d.mask
				found = true
			}
		}
		if !found {
			t.Fatalf("no diff for %s: %#v", id, diffs)
		}
		return got
	}
	// B：index 1→0 变化 + updatedAt 1001→1000（delta -1）；title 未变。
	if got := maskOf("B"); got != forensicsMaskIndex|forensicsMaskUpdatedAt {
		t.Fatalf("B mask=%b want index|updatedAt", got)
	}
	// A：index 0→1 + updatedAt 1000→7000（delta 6000）+ title 变化；dir/proj 不变。
	if got := maskOf("A"); got != forensicsMaskIndex|forensicsMaskUpdatedAt|forensicsMaskTitle {
		t.Fatalf("A mask=%b want index|updatedAt|title", got)
	}
	// C：新增。
	if got := maskOf("C"); got != forensicsMaskAdded {
		t.Fatalf("C mask=%b want added", got)
	}
	// A 的 delta 必须存在且 = 6000（取最后匹配——最新样本的 row_diff）。
	var latestA diffRow
	aFound := false
	for _, d := range diffs {
		if d.key == run.rowKeyHMAC("A") {
			latestA = d
			aFound = true
		}
	}
	if !aFound || latestA.delta != 6000 {
		t.Fatalf("A updatedAtDeltaMs=%d want 6000", latestA.delta)
	}
	// 移除位：pre 的 A/B 都在 next 中 → 无 removed。再造 next2=A only → B removed。
	onlyA := []map[string]interface{}{{"id": "A", "updatedAtMillis": int64(7000), "title": "title-A-renamed", "directory": "/ws/A", "projectId": "proj-A"}}
	s3 := run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerPeriodicTick, onlyA, 1)
	run.commit(s3, listSemanticFingerprint(onlyA), 0, 0, "")
	for _, e := range forensicsEvents(t, logs) {
		if e["recordKind"] == "row_diff" {
			key := e["rowKeyHmac"].(string)
			if key == run.rowKeyHMAC("C") && e["fieldChangeMask"] == float64(forensicsMaskRemoved) {
				return
			}
		}
	}
	t.Fatal("C removal diff not emitted")
}

// TestForensicsHeadCorpusDiff：head 样本 diff 只允许 index/added/removed（无 updatedAt bit）。
func TestForensicsHeadCorpusDiff(t *testing.T) {
	logs := newForensicsBuf(t)
	run := forensicsTestRun(32)
	pre := codexWireN("A", "B")
	s1 := run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, pre, 2)
	run.commit(s1, listOrderFingerprint(pre), 0, 0, "")
	rev := []map[string]interface{}{
		{"id": "B"},
		{"id": "A"},
	}
	s2 := run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, rev, 2)
	run.commit(s2, listOrderFingerprint(rev), 0, 0, "")
	count := 0
	for _, e := range forensicsEvents(t, logs) {
		if e["recordKind"] != "row_diff" {
			continue
		}
		mask := uint8(e["fieldChangeMask"].(float64))
		if mask&forensicsMaskAdded != 0 {
			continue // 前置样本的 added 行不属于本次 rev diff
		}
		count++
		if mask != forensicsMaskIndex {
			t.Fatalf("head diff mask=%b, want index only", mask)
		}
	}
	if count < 2 {
		t.Fatalf("rev diffs=%d, want B+A index diffs", count)
	}
}

// TestForensicsObserverFailureDoesNotAffectDiscovery：capture panic 被转成 dropped，
// 原路径 fingerprint/generation 照常推进（定向测试证明 observer 失败不影响结果）。
func TestForensicsObserverFailureDoesNotAffectDiscovery(t *testing.T) {
	previousHook := forensicsCaptureHook
	forensicsCaptureHook = func() { panic("simulated capture panic") }
	t.Cleanup(func() { forensicsCaptureHook = previousHook })

	logs := newForensicsBuf(t)
	state := &discoveryHintState{headSeeded: make(chan struct{}), workspace: t.TempDir()}
	agent := forensicsHintAgent(t, state)
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	handlers.forensics = forensicsTestRun(64)
	ctx := context.Background()
	seen := map[string]string{}
	// seed 正常（panic 前）
	forensicsCaptureHook = nil
	handlers.snapshotBackendSessionT(ctx, seen, true, "codex", agent, string(forensicsTriggerSeed), "")
	// 变化 + panic 的 capture
	agent.state.mu.Lock()
	agent.state.expanded = true
	agent.state.mu.Unlock()
	forensicsCaptureHook = func() { panic("simulated capture panic") }
	handlers.snapshotBackendSessionT(ctx, seen, false, "codex", agent, string(forensicsTriggerPeriodicTick), "")

	if got := handlers.catalogGeneration.Load(); got != 1 {
		t.Fatalf("generation=%d, panic observer must not block generation advance", got)
	}
	events := forensicsEvents(t, logs)
	var sawRunSummary bool
	for _, e := range events {
		if e["recordKind"] == "run_summary" {
			sawRunSummary = true
			if e["observerError"] != string(forensicsErrorDropped) {
				t.Fatalf("run_summary observerError=%v", e["observerError"])
			}
		}
	}
	if !sawRunSummary {
		t.Fatalf("panic observer did not emit run_summary: %#v", events)
	}
}

// TestForensicsDisabledNoIO：forensics 为 nil 时无任何 catalog_forensics 输出。
func TestForensicsDisabledNoIO(t *testing.T) {
	logs := newForensicsBuf(t)
	state := &discoveryHintState{headSeeded: make(chan struct{}), workspace: t.TempDir()}
	agent := forensicsHintAgent(t, state)
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	handlers.forensics = nil
	ctx := context.Background()
	seen := map[string]string{}
	handlers.snapshotBackendSessionT(ctx, seen, true, "codex", agent, string(forensicsTriggerSeed), "")
	if _, _, err := handlers.codexDiscoveryHintFingerprint(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if events := forensicsEvents(t, logs); len(events) != 0 {
		t.Fatalf("disabled observer emitted %d events", len(events))
	}
}

// TestForensicsLimitRespectsMaxSamples：maxSamples=2 时第 3 个样本被丢弃，
// 输出单个 run_summary（observerError=limit_reached）。
func TestForensicsLimitRespectsMaxSamples(t *testing.T) {
	logs := newForensicsBuf(t)
	run := forensicsTestRun(2)
	run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, codexWireN("A"), 1)
	run.commit(run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, codexWireN("A"), 1), "fp1", 0, 0, "")
	run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, codexWireN("A"), 1)
	run.commit(run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, codexWireN("A"), 1), "fp2", 0, 0, "")
	run.commit(run.capture("codex", forensicsCorpusHead, forensicsTriggerPeriodicTick, codexWireN("A"), 1), "fp3", 0, 0, "")

	events := forensicsEvents(t, logs)
	summaries := 0
	runSummaries := 0
	for _, e := range events {
		switch e["recordKind"] {
		case "sample_summary":
			summaries++
		case "run_summary":
			runSummaries++
			if e["observerError"] != string(forensicsErrorLimitReached) {
				t.Fatalf("run summary err=%v", e["observerError"])
			}
		}
	}
	if summaries > 2 {
		t.Fatalf("samples=%d want ≤2", summaries)
	}
	if runSummaries != 1 {
		t.Fatalf("run_summary count=%d want 1", runSummaries)
	}
}

// TestForensicsByteCapAtomic：预算按事件原子检查——证据总量（含唯一 run_summary）
// 恒 ≤ maxBytes，越界事件本身不被写入。
func TestForensicsByteCapAtomic(t *testing.T) {
	logs := newForensicsBuf(t)
	cfg := forensicsConfig{enabled: true, maxSamples: 1000, maxBytes: 4096, now: time.Now}
	run := newForensicsRun(cfg)
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = fmt.Sprintf("s%02d", i)
	}
	for i := 0; !run.stopped && i < 500; i++ {
		run.commit(run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerPeriodicTick, codexWireN(ids...), len(ids)),
			fmt.Sprintf("fp-%d", i), 0, 0, "")
	}
	if !run.stopped || run.stoppedErr != forensicsErrorLimitReached {
		t.Fatalf("run must stop via limit: stopped=%v err=%v", run.stopped, run.stoppedErr)
	}
	events := forensicsEvents(t, logs)
	var exported, summaries, runSummaries int
	for _, e := range events {
		if b, err := json.Marshal(e); err == nil {
			exported += len(b) + 1 // 与 jsonl 导出一致
		}
		switch e["recordKind"] {
		case "sample_summary":
			summaries++
		case "run_summary":
			runSummaries++
			if e["observerError"] != string(forensicsErrorLimitReached) {
				t.Fatalf("run summary err=%v", e["observerError"])
			}
		}
	}
	if summaries == 0 {
		t.Fatal("no samples emitted before stop")
	}
	if runSummaries != 1 {
		t.Fatalf("run_summary count=%d want 1", runSummaries)
	}
	if exported > int(cfg.maxBytes) {
		t.Fatalf("exported bytes=%d exceed maxBytes=%d", exported, cfg.maxBytes)
	}
}

// TestForensicsMaskCapEnv：maxsamples 钳制与默认值。
func TestForensicsMaskCapEnv(t *testing.T) {
	t.Setenv(forensicsTraceEnv, "")
	cfg := forensicsConfigFromEnv()
	if cfg.enabled {
		t.Fatal("trace env off must disable")
	}
	t.Setenv(forensicsTraceEnv, "1")
	t.Setenv(forensicsMaxEnv, "9999")
	cfg = forensicsConfigFromEnv()
	if !cfg.enabled || cfg.maxSamples != forensicsMaxSamplesCap {
		t.Fatalf("cap not applied: %+v", cfg)
	}
	t.Setenv(forensicsMaxEnv, "not-a-number")
	if cfg = forensicsConfigFromEnv(); cfg.maxSamples != forensicsMaxSamplesDefault {
		t.Fatalf("bad env must fall back to default: %+v", cfg)
	}
}

// TestForensicsSeedHasNoDiff：首个同 backend 同 corpus 样本没有“上一份”，
// 不产出 row_diff（§3.4 相对语义）；第二样本才相对它 diff。
func TestForensicsSeedHasNoDiff(t *testing.T) {
	logs := newForensicsBuf(t)
	run := forensicsTestRun(256)
	s1 := run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerSeed, codexWireN("A"), 1)
	run.commit(s1, "fp1", 0, 0, "")
	events := forensicsEvents(t, logs)
	for _, e := range events {
		if e["recordKind"] == "row_diff" {
			t.Fatalf("first sample must not emit row_diff: %v", e)
		}
	}
}

// TestForensicsTwoBackendsNoCrossDiff：两个 backend 的 catalog 不同，
// 相邻样本 diff 只允许同 backend（本机现场 codex/codex-web rawCount 与指纹不同；
// 交叉比较会伪装出整表 changed）。
func TestForensicsTwoBackendsNoCrossDiff(t *testing.T) {
	logs := newForensicsBuf(t)
	run := forensicsTestRun(256)
	run.commit(run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerSeed, codexWireN("A"), 1), "fp-codex-1", 0, 0, "")
	run.commit(run.capture("codex-web", forensicsCorpusAuthoritative, forensicsTriggerSeed, codexWireN("B"), 1), "fp-web-1", 0, 0, "")
	run.commit(run.capture("codex", forensicsCorpusAuthoritative, forensicsTriggerPeriodicTick, codexWireN("A"), 1), "fp-codex-2", 0, 0, "")
	// codex 第二样本（内容与首样本一致）→ 无 diff；若误与 codex-web 的 B 比较则必出 diff。
	run.commit(run.capture("codex-web", forensicsCorpusAuthoritative, forensicsTriggerCatalogSignal, codexWireN("B", "C"), 2), "fp-web-2", 0, 0, "")

	events := forensicsEvents(t, logs)
	var diffs []map[string]any
	for _, e := range events {
		if e["recordKind"] != "row_diff" {
			continue
		}
		diffs = append(diffs, e)
	}
	var webSecondID string
	for _, e := range events {
		if e["recordKind"] == "sample_summary" && e["fingerprint"] == "fp-web-2" {
			webSecondID = e["sampleId"].(string)
		}
	}
	if webSecondID == "" {
		t.Fatal("no fp-web-2 summary")
	}
	// 只允许 codex-web 第二个样本产生 1 条 added（C 加入）；任何其他 diff 都是串样本。
	if len(diffs) != 1 {
		t.Fatalf("cross-backend or stray diffs: %d (%v)", len(diffs), diffs)
	}
	if diffs[0]["sampleId"] != webSecondID || diffs[0]["fieldChangeMask"] != float64(forensicsMaskAdded) {
		t.Fatalf("unexpected diff: %v", diffs[0])
	}
}
