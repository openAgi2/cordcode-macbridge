package codexweb

// interactions.go —— 审批/提问/elicitation registry（§5.2/§7.2/§7.3）。
//
// Phase 0 实测语义：command approval 的 availableDecisions 未声明 experimentalApi 也物理到达
// （additionalPermissions 被剥除）；decision 枚举 accept/cancel/acceptWithExecpolicyAmendment，
// cancel → turn 终态 interrupted；requestUserInput 批结构按题 id 应答
// {qid:{answers:[..]}}；permission approval 为 RequestPermissionProfile →
// GrantedPermissionProfile+scope（无 availableDecisions）。
//
// registry 纪律（§7.2）：
//   - key = 官方身份 (threadId, turnId, itemId) + connection epoch + 官方 request id；
//     对 iOS 暴露稳定 interactionID = threadId ":" itemId（官方身份派生，不含连接细节）；
//   - response 回原 JSON-RPC request id；
//   - 收口 = serverRequest/resolved 或相关 item/completed；
//   - 断线清理旧 epoch pending；重连后只认官方重新发送的 server request（不本地重放）；
//   - Mac/iOS 同时可答：官方先到者得（second response 由官方拒绝/忽略，我们幂等）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// InteractionKind 是官方 server request 的分类。
type InteractionKind string

const (
	InteractionCommandApproval InteractionKind = "command_approval"
	InteractionFileApproval    InteractionKind = "file_approval"
	InteractionPermission      InteractionKind = "permission"
	InteractionUserInput       InteractionKind = "user_input"
	InteractionElicitation     InteractionKind = "elicitation"
)

// Interaction 是一个 pending 官方 server request 的登记项。
type Interaction struct {
	InteractionID string // threadId ":" itemId（官方身份派生）
	Kind          InteractionKind
	Epoch         ConnectionEpoch
	RequestID     json.Number // 官方 JSON-RPC request id（response 回原 id）
	ThreadID      string
	TurnID        string
	ItemID        string
	Method        string
	Params        json.RawMessage
	Responding    bool // 已 claim 正在/已经写 response，等待官方收口
	// UI 仅 kind=user_input：应答映射快照（userinput.go），登记成功时填充。
	UI *userInputSnapshot
}

// InteractionRegistry 是 server request 生命周期账本。
type InteractionRegistry struct {
	mu      sync.Mutex
	pending map[string]*Interaction // key: InteractionID
	history map[string]bool         // 已收口（幂等去重）
}

func NewInteractionRegistry() *InteractionRegistry {
	return &InteractionRegistry{pending: map[string]*Interaction{}, history: map[string]bool{}}
}

// Register 登记一个 server request；同 interactionID 重复到达（官方重发）刷新
// epoch/requestID（§8.3-5：重连后官方重发才重新 surface）。
func (r *InteractionRegistry) Register(it *Interaction) {
	if it == nil || it.InteractionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[it.InteractionID] = it
	slog.Debug("codexweb interaction: registered", "id", it.InteractionID, "kind", it.Kind, "epoch", it.Epoch)
}

// Lookup 返回 pending 项；已收口或不存在返回 nil。
func (r *InteractionRegistry) Lookup(interactionID string) *Interaction {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[interactionID]
}

// Claim 原子取得一次应答权。pending 的同一交互只允许一个本地 writer；写失败时
// ReleaseClaim 还原为可重试，写成功后保持 claim 直到官方收口。
func (r *InteractionRegistry) Claim(interactionID string) (*Interaction, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it := r.pending[interactionID]
	if it == nil || it.Responding {
		return it, false
	}
	it.Responding = true
	return it, true
}

func (r *InteractionRegistry) ReleaseClaim(interactionID string, claimed *Interaction) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.pending[interactionID]; current == claimed {
		current.Responding = false
	}
}

// MarkResolved 收口（serverRequest/resolved 或 item/completed）；幂等。
// 返回是否发生了实际状态变化（供 resolved 事件只发一次）。
func (r *InteractionRegistry) MarkResolved(interactionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, done := r.history[interactionID]; done {
		return false
	}
	r.history[interactionID] = true
	_, had := r.pending[interactionID]
	delete(r.pending, interactionID)
	return had
}

