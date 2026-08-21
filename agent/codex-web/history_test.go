package codexweb

// history_test.go —— p2-history 测试（§13.1 catalog/history：pathless hydrate、
// 长 history 有界加载、item variant 映射）。
//
// 证据分级：catalog/interaction 真实帧驱动已取样 variant；schema-only variant 的
// 输入按 stable bundle 字段构造（标注为 schema 派生，capability 广告另行门控）。

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func jraw(s string) json.RawMessage { return json.RawMessage(s) }

func fixtureBytes(t *testing.T, group string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/official-0.149.0-alpha.4/dumps/" + group + "/raw.jsonl")
	if err != nil {
		t.Fatalf("read fixture %s: %v", group, err)
	}
	return b
}

// historyFixtureRead 组装 dumps/catalog 的 thread/read id=19 响应（rename 后两 turn）。
func historyFixtureRead(t *testing.T) *ThreadInfo {
	t.Helper()
	resp, _, _ := fixtureDump(t, "catalog")
	th := mustDecode[struct {
		Thread ThreadInfo `json:"thread"`
	}](t, resp["19"]).Thread
	return &th
}

// TestHistoryFixtureTurnMapping 真实帧：官方 identity、parts 顺序、reasoning 不重复。
func TestHistoryFixtureTurnMapping(t *testing.T) {
	th := historyFixtureRead(t)
	ht := turnsFromThread(t, th, 0)

	if len(ht) != 2 {
		t.Fatalf("应 2 个 turn，得 %d", len(ht))
	}
	// 官方 identity：TurnID=官方 turn.id（≠ user item id）
	t1 := ht[0]
	if t1.TurnID != "01a02532-b0ac-7b92-b342-f9902f46f442" {
		t.Fatalf("TurnID=%s", t1.TurnID)
	}
	if t1.UserItemID != "item-1" {
		t.Fatalf("UserItemID=%s", t1.UserItemID)
	}
	if t1.UserText != "MOCK:STREAM catalog first turn" {
		t.Fatalf("UserText=%q", t1.UserText)
	}
	if t1.Status != TurnStatusCompleted || !t1.HasTime {
		t.Fatalf("status/time 解码错误：%s/%v", t1.Status, t1.HasTime)
	}
	if len(t1.Parts) != 1 {
		t.Fatalf("turn1 parts 应 1（agentMessage），得 %d", len(t1.Parts))
	}
	if t1.Parts[0]["type"] != "text" || t1.Parts[0]["itemId"] != "item-2" {
		t.Fatalf("turn1 part0=%v", t1.Parts[0])
	}
	if !strings.Contains(t1.Parts[0]["content"].(string), "catalog first turn<0>") {
		t.Fatalf("agentMessage 文本缺失")
	}

	// turn2：reasoning（summary 优先，不与 content 重复）+ agentMessage，服务端顺序
	t2 := ht[1]
	if t2.TurnID != "01a02532-b1f9-7b60-979b-f4e87a543da9" {
		t.Fatalf("turn2 TurnID=%s", t2.TurnID)
	}
	if len(t2.Parts) != 2 {
		t.Fatalf("turn2 parts 应 2，得 %d", len(t2.Parts))
	}
	if t2.Parts[0]["type"] != "reasoning" || t2.Parts[0]["content"].(string) != "mock summary one\nmock summary two" {
		t.Fatalf("reasoning part=%v", t2.Parts[0])
	}
	if t2.Parts[1]["type"] != "text" || t2.Parts[1]["itemId"] != "item-5" {
		t.Fatalf("turn2 part1=%v", t2.Parts[1])
	}
}

// turnsFromThread 用包内映射处理一个已解码 thread（测试脚手架）。
func turnsFromThread(t *testing.T, th *ThreadInfo, limit int) []HistoryTurn {
	t.Helper()
	client, _ := historyClientWithThread(t, th)
	hts, rpcErr, err := ReadThreadRich(context.Background(), client, th.ID, limit)
	if err != nil || rpcErr != nil {
		t.Fatalf("ReadThreadRich: %v/%v", rpcErr, err)
	}
	return hts
}

