package dshweb

// inbox_test.go — S3 排队消息可见性（方案 §5.3）：codec 的 inbox 折叠与事件
// 投影。夹具形状来自 A3a/A3b 活体证据（scripts/dshweb-phase0/
// alpha1-inbox-wire.json、alpha1-updatequeue-wire.json——官方 fold 语义
// agent-loop/src/inbox.ts apply()/mutate()）。负例（非法 splice）按方案由
// 本测试按源码语义钉死：正常 Remote 写入无法提交这种 splice（官方 fold
// throw），桥的等价物是 codec reset。

import (
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func spliceEnv(seq int64, target string, start, removed int, hasRemoved bool, inserted ...string) sessionEventWire {
	ins := make([]map[string]any, 0, len(inserted))
	for _, id := range inserted {
		ins = append(ins, map[string]any{
			"id":      id,
			"content": []map[string]any{{"type": "text", "text": "queued " + id}},
		})
	}
	data := map[string]any{"target": target, "start": start, "inserted": ins}
	if hasRemoved {
		data["removedCount"] = removed
	}
	return env("agent/inbox/spliced", seq, data)
}

func wantQueued(t *testing.T, ev core.Event, id string) {
	t.Helper()
	if ev.Type != core.EventUserMessageQueued || ev.ItemID != id {
		t.Fatalf("want queued placeholder %q, got type %s item %q", id, ev.Type, ev.ItemID)
	}
	if !strings.Contains(ev.Content, id) {
		t.Fatalf("queued placeholder content = %q, want text for %s", ev.Content, id)
	}
}

func wantRemoved(t *testing.T, ev core.Event, id string) {
	t.Helper()
	if ev.Type != core.EventUserMessageRemoved || ev.ItemID != id {
		t.Fatalf("want removal %q, got type %s item %q", id, ev.Type, ev.ItemID)
	}
}

// 插入 → 占位事件（A3a seq16 形状：纯插入无 removedCount）。
func TestInboxSpliceInsertEmitsQueuedPlaceholder(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	events := collect(t, c, []sessionEventWire{spliceEnv(16, "next-turn", 0, 0, false, "m-queued")})
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	wantQueued(t, events[0], "m-queued")
}

// 消耗（claim，A3a seq22 形状：removedCount 1 无插入）→ 撤行事件。
func TestInboxSpliceRemovalEmitsRetract(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	events := collect(t, c, []sessionEventWire{
		spliceEnv(16, "next-turn", 0, 0, false, "m-1"),
		spliceEnv(17, "next-turn", 0, 1, true),
	})
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	wantQueued(t, events[0], "m-1")
	wantRemoved(t, events[1], "m-1")
}

// updateQueue edit（A3b seq21 形状：removedCount 1 + 同 id 新文本插入）→
// 撤行 + 新占位（reducer 侧原位替换）。
func TestInboxSpliceEditReplacesInPlace(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	events := collect(t, c, []sessionEventWire{
		spliceEnv(16, "next-turn", 0, 0, false, "m-edit"),
		spliceEnv(17, "next-turn", 0, 1, true, "m-edit"),
	})
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3 (queued + removed + re-queued)", len(events))
	}
	wantQueued(t, events[0], "m-edit")
	wantRemoved(t, events[1], "m-edit")
	wantQueued(t, events[2], "m-edit")
}

// updateQueue steer（A3b seq17/18 形状：next-turn 移除 + next-step 同 id 插入）
// → 撤行 + 新占位（列表间移动）。
func TestInboxSpliceSteerMovesBetweenLists(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	events := collect(t, c, []sessionEventWire{
		spliceEnv(16, "next-turn", 0, 0, false, "m-steer"),
		spliceEnv(17, "next-turn", 0, 1, true),
		spliceEnv(18, "next-step", 0, 0, false, "m-steer"),
	})
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	wantQueued(t, events[0], "m-steer")
	wantRemoved(t, events[1], "m-steer")
	wantQueued(t, events[2], "m-steer")
}

// 官方 fold 不变量（inbox.ts apply()）：越界 splice 是硬错误——桥的等价物
// 是 codec reset（*codecReset），绝不静默吞。
func TestInboxSpliceInvalidBoundsResets(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	bad := spliceEnv(16, "next-turn", 5, 0, false, "m-x")
	if _, err := c.apply(&bad); err == nil {
		t.Fatal("out-of-bounds start must reset the codec")
	} else if _, ok := err.(*codecReset); !ok {
		t.Fatalf("want codecReset, got %T: %v", err, err)
	}
	c2 := newSessionCodec("sess-inbox")
	bad2 := spliceEnv(16, "next-turn", 0, 3, true)
	if _, err := c2.apply(&bad2); err == nil {
		t.Fatal("removedCount beyond list length must reset the codec")
	}
}

// 官方跨表 pending id 唯一性：重复 id 是硬错误（reset）。
func TestInboxSpliceDuplicatePendingIdResets(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	first := spliceEnv(16, "next-turn", 0, 0, false, "m-dup")
	if _, err := c.apply(&first); err != nil {
		t.Fatal(err)
	}
	second := spliceEnv(17, "next-step", 0, 0, false, "m-dup")
	if _, err := c.apply(&second); err == nil {
		t.Fatal("duplicate pending id across lists must reset the codec")
	}
}

// 落定链（A3a id 连续性）：splice 插入 → claim 移除 → user/message 同 id
// 落定（turn 上下文中）——codec 侧 user/message 事件携带同一 ItemID，reducer
// 侧按 id 原位替换（go-bridge reducer 测试钉该行为）。
func TestInboxSettleCarriesSameId(t *testing.T) {
	c := newSessionCodec("sess-inbox")
	events := collect(t, c, []sessionEventWire{
		spliceEnv(16, "next-turn", 0, 0, false, "m-settle"),
		spliceEnv(17, "next-turn", 0, 1, true),
		env("turn/start", 18, map[string]any{"turn": 2}),
		env("user/message", 19, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "queued m-settle"}},
			"source":  map[string]any{"kind": "user"}, "id": "m-settle",
		}),
	})
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (queued + removed + turn_started + settled user_message)", len(events))
	}
	wantQueued(t, events[0], "m-settle")
	wantRemoved(t, events[1], "m-settle")
	if events[3].Type != core.EventUserMessage || events[3].ItemID != "m-settle" {
		t.Fatalf("settle must be a user_message with the same id, got type %s item %q", events[3].Type, events[3].ItemID)
	}
}
