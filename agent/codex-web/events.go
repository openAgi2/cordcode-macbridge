package codexweb

// events.go —— agent 级有序事件泵与被动订阅（设计 §5.2/§8.2）。
//
// 连接与读者纪律：
//   - Agent 持有一条官方长连接；其 Notifications()/ServerRequests() 各自只有
//     一个消费者（中央泵）。多个 agentSession 复用该连接的 RPC 面，事件经
//     中央泵按 threadID 分发给注册的 session 监听者——不允许每个 session 各起
//     泵抢读同一通道（真实竞态：终态事件被随机分到错误 session）；
//   - 被动订阅（Subscribe）使用专用连接（观察面，不答请求）。
//
// Phase 0 通知分级（§7.1）：thread/started、thread/status/changed 全局；turn/*、item/*
// 仅订阅连接且不重放。订阅路径 = thread/start/resume 自动 attach；同 daemon 多连接
// resume 无 writer 冲突（dumps/ownership）。
//
// 被动订阅（core.EventSubscriber，§8.2 外部 turn Gate 的产品面）：
//   - 专用连接；对 loaded 集合逐 thread resume 订阅；全局 thread/started 广播补订阅；
//   - 订阅前的 turn 事件不重放（官方边界），由冷基线补齐，不伪造 turn/started；
//   - 断线关闭通道，go-bridge backoff 循环重连；重连缺口按 §8.3 冷校准。

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ensurePump 启动中央泵（幂等）。断线后泵退出并复位标记，重连（reprobe）时再启。
func (a *Agent) ensurePump() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pumpRunning {
		return
	}
	ep := a.endpoint
	if ep == nil || ep.Client() == nil {
		return
	}
	a.pumpRunning = true
	cl := ep.Client()
	codec := a.liveCodec
	go func() {
		for n := range cl.Notifications() {
			for _, ev := range codec.Decode(n) {
				a.dispatchEvent(ev)
			}
		}
		// 连接断开：关闭全部监听者（上层触发重连）；监听者注册表清空。
		a.mu.Lock()
		a.pumpRunning = false
		listeners := a.listeners
		a.listeners = map[string]map[chan core.Event]struct{}{}
		a.mu.Unlock()
		for _, set := range listeners {
			for ch := range set {
				close(ch)
			}
		}
	}()
	go func() {
		for sr := range cl.ServerRequests() {
			a.dispatchServerRequest(sr)
		}
	}()
}

// dispatchEvent 按官方 threadID 分发给 session 监听者（无监听者的 thread 丢弃——
// 目录/历史等非流式操作不注册监听）。
func (a *Agent) dispatchEvent(ev core.Event) {
	a.mu.Lock()
	set := a.listeners[ev.SessionID]
	chans := make([]chan core.Event, 0, len(set))
	for ch := range set {
		chans = append(chans, ch)
	}
	a.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- ev:
		default:
			slog.Warn("codexweb: session listener overflow, dropping event", "thread", ev.SessionID, "type", string(ev.Type))
		}
	}
}

// dispatchServerRequest Phase 3 最小诚实处理：记录不代答（审批/提问 registry 属
// Phase 4；官方超时/取消路径收口）。
func (a *Agent) dispatchServerRequest(sr ServerRequest) {
	slog.Debug("codexweb: server request pending (Phase 4 registry)", "method", sr.Method, "thread", sr.ThreadID)
}

func (a *Agent) addListener(threadID string) chan core.Event {
	ch := make(chan core.Event, 256)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.listeners == nil {
		a.listeners = map[string]map[chan core.Event]struct{}{}
	}
	if a.listeners[threadID] == nil {
		a.listeners[threadID] = map[chan core.Event]struct{}{}
	}
	a.listeners[threadID][ch] = struct{}{}
	return ch
}

func (a *Agent) removeListener(threadID string, ch chan core.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if set := a.listeners[threadID]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(a.listeners, threadID)
		}
	}
}

// agentSession 是 codex-web 的 core.AgentSession 实现：一个官方 thread 的写入面
// （共享连接 RPC）+ 中央泵分发的有序事件流。
type agentSession struct {
	agent    *Agent
	threadID string

	mu           sync.Mutex
	activeTurnID string
	closed       bool
	unsubscribed bool
	events       chan core.Event
}

