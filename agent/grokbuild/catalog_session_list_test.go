package grokbuild

// catalog_session_list_test.go 验证 Grok catalog session/list 的 **frozen fixture 解析**与
// 字段映射（设计 §5.4 Phase 3）。fixture 来源：testdata/session_list_sanitized.json —— 真实
// Grok 1.0.0 `session/list` 响应（已脱敏）。本测试证明 grokSessionListResult +
// grokSessionItemToAgentSessionInfo 能正确解包真实 wire 形态并映射到 core.AgentSessionInfo，
// 而不是基于假设的字段名。
//
// 关键 frozen 字段（来自真实响应，**不是** 猜测）：
//   - session item: sessionId / cwd / title / updatedAt（RFC3339Nano，带时区偏移）/ _meta
//   - _meta key = "x.ai/session"（带斜杠，**非** 点号 "x.ai.session.facets"）
//   - facets: branch / cwd / gitRoot / kind / repo
//   - 映射：ID←sessionId / Summary←title / Directory←cwd / ModifiedAt←parse(updatedAt) /
//     GitBranch←_meta.x.ai/session.facets.branch
//
// discriminator：若 grokSessionListResult 字段名与真实 wire 不符（如误用 "x.ai.session.facets"
// 点号 key、或误用 LastActivity），json.Unmarshal 会静默丢字段 → 断言失败。故本测试对 frozen
// 形态敏感。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const grokCatalogHelperArg = "--grok-catalog-lifetime-test-helper"

// Re-exec this test binary as a tiny ACP catalog process. This stays strictly in the test target;
// production still launches the configured Grok CLI and has no mock/fallback branch.
func init() {
	for _, arg := range os.Args[1:] {
		if arg != grokCatalogHelperArg {
			continue
		}
		scanner := bufio.NewScanner(os.Stdin)
		encoder := json.NewEncoder(os.Stdout)
		for scanner.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(scanner.Bytes(), &req) != nil {
				continue
			}
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"sessionCapabilities": map[string]any{"list": true},
					},
				}
			case "session/list":
				result = map[string]any{"sessions": []map[string]any{{
					"sessionId": "session_lifetime",
					"cwd":       "/tmp/catalog-lifetime",
					"title":     "lifetime proof",
					"updatedAt": "2026-08-11T08:00:00Z",
				}}}
			default:
				result = map[string]any{}
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  result,
			})
		}
		os.Exit(0)
	}
}