// ResolvedKnown 报告交互是否已收口过（幂等 already_resolved 判定用；
// 不含仍 pending 的项）。
func (r *InteractionRegistry) ResolvedKnown(interactionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.history[interactionID]
}

// DropEpoch 断线清理：移除指定 epoch 的全部 pending（旧连接的官方 request id 已失效）。
func (r *InteractionRegistry) DropEpoch(epoch ConnectionEpoch) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for id, it := range r.pending {
		if it.Epoch == epoch {
			delete(r.pending, id)
			dropped++
		}
	}
	return dropped
}

func interactionID(threadID, itemID string) string {
	return threadID + ":" + itemID
}

// ---- Agent 侧：server request 分发与应答（§7.2） ----

// handleServerRequest 解码官方 server request → 登记 + 事件分发。
// 未取样 variant（elicitation）fail closed：登记但不生成产品事件（不代答）。
func (a *Agent) handleServerRequest(sr ServerRequest) []core.Event {
	var probe struct {
		ItemID string `json:"itemId"`
	}
	_ = json.Unmarshal(sr.Params, &probe)
	if sr.ThreadID == "" || probe.ItemID == "" {
		// 身份缺失（协议层不可归因）：回 -32602 不让官方挂起；不登记、不发产品事件。
		slog.Warn("codexweb interaction: server request missing identity", "method", sr.Method)
		if cl := a.currentClient(); cl != nil && cl.Epoch() == sr.Epoch {
			if err := cl.RespondServerRequestError(sr.RequestID, -32602, "invalid server request identity"); err != nil {
				slog.Debug("codexweb interaction: error response failed", "error", err)
			}
		}
		return nil
	}
	kind := classifyServerRequest(sr.Method)
	slog.Info("codexweb interaction: server request arrived",
		"method", sr.Method, "thread", sr.ThreadID, "turn", sr.TurnID,
		"item", probe.ItemID, "rpcId", sr.RequestID, "kind", kind)
	it := &Interaction{
		InteractionID: interactionID(sr.ThreadID, probe.ItemID),
		Kind:          kind,
		Epoch:         sr.Epoch,
		RequestID:     sr.RequestID,
		ThreadID:      sr.ThreadID,
		TurnID:        sr.TurnID,
		ItemID:        probe.ItemID,
		Method:        sr.Method,
		Params:        sr.Params,
	}
	if kind == InteractionUserInput {
		events := a.userInputEvents(it)
		// 规范化失败的请求没有可安全回答的 wire shape：只 surface failed，
		// 不登记、不回写，避免把不可回答项伪装成 pending。
		if it.UI != nil {
			a.registry.Register(it)
		}
		return events
	}
	a.registry.Register(it)

	switch kind {
	case InteractionCommandApproval:
		return []core.Event{a.approvalEvent(it, "Bash")}
	case InteractionFileApproval:
		return []core.Event{a.approvalEvent(it, "Patch")}
	case InteractionPermission:
		return []core.Event{a.approvalEvent(it, "Permissions")}
	default:
		// elicitation 等未取样 variant：capability fail closed；不代答、不合成事件。
		slog.Warn("codexweb interaction: unsampled server request kind (fail closed)", "method", sr.Method)
		return nil
	}
}

func classifyServerRequest(method string) InteractionKind {
	switch method {
	case "item/commandExecution/requestApproval":
		return InteractionCommandApproval
	case "item/fileChange/requestApproval":
		return InteractionFileApproval
	case "item/permissions/requestApproval":
		return InteractionPermission
	case "item/tool/requestUserInput":
		return InteractionUserInput
	default:
		if strings.Contains(method, "elicitation") {
			return InteractionElicitation
		}
		return InteractionKind("unknown:" + method)
	}
}

