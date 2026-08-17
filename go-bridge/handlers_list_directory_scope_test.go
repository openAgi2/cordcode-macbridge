package gobridge

import (
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestGenericListSessionsFiltersByDirectory(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeAgent{
		name: "dsh-web",
		sessionInfos: []core.AgentSessionInfo{
			{ID: "chat-1", Summary: "红楼梦故事简述", Directory: "/Users/x/Chat", ModifiedAt: time.UnixMilli(5)},
			{ID: "ios-1", Summary: "创建TXT文件并编写西游记故事", Directory: "/Users/x/cordcode-ios", ModifiedAt: time.UnixMilli(4)},
			{ID: "chat-2", Summary: "讲封神榜故事", Directory: "/Users/x/Chat", ModifiedAt: time.UnixMilli(3)},
			{ID: "ios-2", Summary: "DeepSeek Harness接入分析", Directory: "/Users/x/cordcode-ios/", ModifiedAt: time.UnixMilli(2)},
			{ID: "stray", Summary: "太阳能发电站的故事", Directory: "未分组", ModifiedAt: time.UnixMilli(1)},
		},
	}
	h.RegisterAgent("dsh-web", agent)

	ios := listIDs(listResult(t, h, "dsh-web", "/Users/x/cordcode-ios", 50, ""))
	if len(ios) != 2 || ios[0] != "ios-1" || ios[1] != "ios-2" {
		t.Fatalf("cordcode-ios 查看更多 must be only that workspace, got %v", ios)
	}

	chat := listIDs(listResult(t, h, "dsh-web", "/Users/x/Chat/", 50, ""))
	if len(chat) != 2 || chat[0] != "chat-1" || chat[1] != "chat-2" {
		t.Fatalf("Chat 查看更多 must be only Chat, got %v", chat)
	}

	ungrouped := listIDs(listResult(t, h, "dsh-web", "未分组", 50, ""))
	if len(ungrouped) != 1 || ungrouped[0] != "stray" {
		t.Fatalf("未分组 查看更多: %v", ungrouped)
	}

	home := listIDs(listResult(t, h, "dsh-web", "", 50, ""))
	if len(home) != 5 {
		t.Fatalf("global list must keep every workspace, got %v", home)
	}
}
