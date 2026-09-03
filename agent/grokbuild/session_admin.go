package grokbuild

// session_admin.go implements core.SessionRenamer + core.SessionDeleter via the
// official Grok session-admin ext methods, sent over the process-level singleton
// catalog rail (`grok agent --no-leader stdio`):
//
//	_x.ai/session/rename  {sessionId, title}      → {"success":true}
//	_x.ai/session/delete  {sessionId}             → {"success":true}
//
// ext 分派是 agent-level（上游 acp_agent.rs:2320-2324，无 resident session 门），
// 所以列表级管理操作不需要 per-turn driver 子进程，也不触碰 leader 订阅 rail。
//
// Wire 形态实测锚定（grok 1.0.13 / 5e9a58528b76 探针，2026-09-03）：ext 方法在
// stdio JSON-RPC 上必须带 `_` 前缀（半包装形态，agent-client-protocol crate 的 wire
// 约定）；裸 `x.ai/session/rename` 返回 -32601 Method not found。错误细节（如
// "session not found: <id>"、官方 title 校验文案）在 JSON-RPC error 的 data 字段，
// message 是泛化的 "Invalid request"——透传时必须带上 data，否则 iOS 只能看到
// "Invalid request"。delete 对不存在的 sessionId 幂等成功（官方 delete_session_history
// 语义），rename 对不存在的 id 报错。
//
// 官方校验（title ≤464 bytes / ≤100 scalars / 剥控制字符）在 agent 边界完成，
// 这里不重复实现，错误原文透传。设计：docs/2026-08-28-grokbuild-leader-mode-design.md §23。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	grokExtRenameMethod = "_x.ai/session/rename"
	grokExtDeleteMethod = "_x.ai/session/delete"

	grokSessionAdminTimeout = 60 * time.Second
)

// RenameSession implements core.SessionRenamer. The official response carries
// only {"success":true}, so the returned info is constructed locally (dsh-web
// pattern): ID + requested title + now. The authoritative list refresh comes
// from signalCatalogRefresh, which re-reads the on-disk title the RPC just
// persisted.
func (a *Agent) RenameSession(ctx context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID == "" {
		return nil, fmt.Errorf("grokbuild: rename session: empty session id")
	}
	if title == "" {
		return nil, fmt.Errorf("grokbuild: rename session: empty title")
	}

	client, err := a.catalogClientInstance(ctx)
	if err != nil {
		return nil, fmt.Errorf("grokbuild: rename session %s: %w", sessionID, err)
	}
	var res struct {
		Success bool `json:"success"`
	}
	if err := client.sessionAdminCall(ctx, grokExtRenameMethod, map[string]any{
		"sessionId": sessionID,
		"title":     title,
	}, &res); err != nil {
		return nil, fmt.Errorf("grokbuild: rename session %s: %w", sessionID, err)
	}
	if !res.Success {
		return nil, fmt.Errorf("grokbuild: rename session %s: backend did not confirm success", sessionID)
	}

	a.signalCatalogRefresh()
	return &core.AgentSessionInfo{
		ID:         sessionID,
		Summary:    title,
		ModifiedAt: time.Now(),
	}, nil
}

var _ core.SessionRenamer = (*Agent)(nil)

// DeleteSession implements core.SessionDeleter. Official semantics: writeback
// users delete remote-first (a remote failure leaves local state untouched and
// the RPC errors — fail-closed, propagated verbatim); the local disk directory
// and search index are then removed. A nonexistent sessionId deletes as a no-op
// success (probe-verified), so repeated deletes on a stale list are idempotent.
func (a *Agent) DeleteSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("grokbuild: delete session: empty session id")
	}

	client, err := a.catalogClientInstance(ctx)
	if err != nil {
		return fmt.Errorf("grokbuild: delete session %s: %w", sessionID, err)
	}
	var res struct {
		Success bool `json:"success"`
	}
	if err := client.sessionAdminCall(ctx, grokExtDeleteMethod, map[string]any{
		"sessionId": sessionID,
	}, &res); err != nil {
		return fmt.Errorf("grokbuild: delete session %s: %w", sessionID, err)
	}
	if !res.Success {
		return fmt.Errorf("grokbuild: delete session %s: backend did not confirm success", sessionID)
	}

	a.signalCatalogRefresh()
	return nil
}

var _ core.SessionDeleter = (*Agent)(nil)

// sessionAdminCall sends one ext request on the catalog rail and decodes the
// result. Unlike callRPCWithCtx it formats errors with the JSON-RPC error's
// data field included, because the official session-admin handlers put the
// user-relevant detail ("session not found: <id>", "title must not be blank",
// …) in data while message stays a generic "Invalid request".
func (c *grokCatalogClient) sessionAdminCall(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := c.idCounter.next()
	ch := make(chan *jsonrpcResponse, 1)
	c.respMu.Lock()
	c.respChannels[id] = ch
	c.respMu.Unlock()
	defer func() {
		c.respMu.Lock()
		delete(c.respChannels, id)
		c.respMu.Unlock()
	}()

	if err := c.writeRequest(id, method, params); err != nil {
		return err
	}

	timer := time.NewTimer(grokSessionAdminTimeout)
	defer timer.Stop()
	var resp *jsonrpcResponse
	select {
	case resp = <-ch:
	case <-timer.C:
		return fmt.Errorf("%s timeout after %s", strings.TrimPrefix(method, "_"), grokSessionAdminTimeout)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("%s aborted (process exited)", strings.TrimPrefix(method, "_"))
	}

	if resp.Error != nil {
		return c.extRPCError(method, resp.Error)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", strings.TrimPrefix(method, "_"), err)
		}
	}
	return nil
}

// extRPCError renders a JSON-RPC error with its data payload. The official
// handlers serialize acp::Error::data(...) as a JSON string when plain text and
// pass structured objects through untouched; extract the string form so iOS
// shows the official user-facing message.
func (c *grokCatalogClient) extRPCError(method string, e *jsonrpcError) error {
	name := strings.TrimPrefix(method, "_")
	if len(e.Data) == 0 {
		return fmt.Errorf("%s error %d: %s", name, e.Code, e.Message)
	}
	var detail string
	if err := json.Unmarshal(e.Data, &detail); err == nil && detail != "" {
		return fmt.Errorf("%s error %d: %s: %s", name, e.Code, e.Message, detail)
	}
	return fmt.Errorf("%s error %d: %s: %s", name, e.Code, e.Message, string(e.Data))
}
