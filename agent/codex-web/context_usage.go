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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const persistedUsageTailBytes int64 = 8 << 20

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
			record.Payload.Type != "token_count" || record.Payload.Info == nil {
			continue
		}
		info := record.Payload.Info
		if info.ModelContextWindow <= 0 || info.Last.TotalTokens < 0 {
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