// approvalEvent 生成 core.EventPermissionRequest（保留官方原始载荷于 ToolInputRaw）。
func (a *Agent) approvalEvent(it *Interaction, toolName string) core.Event {
	ev := core.Event{
		Type:      core.EventPermissionRequest,
		SessionID: it.ThreadID,
		TurnID:    it.TurnID,
		ItemID:    it.ItemID,
		ThreadID:  it.ThreadID,
		ToolName:  toolName,
		RequestID: it.InteractionID,
		// Official Codex approvals are one-shot accept/cancel (or a granted
		// permission profile scoped to this session). There is no distinct
		// persistent "always" decision, so do not advertise a fake one.
		PermissionActions: []string{"approve", "reject"},
	}
	var raw map[string]any
	if json.Unmarshal(it.Params, &raw) == nil {
		ev.ToolInputRaw = raw
		if cmd, ok := raw["command"].(string); ok && cmd != "" {
			ev.ToolInput = cmd
		}
		// 官方 reason（如「需要在 <path>（工作区外路径）…是否允许修改该文件？」）
		// 单独走 Content → wire permission_request.reason → 投影 part.Title，
		// iOS 权限卡与 TaskDock 以此显示审批文案；拼进 ToolInput 会让 iOS 把
		// 命令+文案混成标题（2026-08-25 真机：只显示命令前两行，reason 消失）。
		if reason, ok := raw["reason"].(string); ok && reason != "" {
			ev.Content = reason
		}
	}
	return ev
}

// respondDecision 以官方 decision 词汇应答 command/file approval。
func (a *Agent) respondDecision(ctx context.Context, interactionID string, decision string) error {
	if decision != "accept" && decision != "cancel" {
		return fmt.Errorf("codex-web: unsupported approval decision %q", decision)
	}
	it, claimed := a.registry.Claim(interactionID)
	if it == nil {
		return fmt.Errorf("codex-web: interaction %s 不在 pending（已收口/已断线清理/官方重发前）", interactionID)
	}
	if !claimed {
		return fmt.Errorf("codex-web: interaction %s response already in progress", interactionID)
	}
	sent := false
	defer func() {
		if !sent {
			a.registry.ReleaseClaim(interactionID, it)
		}
	}()
	cl, err := a.clientForEpoch(ctx, it.Epoch)
	if err != nil {
		return err
	}
	if err := cl.RespondServerRequest(it.RequestID, map[string]any{"decision": decision}); err != nil {
		return err
	}
	sent = true
	return nil
}

// currentClient 返回 Agent 当前连接（nil 若未就绪）；仅用于同 epoch 校验下的
// 协议层错误应答。
func (a *Agent) currentClient() *Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.endpoint == nil {
		return nil
	}
	return a.endpoint.Client()
}

// clientForEpoch 校验交互登记的连接仍是当前连接（旧 epoch 的官方 request id 已失效）。
// 观察连接（iOS 打开 session 的 observer 订阅面）持有自己的 epoch：官方只把
// thread-scoped 的 server request 投递给订阅连接，iOS 打开 session 后重放/新请求
// 都到达观察连接，应答必须回同一连接——主连接 epoch 不匹配时按 obsClient 路由。
func (a *Agent) clientForEpoch(ctx context.Context, epoch ConnectionEpoch) (*Client, error) {
	_, cl, err := a.endpointFor(ctx)
	if err != nil {
		return nil, err
	}
	if cl.Epoch() == epoch {
		return cl, nil
	}
	a.obsMu.Lock()
	obs := a.obsClient
	a.obsMu.Unlock()
	if obs != nil && obs.Epoch() == epoch {
		return obs, nil
	}
	return nil, fmt.Errorf("codex-web: connection epoch changed (interaction=%d current=%d)；等待官方重发", epoch, cl.Epoch())
}

// RespondSessionPermission 实现 core.SessionPermissionResponder（无 live session 的
// 观察路径也应答——同 daemon 多连接共享 writer 语义，§7.2 先答者得由官方仲裁）。
func (a *Agent) RespondSessionPermission(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	return a.respondPermission(ctx, sessionID, requestID, result)
}

