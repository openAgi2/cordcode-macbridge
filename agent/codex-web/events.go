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
// 为 daemon 对全部连接（含观察连接）的全局广播（dumps/ownership 连接 #2 在
// resume 前即收到 turn/started 与 item/completed），不重放。订阅路径 =
// thread/start/resume 自动 attach；writer 座位按进程独占——另一 app-server 已在
// resume 的线程会得到 -32600 "already has an active writer"（dumps/ownership
// second_resume），该冲突不影响广播事件与只读 thread/read。
//
// 被动订阅（core.EventSubscriber，§8.2 外部 turn Gate 的产品面）：
//   - 专用连接；对 loaded 集合逐 thread resume 订阅；全局 thread/started 广播补订阅；
//   - 订阅前的 turn 事件不重放（官方边界），由冷基线补齐，不伪造 turn/started；
//   - 断线关闭通道，go-bridge backoff 循环重连；重连缺口按 §8.3 冷校准。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ensurePump 启动中央泵（幂等）。断线后泵退出并复位标记，重连（reprobe）时再启。
func (a *Agent) ensurePump() {
	a.mu.Lock()
	defer a.mu.Unlock()
	ep := a.endpoint
	if ep == nil || ep.Client() == nil {
		return
	}
	cl := ep.Client()
	if a.pumpRunning && a.pumpClient == cl {
		return
	}
	a.pumpRunning = true
	a.pumpClient = cl
	codec := a.liveCodec
	go func() {
		for n := range cl.Notifications() {
			if n.Method == "thread/started" || n.Method == "thread/name/updated" ||
				n.Method == "thread/archived" || n.Method == "thread/deleted" {
				a.signalCatalogRefresh()
			}
			var extra []core.Event
			switch n.Method {
			case "serverRequest/resolved":
				extra = a.resolvedEvents(n)
			case "item/completed":
				var p struct {
					ThreadID string `json:"threadId"`
					Item     struct {
						ID string `json:"id"`
					} `json:"item"`
				}
				if json.Unmarshal(n.Params, &p) == nil {
					extra = a.itemCompletedResolution(p.ThreadID, p.Item.ID)
				}
			}
			for _, ev := range append(codec.Decode(n), extra...) {
				a.dispatchEvent(ev)
				a.recordMetrics(ev)
			}
		}
		// 连接断开（§8.3）：session 监听者保留（事件在重连前丢弃，缺口由冷校准
		// 覆盖）；标记失连并启动后台重连。不向监听者发任何合成事件——无法证明
		// 完成的交互保持未知，不本地合成成功。
		a.mu.Lock()
		if a.pumpClient != cl {
			a.mu.Unlock()
			return
		}
		a.pumpRunning = false
		a.pumpClient = nil
		failedEndpoint := a.endpoint
		if failedEndpoint != nil && failedEndpoint.Client() == cl {
			a.endpoint = nil
		} else {
			failedEndpoint = nil
		}
		epoch := cl.Epoch()
		a.mu.Unlock()
		if failedEndpoint != nil {
			_ = failedEndpoint.Close()
		}
		slog.Info("codexweb: connection lost, starting background re-probe", "epoch", epoch)
		go a.reconnectLoop()
	}()
	go func() {
		for sr := range cl.ServerRequests() {
			for _, ev := range a.handleServerRequest(sr) {
				a.dispatchEvent(ev)
				a.recordMetrics(ev)
			}
		}
		// 连接终结：清理该 epoch 的 pending 交互（官方 request id 已失效；
		// 重连后只认官方重发——§8.3-5）
		dropped := a.registry.DropEpoch(cl.Epoch())
		if dropped > 0 {
			slog.Info("codexweb: dropped pending interactions of dead epoch", "epoch", cl.Epoch(), "count", dropped)
		}
	}()
}

