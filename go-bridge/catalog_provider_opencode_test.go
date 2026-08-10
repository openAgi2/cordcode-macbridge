package gobridge

// catalog_provider_opencode_test.go验证 openCodeCatalogProvider 把 Phase 0 冻结的
// OpenCode /session fixture（testdata/opencode/catalog_sanitized.json）规范化成
// NormalizedSession 的正确性，并冻结 fingerprint 的确定性（设计 §4.1 / §4.1.1 / §11）。
//
// 不启动 live `opencode serve`：用 fakeOpenCodeLister 直接回放 fixture 的 raw session
// 数组，驱动 FetchPage0 → normalizeOpenCodeSession → fingerprintCatalog。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeOpenCodeLister 回放 fixture 的 session_roots.sessions 作为 /session 上游响应，
// 并记录最后一次收到的 opts，用于断言 provider 正确转发 scope。
type fakeOpenCodeLister struct {
	sessions []map[string]interface{}
	lastOpts OpenCodeSessionListOptions
}

func (f *fakeOpenCodeLister) listSessions(opts OpenCodeSessionListOptions) (OpenCodeSessionListResult, error) {
	f.lastOpts = opts
	return OpenCodeSessionListResult{Sessions: f.sessions}, nil
}

func loadFixtureRootsSessions(t *testing.T) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opencode", "catalog_sanitized.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc opencodeCatalogFixtureDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(doc.Responses.SessionRoots.Sessions) == 0 {
		t.Fatal("fixture session_roots.sessions 为空")
	}
	return doc.Responses.SessionRoots.Sessions
}

// TestOpenCodeCatalogProvider_ForwardsScope 断言 provider 把 CatalogQuery 的 Directory /
// RootsOnly 正确转发给上游 lister，上游 fetch 预算固定为 openCodeSessionFetchLimit
// （不随 client limit 变化），且 client cursor **不**越过 provider 边界进入上游调用
// （OpenCode /session 无上游 cursor，设计 §4.1「统一分页边界」）。
func TestOpenCodeCatalogProvider_ForwardsScope(t *testing.T) {
	raw := loadFixtureRootsSessions(t)
	f := &fakeOpenCodeLister{sessions: raw}
	p := &openCodeCatalogProvider{lister: f}
	if _, err := p.FetchPage0(context.Background(), CatalogQuery{
		BackendID: "opencode",
		Directory: "/tmp/fixture-workspace",
		RootsOnly: true,
		Limit:     20,
		Cursor:    "client-cursor-must-not-leak-upstream",
	}); err != nil {
		t.Fatalf("FetchPage0: %v", err)
	}
	if f.lastOpts.Directory != "/tmp/fixture-workspace" {
		t.Errorf("lister Directory = %q, want 转发的 scope", f.lastOpts.Directory)
	}
	if !f.lastOpts.Roots {
		t.Errorf("lister Roots = false, want true（provider 须把 RootsOnly → Roots 转发）")
	}
	if f.lastOpts.Limit != openCodeSessionFetchLimit {
		t.Errorf("lister Limit = %d, want %d（上游 fetch 预算固定，不随 client limit）",
			f.lastOpts.Limit, openCodeSessionFetchLimit)
	}
	if f.lastOpts.Cursor != "" {
		t.Errorf("lister Cursor = %q, want 空（/session 无上游 cursor，client cursor 不越界）",
			f.lastOpts.Cursor)
	}
}

// TestOpenCodeCatalogProvider_NormalizesFixture 断言 provider 把每个 fixture raw session
// 规范化成预期 NormalizedSession：stable id、原生 title（不用 summary 兜底）、directory、
// projectID、time.{created,updated}、无 parent → IsRoot、无 archived → Archived=false。
// 同时证明上游序被保留（page.Sessions[i] 对齐 raw[i]，§4.2 provider 不本地重排）。
func TestOpenCodeCatalogProvider_NormalizesFixture(t *testing.T) {
	raw := loadFixtureRootsSessions(t)
	provider := &openCodeCatalogProvider{lister: &fakeOpenCodeLister{sessions: raw}}

	page, err := provider.FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode", RootsOnly: true})
	if err != nil {
		t.Fatalf("FetchPage0: %v", err)
	}
	if len(page.Sessions) != len(raw) {
		t.Fatalf("session count = %d, want %d", len(page.Sessions), len(raw))
	}
	for i, ns := range page.Sessions {
		r := raw[i]
		wantID, _ := r["id"].(string)
		if ns.StableID != wantID {
			t.Errorf("session[%d].StableID = %q, want %q", i, ns.StableID, wantID)
		}
		wantTitle, _ := r["title"].(string)
		if ns.Title != wantTitle {
			t.Errorf("session[%d].Title = %q, want %q（§4.2 标题 = backend catalog 原生返回）",
				i, ns.Title, wantTitle)
		}
		wantDir, _ := r["directory"].(string)
		if ns.Directory != wantDir {
			t.Errorf("session[%d].Directory = %q, want %q", i, ns.Directory, wantDir)
		}
		wantProj, _ := r["projectID"].(string)
		if ns.ProjectID != wantProj {
			t.Errorf("session[%d].ProjectID = %q, want %q", i, ns.ProjectID, wantProj)
		}
		tm, _ := r["time"].(map[string]interface{})
		wantCreated := int64(tm["created"].(float64))
		wantUpdated := int64(tm["updated"].(float64))
		if ns.CreatedAtMillis != wantCreated {
			t.Errorf("session[%d].CreatedAtMillis = %d, want %d", i, ns.CreatedAtMillis, wantCreated)
		}
		if ns.UpdatedAtMillis != wantUpdated {
			t.Errorf("session[%d].UpdatedAtMillis = %d, want %d", i, ns.UpdatedAtMillis, wantUpdated)
		}
		if ns.BackendID != "opencode" {
			t.Errorf("session[%d].BackendID = %q, want opencode", i, ns.BackendID)
		}
		// fixture 无 parent 字段 → IsRoot；无 archived → Archived=false。
		if !ns.IsRoot {
			t.Errorf("session[%d].IsRoot = false, want true（fixture 无 parentID）", i)
		}
		if ns.Archived {
			t.Errorf("session[%d].Archived = true, want false（fixture time 无 archived）", i)
		}
		if ns.ParentID != "" {
			t.Errorf("session[%d].ParentID = %q, want empty", i, ns.ParentID)
		}
		if ns.OrderingKey != wantID {
			t.Errorf("session[%d].OrderingKey = %q, want %q", i, ns.OrderingKey, wantID)
		}
	}
}