// respondPermission 把 bridge PermissionResult 映射为官方响应：
//   - command/file：allow→{"decision":"accept"}，deny→{"decision":"cancel"}；
//   - permission：deny→{"permissions":{},"scope":"session"}（样本冻结），
//     allow→回显请求的 permissions（RequestPermissionProfile→GrantedPermissionProfile，§7）。
func (a *Agent) respondPermission(ctx context.Context, _, interactionID string, result core.PermissionResult) error {
	if result.Behavior != "allow" && result.Behavior != "deny" {
		return fmt.Errorf("codex-web: unsupported permission behavior %q", result.Behavior)
	}
	it, claimed := a.registry.Claim(interactionID)
	if it == nil {
		return fmt.Errorf("codex-web: interaction %s 不在 pending", interactionID)
	}
	if !claimed {
		return fmt.Errorf("codex-web: interaction %s response already in progress", interactionID)
	}
	sent := false
	defer func() {
		if !sent {
			a.registry.ReleaseClaim(interactionID, it)
		}
	}()
	cl, err := a.clientForEpoch(ctx, it.Epoch)
	if err != nil {
		return err
	}
	allow := result.Behavior == "allow"
	var payload map[string]any
	switch it.Kind {
	case InteractionCommandApproval, InteractionFileApproval:
		if allow {
			payload = map[string]any{"decision": "accept"}
		} else {
			payload = map[string]any{"decision": "cancel"}
		}
	case InteractionPermission:
		if allow {
			granted := map[string]any{}
			var p struct {
				Permissions map[string]any `json:"permissions"`
			}
			if json.Unmarshal(it.Params, &p) == nil && p.Permissions != nil {
				granted = p.Permissions
			}
			payload = map[string]any{"permissions": granted, "scope": "session"}
		} else {
			payload = map[string]any{"permissions": map[string]any{}, "scope": "session"}
		}
	default:
		return fmt.Errorf("codex-web: kind %s 不走 permission 应答", it.Kind)
	}
	if err := cl.RespondServerRequest(it.RequestID, payload); err != nil {
		return err
	}
	sent = true
	return nil
}

// resolvedEvents 处理 serverRequest/resolved 通知 → 收口 + 事件（只对已登记交互发一次）。
// 按 Kind 产出：user_input → EventUserInputResolved（投影把 user_input part 从 pending
// 收为 answered，iOS 面板消失）；审批/其他 → EventPermissionResolved。
func (a *Agent) resolvedEvents(n Notification) []core.Event {
	var p struct {
		ThreadID  string      `json:"threadId"`
		RequestID json.Number `json:"requestId"`
	}
	if json.Unmarshal(n.Params, &p) != nil || p.ThreadID == "" {
		return nil
	}
	// 官方 requestId 是连接级数字；收口按 (thread, requestId→interaction) 匹配。
	a.registry.mu.Lock()
	var match string
	var kind InteractionKind
	for id, it := range a.registry.pending {
		if it.ThreadID == p.ThreadID && it.RequestID == p.RequestID {
			match = id
			kind = it.Kind
			break
		}
	}
	// 已收口的不再找 pending——history 里按 (thread,requestId) 无法回查（interactionID
	// 才是稳定键），resolved 二次到达是官方幂等，直接吞掉。
	a.registry.mu.Unlock()
	if match == "" {
		return nil
	}
	if !a.registry.MarkResolved(match) {
		return nil
	}
	if kind == InteractionUserInput {
		return []core.Event{{
			Type:      core.EventUserInputResolved,
			SessionID: p.ThreadID,
			ThreadID:  p.ThreadID,
			UserInput: &core.UserInputInteraction{
				InteractionID:    match,
				Status:           core.UserInputStatusAnswered,
				ResolutionSource: "official",
			},
		}}
	}
	return []core.Event{{
		Type:      core.EventPermissionResolved,
		SessionID: p.ThreadID,
		ThreadID:  p.ThreadID,
		RequestID: match,
	}}
}

// itemCompletedResolution 相关 item completed 也是收口信号（§7.2）。
func (a *Agent) itemCompletedResolution(threadID, itemID string) []core.Event {
	id := interactionID(threadID, itemID)
	if !a.registry.MarkResolved(id) {
		return nil
	}
	return []core.Event{{
		Type:      core.EventPermissionResolved,
		SessionID: threadID,
		ThreadID:  threadID,
		RequestID: id,
	}}
}