// StartSession 建立或恢复一个官方 thread：共享连接上 start/resume（订阅自动
// attach），事件经中央泵按 threadID 分发。sessionID 空 = 新建；非空 = resume
// （写入 ownership，§10.2 冲突翻译）。
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	_, cl, err := a.endpointFor(ctx)
	if err != nil {
		return nil, err
	}
	var threadID string
	if sessionID == "" {
		res, rpcErr, err := StartThread(ctx, cl, StartThreadOptions{Cwd: a.workDir})
		switch {
		case err != nil:
			return nil, err
		case rpcErr != nil:
			return nil, rpcErr
		}
		threadID = res.Thread.ID
	} else {
		res, oc, rpcErr, err := ResumeThread(ctx, cl, sessionID)
		switch {
		case err != nil:
			return nil, err
		case oc != nil:
			return nil, oc
		case rpcErr != nil:
			return nil, rpcErr
		}
		threadID = res.Thread.ID
	}
	a.ensurePump()
	s := &agentSession{
		agent:    a,
		threadID: threadID,
		events:   a.addListener(threadID),
	}
	return s, nil
}

// client 经 Agent 解析当前连接（断线重连后自动指向新连接）。
func (s *agentSession) client(ctx context.Context) (*Client, error) {
	_, cl, err := s.agent.endpointFor(ctx)
	return cl, err
}

func (s *agentSession) Events() <-chan core.Event { return s.events }

func (s *agentSession) CurrentSessionID() string { return s.threadID }

func (s *agentSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// Send 发送用户消息（turn/start，仅 text part——image 未采样 fail closed）。
func (s *agentSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	return s.SendWithOptions(prompt, images, files, core.PromptOptions{})
}

// SendWithOptions 携带 per-request 选项；ModelID 映射官方 turn/start{model}（对本
// turn 及后续 turn 生效，§7）；ProviderID/Agent/Variant 官方 turn/start 不支持——
// 非空时显式报错，不静默忽略。
func (s *agentSession) SendWithOptions(prompt string, images []core.ImageAttachment, files []core.FileAttachment, opts core.PromptOptions) error {
	if len(images) > 0 || len(files) > 0 {
		return errUnsampledInputKind
	}
	if opts.ProviderID != "" || opts.Agent != "" || opts.Variant != "" {
		return errUnsupportedTurnOption
	}
	ctx := context.Background()
	cl, err := s.client(ctx)
	if err != nil {
		return err
	}
	parts := []InputPart{}
	if prompt != "" {
		parts = append(parts, TextPart(prompt))
	}
	res, rpcErr, err := TurnStart(ctx, cl, s.threadID, parts, opts.ModelID)
	switch {
	case err != nil:
		return err
	case rpcErr != nil:
		return rpcErr
	}
	s.mu.Lock()
	s.activeTurnID = res.ID
	s.mu.Unlock()
	return nil
}

var errUnsampledInputKind = &userError{"codex-web: image/file 输入 part 未取得官方真实样本，fail closed（Phase 0 §12 边界）"}
var errUnsupportedTurnOption = &userError{"codex-web: 官方 turn/start 不支持 provider/agent/variant 逐 turn 覆盖（§7）"}

type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

// CancelTurn 中断当前 active turn（turn/interrupt；终态真相仍是 turn/completed）。
func (s *agentSession) CancelTurn(ctx context.Context) error {
	cl, err := s.client(ctx)
	if err != nil {
		return err
	}
	turnID := s.currentTurnForControl()
	if turnID == "" {
		return &userError{"codex-web: no active turn to interrupt"}
	}
	if rpcErr := TurnInterrupt(ctx, cl, s.threadID, turnID); rpcErr != nil {
		return rpcErr
	}
	return nil
}

// currentTurnForControl 取 steer/interrupt 的 turn 身份：本端 turn/start 返回 id
// 优先；否则中央泵 codec 观测（外部 turn/started）。Phase 0 同毫秒边界：立即控制
// 可能报官方 -32600——原样透传，不重试伪装。
func (s *agentSession) currentTurnForControl() string {
	s.mu.Lock()
	local := s.activeTurnID
	s.mu.Unlock()
	if local != "" {
		return local
	}
	s.agent.mu.Lock()
	defer s.agent.mu.Unlock()
	return s.agent.liveCodec.ActiveTurn(s.threadID)
}

// Steer 注入输入到 active regular turn。
func (s *agentSession) Steer(ctx context.Context, prompt string) (string, error) {
	cl, err := s.client(ctx)
	if err != nil {
		return "", err
	}
	turnID := s.currentTurnForControl()
	if turnID == "" {
		return "", &userError{"codex-web: no active turn to steer (expectedTurnId 必填，§7)"}
	}
	steered, rpcErr, err := TurnSteer(ctx, cl, s.threadID, turnID, []InputPart{TextPart(prompt)})
	switch {
	case err != nil:
		return "", err
	case rpcErr != nil:
		return "", rpcErr
	}
	return steered, nil
}

// RespondPermission / RespondQuestion / RejectQuestion 属 Phase 4（审批/提问
// registry）；当前显式 fail closed，不假装成功。
func (s *agentSession) RespondPermission(requestID string, result core.PermissionResult) error {
	return errInteractionPhase4
}
func (s *agentSession) RespondQuestion(questionID string, optionIDs []string) error {
	return errInteractionPhase4
}
func (s *agentSession) RejectQuestion(questionID string) error {
	return errInteractionPhase4
}

var errInteractionPhase4 = &userError{"codex-web: approval/question response lands in Phase 4 (registry + official decision vocabulary)"}

// observeEvent 由中央泵外的事件路径（测试）维护 active turn。
func (s *agentSession) observeEvent(ev core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case ev.Type == core.EventTurnStarted && ev.TurnID != "":
		s.activeTurnID = ev.TurnID
	case (ev.Type == core.EventResult || ev.Type == core.EventError) && ev.Done:
		if s.activeTurnID == ev.TurnID {
			s.activeTurnID = ""
		}
	}
}

