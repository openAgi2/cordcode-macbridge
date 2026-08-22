package codexweb

// session.go —— Agent 组装根：官方服务连接的持有/复用与 catalog/history 的
// core.Agent 面（§5.1/§5.2/§9.1）。
//
// 生命周期纪律：
//   - 连接懒建立（第一次目录/历史请求时 Probe），复用同一 ServiceEndpoint；
//   - 断线（Request 返回 connection closed/lost）→ 丢弃 endpoint 重 Probe 一次；
//     仍失败则错误上浮，不静默降级（§6.2）；
//   - Stop 只走 ServiceEndpoint.Close 的归属语义：复用/自启 daemon 不 stop，
//     托管 WS 独占回收（§6.3）。
//
// StartSession/turn 生命周期属 Phase 3——当前显式报错，不假装可用（fail closed）。
//
// turn 语义备忘（Phase 0 实测，Phase 3 落地）：steer 必填 expectedTurnId
// （turn/start 响应的 turn.id）；interrupt 必填 turnId；turn/start 响应先于
// active-turn 注册（同毫秒操作报 no active turn）；terminal 只认 turn/completed
// （completed/failed/interrupted）。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Agent 实现 core.Agent（注册名 codex-web）。
type Agent struct {
	workDir     string
	explicitURL string
	codexHome   string
	dataDir     string
	// lifecycleDeps is nil in production; tests inject the same deterministic
	// lifecycle seam used by ProbeWith without changing product configuration.
	lifecycleDeps *LifecycleDeps

	mu        sync.Mutex
	endpoint  *ServiceEndpoint
	probing   bool
	probeDone chan struct{}
	// lastStatus 缓存最近一次 probe 结果供 InstanceStatus 只读镜像（不主动探测）。
	lastStatus *ProbeSnapshot

	// 中央泵（events.go）：一条连接一套 codec，session 按 threadID 注册监听。
	liveCodec   *LiveCodec
	listeners   map[string]map[chan core.Event]struct{}
	pumpRunning bool
	pumpClient  *Client
	stopped     bool

	// metricsMu 保护 §13.2 帧级指标（per thread+turn）。
	metricsMu   sync.Mutex
	turnMetrics map[string]*TurnMetrics // key: threadID + "/" + turnID
	sendAt      map[string]time.Time    // key: threadID（Send 时刻，started 到达时结算）

	// registry 是官方 server request 生命周期账本（interactions.go，§7.2）。
	registry *InteractionRegistry

	// 官方 model/list + typed config/read 的只读快照（models.go）。仅用于校验用户
	// 从刚取得目录中选择的 model；读取失败不回退到该快照冒充新结果。
	modelProvider      string
	effectiveModel     string
	selectedModel      string
	modelExplicit      bool
	modelKnown         map[string]string
	modelEfforts       map[string][]string
	modelDefaultEffort map[string]string
}

// ProbeSnapshot 是一次生命周期的只读快照（descriptor 镜像用）。
type ProbeSnapshot struct {
	Available  bool
	Source     ServiceSource
	Detail     string
	CLIVersion string
	At         time.Time
}

// New 按 opts 构造（main.go buildAgentOptions 的键：work_dir、
// codex_web_app_server_url、codex_web_codex_home）。
func New(opts map[string]any) *Agent {
	a := &Agent{liveCodec: NewLiveCodec(), listeners: map[string]map[chan core.Event]struct{}{}, turnMetrics: map[string]*TurnMetrics{}, sendAt: map[string]time.Time{}, registry: NewInteractionRegistry(), modelKnown: map[string]string{}, modelEfforts: map[string][]string{}, modelDefaultEffort: map[string]string{}}
	if opts == nil {
		return a
	}
	a.workDir, _ = opts["work_dir"].(string)
	a.explicitURL, _ = opts["codex_web_app_server_url"].(string)
	a.codexHome, _ = opts["codex_web_codex_home"].(string)
	a.dataDir, _ = opts["data_dir"].(string)
	return a
}

func (a *Agent) Name() string { return BackendID }

// probeOptions 组装 Probe 入参。
func (a *Agent) probeOptions() ProbeOptions {
	return ProbeOptions{
		ExplicitURL: strings.TrimSpace(a.explicitURL),
		CodexHome:   a.codexHome,
		WorkDir:     a.workDir,
		DataDir:     a.dataDir,
	}
}

