package codexweb

// history_e2e_test.go —— p2-history-regression：真实官方 app-server 上的
// includeTurns 读取与冷 hydrate——冷基线官方 identity 与 live turn/start 返回一致。
//
// 上游模型行为由 Phase 0 的本地 mock Responses provider 控制（仅上游；app-server
// 全部行为为官方二进制）。turn 真实完成（MOCK:STREAM 多 delta）后断言冷基线：
// TurnID/用户输入/agentMessage/官方 completed 状态。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestE2EHistoryColdBaselineIdentity(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	providerURL := e2eMockProvider(t)
	_, home, workDir := e2eSetupBase(t, providerURL, nil)

	ep, err := Probe(ProbeOptions{CodexHome: home, WorkDir: workDir})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer func() { _ = ep.Close() }()
	cl := ep.Client()
	ctx := context.Background()

	threadID := e2eStartThread(t, cl, workDir)
	prompt := "MOCK:STREAM e2e history cold baseline"
	raw, rpcErr, err := cl.RequestContext(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("turn/start: %v / %v", err, rpcErr)
	}
	var resp struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Turn.ID == "" {
		t.Fatalf("turn/start decode: %v (%s)", err, raw)
	}
	liveTurnID := resp.Turn.ID

	// 等 turn/completed（官方唯一完成真相；不按时间猜测）
	completed := waitNotification(t, cl, 30*time.Second, "turn/completed", func(params json.RawMessage) bool {
		// 官方 params 形状（dumps/catalog + 实测）：{threadId, turn:{id,...}}
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		return p.ThreadID == threadID && p.Turn.ID == liveTurnID
	})
	if !completed {
		t.Fatal("30s 内未收到官方 turn/completed")
	}

	// 冷基线：thread/read(includeTurns) → 官方 identity 的 turn
	turns, rpcErr, err := ReadThreadRich(ctx, cl, threadID, 0)
	if err != nil {
		t.Fatalf("ReadThreadRich: %v", err)
	}
	if rpcErr != nil {
		t.Fatalf("ReadThreadRich 官方错误: %v", rpcErr)
	}
	if len(turns) != 1 {
		t.Fatalf("应 1 个 turn，得 %d", len(turns))
	}
	turn := turns[0]
	if turn.TurnID != liveTurnID {
		t.Fatalf("冷基线 TurnID=%s ≠ live turn/start 返回 %s（官方 identity 必须一致）", turn.TurnID, liveTurnID)
	}
	if turn.UserItemID == "" || turn.UserText != prompt {
		t.Fatalf("user 输入缺失：%q / %q", turn.UserItemID, turn.UserText)
	}
	if turn.Status != TurnStatusCompleted {
		t.Fatalf("官方 completed turn 冷基线 status=%s", turn.Status)
	}
	var agentText string
	for _, p := range turn.Parts {
		if p["type"] == "text" {
			if s, ok := p["content"].(string); ok {
				agentText += s
			}
		}
	}
	if !strings.Contains(agentText, "e2e history cold baseline") {
		t.Fatalf("agentMessage 冷基线正文缺失：%q", agentText)
	}

	// 有界加载：limit=1 仍返回该最新 turn
	limited, _, err := ReadThreadRich(ctx, cl, threadID, 1)
	if err != nil || len(limited) != 1 || limited[0].TurnID != liveTurnID {
		t.Fatalf("limit=1 有界加载错误：%v", limited)
	}
}

// waitNotification 等待一个匹配的通知（单读者通道；测试专用泵）。
func waitNotification(t *testing.T, cl *Client, timeout time.Duration, method string, match func(json.RawMessage) bool) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case n := <-cl.Notifications():
			if n.Method == method && (match == nil || match(n.Params)) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