// Close 注销监听者。共享连接纪律：多个 session 复用 Agent 的一条连接，而官方
// thread/unsubscribe 的作用域是**连接**——任何 session 在共享连接上 unsubscribe
// 都会把同 thread 的其他 session 一并断流（真实故障：第二 session Close 后
// 后续 turn 事件不再到达）。因此 per-session Close 只注销监听者；订阅随连接
// 生命周期（Agent.Stop / 断线重连）释放，loaded 保持由官方 30min 策略管理（§7）。
func (s *agentSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.agent.removeListener(s.threadID, s.events)
	return nil
}

// ---- 被动订阅（外部 turn 实时旁观，§8.2 Gate PASS 的产品面） ----

// Subscribe 实现 core.EventSubscriber：专用连接订阅全部 loaded threads，流出
// 官方 live 事件（SessionID=thread.id）。断线即关闭通道。
func (a *Agent) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	ep, err := Probe(a.probeOptions())
	if err != nil {
		return nil, err
	}
	cl := ep.Client()
	if cl == nil {
		_ = ep.Close()
		return nil, &userError{"codexweb: probe ready but no client"}
	}

	codec := NewLiveCodec()
	events := make(chan core.Event, 256)
	subscribed := map[string]bool{}

	resume := func(threadID string) {
		if threadID == "" || subscribed[threadID] {
			return
		}
		_, _, rpcErr, err := ResumeThread(ctx, cl, threadID)
		switch {
		case err != nil:
			slog.Debug("codexweb passive: resume transport error", "thread", threadID, "error", err)
		case rpcErr != nil:
			slog.Debug("codexweb passive: resume rejected", "thread", threadID, "official", rpcErr.Message)
		default:
			subscribed[threadID] = true
		}
	}

	// 初始订阅面：loaded 集合（可产生事件的全部官方 thread）。
	if raw, rpcErr, err := cl.RequestContext(ctx, "thread/loaded/list", map[string]any{}); err == nil && rpcErr == nil {
		var list struct {
			Data []string `json:"data"`
		}
		if json.Unmarshal(raw, &list) == nil {
			for _, id := range list.Data {
				resume(id)
			}
		}
	}

	go func() {
		defer close(events)
		defer func() { _ = ep.Close() }()
		for n := range cl.Notifications() {
			// 新 thread 广播 → 补订阅（此前的 turn 事件不重放，官方边界；冷基线补齐）
			if n.Method == "thread/started" {
				var p struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				}
				if json.Unmarshal(n.Params, &p) == nil {
					resume(p.Thread.ID)
				}
				continue
			}
			for _, ev := range codec.Decode(n) {
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
		// 连接断开：通道关闭，startPassiveSubscription 以 backoff 重连；
		// 重连后的缺口由 §8.3 冷校准（thread/read includeTurns）覆盖。
	}()
	// server requests 通道需有消费者（避免 readLoop 阻塞）；被动连接不答请求。
	go func() {
		for sr := range cl.ServerRequests() {
			slog.Debug("codexweb passive: server request ignored (observer connection)", "method", sr.Method)
		}
	}()
	return events, nil
}

var _ core.AgentSession = (*agentSession)(nil)
var _ core.EventSubscriber = (*Agent)(nil)
var _ core.PromptOptionsSender = (*agentSession)(nil)
var _ core.TurnCanceler = (*agentSession)(nil)