// endpointFor 返回可用的 ServiceEndpoint；断线时重 Probe 一次。
func (a *Agent) endpointFor(ctx context.Context) (*ServiceEndpoint, *Client, error) {
	for {
		a.mu.Lock()
		if a.stopped {
			a.mu.Unlock()
			return nil, nil, fmt.Errorf("codexweb: agent stopped")
		}
		if ep := a.endpoint; ep != nil && ep.Client() != nil {
			a.mu.Unlock()
			return ep, ep.Client(), nil
		}
		if a.probing {
			done := a.probeDone
			a.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		a.probing = true
		a.probeDone = make(chan struct{})
		done := a.probeDone
		a.mu.Unlock()

		ep, err := a.probeEndpoint()
		a.mu.Lock()
		stopped := a.stopped
		if err == nil && !stopped {
			a.endpoint = ep
			a.lastStatus = &ProbeSnapshot{
				Available: true, Source: ep.Source, CLIVersion: ep.CLIVersion,
				Detail: fmt.Sprintf("source=%s cli=%s", ep.Source, ep.CLIVersion), At: time.Now(),
			}
		} else {
			a.lastStatus = &ProbeSnapshot{Available: false, Detail: err.Error(), At: time.Now()}
		}
		a.probing = false
		close(done)
		a.mu.Unlock()
		if stopped {
			if ep != nil {
				_ = ep.Close()
			}
			return nil, nil, fmt.Errorf("codexweb: agent stopped")
		}
		if err != nil {
			return nil, nil, err
		}
		return ep, ep.Client(), nil
	}
}

func (a *Agent) probeEndpoint() (*ServiceEndpoint, error) {
	if a.lifecycleDeps != nil {
		return ProbeWith(*a.lifecycleDeps, a.probeOptions())
	}
	return Probe(a.probeOptions())
}

func (a *Agent) invalidateEndpoint(expected *ServiceEndpoint) {
	a.mu.Lock()
	if a.endpoint != expected {
		a.mu.Unlock()
		return
	}
	a.endpoint = nil
	a.mu.Unlock()
	if expected != nil {
		_ = expected.Close()
	}
}

// withClient 执行一次官方 API 调用；连接类失败自动重 Probe 一次后重试。
func (a *Agent) withClient(ctx context.Context, fn func(*Client) error) error {
	ep, cl, err := a.endpointFor(ctx)
	if err != nil {
		return err
	}
	if err := fn(cl); err == nil {
		return nil
	} else if !isConnectionLoss(err) {
		return err
	}
	slog.Info("codexweb: connection lost, re-probing official service")
	a.invalidateEndpoint(ep)
	_, cl2, err := a.endpointFor(ctx)
	if err != nil {
		return err
	}
	a.ensurePump()
	return fn(cl2)
}

func isConnectionLoss(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "connection lost") ||
		strings.Contains(msg, "websocket: close")
}

// ListSessions 实现 core.Agent：官方 thread/list 聚合（服务端默认页大小单页覆盖，
// §22-6 同秒 cursor 跳过边界），字段映射保持官方真相：
//   - Summary = 官方 name 优先，否则 preview（§7）；
//   - ModifiedAt = 官方 updatedAt；Directory = 官方 cwd；ProviderID = modelProvider；
//   - ModelID 官方列表不提供（thread/read 亦无）——保持空，不编造；
//   - ArchivedAt 官方 wire 无该时间戳——保持零值（§7.3）。
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	var out []core.AgentSessionInfo
	err := a.withClient(ctx, func(cl *Client) error {
		threads, rpcErr, err := ListAllThreads(ctx, cl, ListThreadsParams{})
		if err != nil {
			return err
		}
		if rpcErr != nil {
			return rpcErr
		}
		out = make([]core.AgentSessionInfo, 0, len(threads))
		for i := range threads {
			th := threads[i]
			info := core.AgentSessionInfo{
				ID:         th.ID,
				Summary:    th.Title(),
				Directory:  th.Cwd,
				ProviderID: th.ModelProvider,
				ModifiedAt: time.Unix(th.UpdatedAt, 0).UTC(),
			}
			if th.GitInfo != nil && th.GitInfo.Branch != nil {
				info.GitBranch = *th.GitInfo.Branch
			}
			out = append(out, info)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartSession/turn 生命周期实现在 events.go（agentSession）。

// Stop 关闭持有的连接（共享 daemon 不 stop；托管 WS 独占回收——§6.3）。
func (a *Agent) Stop() error {
	a.mu.Lock()
	a.stopped = true
	ep := a.endpoint
	a.endpoint = nil
	done := a.probeDone
	probing := a.probing
	a.mu.Unlock()
	var closeErr error
	if ep != nil {
		closeErr = ep.Close()
	}
	if probing {
		<-done
	}
	return closeErr
}
