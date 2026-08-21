package codexweb

// codec_e2e_test.go —— p3-codec-regression：真实服务流式 turn 上，codec 输出
// 时间线与官方通知帧一一对应（无重复/无丢失）。
//
// 方法：中央泵之外用专用观察连接订阅同一线程记录官方原始帧（订阅面与发送面
// 分离），同一 turn 的官方 delta 帧序列与 session 侧 EventText 序列按
// (itemId, delta) 逐帧对齐。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestE2ECodecTimelineCorrespondence(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	providerURL := e2eMockProvider(t)
	_, home, workDir := e2eSetupBase(t, providerURL, nil)
	ctx := context.Background()

	agent := New(map[string]any{"codex_web_codex_home": home, "work_dir": workDir})
	defer func() { _ = agent.Stop() }()

	// 观察连接（记录官方原始通知）
	obsEp, err := Probe(ProbeOptions{CodexHome: home, WorkDir: workDir})
	if err != nil {
		t.Fatalf("observer probe: %v", err)
	}
	defer func() { _ = obsEp.Close() }()
	obs := obsEp.Client()

	sess, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _ = sess.Close() }()
	threadID := sess.CurrentSessionID()

	// 官方原始帧记录器（观察连接单读者）
	officialDeltas := make(chan [2]string, 256) // itemId, delta
	officialTerminal := make(chan string, 4)    // turn id
	go func() {
		for n := range obs.Notifications() {
			switch n.Method {
			case "item/agentMessage/delta":
				var p struct {
					ItemID string `json:"itemId"`
					Delta  string `json:"delta"`
				}
				if json.Unmarshal(n.Params, &p) == nil && p.ItemID != "" {
					officialDeltas <- [2]string{p.ItemID, p.Delta}
				}
			case "turn/completed":
				var p struct {
					Turn struct {
						ID string `json:"id"`
					} `json:"turn"`
				}
				if json.Unmarshal(n.Params, &p) == nil && p.Turn.ID != "" {
					officialTerminal <- p.Turn.ID
				}
			}
		}
	}()

	// SLOW turn（~16s 多 delta）——先发 turn 让 rollout 物化（§22-5：无 turn 的
	// thread 不能 resume），观察连接随后补订阅。官方不重放订阅前帧（§7.1），
	// 对齐断言因此采用后缀对齐：观察序列必须是 codec 序列的精确后缀。
	prompt := "MOCK:SLOW codec timeline correspondence"
	if err := sess.Send(prompt, nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	subscribed := false
	for i := 0; i < 100; i++ {
		if _, _, rpcErr, err := ResumeThread(ctx, obs, threadID); err == nil && rpcErr == nil {
			subscribed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !subscribed {
		t.Fatal("观察连接 10s 内未能 resume（物化异常）")
	}

	// session 侧：收集 EventText/终态
	var texts []core.Event
	var terminal *core.Event
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatal("session 事件通道提前关闭")
			}
			switch {
			case ev.Type == core.EventText:
				texts = append(texts, ev)
			case (ev.Type == core.EventResult || ev.Type == core.EventError) && ev.Done:
				e := ev
				terminal = &e
			}
			if terminal != nil {
				goto collected
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("30s 内未完成")
collected:

	// 对齐：官方 delta 帧数 == EventText 数；逐帧 (itemId, content) 一致
	var official [][2]string
	for {
		select {
		case d := <-officialDeltas:
			official = append(official, d)
		default:
			goto align
		}
	}
align:
	if len(official) == 0 {
		t.Fatal("观察连接未收到官方 delta（订阅失效）")
	}
	if len(official) > len(texts) {
		t.Fatalf("观察帧 %d 多于 codec 帧 %d（不可能）", len(official), len(texts))
	}
	// 后缀对齐：观察序列（订阅后）== codec 序列尾部（订阅前帧不重放，官方边界）
	offset := len(texts) - len(official)
	for i := range official {
		c := texts[offset+i]
		if c.ItemID != official[i][0] || c.Content != official[i][1] {
			t.Fatalf("对齐失败(官方#%d vs codec#%d)：官方(%s,%q) codec(%s,%q)",
				i, offset+i, official[i][0], official[i][1], c.ItemID, c.Content)
		}
		if c.TurnID != terminal.TurnID {
			t.Fatalf("delta turnId=%s 与终态 turn=%s 不一致", c.TurnID, terminal.TurnID)
		}
	}

	// 官方终态 turn id == codec 终态 turn id
	select {
	case id := <-officialTerminal:
		if id != terminal.TurnID {
			t.Fatalf("终态 turn id 不一致：官方 %s codec %s", id, terminal.TurnID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("观察连接未收到官方 turn/completed")
	}
}