// TestOpenCodeCatalogProvider_FingerprintDeterministic 断言等量数据 → 相同 fingerprint，
// 且 fingerprint 非空（设计 §4.1.1「数据未变 → epoch 不变」）。fingerprint 是 v2 snapshot
// epoch 的输入（deriveSnapshotEpoch），漂移会让旧 cursor 命中 cursorStaleEpochMismatch。
func TestOpenCodeCatalogProvider_FingerprintDeterministic(t *testing.T) {
	raw := loadFixtureRootsSessions(t)
	provider := &openCodeCatalogProvider{lister: &fakeOpenCodeLister{sessions: raw}}

	p1, err := provider.FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode"})
	if err != nil {
		t.Fatalf("FetchPage0 #1: %v", err)
	}
	p2, err := provider.FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode"})
	if err != nil {
		t.Fatalf("FetchPage0 #2: %v", err)
	}
	if p1.Fingerprint == "" {
		t.Fatal("fingerprint 为空 — Phase 1B snapshot epoch 无输入")
	}
	if p1.Fingerprint != p2.Fingerprint {
		t.Fatalf("等量数据 fingerprint 不稳定：%s != %s（§4.1.1 数据未变须 epoch 不变）",
			p1.Fingerprint, p2.Fingerprint)
	}
}

// TestOpenCodeCatalogProvider_FingerprintDetectsChange 断言 catalog 数据变化
// （session 的 updated 时间偏移）→ fingerprint 变化。这是 §4.1.1「fingerprint 变化 →
// cursor_stale」的前提：旧 v2 cursor 的 epoch 与新快照不匹配，iOS 回到 page-0。
func TestOpenCodeCatalogProvider_FingerprintDetectsChange(t *testing.T) {
	raw := loadFixtureRootsSessions(t)
	base, err := (&openCodeCatalogProvider{lister: &fakeOpenCodeLister{sessions: raw}}).
		FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode"})
	if err != nil {
		t.Fatalf("base FetchPage0: %v", err)
	}
	mutated := bumpFirstUpdated(raw, 60_000)
	mut, err := (&openCodeCatalogProvider{lister: &fakeOpenCodeLister{sessions: mutated}}).
		FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode"})
	if err != nil {
		t.Fatalf("mutated FetchPage0: %v", err)
	}
	if base.Fingerprint == mut.Fingerprint {
		t.Fatalf("recency 变化后 fingerprint 未变 — §4.1.1 cursor_stale 检测前提失效")
	}
}

// TestOpenCodeCatalogProvider_FingerprintDetectsMembershipChange 断言成员变化（移除一个
// session）→ fingerprint 变化，覆盖 §4.1.1 的删除场景（与上一条的 recency 变化互补）。
func TestOpenCodeCatalogProvider_FingerprintDetectsMembershipChange(t *testing.T) {
	raw := loadFixtureRootsSessions(t)
	if len(raw) < 2 {
		t.Fatal("fixture 需要 ≥2 sessions 才能测成员变化")
	}
	base, err := (&openCodeCatalogProvider{lister: &fakeOpenCodeLister{sessions: raw}}).
		FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode"})
	if err != nil {
		t.Fatalf("base FetchPage0: %v", err)
	}
	pruned := append(append([]map[string]interface{}{}, raw[:1]...), raw[2:]...) // 丢掉 raw[1]
	prunedPage, err := (&openCodeCatalogProvider{lister: &fakeOpenCodeLister{sessions: pruned}}).
		FetchPage0(context.Background(), CatalogQuery{BackendID: "opencode"})
	if err != nil {
		t.Fatalf("pruned FetchPage0: %v", err)
	}
	if base.Fingerprint == prunedPage.Fingerprint {
		t.Fatalf("成员被移除后 fingerprint 未变 — §4.1.1 删除场景检测失效")
	}
	if len(prunedPage.Sessions) != len(raw)-1 {
		t.Errorf("pruned session count = %d, want %d", len(prunedPage.Sessions), len(raw)-1)
	}
}

// bumpFirstUpdated 返回 raw 的一个浅拷贝，其中 session[0].time.updated 增加了 deltaMillis。
// 不修改原 raw（避免测试间共享状态污染）。
func bumpFirstUpdated(raw []map[string]interface{}, deltaMillis int64) []map[string]interface{} {
	out := make([]map[string]interface{}, len(raw))
	copy(out, raw)
	src := out[0]
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	if tm, ok := src["time"].(map[string]interface{}); ok {
		newTm := make(map[string]interface{}, len(tm))
		for k, v := range tm {
			newTm[k] = v
		}
		newTm["updated"] = float64(int64(tm["updated"].(float64)) + deltaMillis)
		dst["time"] = newTm
	}
	out[0] = dst
	return out
}
