package codexweb

// Context usage has two official surfaces in Codex 0.149:
//   - thread/tokenUsage/updated is the live and cold-resume notification;
//   - Thread.path identifies Codex's persisted event stream containing the same
//     token_count state used by app-server's cold-resume replay.
//
// Codex explicitly does not replay usage when a thread is already loaded. In
// that case the persisted record is the only current read surface: no
// thread/tokenUsage/read RPC exists. We only open the exact path returned by
// official thread/read; this is not session discovery or a second catalog.
//
// 记录在案的豁免（owner 裁决 2026-08-25，审计 §3.3-C1「保留并加固 + 设计修订」）：
// rollout 内部格式无稳定性契约，本路径受三层加固——契约 fixture
// （testdata/.../dumps/usage/，形状不吻合弃用+诊断）、版本门控
// （persistedUsageVerifiedCLIFamilies）、可见性（descriptor
// usage-source: rollout-tail-experimental + 解析失败 warn）。官方提供冷用量
// RPC 后退役。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const persistedUsageTailBytes int64 = 8 << 20

// persistedUsageVerifiedCLIFamilies：rollout token_count 记录形状按这些 CLI
// 版本族经 fixture 冻结验证（testdata/official-0.149.0-alpha.4/dumps/usage/；
// pin 536f86e5 protocol.rs:2094-2164 TokenUsageInfo/TokenCountEvent/TokenUsage +
// history/src/rollout_payload.rs RolloutItemWire::EventMsg）。官方内部格式无稳定性
// 契约——版本族外不走文件路径（owner 裁决 2026-08-25，审计 §3.3-C1-2）。
var persistedUsageVerifiedCLIFamilies = []string{"0.149."}

func cliVersionAllowsPersistedUsage(version string) bool {
	for _, family := range persistedUsageVerifiedCLIFamilies {
		if strings.HasPrefix(version, family) {
			return true
		}
	}
	return false
}

func (a *Agent) cliVersion() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.endpoint == nil {
		return ""
	}
	return a.endpoint.CLIVersion
}

type persistedTokenBreakdown struct {
	TotalTokens           int `json:"total_tokens"`
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

type persistedTokenCountRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			Total              persistedTokenBreakdown `json:"total_token_usage"`
			Last               persistedTokenBreakdown `json:"last_token_usage"`
			ModelContextWindow int                     `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

func cloneContextUsage(usage *core.ContextUsage) *core.ContextUsage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	return &cloned
}

func (a *Agent) rememberContextUsage(sessionID string, usage *core.ContextUsage) {
	if strings.TrimSpace(sessionID) == "" || usage == nil || usage.ContextWindow <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.usageBySession == nil {
		a.usageBySession = map[string]*core.ContextUsage{}
	}
	if len(a.usageBySession) >= 1024 && a.usageBySession[sessionID] == nil {
		a.usageBySession = map[string]*core.ContextUsage{}
	}
	a.usageBySession[sessionID] = cloneContextUsage(usage)
}

func (a *Agent) cachedContextUsage(sessionID string) *core.ContextUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneContextUsage(a.usageBySession[sessionID])
}

// GetSessionContextUsage supplies get_session's initial control-plane snapshot.
func (a *Agent) GetSessionContextUsage(ctx context.Context, sessionID string) (*core.ContextUsage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("codexweb: context usage requires thread id")
	}
	var thread *ThreadInfo
	err := a.withClient(ctx, func(cl *Client) error {
		result, rpcErr, err := ReadThread(ctx, cl, sessionID, false)
		if err != nil {
			return err
		}
		if rpcErr != nil {
			return rpcErr
		}
		thread = result
		return nil
	})
	if err != nil {
		return nil, err
	}
	if thread == nil || strings.TrimSpace(thread.Path) == "" {
		return a.cachedContextUsage(sessionID), nil
	}
	// 版本门控（审计 §3.3-C1-2）：initialize 记录的 CLI 版本不在已验证版本族 →
	// 不走文件路径（弃用 + 诊断，不静默）。
	if !cliVersionAllowsPersistedUsage(a.cliVersion()) {
		slog.Info("codexweb usage: skip rollout-tail path (unverified CLI version)",
			"thread", sessionID, "cli", a.cliVersion(),
			"usage-source", "rollout-tail-experimental")
		return a.cachedContextUsage(sessionID), nil
	}
	usage, err := readPersistedContextUsage(thread.Path)
	if err != nil {
		return nil, err
	}
	if usage != nil {
		a.rememberContextUsage(sessionID, usage)
		return usage, nil
	}
	return a.cachedContextUsage(sessionID), nil
}

func readPersistedContextUsage(path string) (*core.ContextUsage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("codexweb: open official thread path: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("codexweb: stat official thread path: %w", err)
	}
	start := stat.Size() - persistedUsageTailBytes
	if start < 0 {
		start = 0
	}
	buffer := make([]byte, stat.Size()-start)
	read, err := file.ReadAt(buffer, start)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("codexweb: read official thread usage tail: %w", err)
	}
	buffer = buffer[:read]
	lines := bytes.Split(buffer, []byte{'\n'})
	for index := len(lines) - 1; index >= 0; index-- {
		if start > 0 && index == 0 {
			break // the first tail fragment may start inside a JSON record
		}
		var record persistedTokenCountRecord
		if json.Unmarshal(lines[index], &record) != nil || record.Type != "event_msg" ||
			record.Payload.Type != "token_count" {
			continue
		}
		// 契约不吻合检测（审计 §3.3-C1-1）：最新一条 token_count 记录与冻结
		// fixture 形状不符 → 弃用文件路径 + warn 诊断（不静默回退 cache）。
		// 官方 model_context_window 为 Option（protocol.rs:2097-2099 TODO
		// "make this not optional"）——null/≤0 时无法计算占用比，同样弃用。
		info := record.Payload.Info
		if info == nil || info.ModelContextWindow <= 0 || info.Last.TotalTokens < 0 {
			slog.Warn("codexweb usage: rollout token_count shape mismatch with frozen fixture — abandoning file path",
				"path", path, "usage-source", "rollout-tail-experimental")
			return nil, nil
		}
		return &core.ContextUsage{
			UsedTokens:            info.Last.TotalTokens,
			TotalTokens:           info.Total.TotalTokens,
			InputTokens:           info.Last.InputTokens,
			CachedInputTokens:     info.Last.CachedInputTokens,
			OutputTokens:          info.Last.OutputTokens,
			ReasoningOutputTokens: info.Last.ReasoningOutputTokens,
			ContextWindow:         info.ModelContextWindow,
		}, nil
	}
	return nil, nil
}