// historyClientWithThread 构造回放固定 thread/read 响应的客户端。
func historyClientWithThread(t *testing.T, th *ThreadInfo) (*Client, *scriptedTransport) {
	t.Helper()
	s := newScripted()
	c := NewClient(s, 1)
	t.Cleanup(func() { _ = c.Close() })
	go drainNotifications(c)
	payload, _ := json.Marshal(map[string]any{"thread": th})
	captureParams(s, "thread/read", json.RawMessage(payload))
	return c, s
}

// interactionFixtureItems 提取 dumps/interaction 中 commandExecution/fileChange 的
// item/completed 真实帧。
func interactionFixtureItems(t *testing.T, typ string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for _, line := range strings.Split(string(fixtureBytes(t, "interaction")), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Dir string `json:"dir"`
			Msg struct {
				Method string `json:"method"`
				Params struct {
					Item json.RawMessage `json:"item"`
				} `json:"params"`
			} `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Dir == "server" && e.Msg.Method == "item/completed" {
			var probe struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(e.Msg.Params.Item, &probe)
			if probe.Type == typ {
				out = append(out, e.Msg.Params.Item)
			}
		}
	}
	return out
}

// TestHistoryCommandFileVariants 真实帧：commandExecution（completed+declined）与 fileChange。
func TestHistoryCommandFileVariants(t *testing.T) {
	cmds := interactionFixtureItems(t, "commandExecution")
	if len(cmds) < 2 {
		t.Fatalf("interaction 应含 2 个 commandExecution 帧，得 %d", len(cmds))
	}
	th := &ThreadInfo{ID: "th-c", Turns: []TurnInfo{{
		ID: "turn-c", Status: TurnStatusCompleted,
		Items: append(cmds, interactionFixtureItems(t, "fileChange")...),
	}}}
	ht := turnsFromThread(t, th, 0)
	if len(ht) != 1 {
		t.Fatalf("应 1 turn，得 %d", len(ht))
	}
	parts := ht[0].Parts
	if len(parts) != 3 {
		t.Fatalf("parts 应 3（Bash/Bash/Patch），得 %d", len(parts))
	}
	// completed 命令：官方 aggregatedOutput/exitCode/title 保留
	s0 := parts[0]["step"].(map[string]any)
	if s0["toolName"] != "Bash" || s0["status"] != "completed" {
		t.Fatalf("cmd step=%v", s0)
	}
	if s0["output"] != "phase0-approval\n" || s0["exitCode"] != int32(0) {
		t.Fatalf("cmd 输出/退出码=%v/%v", s0["output"], s0["exitCode"])
	}
	if s0["title"] != "/bin/zsh -lc 'echo phase0-approval'" {
		t.Fatalf("cmd title=%v", s0["title"])
	}
	// declined 命令（审批拒绝官方终态）：status 原样、无输出不伪造
	s1 := parts[1]["step"].(map[string]any)
	if s1["status"] != "declined" {
		t.Fatalf("declined status=%v", s1["status"])
	}
	if _, has := s1["output"]; has {
		t.Fatal("declined 命令不应伪造 output")
	}
	// fileChange：官方 changes→fileChanges（path/kind/diff）
	s2 := parts[2]["step"].(map[string]any)
	if s2["toolName"] != "Patch" {
		t.Fatalf("patch toolName=%v", s2["toolName"])
	}
	changes := s2["fileChanges"].([]map[string]any)
	if len(changes) != 1 || changes[0]["path"] != "$WORKSPACE/newfile.txt" ||
		changes[0]["kind"] != "add" || changes[0]["diff"] != "phase0 content\n" {
		t.Fatalf("fileChanges=%v", changes)
	}
}

// TestHistorySchemaOnlyVariants schema 派生（未取样；capability 广告保持关闭）：
// mcpToolCall/dynamicToolCall/plan/webSearch/contextCompaction 结构化映射。
func TestHistorySchemaOnlyVariants(t *testing.T) {
	items := []json.RawMessage{
		jraw(`{"type":"mcpToolCall","id":"m1","server":"srv","tool":"lookup","arguments":{"q":"x"},"status":"completed","result":{"content":[{"type":"text","text":"hit"}]},"durationMs":5,"error":null}`),
		jraw(`{"type":"dynamicToolCall","id":"d1","tool":"customTool","arguments":{"a":1},"status":"failed","success":false}`),
		jraw(`{"type":"plan","id":"p1","text":"1. step"}`),
		jraw(`{"type":"webSearch","id":"w1","query":"codex app-server","results":null}`),
		jraw(`{"type":"contextCompaction","id":"c1"}`),
		jraw(`{"type":"hookPrompt","id":"h1","fragments":[]}`),
	}
	th := &ThreadInfo{ID: "th-s", Turns: []TurnInfo{{ID: "turn-s", Status: TurnStatusCompleted, Items: items}}}
	ht := turnsFromThread(t, th, 0)[0]

	if len(ht.Parts) != 4 {
		t.Fatalf("parts 应 4（mcp/dynamic/plan/webSearch），得 %d", len(ht.Parts))
	}
	m := ht.Parts[0]["step"].(map[string]any)
	if m["toolName"] != "MCP" || m["title"] != "srv lookup" || m["status"] != "completed" {
		t.Fatalf("mcp step=%v", m)
	}
	if string(m["output"].(json.RawMessage)) != `{"content":[{"type":"text","text":"hit"}]}` {
		t.Fatalf("mcp output=%v", m["output"])
	}
	d := ht.Parts[1]["step"].(map[string]any)
	if d["toolName"] != "customTool" || d["status"] != "failed" {
		t.Fatalf("dynamic step=%v", d)
	}
	p := ht.Parts[2]["step"].(map[string]any)
	if p["toolName"] != "Plan" || p["output"] != "1. step" {
		t.Fatalf("plan step=%v", p)
	}
	w := ht.Parts[3]["step"].(map[string]any)
	if w["toolName"] != "WebSearch" || w["title"] != "codex app-server" {
		t.Fatalf("webSearch step=%v", w)
	}
	if len(ht.SystemNotes) != 1 || ht.SystemNotes[0] != "contextCompaction" {
		t.Fatalf("SystemNotes=%v", ht.SystemNotes)
	}
	if len(ht.SkippedTypes) != 1 || ht.SkippedTypes[0] != "hookPrompt" {
		t.Fatalf("未取样 variant 应跳过并记录：%v", ht.SkippedTypes)
	}
}

// TestHistoryFailedInterruptedInProgress 官方 turn 终态语义透传（§9.2 不本地猜）。
func TestHistoryFailedInterruptedInProgress(t *testing.T) {
	th := &ThreadInfo{ID: "th-f", Turns: []TurnInfo{
		{ID: "tf", Status: TurnStatusFailed, Error: &TurnErrorInfo{Message: "provider unreachable"},
			Items: []json.RawMessage{jraw(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"hi"}]}`)}},
		{ID: "ti", Status: TurnStatusInterrupted,
			Items: []json.RawMessage{jraw(`{"type":"userMessage","id":"u2","content":[{"type":"text","text":"stop"}]}`)}},
		{ID: "tp", Status: TurnStatusInProgress,
			Items: []json.RawMessage{jraw(`{"type":"userMessage","id":"u3","content":[{"type":"text","text":"running"}]}`)}},
	}}
	ht := turnsFromThread(t, th, 0)
	if ht[0].Status != TurnStatusFailed || ht[0].ErrorMessage != "provider unreachable" {
		t.Fatalf("failed turn=%+v", ht[0])
	}
	if ht[1].Status != TurnStatusInterrupted {
		t.Fatalf("interrupted status=%s", ht[1].Status)
	}
	if ht[2].Status != TurnStatusInProgress {
		t.Fatalf("inProgress 不得本地改写：%s", ht[2].Status)
	}
}

