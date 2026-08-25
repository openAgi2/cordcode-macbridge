package codexweb

// turn.go —— thread/turn 写入口（§7 create/resume/send/steer/stop/unsubscribe）。
//
// 全部写操作只走官方 JSON-RPC（§9 唯一 writer）。官方 wire 事实（0.149 样本）：
//   - thread/start 响应 {thread, model, modelProvider, cwd, approvalPolicy, sandbox...}；
//   - thread/resume 同形状；跨进程 writer 冲突 = -32600 "already has an active writer"
//     （同 daemon 多连接 resume 无冲突——Phase 0 ownership 实证）；
//   - turn/start 响应 {turn:{id, items:[], itemsView:"notLoaded", status:"inProgress"}}；
//     turn.id 是 steer/interrupt 的必填身份（§2.5）；
//   - turn/steer 响应 {turnId}；stale/无 active turn = -32600 "no active turn to steer"；
//   - turn/interrupt 响应 {}，turn 终态 interrupted（唯一完成真相仍是 turn/completed）；
//   - thread/unsubscribe 响应 {status:"unsubscribed"}；只代表取消订阅，不承诺卸载/释放。
//
// 同毫秒边界（Phase 0 catalog 实测）：turn/start 响应先于服务端 active-turn 注册，
// 立即 steer/interrupt 可能报 no active turn——调用方（session 层）在首个事件后
// 再发控制命令。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StartThreadOptions 是 thread/start 的已验证字段集（cwd 必填；model/modelProvider
// 仅创建时输入，provider id 必须来自已验证官方事实——§7 new-thread provider 行）。
type StartThreadOptions struct {
	Cwd            string
	Model          string
	ModelProvider  string
	PermissionMode string
}

// ThreadStartResult 是 thread/start / thread/resume 响应（官方字段原样保留）。
type ThreadStartResult struct {
	Thread                  ThreadInfo      `json:"thread"`
	Model                   string          `json:"model"`
	ModelProvider           string          `json:"modelProvider"`
	Cwd                     string          `json:"cwd"`
	ApprovalPolicy          json.RawMessage `json:"approvalPolicy"`
	ApprovalsReviewer       string          `json:"approvalsReviewer"`
	ActivePermissionProfile struct {
		ID string `json:"id"`
	} `json:"activePermissionProfile"`
	ReasoningEffort *string `json:"reasoningEffort"`
}

// StartThread 创建官方 thread（取得该 thread 的写入面）。
func StartThread(ctx context.Context, cl *Client, opts StartThreadOptions) (*ThreadStartResult, *RPCError, error) {
	if opts.Cwd == "" {
		return nil, nil, fmt.Errorf("codexweb: thread/start requires cwd")
	}
	params := map[string]any{"cwd": opts.Cwd}
	if opts.Model != "" {
		params["model"] = opts.Model
	}
	if opts.ModelProvider != "" {
		params["modelProvider"] = opts.ModelProvider
	}
	for key, value := range permissionModeParams(opts.PermissionMode) {
		params[key] = value
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/start", params)
	if err != nil {
		return nil, nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr, nil
	}
	var res ThreadStartResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("codexweb: thread/start decode: %w", err)
	}
	if err := validateThreadSettingsResult("thread/start", &res); err != nil {
		return nil, nil, err
	}
	return &res, nil, nil
}

// UpdateThreadPermissionMode changes subsequent turns on a loaded thread using
// fields defined by Codex's official ThreadSettingsUpdateParams.
func UpdateThreadPermissionMode(ctx context.Context, cl *Client, threadID, mode string) error {
	params := map[string]any{"threadId": threadID}
	for key, value := range permissionModeParams(mode) {
		params[key] = value
	}
	if len(params) == 1 {
		return fmt.Errorf("codexweb: permission mode %q has no live override", mode)
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/settings/update", params)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("codexweb: thread/settings/update decode: %w", err)
	}
	return nil
}

// ResumeThread 恢复官方 thread（写入 ownership）。跨进程冲突按 §10.2 翻译。
func ResumeThread(ctx context.Context, cl *Client, threadID string) (*ThreadStartResult, *OwnershipConflictError, *RPCError, error) {
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/resume", map[string]string{"threadId": threadID})
	if err != nil {
		return nil, nil, nil, err
	}
	if rpcErr != nil {
		if oc := TranslateOwnershipConflict("thread/resume", ServiceSource(""), threadID, rpcErr); oc != nil {
			return nil, oc, nil, nil
		}
		return nil, nil, rpcErr, nil
	}
	var res ThreadStartResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, nil, fmt.Errorf("codexweb: thread/resume decode: %w", err)
	}
	if err := validateThreadSettingsResult("thread/resume", &res); err != nil {
		return nil, nil, nil, err
	}
	return &res, nil, nil, nil
}

