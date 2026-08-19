package gobridge

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 2026-08-19 owner 真机：opencode-web 1.18 的 GET /session 按 x-opencode-directory
// 头按目录返回；directory 请求必须走 DirectorySessionLister 限定拉取（活体：桌面端
// 新建会话在无头全局列表里完全缺席，旧 post-filter 路径永远看不到）。幽灵目录
// （磁盘已不存在）同时被共享可见性规则滤除。

type scopedListerStubAgent struct {
	core.Agent
	name      string
	gotDir    string
	listCalls int
}

func (s *scopedListerStubAgent) Name() string { return s.name }

func (s *scopedListerStubAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	s.listCalls++
	return []core.AgentSessionInfo{
		{ID: "ses_union_live", Directory: s.gotDir, ModifiedAt: time.Now()},
		{ID: "ses_union_ghost", Directory: "/definitely/not/on/disk", ModifiedAt: time.Now()},
	}, nil
}

func (s *scopedListerStubAgent) ListSessionsInDirectory(_ context.Context, directory string) ([]core.AgentSessionInfo, error) {
	s.gotDir = directory
	return []core.AgentSessionInfo{
		{ID: "ses_scoped_live", Directory: directory, ModifiedAt: time.Now()},
		{ID: "ses_scoped_leak", Directory: directory + "-other", ModifiedAt: time.Now()},
	}, nil
}

func TestHandleListSessionsDirectoryRequestUsesScopedLister(t *testing.T) {
	dir := t.TempDir()
	agent := &scopedListerStubAgent{name: "opencode-web", gotDir: dir}
	h := NewHandlers()
	h.RegisterAgent("opencode-web", agent)

	data := listResult(t, h, "opencode-web", dir, 10, "")
	ids := listIDs(data)
	if len(ids) != 1 || ids[0] != "ses_scoped_live" {
		t.Fatalf("directory request must serve the scoped fetch, dropping leaked rows, got %v", ids)
	}
	if agent.gotDir != dir {
		t.Fatalf("scoped lister must receive the requested directory, got %q", agent.gotDir)
	}
	if agent.listCalls != 0 {
		t.Fatalf("global ListSessions must not run for a directory request, got %d calls", agent.listCalls)
	}
}

func TestHandleListSessionsGlobalRequestFiltersGhostDirectories(t *testing.T) {
	dir := t.TempDir()
	agent := &scopedListerStubAgent{name: "opencode-web", gotDir: dir}
	h := NewHandlers()
	h.RegisterAgent("opencode-web", agent)

	data := listResult(t, h, "opencode-web", "", 10, "")
	ids := listIDs(data)
	if len(ids) != 1 || ids[0] != "ses_union_live" {
		t.Fatalf("global request must run the merged list with ghost directories dropped, got %v", ids)
	}
}

// 非 DirectorySessionLister 的 generic backend 保持既有行为：全局列表 +
// bridge 侧 directory post-filter（dsh-web/deepseek 依赖该契约）。
type plainStubAgent struct {
	core.Agent
}

func (s *plainStubAgent) Name() string { return "generic-test" }

func (s *plainStubAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return []core.AgentSessionInfo{
		{ID: "ses_in_dir", Directory: "/tmp/plain-dir", ModifiedAt: time.Now()},
		{ID: "ses_other", Directory: "/tmp/plain-other", ModifiedAt: time.Now()},
	}, nil
}

func TestHandleListSessionsPlainBackendKeepsPostFilter(t *testing.T) {
	agent := &plainStubAgent{}
	h := NewHandlers()
	h.RegisterAgent("generic-test", agent)

	data := listResult(t, h, "generic-test", "/tmp/plain-dir", 10, "")
	ids := listIDs(data)
	if len(ids) != 1 || ids[0] != "ses_in_dir" {
		t.Fatalf("plain backend must keep the global fetch + post-filter contract, got %v", ids)
	}
}
