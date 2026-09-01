package gobridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const catalogRequestTimeout = 8 * time.Second

// codexVisibleMembership and grokVisibleMembership are the single pre-enrichment membership
// owners shared by declared snapshots and discovery polling. The former undeclared v1
// presentation path was retired in Phase 8B Stage 2.
func (h *Handlers) codexVisibleMembership(ctx context.Context, backendID, dir string) ([]map[string]interface{}, core.Agent, error) {
	agent, ok := h.getAgent(backendID)
	if !ok {
		return nil, nil, fmt.Errorf("codex agent not registered for backend %q", backendID)
	}
	lister, ok := agent.(codexThreadLister)
	if !ok {
		return nil, nil, fmt.Errorf("codex agent %q does not support thread/list catalog", backendID)
	}
	sessions, err := lister.FetchThreadList(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	return filterCodexCatalogSessions(sessionsToWire(sessions)), agent, nil
}

// codexVisibleMembershipCounts 同 codexVisibleMembership 但同一次 fetch 内返回
// 过滤前 raw 计数（discovery 日志审计：raw/filter 两计数确认 438/429 类差异来自
// workspace 过滤而非数据源漂移）。只读一次 thread/list，不重复拉取。
func (h *Handlers) codexVisibleMembershipCounts(ctx context.Context, backendID, dir string) ([]map[string]interface{}, int, error) {
	agent, ok := h.getAgent(backendID)
	if !ok {
		return nil, 0, fmt.Errorf("codex agent not registered for backend %q", backendID)
	}
	lister, ok := agent.(codexThreadLister)
	if !ok {
		return nil, 0, fmt.Errorf("codex agent %q does not support thread/list catalog", backendID)
	}
	sessions, err := lister.FetchThreadList(ctx, dir)
	if err != nil {
		return nil, 0, err
	}
	return filterCodexCatalogSessions(sessionsToWire(sessions)), len(sessions), nil
}

func (h *Handlers) grokVisibleMembership(ctx context.Context, backendID string) ([]map[string]interface{}, core.Agent, error) {
	agent, ok := h.getAgent(backendID)
	if !ok {
		return nil, nil, fmt.Errorf("grokbuild agent not registered for backend %q", backendID)
	}
	lister, ok := agent.(grokSessionLister)
	if !ok {
		return nil, nil, fmt.Errorf("grokbuild agent %q does not support session/list catalog", backendID)
	}
	sessions, err := lister.FetchSessionList(ctx)
	if err != nil {
		return nil, nil, err
	}
	mapped := filterGrokPlaceholderSessions(sessionsToWire(sessions))
	return filterSessionsMissingWorkspace(mapped), agent, nil
}

func copyWireMaps(maps []map[string]interface{}) []map[string]interface{} {
	return append([]map[string]interface{}(nil), maps...)
}

// listSemanticFingerprint is shared by discovery and snapshot epochs; presentation-only
// pin/running overlays are deliberately excluded.
func listSemanticFingerprint(maps []map[string]interface{}) string {
	var b strings.Builder
	for index, item := range maps {
		id, _ := item["id"].(string)
		ts, _ := item["updatedAtMillis"].(int64)
		title, _ := item["title"].(string)
		dir := sessionDirectoryKey(item)
		if normalized, ok := normalizeCatalogDirectory(dir); ok {
			dir = normalized
		}
		project, _ := item["projectId"].(string)
		b.WriteString(strconv.Itoa(index))
		b.WriteByte('|')
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(ts, 10))
		b.WriteByte('|')
		b.WriteString(dir)
		b.WriteByte('|')
		b.WriteString(project)
		b.WriteByte('|')
		b.WriteString(title)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// remoteCatalogFingerprint covers the metadata that can change the Codex Desktop
// session row, while deliberately excluding updatedAtMillis. Remote Control sends
// live turn deltas on the same stream; its recency timestamp changes during every
// turn and must not turn the discovery safety poll into a sessions_changed storm.
// Lifecycle notifications remain the fast path for create/name/archive/delete,
// and the periodic safety scan still detects membership or presentation changes.
func remoteCatalogFingerprint(maps []map[string]interface{}) string {
	var b strings.Builder
	for index, item := range maps {
		id, _ := item["id"].(string)
		title, _ := item["title"].(string)
		dir := sessionDirectoryKey(item)
		if normalized, ok := normalizeCatalogDirectory(dir); ok {
			dir = normalized
		}
		project, _ := item["projectId"].(string)
		b.WriteString(strconv.Itoa(index))
		b.WriteByte('|')
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(dir)
		b.WriteByte('|')
		b.WriteString(project)
		b.WriteByte('|')
		b.WriteString(title)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// listOrderFingerprint 只覆盖原生顺序 + 成员 id，供 3s head 提示使用。提示的职责是
// 廉价决定「是否跑一次权威全量刷新」：新增/删除/recency 变化都会体现为成员集合或
// id 顺序的变化；而 updatedAt 在流式 turn 中随每个 text_delta 变化，语义指纹会让
// 提示在长任务执行期间每 3s 误触发一次全量刷新（2026-08-23 真机：codex-web 的
// sessions_changed generation 1→108 风暴）。title/directory 变化不影响 head 命中面，
// 由权威全量指纹 + 60s 兜底扫描覆盖。
func listOrderFingerprint(maps []map[string]interface{}) string {
	var b strings.Builder
	for index, item := range maps {
		id, _ := item["id"].(string)
		b.WriteString(strconv.Itoa(index))
		b.WriteByte('|')
		b.WriteString(id)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
