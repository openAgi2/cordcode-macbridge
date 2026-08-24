package codexweb

// todo_test.go —— P0-1 cache/plan 消费集：codex-web TodoProvider 的缓存写入、
// 拷贝语义、有界全清、官方删除清镜像，以及事件解码→缓存→FetchTodos 全链路。

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func plan(todos ...core.Todo) core.Event {
	return core.Event{Type: core.EventPlan, SessionID: "th-1", Plan: todos}
}

func TestAgentFetchTodosRoundTripCopy(t *testing.T) {
	a := New(nil)
	a.dispatchEvent(plan(core.Todo{Content: "step 1", Status: "completed"}))

	got, err := a.FetchTodos(context.Background(), "th-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "step 1" || got[0].Status != "completed" {
		t.Fatalf("FetchTodos 应返回缓存镜像：%+v", got)
	}
	// 调用方篡改返回切片不得污染缓存。
	got[0].Content = "poisoned"
	got2, _ := a.FetchTodos(context.Background(), "th-1")
	if got2[0].Content != "step 1" {
		t.Fatalf("篡改返回切片泄漏进缓存：%+v", got2)
	}

	// 新 plan 覆盖旧镜像（官方最新 plan 即真相）。
	a.dispatchEvent(plan(core.Todo{Content: "step A", Status: "in_progress"}, core.Todo{Content: "step B", Status: "pending"}))
	got3, _ := a.FetchTodos(context.Background(), "th-1")
	if len(got3) != 2 || got3[0].Content != "step A" {
		t.Fatalf("plan 覆盖失败：%+v", got3)
	}
}

func TestAgentFetchTodosAbsentReturnsEmpty(t *testing.T) {
	a := New(nil)
	got, err := a.FetchTodos(context.Background(), "th-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("无缓存应返回空（非 not_supported）：%+v", got)
	}
}

func TestAgentPlanCacheCapReset(t *testing.T) {
	a := New(nil)
	planCacheLen := func() int {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.planCache)
	}
	for i := 0; i < planCacheMaxEntries; i++ {
		a.rememberPlan("t-"+string(rune(i)), []core.Todo{{Content: "x"}})
	}
	// 达到上限后再记已有 thread：全清不触发，只更新该条目。
	a.rememberPlan("t-0", []core.Todo{{Content: "updated"}})
	got, _ := a.FetchTodos(context.Background(), "t-0")
	if len(got) != 1 || got[0].Content != "updated" {
		t.Fatalf("已有条目更新失败：%+v", got)
	}
	if after := planCacheLen(); after != planCacheMaxEntries {
		t.Fatalf("全清不应在已有条目时触发：len=%d", after)
	}
	// 全新条目触发全清，防止无界膨胀。
	a.rememberPlan("t-new", []core.Todo{{Content: "fresh"}})
	if after := planCacheLen(); after != 1 {
		t.Fatalf("超限应全清为新条目标：len=%d", after)
	}
	if got, _ := a.FetchTodos(context.Background(), "t-0"); got != nil {
		t.Fatalf("全清后旧条目不应保留：%+v", got)
	}
}

func TestAgentPlanEventCachedViaLiveCodec(t *testing.T) {
	a := New(nil)
	codec := NewLiveCodec()
	evs := codec.Decode(Notification{
		Method: "turn/plan/updated",
		Params: mustJSON(t, map[string]any{
			"threadId": "th-live", "turnId": "t1",
			"plan": []map[string]any{
				{"step": "first", "status": "completed"},
				{"step": "second", "status": "inProgress"},
				{"step": "third", "status": "pending"},
			},
		}),
	})
	if len(evs) != 1 || evs[0].Type != core.EventPlan {
		t.Fatalf("live 解码应产出单条 EventPlan：%+v", evs)
	}
	a.dispatchEvent(evs[0])
	got, err := a.FetchTodos(context.Background(), "th-live")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].Status != "in_progress" || got[2].Priority != "normal" {
		t.Fatalf("解码→缓存→FetchTodos 链路错误：%+v", got)
	}
}

func TestAgentDeleteSessionDropsPlanMirror(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)

	captureParams(s, "thread/delete", map[string]any{})
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep

	a.rememberPlan("thread-delete", []core.Todo{{Content: "stale", Status: "pending"}})
	if err := a.DeleteSession(context.Background(), "thread-delete"); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.FetchTodos(context.Background(), "thread-delete"); got != nil {
		t.Fatalf("删除成功后 plan 镜像应清空：%+v", got)
	}
}