// dispatchEvent 按官方 threadID 分发给 session 监听者（无监听者的 thread 丢弃——
// 目录/历史等非流式操作不注册监听）。
func (a *Agent) dispatchEvent(ev core.Event) {
	if ev.Type == core.EventPlan && ev.SessionID != "" {
		a.rememberPlan(ev.SessionID, ev.Plan)
	}
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

// planCacheMaxEntries 是 planCache 的简单有界策略：超过即全清（易失状态，重拉成本
// 低于长期膨胀；turn 结束后 plan 本就随新 turn 被官方覆盖）。
const planCacheMaxEntries = 1024

// rememberPlan 记录某 thread 最新的官方 plan（EventPlan 镜像），供 FetchTodos 冷拉取。
func (a *Agent) rememberPlan(threadID string, todos []core.Todo) {
	if len(todos) == 0 {
		return
	}
	cp := make([]core.Todo, len(todos))
	copy(cp, todos)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.planCache == nil {
		a.planCache = map[string][]core.Todo{}
	}
	if len(a.planCache) >= planCacheMaxEntries && a.planCache[threadID] == nil {
		a.planCache = map[string][]core.Todo{}
	}
	a.planCache[threadID] = cp
}

// forgetPlan 删除某 thread 的 plan 镜像（官方删除/退出会话后该镜像不再可信）。
func (a *Agent) forgetPlan(threadID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.planCache != nil {
		delete(a.planCache, threadID)
	}
}

// FetchTodos 实现 core.TodoProvider：返回最近一次 EventPlan 镜像。官方 thread/read
// 的 plan item 只有 text（无结构化 steps），turn/plan/updated 通知才是结构化
// {step,status} 的唯一真相，因此数据源是事件缓存而非历史扫描；没有缓存（turn 未
// 产生过 plan/updated）时返回空列表而非 not_supported，让 iOS 侧留待事件/轮询。
func (a *Agent) FetchTodos(ctx context.Context, sessionID string) ([]core.Todo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.planCache == nil {
		return nil, nil
	}
	cached, ok := a.planCache[sessionID]
	if !ok {
		return nil, nil
	}
	out := make([]core.Todo, len(cached))
	copy(out, cached)
	return out, nil
}

// reconnectLoop §8.3 断线恢复：退避重 Probe 直到成功（或 Agent 停止）；成功后
// 六步就绪 + 中央泵重启。缺口由上层冷校准（thread/read includeTurns）覆盖，
// 不重放、不合成。
func (a *Agent) reconnectLoop() {
	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second
	for {
		a.mu.Lock()
		stopped := a.stopped
		running := a.pumpRunning
		a.mu.Unlock()
		if stopped || running {
			return
		}
		ep, _, err := a.endpointFor(context.Background())
		if err != nil {
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		a.ensurePump()
		slog.Info("codexweb: re-connected to official service", "source", ep.Source)
		return
	}
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

	mu              sync.Mutex
	activeTurnID    string
	effectiveModel  string
	modelProvider   string
	reasoningEffort string
	closed          bool
	unsubscribed    bool
	rawEvents       chan core.Event // 中央泵监听者通道（addListener），只在 Close 解注册
	quit            chan struct{}   // Close 关闭，驱动事件转发退出并关闭 events
	events          chan core.Event // 对外消费通道（relayEvents），由 startEventForward 填
}

// StartSession 建立或恢复一个官方 thread：共享连接上 start/resume（订阅自动
// attach），事件经中央泵按 threadID 分发。sessionID 空 = 新建；非空 = resume
// （写入 ownership，§10.2 冲突翻译）。
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	_, cl, err := a.endpointFor(ctx)
	if err != nil {
		return nil, err
	}
	var settings *ThreadStartResult
	if sessionID == "" {
		model, provider := a.selectedModelForStart()
		res, rpcErr, err := StartThread(ctx, cl, StartThreadOptions{Cwd: a.GetWorkDir(), Model: model, ModelProvider: provider, PermissionMode: a.GetMode()})
		switch {
		case err != nil:
			return nil, err
		case rpcErr != nil:
			return nil, rpcErr
		}
		settings = res
	} else {
		res, oc, rpcErr, err := ResumeThread(ctx, cl, sessionID)
		switch {
		case err != nil:
			return nil, err
		case oc != nil:
			oc.TransportSource = a.endpointSource()
			return nil, oc
		case rpcErr != nil:
			return nil, rpcErr
		}
		settings = res
	}
	a.rememberPermissionMode(settings)
	threadID := settings.Thread.ID
	a.ensurePump()
	raw := a.addListener(threadID)
	s := &agentSession{
		agent:           a,
		threadID:        threadID,
		effectiveModel:  settings.Model,
		modelProvider:   settings.ModelProvider,
		reasoningEffort: stringValue(settings.ReasoningEffort),
		rawEvents:       raw,
		quit:            make(chan struct{}),
		events:          make(chan core.Event, 256),
	}
	s.startEventForward()
	return s, nil
}

// startEventForward 把中央泵分发的原始事件转发到 s.events（relayEvents 消费），
// 并在转发前用事件流维护本 session 的 active turn 状态。activeTurnID 的唯一真相
// 是官方 turn/started 与 turn/completed：本端 TurnStart 的返回 id 只代表请求被
// 接受，同一 thread 上由 Mac Desktop 等其他客户端发起的 turn 也会经中央泵广播，
// 二者必须合流（2026-08-24 真机：外部 turn 开始后 activeTurnID 停留在旧值，停止
// 请求报 -32600 expected active turn id ... but found ...）。Close 时关闭
// s.events——这是 relayEvents（go-bridge）对该会话事件通道的关闭信号，退出 agent
// relay、释放 agentRelayRunning，让被动观察泵接管官方后续帧（防止关掉监听者后
// relay 变成读不到事件的僵尸并永久挡住被动泵摄入）。
func (s *agentSession) startEventForward() {
	go func() {
		defer close(s.events)
		for {
			select {
			case <-s.quit:
				return
			case ev, ok := <-s.rawEvents:
				if !ok {
					return
				}
				s.observeEvent(ev)
				select {
				case s.events <- ev:
				case <-s.quit:
					return
				}
			}
		}
	}()
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
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

// SetLiveMode applies a composer permission choice through Codex's official
// thread/settings/update API. "custom" remains config-owned and only affects
// future sessions because the official API has no clear-override operation.
func (s *agentSession) SetLiveMode(mode string) bool {
	mode = normalizePermissionMode(mode)
	if mode == "custom" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err := s.client(ctx)
	if err != nil {
		return false
	}
	if err := UpdateThreadPermissionMode(ctx, cl, s.threadID, mode); err != nil {
		slog.Warn("codexweb: official permission mode update failed", "thread", s.threadID, "mode", mode, "error", err)
		return false
	}
	return true
}

// Send 发送用户消息（turn/start，仅 text part——image 未采样 fail closed）。
func (s *agentSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	return s.SendWithOptions(prompt, images, files, core.PromptOptions{})
}

// SendWithOptions 携带 per-request 选项。官方源码锚点：
// codex-rs/app-server-protocol/src/protocol/v2/turn.rs::TurnStartParams 不含 provider；
// codex-rs/tui/src/app_server_session.rs 先把 thread start/resume response 转为
// ThreadSessionState，再在 turn_start 只发送 model/effort 等官方 override。
// iOS 的 ProviderID 是模型目录命名空间：与 thread provider 一致时用于校验，不作为
// turn 字段发送；不一致才是官方不支持的 running-thread provider switch。
func (s *agentSession) SendWithOptions(prompt string, images []core.ImageAttachment, files []core.FileAttachment, opts core.PromptOptions) error {
	if len(images) > 0 || len(files) > 0 {
		return errUnsampledInputKind
	}
	if opts.Agent != "" || opts.Variant != "" {
		return errUnsupportedTurnOption
	}
	s.mu.Lock()
	threadProvider := s.modelProvider
	threadModel := s.effectiveModel
	s.mu.Unlock()
	requestedProvider := strings.TrimSpace(opts.ProviderID)
	if requestedProvider != "" && requestedProvider != threadProvider {
		return &userError{fmt.Sprintf("codex-web: provider switch %q -> %q is not supported by official turn/start", threadProvider, requestedProvider)}
	}
	model, modelProvider, err := s.agent.modelForTurn(opts.ModelID)
	if err != nil {
		return err
	}
	if modelProvider != "" && modelProvider != threadProvider {
		return &userError{fmt.Sprintf("codex-web: model %q belongs to provider %q, but thread provider is %q", opts.ModelID, modelProvider, threadProvider)}
	}
	modelKey := qualifyModel(threadProvider, threadModel)
	if model != "" {
		modelKey = qualifyModel(threadProvider, model)
	}
	effort, err := s.agent.effortForTurn(modelKey, opts.ReasoningEffort)
	if err != nil {
		return err
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
	sendAt := time.Now()
	res, rpcErr, err := TurnStart(ctx, cl, s.threadID, parts, TurnStartOptions{Model: model, Effort: effort})
	switch {
	case err != nil:
		return err
	case rpcErr != nil:
		return rpcErr
	}
	s.mu.Lock()
	s.activeTurnID = res.ID
	if model != "" {
		s.effectiveModel = model
	}
	if effort != "" {
		s.reasoningEffort = effort
	}
	s.mu.Unlock()
	s.agent.noteSend(s.threadID, sendAt)
	return nil
}

var errUnsampledInputKind = &userError{"codex-web: image/file 输入 part 未取得官方真实样本，fail closed（Phase 0 §12 边界）"}
var errUnsupportedTurnOption = &userError{"codex-web: 官方 turn/start 不支持 agent/variant 逐 turn 覆盖（§7）"}

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

// currentTurnForControl 取 steer/interrupt 的 turn 身份：中央泵观测（liveCodec）
// 优先，本端 turn/start 返回 id 兜底。后者在同一 thread 上被外部客户端（共享
// daemon 上的 Mac Desktop）发起的 turn 取代后过期——external turn 的
// turn/started 广播先到中央泵，liveCodec 才是当前 turn 的权威身份（2026-08-24
// 真机：过期 local 使首次停止报 -32600 expected active turn id ... but found ...）。
// liveCodec 无观测时（本端 turn/start 已返回、turn/started 事件到达前的毫秒窗口）
// 回退 local。Phase 0 同毫秒边界：立即控制仍可能报官方 -32600——原样透传，
// 不重试伪装。
func (s *agentSession) currentTurnForControl() string {
	s.agent.mu.Lock()
	observed := s.agent.liveCodec.ActiveTurn(s.threadID)
	s.agent.mu.Unlock()
	if observed != "" {
		return observed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTurnID
}

// CancelTurnForThread 对观测/被动订阅的 thread 执行官方 turn/interrupt
// （core.ThreadTurnCanceler）：iOS 停止 Mac 在共享 daemon 上发起的 turn 时，
// 本端没有该 thread 的写会话，turnID 只能取中央泵 liveCodec 观测到的 active turn。
// 无活动 turn / 官方 -32600 均原样返回，不重试伪装。
//
// 订阅前已运行的 turn（2026-08-23 真机 01a02f29）：观察连接在 turn/started 广播
// 之后才 attach，官方不重放开始事件——liveCodec 永远看不到。此时 thread/read
// 的 inProgress 状态是官方身份的另一条合法来源（与外置 turn 冷基线同源），
// 在 liveCodec miss 时兜底查询，仍然 fail closed（找不到才算 no active turn）。
func (a *Agent) CancelTurnForThread(ctx context.Context, threadID string) error {
	_, cl, err := a.endpointFor(ctx)
	if err != nil {
		return err
	}
	turnID := a.liveCodec.ActiveTurn(threadID)
	if turnID == "" {
		turnID = a.inProgressTurnFromColdBaseline(ctx, cl, threadID)
	}
	if turnID == "" {
		return &userError{"codex-web: no active turn to interrupt"}
	}
	if rpcErr := TurnInterrupt(ctx, cl, threadID, turnID); rpcErr != nil {
		return rpcErr
	}
	return nil
}

// inProgressTurnFromColdBaseline 读官方 thread/read(includeTurns) 找状态为
// inProgress 的 turn（订阅前已开始的观察 turn 的兜底身份来源）。
func (a *Agent) inProgressTurnFromColdBaseline(ctx context.Context, cl *Client, threadID string) string {
	turns, rpcErr, err := ReadThreadRich(ctx, cl, threadID, 0)
	if err != nil || rpcErr != nil {
		if err != nil {
			slog.Debug("codexweb: cold baseline turn read failed", "thread", threadID, "error", err)
		} else {
			slog.Debug("codexweb: cold baseline turn read rejected", "thread", threadID, "official", rpcErr.Message)
		}
		return ""
	}
	for _, t := range turns {
		if t.Status == TurnStatusInProgress && t.TurnID != "" {
			return t.TurnID
		}
	}
	return ""
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

// RespondPermission 应答审批（requestID = registry interactionID =
// threadId ":" itemId；官方 decision 词汇见 interactions.go）。
func (s *agentSession) RespondPermission(requestID string, result core.PermissionResult) error {
	return s.agent.respondPermission(context.Background(), s.threadID, requestID, result)
}

// RespondQuestion / RejectQuestion 应答 requestUserInput（interactionID 级整批提交）。
func (s *agentSession) RespondQuestion(questionID string, optionIDs []string) error {
	return s.agent.respondUserInput(context.Background(), questionID, optionIDs, false)
}
func (s *agentSession) RejectQuestion(questionID string) error {
	return s.agent.respondUserInput(context.Background(), questionID, nil, true)
}

// observeEvent 由事件转发（startEventForward）维护 active turn。
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

// Close 注销监听者并关闭事件通道。共享连接纪律：多个 session 复用 Agent 的一条
// 连接，而官方 thread/unsubscribe 的作用域是**连接**——任何 session 在共享连接上
// unsubscribe 都会把同 thread 的其他 session 一并断流（真实故障：第二 session
// Close 后后续 turn 事件不再到达）。因此 per-session Close 只注销监听者、不向
// daemon 发 unsubscribe；订阅随连接生命周期（Agent.Stop / 断线重连）释放，
// loaded 保持由官方 30min 策略管理（§7）。events 通道随 Close 关闭：go-bridge 的
// relayEvents 据此退出（官方 turn 事件改由被动观察泵摄入，详见
// startEventForward 注释）。
func (s *agentSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	if s.quit != nil {
		close(s.quit)
	}
	s.agent.removeListener(s.threadID, s.rawEvents)
	return nil
}

// ---- 被动订阅（外部 turn 实时旁观，§8.2 Gate PASS 的产品面） ----

// Subscribe 实现 core.EventSubscriber：在 Agent 唯一解析出的 ServiceEndpoint 上
// 新建观察 connection，订阅全部 loaded threads。这里禁止再次 Probe：endpoint 已冻结为
// 同一个官方 daemon，观察 connection 必须通过 OpenClient 复用该解析结果；任何重新解析并
// 另起 service 的行为都直接违反设计 §4/§6 的 single shared service。
func (a *Agent) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	ep, _, err := a.endpointFor(ctx)
	if err != nil {
		return nil, err
	}
	cl, err := ep.OpenClient(ctx, a.probeOptions())
	if err != nil {
		return nil, err
	}

	a.obsMu.Lock()
	a.obsClient = cl
	a.obsSubscribed = map[string]bool{}
	a.obsMu.Unlock()

	// 观察连接解码必须与中央泵共享同一个 liveCodec：turn/started 在此解码后
	// CancelTurnForThread（外部 turn 停止）才认识该 turn（2026-08-23 真机：
	// Mac 发起的观察 turn，iOS 停止报 "no active turn to interrupt"——被动
	// 订阅用独立 codec 时 a.liveCodec 永远不知情）。本地 codec 会 +90s
	// restart 周期被替换，但不影响正确性——共享对象随 Agent 存活。
	codec := a.liveCodec
	events := make(chan core.Event, 256)

	// 初始订阅面：loaded 集合（可产生事件的全部官方 thread）。
	if raw, rpcErr, err := cl.RequestContext(ctx, "thread/loaded/list", map[string]any{}); err == nil && rpcErr == nil {
		var list struct {
			Data []string `json:"data"`
		}
		if json.Unmarshal(raw, &list) == nil {
			for _, id := range list.Data {
				a.observeThread(ctx, cl, id)
			}
		}
	}

	go func() {
		defer close(events)
		defer func() {
			_ = cl.Close()
			a.obsMu.Lock()
			if a.obsClient == cl {
				a.obsClient = nil
				a.obsSubscribed = nil
			}
			a.obsMu.Unlock()
		}()
		for n := range cl.Notifications() {
			// 新 thread 广播 → 补订阅（此前的 turn 事件不重放，官方边界；冷基线补齐）
			if n.Method == "thread/started" {
				a.signalCatalogRefresh()
				var p struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				}
				if json.Unmarshal(n.Params, &p) == nil && p.Thread.ID != "" {
					go a.observeThread(context.Background(), cl, p.Thread.ID)
				}
				continue
			}
			if n.Method == "thread/name/updated" || n.Method == "thread/archived" ||
				n.Method == "thread/deleted" {
				a.signalCatalogRefresh()
			}
			for _, ev := range codec.Decode(n) {
				if ev.Type == core.EventPlan && ev.SessionID != "" {
					a.rememberPlan(ev.SessionID, ev.Plan)
				}
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

// AttachLiveThread 让观察 connection 订阅 iOS 正在看的 thread。thread/start 只
// attach 写连接；Mac 后续 turn 的 item/* 若观察连接未 resume，iOS 就收不到实时流。
func (a *Agent) AttachLiveThread(ctx context.Context, threadID string) error {
	a.obsMu.Lock()
	cl := a.obsClient
	a.obsMu.Unlock()
	if cl == nil {
		return core.ErrObserverNotReady
	}
	return a.observeThread(ctx, cl, threadID)
}

func (a *Agent) observeThread(ctx context.Context, cl *Client, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || cl == nil {
		return core.ErrObserverNotReady
	}
	a.obsMu.Lock()
	if a.obsClient != cl {
		a.obsMu.Unlock()
		return core.ErrObserverNotReady
	}
	if a.obsSubscribed[threadID] {
		a.obsMu.Unlock()
		return nil
	}
	a.obsMu.Unlock()

	_, oc, rpcErr, err := ResumeThread(ctx, cl, threadID)
	switch {
	case err != nil:
		slog.Warn("codexweb passive: resume transport error", "thread", threadID, "error", err)
		return err
	case oc != nil:
		// 官方预期（dumps/ownership second_resume：-32600 "already has an active
		// writer"）：writer 座位按进程独占，本观察连接拿不到。turn/item 事件是
		// daemon 对全部连接的全局广播（dump 连接 #2 在 resume 前即收到事件），
		// 只读 thread/read 在 writer 持有期同样可用，故 attach 失败不影响
		// 实时事件流与投影——仅记录该事实，不要把「握有 writer 的进程」误判为
		// 「Desktop 离开了共享 daemon」。
		slog.Warn("codexweb passive: thread writer held by another app-server (expected); read-only paths remain",
			"thread", threadID, "official", oc.OfficialMessage)
		return oc
	case rpcErr != nil:
		slog.Warn("codexweb passive: resume rejected", "thread", threadID, "official", rpcErr.Message)
		return rpcErr
	default:
		a.obsMu.Lock()
		if a.obsClient == cl {
			if a.obsSubscribed == nil {
				a.obsSubscribed = map[string]bool{}
			}
			a.obsSubscribed[threadID] = true
		}
		a.obsMu.Unlock()
		slog.Info("codexweb passive: subscribed", "thread", threadID)
		return nil
	}
}

var _ core.AgentSession = (*agentSession)(nil)
var _ core.EventSubscriber = (*Agent)(nil)
var _ core.ThreadLiveAttacher = (*Agent)(nil)
var _ core.PromptOptionsSender = (*agentSession)(nil)
var _ core.TurnCanceler = (*agentSession)(nil)
var _ core.TodoProvider = (*Agent)(nil)
var _ core.ThreadTurnCanceler = (*Agent)(nil)
