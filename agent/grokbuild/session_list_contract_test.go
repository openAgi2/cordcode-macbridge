package grokbuild

// session_list_contract_test.go冻结 Grok ACP `session/list` 的 wire 契约（设计 §3.4 / §5.4 /
// §10 Phase 0）。fixture 是对真实 grok 1.0.0（`grok agent --no-leader stdio`）的脱敏实跑捕获
// （testdata/session_list_sanitized.json）：字段名/类型/顺序原样保留，仅替换 sessionId、title、
// cwd、_meta 中的 git facets 等敏感值。本文件是 Phase 0 格式冻结门——若 Grok 升级导致 schema
// 漂移，本文件先红，再由设计阶段重新取证，而不是在 Phase 3 catalog client 现场猜格式。
//
// 关键冻结点（修正既有 struct 漂移）：per-session 时间字段是 `updatedAt`（ISO-8601 string），
// 不是 acpSessionInfo 目前的 `LastActivity`。Phase 3 字段映射必须以本 fixture 为准。

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sessionListFixture = "testdata/session_list_sanitized.json"

// frozenGrokSessionFields 是 session/list 单个 session 条目的冻结字段集（实跑捕获原样，
// 设计 §3.4）。注意是 updatedAt，不是 lastActivity。
var frozenGrokSessionFields = []string{"sessionId", "cwd", "title", "updatedAt", "_meta"}