// TestHistoryNotLoadedAndUnknown itemsView=notLoaded 不伪造；未知 item 不崩不猜。
func TestHistoryNotLoadedAndUnknown(t *testing.T) {
	th := &ThreadInfo{ID: "th-n", Turns: []TurnInfo{
		{ID: "tn", Status: TurnStatusCompleted, ItemsView: TurnItemsViewNotLoaded},
		{ID: "tu", Status: TurnStatusCompleted, Items: []json.RawMessage{
			jraw(`{"type":"brandNewItem","id":"x1","payload":{"a":1}}`),
			jraw(`{"type":"userMessage","id":"u9","content":[{"type":"text","text":"ok"}]}`),
		}},
	}}
	ht := turnsFromThread(t, th, 0)
	if ht[0].UserItemID != "" || len(ht[0].Parts) != 0 {
		t.Fatalf("notLoaded turn 不应有任何编造内容：%+v", ht[0])
	}
	if len(ht[0].SkippedTypes) != 1 || ht[0].SkippedTypes[0] != "itemsView:notLoaded" {
		t.Fatalf("notLoaded 应记录边界：%v", ht[0].SkippedTypes)
	}
	if ht[1].SkippedTypes[0] != "brandNewItem" || ht[1].UserItemID != "u9" {
		t.Fatalf("未知 item 应跳过记录：%+v", ht[1])
	}
}

