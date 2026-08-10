package gobridge

// opencode_catalog_contract_test.go冻结 OpenCode HTTP catalog (/session, /project) 的 wire 契约
// （跨后端 session catalog 同源改造 §3.3 / §5.3 / §10 Phase 0）。fixture 是对真实
// `opencode serve`（127.0.0.1:4097）的脱敏实跑捕获（testdata/opencode/catalog_sanitized.json）：
// 复用 MacBridge 的认证上下文（opencode-managed-server.json 的 username/password），字段名/
// 类型/嵌套/顺序原样保留，仅替换 session id、title、directory、projectID、model id、时间戳等
// 敏感值。本文件是 Phase 0 格式冻结门——若 OpenCode 升级导致 schema 漂移，本文件先红，再由
// 设计阶段重新取证，而不是在 Phase 3/4 catalog client 现场猜格式。
//
// 关键冻结点（修正既有 untyped map[string]interface{} + mapSession 漂移）：
//   - /session 是 array-only 裸数组（无 envelope、无上游 cursor）。
//   - 单 session 时间是 time.{created,updated}（unix ms int），不是顶层 created/updated。
//   - summary 是 diff 统计 dict {additions,deletions,files}，title 是独立字符串字段——二者不是
//     同义互斥（mapSession 当前按 title/summary 互斥处理是漂移，Phase 3/4 须按本 fixture 修正）。
//   - /session 硬上限 100：limit>100 → HTTP 500；当前抓取 workspace 已 >100，超出部分静默不可见。
//     openCodeSessionFetchLimit=100 不得上调；bridge-owned 合成 cursor 是 iOS 唯一可见 cursor。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const opencodeCatalogFixture = "testdata/opencode/catalog_sanitized.json"

// frozenOpenCodeSessionFields 是 /session 单个 session 条目的冻结顶层字段集（实跑捕获原样，
// 13 个）。Phase 3/4 catalog client 的字段映射必须以此为准。
var frozenOpenCodeSessionFields = []string{
	"id", "slug", "projectID", "directory", "path", "summary", "cost", "tokens",
	"title", "agent", "model", "version", "time",
}

// frozenOpenCodeProjectFields 是 /project 单个 project 条目的冻结字段集。
var frozenOpenCodeProjectFields = []string{"id", "worktree", "vcs", "time", "sandboxes"}

type opencodeCatalogFixtureDoc struct {
	Comment        string                 `json:"_comment"`
	OpencodeVersion string                `json:"opencodeVersion"`
	Auth           map[string]any         `json:"auth"`
	Ceiling        map[string]any         `json:"sessionEndpointCeiling"`
	Requests       map[string]any         `json:"requests"`
	Responses      struct {
		SessionRoots struct {
			Shape                string              `json:"_shape"`
			CapturedTotalReturned int                 `json:"_capturedTotalReturned"`
			CapturedTotalIsCapped bool                `json:"_capturedTotalIsCapped"`
			Sessions              []map[string]any    `json:"sessions"`
		} `json:"session_roots"`
		SessionScoped struct {
			Shape    string           `json:"_shape"`
			Sessions []map[string]any `json:"sessions"`
		} `json:"session_scoped"`
		Project struct {
			Shape                string           `json:"_shape"`
			CapturedTotalReturned int              `json:"_capturedTotalReturned"`
			Projects             []map[string]any `json:"projects"`
		} `json:"project"`
	} `json:"responses"`
}

func loadOpenCodeCatalogFixture(t *testing.T) opencodeCatalogFixtureDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(opencodeCatalogFixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc opencodeCatalogFixtureDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return doc
}

// TestOpenCodeCatalog_AuthModel 锁定 §5.3.6 认证模型：HTTP Basic、realm "Secure Area"、
// 认证上下文来自 MacBridge opencode-managed-server.json、passwordless server 被拒绝。
func TestOpenCodeCatalog_AuthModel(t *testing.T) {
	doc := loadOpenCodeCatalogFixture(t)
	if doc.Auth["scheme"] != "HTTP Basic" {
		t.Fatalf("auth scheme = %v, want HTTP Basic", doc.Auth["scheme"])
	}
	if doc.Auth["wwwAuthenticateRealm"] != "Secure Area" {
		t.Errorf("realm = %v, want 'Secure Area'（401 challenge 实测值）", doc.Auth["wwwAuthenticateRealm"])
	}
	if src, _ := doc.Auth["authContextSource"].(string); src == "" {
		t.Error("authContextSource 缺失 — Phase 0 须坐实 creds 来自 managed-server.json")
	}
	if rejected, _ := doc.Auth["passwordlessRejected"].(bool); !rejected {
		t.Error("passwordlessRejected 应为 true — OpenCodeManagedServer 要求 unauth→401 且 authed→200")
	}
}