func validateThreadSettingsResult(method string, result *ThreadStartResult) error {
	if result.Thread.ID == "" {
		return fmt.Errorf("codexweb: %s response missing thread id", method)
	}
	if strings.TrimSpace(result.Model) == "" {
		return fmt.Errorf("codexweb: %s response missing effective model", method)
	}
	if strings.TrimSpace(result.ModelProvider) == "" {
		return fmt.Errorf("codexweb: %s response missing effective model provider", method)
	}
	return nil
}

// InputPart 是 turn 输入 part。一期只实现 text（Phase 0 已采样）；
// image/localImage 等官方 variant 未采样——session 层显式拒绝（fail closed）。
type InputPart struct {
	Type string
	Text string
}

func TextPart(text string) InputPart { return InputPart{Type: "text", Text: text} }

func encodeInput(parts []InputPart) ([]map[string]string, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("codexweb: turn input is empty")
	}
	out := make([]map[string]string, 0, len(parts))
	for _, p := range parts {
		if p.Type != "text" {
			return nil, fmt.Errorf("codexweb: turn input part type %q not sampled/validated (fail closed)", p.Type)
		}
		if p.Text == "" {
			return nil, fmt.Errorf("codexweb: turn input text part is empty")
		}
		out = append(out, map[string]string{"type": "text", "text": p.Text})
	}
	return out, nil
}

// TurnStartResult 是 turn/start 响应的 turn 部分。
type TurnStartResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ItemsView string `json:"itemsView"`
}

// TurnStartOptions 镜像官方 turn/start 的一期稳定 override：model 与 effort 对本
// turn 及后续 turn 生效；provider 不属于 turn/start，由 thread effective settings
// 持有并校验。
type TurnStartOptions struct {
	Model  string
	Effort string
}

// TurnStart 发起 turn。只发送官方 TurnStartParams 支持的字段。
func TurnStart(ctx context.Context, cl *Client, threadID string, parts []InputPart, opts TurnStartOptions) (*TurnStartResult, *RPCError, error) {
	input, err := encodeInput(parts)
	if err != nil {
		return nil, nil, err
	}
	params := map[string]any{"threadId": threadID, "input": input}
	if opts.Model != "" {
		params["model"] = opts.Model
	}
	if opts.Effort != "" {
		params["effort"] = opts.Effort
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "turn/start", params)
	if err != nil {
		return nil, nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr, nil
	}
	var res struct {
		Turn TurnStartResult `json:"turn"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("codexweb: turn/start decode: %w", err)
	}
	if res.Turn.ID == "" {
		return nil, nil, fmt.Errorf("codexweb: turn/start response missing turn id")
	}
	return &res.Turn, nil, nil
}

// TurnSteer 注入输入到 active regular turn（expectedTurnId 必填——§7 steer 行；
// review/compact turn 拒绝时官方错误原样透传）。
func TurnSteer(ctx context.Context, cl *Client, threadID, expectedTurnID string, parts []InputPart) (steeredTurnID string, rpcErr *RPCError, err error) {
	input, err := encodeInput(parts)
	if err != nil {
		return "", nil, err
	}
	if expectedTurnID == "" {
		return "", nil, fmt.Errorf("codexweb: turn/steer requires expectedTurnId (bridge 必须跟踪 active turn)")
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "turn/steer", map[string]any{
		"threadId":       threadID,
		"expectedTurnId": expectedTurnID,
		"input":          input,
	})
	if err != nil {
		return "", nil, err
	}
	if rpcErr != nil {
		return "", rpcErr, nil
	}
	var res struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", nil, fmt.Errorf("codexweb: turn/steer decode: %w", err)
	}
	return res.TurnID, nil, nil
}

// TurnInterrupt 中断 turn（响应 {}；终态真相仍是 turn/completed(interrupted)）。
func TurnInterrupt(ctx context.Context, cl *Client, threadID, turnID string) *RPCError {
	if turnID == "" {
		return &RPCError{Code: -1, Message: "codexweb: turn/interrupt requires turnId"}
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "turn/interrupt", map[string]string{
		"threadId": threadID,
		"turnId":   turnID,
	})
	switch {
	case err != nil:
		return &RPCError{Code: -1, Message: err.Error()}
	case rpcErr != nil:
		return rpcErr
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return &RPCError{Code: -1, Message: fmt.Sprintf("codexweb: turn/interrupt decode: %v", err)}
	}
	return nil
}

// ThreadUnsubscribe 取消订阅（{status:"unsubscribed"}；loaded 保持与卸载由官方
// 30min 延迟策略决定——Phase 0 ownership 实证）。
func ThreadUnsubscribe(ctx context.Context, cl *Client, threadID string) (string, *RPCError, error) {
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/unsubscribe", map[string]string{"threadId": threadID})
	if err != nil {
		return "", nil, err
	}
	if rpcErr != nil {
		return "", rpcErr, nil
	}
	var res struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", nil, fmt.Errorf("codexweb: thread/unsubscribe decode: %w", err)
	}
	return res.Status, nil, nil
}
