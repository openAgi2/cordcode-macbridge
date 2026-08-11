package codex

// thread_list_contract_test.go冻结 Codex app-server `thread/list` 的 wire 契约（设计 §3.1 /
// §4.3 / §10 Phase 0）。fixture 是对真实 codex-cli 0.147.0-alpha.6.5 app-server 的脱敏实跑
// 捕获（testdata/thread_list_sanitized.json）：字段名/类型/顺序原样保留，仅替换 session id、
// 标题、preview、路径、gitInfo 等敏感值。本文件是 Phase 0 的格式冻结门——若 Codex 升级导致
// schema 漂移，本文件先红，再由设计阶段重新取证，而不是在 Phase 2 catalog client 现场猜格式。
//
// 同时冻结 §4.3 的 catalog 传输选型（codexCatalogTransportSelection 常量）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const threadListFixture = "testdata/thread_list_sanitized.json"

// codexCatalogTransportSelection 冻结 Codex catalog client 的传输选型（设计 §4.3，Phase 0
// 冻结，二选一后不得自由切换）：
//   - 配置了 `-codex-app-server-url`（共享 WebSocket）→ 走共享 ws；
//   - 否则 → 单例 stdio `app-server` 子进程。
//
// 这与 per-session turn 载体 appServerSession 的生命周期不同：catalog client 不绑 thread、
// 长寿命，stdio 模式的进程回收必须走 codexSession 的进程组模式（Setpgid + 进程组 kill），
// 不得照搬 appServerSession 的 Process.Kill()。实现归 Phase 2，此处只冻结决策。
const codexCatalogTransportSelection = "shared-websocket-if-configured-else-singleton-stdio"

// frozenThreadListRequestParams 是 Phase 0 冻结的 thread/list 请求参数（设计 §3.1：cwd 精确
// 过滤、非归档、交互式 source、sortKey=recency_at、sortDirection=desc）。limit 是分页大小、
// cursor 仅供 MacBridge 内部有界读取，二者不在冻结的核心 scope 字段里。
var frozenThreadListRequestParams = map[string]any{
	"cwd":            "/tmp/fixture-workspace", // sanitized
	"archived":       false,
	"source":         "interactive",
	"sortKey":        "recency_at",
	"sortDirection":  "desc",
}

// frozenThreadFields 是 thread/list 单个 thread 条目的冻结字段集（实跑捕获原样）。Phase 2
// catalog client 的字段映射必须以此为准，不得凭记忆新增/重命名字段。
var frozenThreadFields = []string{
	"id", "extra", "sessionId", "forkedFromId", "parentThreadId", "preview",
	"ephemeral", "section", "sectionEnteredAt", "historyMode", "modelProvider",
	"createdAt", "updatedAt", "recencyAt", "status", "path", "cwd", "cliVersion",
	"source", "canAcceptDirectInput", "threadSource", "agentNickname", "agentRole",
	"gitInfo", "name", "turns",
}

type threadListFixtureDoc struct {
	Comment        string          `json:"_comment"`
	CodexCliVersion string         `json:"codexCliVersion"`
	Request        map[string]any  `json:"request"`
	Response       json.RawMessage `json:"response"`
}

func loadThreadListFixture(t *testing.T) threadListFixtureDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(threadListFixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc threadListFixtureDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return doc
}

// TestThreadList_FrozenRequestParams 锁定 thread/list 的冻结请求 scope 字段（设计 §3.1）。
// cwd 已脱敏为 /tmp/fixture-workspace；只校验 scope 字段（archived/source/sortKey/sortDirection）。
func TestThreadList_FrozenRequestParams(t *testing.T) {
	doc := loadThreadListFixture(t)
	for k, want := range frozenThreadListRequestParams {
		got, ok := doc.Request[k]
		if !ok {
			t.Errorf("frozen request missing scope param %q", k)
			continue
		}
		if got != want {
			t.Errorf("request[%q] = %v, want frozen %v", k, got, want)
		}
	}
	if v, _ := doc.Request["sortKey"].(string); v != "recency_at" {
		t.Errorf("sortKey = %q, recency_at 是 Mac Codex UI 顺序的唯一冻结值", v)
	}
	if v, _ := doc.Request["sortDirection"].(string); v != "desc" {
		t.Errorf("sortDirection = %q, want desc", v)
	}
}