// TestOpenCodeCatalog_SessionArrayOnlyAndCeiling 锁定 §3.3 / §5.3.3 / §5.3.4：/session 是
// array-only 裸数组（无 envelope、无上游 cursor），硬上限 100，且当前 workspace 已超限。
// openCodeSessionFetchLimit=100 不得上调（>100 → HTTP 500）。
func TestOpenCodeCatalog_SessionArrayOnlyAndCeiling(t *testing.T) {
	doc := loadOpenCodeCatalogFixture(t)
	sr := doc.Responses.SessionRoots
	// array-only（无 envelope、无上游 cursor）。
	if sr.CapturedTotalIsCapped != true {
		t.Error("workspaceExceeds/capped 应为 true — 实测 roots 返回恰好 100 = 触顶")
	}
	if doc.Ceiling["arrayOnly"] != true {
		t.Error("arrayOnly 必须为 true（/session 无上游 cursor，§3.3）")
	}
	hardCap, _ := doc.Ceiling["hardCap"].(float64)
	if hardCap != 100 {
		t.Errorf("hardCap = %v, want 100（openCodeSessionFetchLimit=100，不得上调）", hardCap)
	}
	ev, _ := doc.Ceiling["evidence"].(map[string]any)
	if ev == nil {
		t.Fatal("ceiling evidence 缺失 — Phase 0 必须记录 limit=200→500 / no-limit→100 的实测证据")
	}
	if ev["limit200"] == "" {
		t.Error("limit200 证据缺失（>100 → HTTP 500 UnknownError 是硬上限的铁证）")
	}
	if exceeded, _ := doc.Ceiling["workspaceExceedsCeiling"].(bool); !exceeded {
		t.Error("workspaceExceedsCeiling 应为 true — 实测该 workspace >100，超出部分静默不可见（既有天花板）")
	}
}

// TestOpenCodeCatalog_SessionFrozenFields 锁定每个 session 条目的 13 个冻结字段及其类型/嵌套
// （设计 §3.1/§3.3「字段以 fixture 为准」）。
func TestOpenCodeCatalog_SessionFrozenFields(t *testing.T) {
	doc := loadOpenCodeCatalogFixture(t)
	sessions := doc.Responses.SessionRoots.Sessions
	if len(sessions) == 0 {
		t.Fatal("session_roots.sessions 为空")
	}
	for i, s := range sessions {
		for _, f := range frozenOpenCodeSessionFields {
			if _, ok := s[f]; !ok {
				t.Errorf("session[%d] 缺少冻结字段 %q", i, f)
			}
		}
		// id / slug / title / agent / version: string。
		for _, sf := range []string{"id", "slug", "title", "agent", "version"} {
			if _, ok := s[sf].(string); !ok {
				t.Errorf("session[%d].%s 必须是 string，实际 %T", i, sf, s[sf])
			}
		}
		// summary: dict {additions,deletions,files}（diff 统计，非 title 同义词）。
		summ, ok := s["summary"].(map[string]any)
		if !ok {
			t.Fatalf("session[%d].summary 必须是对象 {additions,deletions,files}", i)
		}
		for _, k := range []string{"additions", "deletions", "files"} {
			if _, ok := summ[k].(float64); !ok {
				t.Errorf("session[%d].summary.%s 必须是数值", i, k)
			}
		}
		// tokens: {input,output,reasoning,cache{read,write}}。
		tok, ok := s["tokens"].(map[string]any)
		if !ok {
			t.Fatalf("session[%d].tokens 必须是对象", i)
		}
		for _, k := range []string{"input", "output", "reasoning"} {
			if _, ok := tok[k].(float64); !ok {
				t.Errorf("session[%d].tokens.%s 必须是数值", i, k)
			}
		}
		cache, ok := tok["cache"].(map[string]any)
		if !ok {
			t.Fatalf("session[%d].tokens.cache 必须是对象 {read,write}", i)
		}
		for _, k := range []string{"read", "write"} {
			if _, ok := cache[k].(float64); !ok {
				t.Errorf("session[%d].tokens.cache.%s 必须是数值", i, k)
			}
		}
		// model: {id,providerID,variant}。
		model, ok := s["model"].(map[string]any)
		if !ok {
			t.Fatalf("session[%d].model 必须是对象 {id,providerID,variant}", i)
		}
		for _, k := range []string{"id", "providerID", "variant"} {
			if _, ok := model[k].(string); !ok {
				t.Errorf("session[%d].model.%s 必须是 string", i, k)
			}
		}
		// time: {created,updated} unix ms int。
		tm, ok := s["time"].(map[string]any)
		if !ok {
			t.Fatalf("session[%d].time 必须是对象 {created,updated}", i)
		}
		for _, k := range []string{"created", "updated"} {
			if _, ok := tm[k].(float64); !ok {
				t.Errorf("session[%d].time.%s 必须是数值（unix ms）", i, k)
			}
		}
		// cost: int。
		if _, ok := s["cost"].(float64); !ok {
			t.Errorf("session[%d].cost 必须是数值", i)
		}
	}
}

