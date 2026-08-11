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