// loadFrozenSessionListResult 从 testdata/session_list_sanitized.json 取出 response.result，
// 反序列化为 grokSessionListResult。fixture 结构：{"request":{...},"response":{...,"result":{...}}}。
func loadFrozenSessionListResult(t *testing.T) grokSessionListResult {
	t.Helper()
	path := filepath.Join("testdata", "session_list_sanitized.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen fixture: %v", err)
	}
	var top struct {
		Response struct {
			Result json.RawMessage `json:"result"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal fixture top: %v", err)
	}
	if len(top.Response.Result) == 0 {
		t.Fatal("frozen fixture missing response.result")
	}
	var res grokSessionListResult
	if err := json.Unmarshal(top.Response.Result, &res); err != nil {
		t.Fatalf("unmarshal session/list result: %v", err)
	}
	return res
}

// TestFrozenSessionListResult_ParsesRealShape：frozen fixture 必须解析出 >=1 条 session，
// 每条字段齐全（sessionId 非空、updatedAt 可被 RFC3339Nano 解析、_meta.x.ai/session.facets.branch
// 存在）。证明 frozen struct 字段名与真实 Grok 1.0.0 wire 一致。
func TestFrozenSessionListResult_ParsesRealShape(t *testing.T) {
	res := loadFrozenSessionListResult(t)
	if len(res.Sessions) == 0 {
		t.Fatal("frozen fixture 解析出 0 sessions（grokSessionListResult 字段名与真实 wire 不符？）")
	}
	// nextCursor 在 frozen fixture 中存在（opaque base64），应被解析（非必需非空，但 frozen 有值）。
	if res.NextCursor == "" {
		t.Log("frozen fixture nextCursor 为空（部分 fixture 可能裁剪了分页 token，可接受）")
	}
	for i, s := range res.Sessions {
		if s.SessionID == "" {
			t.Fatalf("session[%d] sessionId 为空（frozen key 误用？）", i)
		}
		if s.Cwd == "" {
			t.Fatalf("session[%d] cwd 为空", i)
		}
		// updatedAt 必须是可解析的 RFC3339Nano（带时区偏移）。
		if s.UpdatedAt == "" {
			t.Fatalf("session[%d] updatedAt 为空", i)
		}
		if _, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err != nil {
			t.Fatalf("session[%d] updatedAt %q 非 RFC3339Nano：%v", i, s.UpdatedAt, err)
		}
		// frozen facets.branch 在 fixture 中为 "main"（sanitized）。证明 _meta key 是
		// "x.ai/session"（带斜杠）且 facets 子对象正确解包。
		if s.Meta.Session.Facets.Branch == "" {
			t.Fatalf("session[%d] _meta.x.ai/session.facets.branch 为空（key 误用点号或结构错？）", i)
		}
	}
}

// TestFrozenSessionListResult_MapsToAgentSessionInfo：frozen fixture 经
// grokSessionItemToAgentSessionInfo 映射后，core.AgentSessionInfo 字段必须正确（ID/Summary/
// Directory/ModifiedAt/GitBranch）。证明映射遵循 frozen fixture，而非假设字段名。
func TestFrozenSessionListResult_MapsToAgentSessionInfo(t *testing.T) {
	res := loadFrozenSessionListResult(t)
	first := res.Sessions[0]
	info := grokSessionItemToAgentSessionInfo(first)

	if info.ID != first.SessionID {
		t.Fatalf("ID = %q, want %q（sessionId）", info.ID, first.SessionID)
	}
	if info.Summary != first.Title {
		t.Fatalf("Summary = %q, want %q（title）", info.Summary, first.Title)
	}
	if info.Directory != first.Cwd {
		t.Fatalf("Directory = %q, want %q（cwd）", info.Directory, first.Cwd)
	}
	wantT, err := time.Parse(time.RFC3339Nano, first.UpdatedAt)
	if err != nil {
		t.Fatalf("frozen updatedAt parse: %v", err)
	}
	if !info.ModifiedAt.Equal(wantT) {
		t.Fatalf("ModifiedAt = %v, want %v（updatedAt UTC）", info.ModifiedAt, wantT.UTC())
	}
	if info.GitBranch != first.Meta.Session.Facets.Branch {
		t.Fatalf("GitBranch = %q, want %q（_meta.x.ai/session.facets.branch）",
			info.GitBranch, first.Meta.Session.Facets.Branch)
	}
}

// TestParseGrokCatalogUpdatedAt_EdgeCases：空串 / 非法串 → 零值（不 panic）。合法 RFC3339Nano
// → UTC time。证明 parser 对缺值 session 不崩溃（排序按零值兜底）。
func TestParseGrokCatalogUpdatedAt_EdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"not-a-date", time.Time{}},
		{"2026-08-08T14:36:11.900198+00:00",
			time.Date(2026, 8, 8, 14, 36, 11, 900198000, time.UTC)},
	}
	for _, c := range cases {
		got := parseGrokCatalogUpdatedAt(c.in)
		if !got.Equal(c.want) {
			t.Fatalf("parseGrokCatalogUpdatedAt(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestGrokSessionItemToAgentSessionInfo_EmptyMeta：_meta 为零值（缺 facets）时映射不 panic，
// GitBranch="" 。证明对真实中可能出现的缺 facets session 容错。
func TestGrokSessionItemToAgentSessionInfo_EmptyMeta(t *testing.T) {
	info := grokSessionItemToAgentSessionInfo(grokSessionListItem{
		SessionID: "s1",
		Cwd:       "/tmp",
		Title:     "t",
	})
	if info.ID != "s1" || info.Directory != "/tmp" || info.Summary != "t" {
		t.Fatalf("basic mapping wrong: %+v", info)
	}
	if info.GitBranch != "" {
		t.Fatalf("GitBranch = %q, want empty（无 facets）", info.GitBranch)
	}
	if !info.ModifiedAt.IsZero() {
		t.Fatalf("ModifiedAt = %v, want zero（无 updatedAt）", info.ModifiedAt)
	}
}

func TestGrokCatalogSingletonOutlivesFirstRequestContext(t *testing.T) {
	a := &Agent{
		cliBin:       os.Args[0],
		cliExtraArgs: []string{grokCatalogHelperArg},
		workDir:      t.TempDir(),
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first, err := a.FetchSessionList(firstCtx)
	if err != nil {
		t.Fatalf("first session/list: %v", err)
	}
	if len(first) != 1 || first[0].ID != "session_lifetime" {
		t.Fatalf("first session/list = %+v", first)
	}
	client := a.catalogClient
	pid := client.cmd.Process.Pid
	cancelFirst() // exact production shape: discovery/request scope ends after lazy creation
	time.Sleep(20 * time.Millisecond)

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	second, err := a.FetchSessionList(secondCtx)
	if err != nil {
		t.Fatalf("later session/list reused context-canceled zombie: %v", err)
	}
	if len(second) != 1 || second[0].ID != "session_lifetime" {
		t.Fatalf("later session/list = %+v", second)
	}
	if a.catalogClient != client || a.catalogClient.cmd.Process.Pid != pid {
		t.Fatalf("catalog transport rebuilt after creator cancellation: old_pid=%d new_pid=%d", pid, a.catalogClient.cmd.Process.Pid)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.FetchSessionList(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("per-call cancellation err=%v, want context.Canceled", err)
	}
	if err := a.catalogClient.Close(); err != nil {
		t.Fatalf("close catalog client: %v", err)
	}
}