// TestThreadList_FrozenResponseShape 锁定响应顶层结构与 cursor 格式。
func TestThreadList_FrozenResponseShape(t *testing.T) {
	doc := loadThreadListFixture(t)
	var resp struct {
		Result struct {
			Data             []map[string]any `json:"data"`
			NextCursor       string           `json:"nextCursor"`
			BackwardsCursor  string           `json:"backwardsCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(doc.Response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Result.Data) == 0 {
		t.Fatal("data array empty in fixture")
	}
	// nextCursor / backwardsCursor 格式："<ISO-ts>|<session-id>"，且引用 data 中存在的 id。
	if resp.Result.NextCursor == "" {
		t.Fatal("nextCursor empty — cursor 是 MacBridge 内部有界读取的冻结字段")
	}
	if !strings.Contains(resp.Result.NextCursor, "|") {
		t.Fatalf("nextCursor %q 不符合 ISO-ts|session-id 格式", resp.Result.NextCursor)
	}
	ids := map[string]bool{}
	for _, th := range resp.Result.Data {
		if id, _ := th["id"].(string); id != "" {
			ids[id] = true
		}
	}
	cursorSID := resp.Result.NextCursor[strings.Index(resp.Result.NextCursor, "|")+1:]
	if !ids[cursorSID] {
		t.Fatalf("nextCursor session id %q 未出现在 data ids 中", cursorSID)
	}
}

// TestThreadList_FrozenThreadFields 锁定每个 thread 条目的字段集（设计 §3.1「字段以 fixture 为准」）。
func TestThreadList_FrozenThreadFields(t *testing.T) {
	doc := loadThreadListFixture(t)
	var resp struct {
		Result struct {
			Data []map[string]any `json:"data"`
		} `json:"result"`
	}
	json.Unmarshal(doc.Response, &resp)
	for i, th := range resp.Result.Data {
		for _, field := range frozenThreadFields {
			if _, ok := th[field]; !ok {
				t.Errorf("thread[%d] 缺少冻结字段 %q", i, field)
			}
		}
		// 关键字段类型断言（设计 §3.1 列出的映射字段）。
		if id, _ := th["id"].(string); id == "" {
			t.Errorf("thread[%d].id 非字符串或空", i)
		}
		if sid, _ := th["sessionId"].(string); sid == "" {
			t.Errorf("thread[%d].sessionId 非字符串或空", i)
		}
		if name, _ := th["name"].(string); name == "" {
			t.Errorf("thread[%d].name 非字符串或空", i)
		}
		if _, ok := th["status"].(map[string]any); !ok {
			t.Errorf("thread[%d].status 必须是对象（含 type）", i)
		}
		if _, ok := th["gitInfo"].(map[string]any); !ok {
			t.Errorf("thread[%d].gitInfo 必须是对象（sha/branch/originUrl）", i)
		}
		for _, tsField := range []string{"createdAt", "updatedAt", "recencyAt"} {
			if _, ok := th[tsField].(float64); !ok {
				t.Errorf("thread[%d].%s 必须是数值（unix 秒）", i, tsField)
			}
		}
	}
}

// TestThreadList_DescByRecencyAt 锁定 sortKey=recency_at + desc 的真实结果顺序。
// thread/list 的排序由上游 app-server 完成，MacBridge 不得本地重排覆盖（设计 §4.1.1）。
func TestThreadList_DescByRecencyAt(t *testing.T) {
	doc := loadThreadListFixture(t)
	var resp struct {
		Result struct {
			Data []map[string]any `json:"data"`
		} `json:"result"`
	}
	json.Unmarshal(doc.Response, &resp)
	var prev float64 = -1
	for i, th := range resp.Result.Data {
		recency, _ := th["recencyAt"].(float64)
		if i > 0 && recency > prev {
			t.Errorf("thread[%d].recencyAt=%v > prev=%v；应按 recency_at desc", i, recency, prev)
		}
		// recencyAt <= updatedAt（recency 是最近活动，不应晚于最后更新）。
		if updated, _ := th["updatedAt"].(float64); recency > updated {
			t.Errorf("thread[%d].recencyAt=%v > updatedAt=%v", i, recency, updated)
		}
		prev = recency
	}
}

// TestCodexCatalogTransportSelection_Frozen 锁定 §4.3 传输选型决策（Phase 0 冻结）。
func TestCodexCatalogTransportSelection_Frozen(t *testing.T) {
	if !strings.Contains(codexCatalogTransportSelection, "shared-websocket") {
		t.Fatal("冻结决策必须包含 shared-websocket 分支")
	}
	if !strings.Contains(codexCatalogTransportSelection, "singleton-stdio") {
		t.Fatal("冻结决策必须包含 singleton-stdio 分支")
	}
}
