package gobridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

const claudeSourceTraceEnv = "GO_BRIDGE_CLAUDE_SOURCE_TRACE"

type claudeSourceCorrelation struct {
	SegmentStableKey  string
	SegmentGeneration string
}

type claudeSourceCorrelationObservation struct {
	info         os.FileInfo
	cursor       int64
	prefixDigest string
	ordinal      uint64
	correlation  claudeSourceCorrelation
}

// claudeSourceCorrelationTracker assigns a private generation to one physical file incarnation.
// It keeps paths only as in-process map keys. Logs receive opaque hashes, never local paths.
type claudeSourceCorrelationTracker struct {
	mu           sync.Mutex
	observations map[string]claudeSourceCorrelationObservation
}

func newClaudeSourceCorrelationTracker() *claudeSourceCorrelationTracker {
	return &claudeSourceCorrelationTracker{
		observations: make(map[string]claudeSourceCorrelationObservation),
	}
}

func (t *claudeSourceCorrelationTracker) Observe(
	backendID, sessionID, segmentIdentity, path string,
	cursor int64,
) (claudeSourceCorrelation, error) {
	if t == nil || backendID == "" || sessionID == "" || path == "" || cursor < 0 {
		return claudeSourceCorrelation{}, fmt.Errorf("invalid Claude source correlation input")
	}
	file, err := os.Open(path)
	if err != nil {
		return claudeSourceCorrelation{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return claudeSourceCorrelation{}, err
	}
	if info.Size() < cursor {
		return claudeSourceCorrelation{}, fmt.Errorf("Claude source cursor %d exceeds size %d", cursor, info.Size())
	}
	stableIdentity := strings.TrimSpace(segmentIdentity)
	if stableIdentity == "" {
		stableIdentity = sessionID
	}
	stableSum := sha256.Sum256([]byte(backendID + "\x00" + sessionID + "\x00" + stableIdentity))
	stableKey := hex.EncodeToString(stableSum[:])
	observationKey := backendID + "\x00" + sessionID + "\x00" + stableIdentity + "\x00" + path

	t.mu.Lock()
	defer t.mu.Unlock()
	previous, exists := t.observations[observationKey]
	sameGeneration := false
	if exists && os.SameFile(previous.info, info) && previous.cursor <= cursor {
		currentOldPrefix, digestErr := digestClaudeSourcePrefix(file, previous.cursor)
		if digestErr != nil {
			return claudeSourceCorrelation{}, digestErr
		}
		sameGeneration = currentOldPrefix == previous.prefixDigest
	}
	if sameGeneration {
		if cursor > previous.cursor {
			digest, digestErr := digestClaudeSourcePrefix(file, cursor)
			if digestErr != nil {
				return claudeSourceCorrelation{}, digestErr
			}
			previous.cursor = cursor
			previous.prefixDigest = digest
			previous.info = info
			t.observations[observationKey] = previous
		}
		return previous.correlation, nil
	}

	ordinal := uint64(1)
	if exists {
		ordinal = previous.ordinal + 1
	}
	digest, err := digestClaudeSourcePrefix(file, cursor)
	if err != nil {
		return claudeSourceCorrelation{}, err
	}
	generationSum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%d\x00%s", stableKey, ordinal, digest,
	)))
	correlation := claudeSourceCorrelation{
		SegmentStableKey:  stableKey,
		SegmentGeneration: hex.EncodeToString(generationSum[:]),
	}
	t.observations[observationKey] = claudeSourceCorrelationObservation{
		info: info, cursor: cursor, prefixDigest: digest, ordinal: ordinal, correlation: correlation,
	}
	return correlation, nil
}