// TestHistoryBoundedLimit 有界加载：官方升序取尾部（最新 N turn）。
func TestHistoryBoundedLimit(t *testing.T) {
	var turns []TurnInfo
	for i := 0; i < 5; i++ {
		turns = append(turns, TurnInfo{ID: string(rune('a'+i)), Status: TurnStatusCompleted})
	}
	th := &ThreadInfo{ID: "th-l", Turns: turns}
	ht := turnsFromThread(t, th, 2)
	if len(ht) != 2 || ht[0].TurnID != "d" || ht[1].TurnID != "e" {
		t.Fatalf("limit=2 应保留最新 2 turn（d,e），得 %v", ht)
	}
}

// TestHistorySteerSecondUserMessage turn 内第二个 userMessage（steer 注入）不丢。
func TestHistorySteerSecondUserMessage(t *testing.T) {
	th := &ThreadInfo{ID: "th-st", Turns: []TurnInfo{{
		ID: "t-st", Status: TurnStatusCompleted,
		Items: []json.RawMessage{
			jraw(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"first"}]}`),
			jraw(`{"type":"agentMessage","id":"a1","text":"partial"}`),
			jraw(`{"type":"userMessage","id":"u2","content":[{"type":"text","text":"steered"}]}`),
			jraw(`{"type":"agentMessage","id":"a2","text":"final"}`),
		},
	}}}
	ht := turnsFromThread(t, th, 0)[0]
	if ht.UserItemID != "u1" || ht.UserText != "first" {
		t.Fatalf("首个 userMessage 应作为 turn 输入：%+v", ht)
	}
	var texts []string
	for _, p := range ht.Parts {
		if p["type"] == "text" {
			texts = append(texts, p["content"].(string))
		}
	}
	if len(texts) != 3 || texts[0] != "partial" || texts[1] != "steered" || texts[2] != "final" {
		t.Fatalf("steer 注入正文顺序错误：%v", texts)
	}
}

// TestHistoryReadRequestShape thread/read 请求形状冻结。
func TestHistoryReadRequestShape(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)
	th := ThreadInfo{ID: "th-x"}
	payload, _ := json.Marshal(map[string]any{"thread": th})
	capRead := captureParams(s, "thread/read", json.RawMessage(payload))

	if _, _, err := ReadThread(context.Background(), c, "th-x", false); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capRead)[0], map[string]any{"threadId": "th-x"})

	if _, _, err := ReadThread(context.Background(), c, "th-x", true); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*capRead)[1], map[string]any{"threadId": "th-x", "includeTurns": true})
}