// TestOpenCodeCatalog_SessionDescByTimeUpdated 锁定 /session 的上游排序（time.updated desc）。
// 排序由上游 opencode server 决定；MacBridge 在上游有定义序时不得用 sortSessionsByUpdatedAt
// 覆盖（设计 §5.3.3 / Phase 4 §421）。
func TestOpenCodeCatalog_SessionDescByTimeUpdated(t *testing.T) {
	doc := loadOpenCodeCatalogFixture(t)
	sessions := doc.Responses.SessionRoots.Sessions
	var prev float64 = -1
	for i, s := range sessions {
		tm, _ := s["time"].(map[string]any)
		updated, _ := tm["updated"].(float64)
		if i > 0 && updated > prev {
			t.Errorf("session[%d].time.updated=%v > prev=%v；上游应按 updated desc", i, updated, prev)
		}
		// created <= updated（创建不应晚于最后更新）。
		created, _ := tm["created"].(float64)
		if created > updated {
			t.Errorf("session[%d].time.created=%v > updated=%v", i, created, updated)
		}
		prev = updated
	}
}

// TestOpenCodeCatalog_ProjectFrozenFields 锁定 /project 响应字段集（设计 §5.3.2：以 /project
// 决定项目列表）。
func TestOpenCodeCatalog_ProjectFrozenFields(t *testing.T) {
	doc := loadOpenCodeCatalogFixture(t)
	projects := doc.Responses.Project.Projects
	if len(projects) == 0 {
		t.Fatal("project.projects 为空")
	}
	for i, p := range projects {
		for _, f := range frozenOpenCodeProjectFields {
			if _, ok := p[f]; !ok {
				t.Errorf("project[%d] 缺少冻结字段 %q", i, f)
			}
		}
		for _, sf := range []string{"id", "worktree", "vcs"} {
			if _, ok := p[sf].(string); !ok {
				t.Errorf("project[%d].%s 必须是 string", i, sf)
			}
		}
		tm, ok := p["time"].(map[string]any)
		if !ok {
			t.Fatalf("project[%d].time 必须是对象 {created,updated}", i)
		}
		for _, k := range []string{"created", "updated"} {
			if _, ok := tm[k].(float64); !ok {
				t.Errorf("project[%d].time.%s 必须是数值（unix ms）", i, k)
			}
		}
		// sandboxes: array（实跑为空数组，保留 array 类型）。
		if _, ok := p["sandboxes"].([]any); !ok {
			t.Errorf("project[%d].sandboxes 必须是数组", i)
		}
	}
}

// TestOpenCodeCatalog_DirectoryScope 锁定 §5.3.5：x-opencode-directory header 把 /session
// 作用域到单个 directory，iOS 不重建 parent/root 过滤（upstream 处理）。
func TestOpenCodeCatalog_DirectoryScope(t *testing.T) {
	doc := loadOpenCodeCatalogFixture(t)
	scoped := doc.Responses.SessionScoped.Sessions
	if len(scoped) == 0 {
		t.Fatal("session_scoped.sessions 为空 — Phase 0 须捕获一次 x-opencode-directory 作用域响应")
	}
	// 作用域响应里所有 session 的 directory 应一致（upstream 已按 header 过滤）。
	firstDir, _ := scoped[0]["directory"].(string)
	if firstDir == "" {
		t.Fatal("scoped session[0].directory 为空")
	}
	for i, s := range scoped[1:] {
		if d, _ := s["directory"].(string); d != firstDir {
			t.Errorf("scoped session[%d].directory=%q != %q（x-opencode-directory 应过滤到单一 directory）", i+1, d, firstDir)
		}
	}
}