func digestClaudeSourcePrefix(file *os.File, cursor int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if cursor > 0 {
		if _, err := io.CopyN(hash, file, cursor); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type claudeSourceTraceRecord struct {
	Phase             string
	IngestDomain      string
	BackendID         string
	SessionID         string
	Correlation       claudeSourceCorrelation
	Record            claudeRelayScannedRecord
	FileOrderTurnID   string
	ProjectionEvents  int
	Transition        string
	SourceStateRev    uint64
	ProjectionTurnID  string
	ProjectionPartID  string
	PhysicalRowsAcked int
	LogicalChanged    int
	PublicDelivered   int
}

func claudeSourceTraceEnabled() bool {
	return os.Getenv(claudeSourceTraceEnv) == "1"
}

func emitClaudeSourceTrace(trace claudeSourceTraceRecord) {
	if !claudeSourceTraceEnabled() {
		return
	}
	role := ""
	messageID := ""
	if trace.Record.Entry.Message != nil {
		role = trace.Record.Entry.Message.Role
		messageID = trace.Record.Entry.Message.ID
	}
	slog.Info(
		"go-bridge: claude_source_trace",
		"phase", trace.Phase,
		"ingestDomain", trace.IngestDomain,
		"backendID", trace.BackendID,
		"sessionPrefix", projectionSessionLogPrefix(trace.SessionID),
		"segmentStableKey", trace.Correlation.SegmentStableKey,
		"segmentGeneration", trace.Correlation.SegmentGeneration,
		"byteStart", trace.Record.ByteStart,
		"byteEnd", trace.Record.ByteEnd,
		"complete", true,
		"admitted", trace.Record.Admitted,
		"uuid", trace.Record.Entry.UUID,
		"parentUuid", trace.Record.Entry.ParentUUID,
		"type", trace.Record.Entry.Type,
		"subtype", trace.Record.Entry.Subtype,
		"role", role,
		"messageId", messageID,
		"fileOrderTurnId", trace.FileOrderTurnID,
		"projectionEvents", trace.ProjectionEvents,
		"transition", trace.Transition,
		"sourceStateRev", trace.SourceStateRev,
		"projectionTurnId", trace.ProjectionTurnID,
		"projectionPartId", trace.ProjectionPartID,
		"physicalRowsAcknowledged", trace.PhysicalRowsAcked,
		"logicalRecordsChanged", trace.LogicalChanged,
		"publicSubeventsDelivered", trace.PublicDelivered,
	)
}

func claudeProjectionTraceIdentity(events []projectionHydrateEvent) (string, string) {
	for _, event := range events {
		turnID, _ := event.Data["turnId"].(string)
		partID, _ := event.Data["itemId"].(string)
		if turnID != "" || partID != "" {
			return turnID, partID
		}
	}
	return "", ""
}

func (h *Handlers) traceClaudeHydrateRange(
	ctx context.Context,
	backendID, sessionID, segmentIdentity, path string,
	startOffset, endOffset int64,
	currentTurnID *string,
) error {
	if !claudeSourceTraceEnabled() || startOffset == endOffset {
		return nil
	}
	if currentTurnID == nil {
		currentTurnID = new(string)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return err
	}
	reader := io.Reader(file)
	if endOffset >= 0 {
		if endOffset < startOffset {
			return fmt.Errorf("invalid Claude trace range [%d,%d)", startOffset, endOffset)
		}
		reader = io.LimitReader(file, endOffset-startOffset)
	}
	correlation, err := h.claudeSourceCorrelation.Observe(
		backendID, sessionID, segmentIdentity, path, endOffset,
	)
	if err != nil {
		return err
	}
	state := claudeRelayScanState{}
	scan, err := scanCompleteClaudeRelayEntriesFromReader(reader, startOffset, &state)
	if err != nil {
		return err
	}
	if scan.Poison != nil {
		return fmt.Errorf("Claude trace source poison at [%d,%d)", scan.Poison.ByteStart, scan.Poison.ByteEnd)
	}
	for _, record := range scan.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		events := []projectionHydrateEvent(nil)
		transition := "ignored_source_only"
		if record.Admitted {
			events = claudeEntryToProjectionEvents(record.Entry, currentTurnID, nil)
			transition = "trace_only_not_kernel_joined"
		}
		projectionTurnID, projectionPartID := claudeProjectionTraceIdentity(events)
		emitClaudeSourceTrace(claudeSourceTraceRecord{
			Phase: "hydrate", IngestDomain: "baseline", BackendID: backendID,
			SessionID: sessionID, Correlation: correlation, Record: record,
			FileOrderTurnID: *currentTurnID, ProjectionEvents: len(events),
			Transition: transition, ProjectionTurnID: projectionTurnID,
			ProjectionPartID: projectionPartID,
		})
	}
	return nil
}