type grokSessionListFixtureDoc struct {
	Comment     string `json:"_comment"`
	GrokVersion string `json:"grokVersion"`
	Initialize  struct {
		ProtocolVersion   any `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession         any `json:"loadSession"`
			SessionCapabilities struct {
				List   any `json:"list"`
				Resume any `json:"resume"`
				Close  any `json:"close"`
				Delete any `json:"delete"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
		AuthMethods []map[string]any `json:"authMethods"`
	} `json:"initialize"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

func loadGrokSessionListFixture(t *testing.T) grokSessionListFixtureDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sessionListFixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc grokSessionListFixtureDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return doc
}

// acpFlagEnabled 判定 ACP capability flag 是否启用。ACP flag 可能是 JSON bool true，
// 也可能是空对象 {}（「supported」），二者都算启用（见 acpFlag 解码逻辑）。
func acpFlagEnabled(v any) bool {
	switch vv := v.(type) {
	case bool:
		return vv
	case map[string]any:
		return true // 空对象 = supported
	}
	return false
}

// TestGrokACP_InitializeCapabilities 锁定握手声明 sessionCapabilities.list（设计 §3.4：旧实现
// 「Grok 没有 ACP list」的前提已失效）。Phase 3 catalog client 必须用此 capability 做显式门控，
// 未声明时报告版本不支持，不静默退回磁盘 scanner。
func TestGrokACP_InitializeCapabilities(t *testing.T) {
	doc := loadGrokSessionListFixture(t)
	if !acpFlagEnabled(doc.Initialize.AgentCapabilities.SessionCapabilities.List) {
		t.Fatal("sessionCapabilities.list 未声明 — Phase 3 必须据此门控，不得静默退回 scanner")
	}
	if !acpFlagEnabled(doc.Initialize.AgentCapabilities.LoadSession) {
		t.Fatal("loadSession 未声明（设计 §3.4 冻结 loadSession=true）")
	}
	// authenticate 在本环境走 cached_token（~/.grok/auth.json）；fixture 记录真实 authMethods 形态。
	if len(doc.Initialize.AuthMethods) == 0 {
		t.Fatal("authMethods 为空 — 真实握手声明了 cached_token + grok.com")
	}
}

// TestGrokSessionList_FrozenShape 锁定响应顶层结构与 session 字段集。
func TestGrokSessionList_FrozenShape(t *testing.T) {
	doc := loadGrokSessionListFixture(t)
	var resp struct {
		Result struct {
			Sessions   []map[string]any `json:"sessions"`
			NextCursor string           `json:"nextCursor"`
			Meta       json.RawMessage  `json:"_meta"`
		} `json:"result"`
	}
	if err := json.Unmarshal(doc.Response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Result.Sessions) == 0 {
		t.Fatal("sessions 数组为空")
	}
	if resp.Result.NextCursor == "" {
		t.Fatal("nextCursor 缺失 — 设计 §5.4.3 要求用 _meta/nextCursor 的真实分页字段")
	}
	if len(resp.Result.Meta) == 0 {
		t.Fatal("顶层 _meta 缺失（x.ai/facets 聚合）")
	}
	for i, s := range resp.Result.Sessions {
		for _, f := range frozenGrokSessionFields {
			if _, ok := s[f]; !ok {
				t.Errorf("session[%d] 缺少冻结字段 %q", i, f)
			}
		}
		// 关键类型断言：updatedAt 是 ISO-8601 string（不是 lastActivity、不是数值）。
		updatedAt, ok := s["updatedAt"].(string)
		if !ok {
			t.Fatalf("session[%d].updatedAt 必须是 string，实际 %T（设计 §3.4 冻结 updatedAt）", i, s["updatedAt"])
		}
		if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
			t.Errorf("session[%d].updatedAt %q 不是合法 RFC3339Nano: %v", i, updatedAt, err)
		}
		if sid, _ := s["sessionId"].(string); sid == "" {
			t.Errorf("session[%d].sessionId 非字符串或空", i)
		}
		// _meta 必须含 x.ai/session vendor 扩展（kind + git facets）。
		meta, ok := s["_meta"].(map[string]any)
		if !ok {
			t.Fatalf("session[%d]._meta 必须是对象", i)
		}
		if _, ok := meta["x.ai/session"]; !ok {
			t.Errorf("session[%d]._meta 缺少 x.ai/session 扩展", i)
		}
	}
}

// TestGrokSessionList_NextCursorIsOpaque 锁定 nextCursor 是 opaque base64（客户端不解析）。
// MacBridge 只内部有界读取/透传，不向 iOS 透传上游 cursor（设计 §4.1 boundary）。
func TestGrokSessionList_NextCursorIsOpaque(t *testing.T) {
	doc := loadGrokSessionListFixture(t)
	var resp struct {
		Result struct {
			NextCursor string `json:"nextCursor"`
		} `json:"result"`
	}
	json.Unmarshal(doc.Response, &resp)
	c := resp.Result.NextCursor
	// base64 (std or url-safe, with or without padding) — opaque to the client.
	if _, err := base64.StdEncoding.DecodeString(c); err != nil {
		if _, err := base64.RawStdEncoding.DecodeString(c); err != nil {
			if _, err := base64.RawURLEncoding.DecodeString(c); err != nil {
				t.Fatalf("nextCursor %q 不是 base64 opaque（客户端应不解析，MacBridge 仅内部有界读取）", c)
			}
		}
	}
}

// TestGrokSessionList_DescByUpdatedAt 锁定 session/list 的返回顺序（updatedAt desc）。
// 排序由上游 ACP 服务端决定，MacBridge 不得本地重排覆盖（设计 §4.1.1）。
func TestGrokSessionList_DescByUpdatedAt(t *testing.T) {
	doc := loadGrokSessionListFixture(t)
	var resp struct {
		Result struct {
			Sessions []map[string]any `json:"sessions"`
		} `json:"result"`
	}
	json.Unmarshal(doc.Response, &resp)
	var prev time.Time
	for i, s := range resp.Result.Sessions {
		ts, _ := s["updatedAt"].(string)
		tt, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			t.Fatalf("session[%d] updatedAt parse: %v", i, err)
		}
		if i > 0 && tt.After(prev) {
			t.Errorf("session[%d].updatedAt=%v 在 prev=%v 之后；应按 updatedAt desc", i, tt, prev)
		}
		prev = tt
	}
}
