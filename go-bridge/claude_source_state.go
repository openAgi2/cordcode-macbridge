package gobridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const ClaudeSourceStateSchemaVersion uint32 = 1

type ClaudeSourceCursor struct {
	SegmentStableKey  string `json:"segmentStableKey"`
	SegmentGeneration string `json:"segmentGeneration"`
	RawByteEnd        int64  `json:"rawByteEnd"`
	MembershipDigest  string `json:"membershipDigest"`
}

type ClaudeSourceParserFlags struct {
	SkipNextResumeNoResponse bool `json:"skipNextResumeNoResponse"`
}

type ClaudeGraphOccurrence struct {
	SourceStateRev    uint64 `json:"sourceStateRev"`
	ParentUUID        string `json:"parentUuid,omitempty"`
	StructuralKind    string `json:"structuralKind"`
	GraphResolvedTurn string `json:"graphResolvedTurn,omitempty"`
	SegmentStableKey  string `json:"segmentStableKey"`
	SourceGeneration  string `json:"sourceGeneration"`
	RawByteStart      int64  `json:"rawByteStart"`
	RawByteEnd        int64  `json:"rawByteEnd"`
}

type ClaudeProjectionContribution struct {
	TurnID string `json:"turnId"`
	PartID string `json:"partId"`
}

type ClaudeLogicalRecord struct {
	ContentSequenceHash   string                       `json:"contentSequenceHash"`
	SemanticLifecycleHash string                       `json:"semanticLifecycleHash"`
	BlockOccurrenceIDs    []string                     `json:"blockOccurrenceIds"`
	Contribution          ClaudeProjectionContribution `json:"contribution"`
}

type ClaudeSourceState struct {
	SchemaVersion    uint32                             `json:"schemaVersion"`
	SourceGeneration string                             `json:"sourceGeneration"`
	SourceStateRev   uint64                             `json:"sourceStateRev"`
	CursorVector     []ClaudeSourceCursor               `json:"cursorVector"`
	ParserFlags      ClaudeSourceParserFlags            `json:"parserFlags"`
	GraphNodes       map[string][]ClaudeGraphOccurrence `json:"graphNodes"`
	LogicalRecords   map[string]ClaudeLogicalRecord     `json:"logicalRecords"`
}

type ClaudeSourceRecordTransition struct {
	LogicalRecordUUID string
	ParentUUID        string
	StructuralKind    string
	GraphResolvedTurn string
	SegmentStableKey  string
	SegmentGeneration string
	SourceGeneration  string
	RawByteStart      int64
	RawByteEnd        int64
	ContentBlocks     []json.RawMessage
	SemanticLifecycle json.RawMessage
	Contribution      ClaudeProjectionContribution
	ControlOperation  string
}

type ClaudeSourceTransitionResult string

const (
	ClaudeTransitionAcceptedProjection ClaudeSourceTransitionResult = "accepted_projection"
	ClaudeTransitionAcceptedSourceOnly ClaudeSourceTransitionResult = "accepted_source_only"
)

type ClaudeSourceTransitionProposal struct {
	BaseGeneration        string
	BaseSourceStateRev    uint64
	Next                  ClaudeSourceState
	Result                ClaudeSourceTransitionResult
	NewBlockOccurrenceIDs []string
}

func EncodeClaudeSourceState(state ClaudeSourceState) ([]byte, error) {
	if err := ValidateClaudeSourceState(state); err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func DecodeClaudeSourceState(raw []byte) (ClaudeSourceState, error) {
	var state ClaudeSourceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ClaudeSourceState{}, fmt.Errorf("%w: decode Claude source state: %v", ErrProjectionCheckpointInvalid, err)
	}
	if err := ValidateClaudeSourceState(state); err != nil {
		return ClaudeSourceState{}, err
	}
	return state, nil
}

func ValidateClaudeSourceState(state ClaudeSourceState) error {
	if state.SchemaVersion != ClaudeSourceStateSchemaVersion {
		return fmt.Errorf("%w: unsupported Claude source-state schema %d", ErrProjectionCheckpointInvalid, state.SchemaVersion)
	}
	if state.SourceGeneration == "" || state.GraphNodes == nil || state.LogicalRecords == nil {
		return fmt.Errorf("%w: incomplete Claude source state", ErrProjectionCheckpointInvalid)
	}
	for _, cursor := range state.CursorVector {
		if cursor.SegmentStableKey == "" || cursor.SegmentGeneration == "" ||
			cursor.MembershipDigest == "" || cursor.RawByteEnd < 0 {
			return fmt.Errorf("%w: invalid Claude cursor vector", ErrProjectionCheckpointInvalid)
		}
	}
	for uuid, occurrences := range state.GraphNodes {
		if uuid == "" {
			return fmt.Errorf("%w: empty Claude graph UUID", ErrProjectionCheckpointInvalid)
		}
		var previous uint64
		for index, occurrence := range occurrences {
			if occurrence.SourceGeneration != state.SourceGeneration ||
				occurrence.SegmentStableKey == "" ||
				occurrence.RawByteStart < 0 ||
				occurrence.RawByteEnd <= occurrence.RawByteStart ||
				(index > 0 && occurrence.SourceStateRev <= previous) {
				return fmt.Errorf("%w: invalid Claude graph occurrence", ErrProjectionCheckpointInvalid)
			}
			previous = occurrence.SourceStateRev
		}
	}
	for uuid, record := range state.LogicalRecords {
		if uuid == "" || record.ContentSequenceHash == "" || record.SemanticLifecycleHash == "" ||
			record.Contribution.TurnID == "" || record.Contribution.PartID == "" {
			return fmt.Errorf("%w: invalid Claude logical record", ErrProjectionCheckpointInvalid)
		}
		for _, blockID := range record.BlockOccurrenceIDs {
			if blockID == "" {
				return fmt.Errorf("%w: empty Claude block occurrence ID", ErrProjectionCheckpointInvalid)
			}
		}
	}
	return nil
}

func ProposeClaudeSourceTransition(
	current ClaudeSourceState,
	record ClaudeSourceRecordTransition,
) (ClaudeSourceTransitionProposal, error) {
	if err := ValidateClaudeSourceState(current); err != nil {
		return ClaudeSourceTransitionProposal{}, err
	}
	if record.SourceGeneration != current.SourceGeneration ||
		record.SegmentStableKey == "" || record.SegmentGeneration == "" ||
		record.RawByteStart < 0 || record.RawByteEnd <= record.RawByteStart {
		return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: stale or invalid Claude source transition", ErrProjectionCheckpointInvalid)
	}
	cursorIndex := -1
	for index := range current.CursorVector {
		cursor := current.CursorVector[index]
		if cursor.SegmentStableKey == record.SegmentStableKey {
			if cursor.SegmentGeneration != record.SegmentGeneration || cursor.RawByteEnd != record.RawByteStart {
				return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: Claude source transition gap/generation mismatch", ErrProjectionCheckpointInvalid)
			}
			cursorIndex = index
			break
		}
	}
	if cursorIndex < 0 {
		return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: unknown Claude source segment", ErrProjectionCheckpointInvalid)
	}
	next, err := cloneClaudeSourceState(current)
	if err != nil {
		return ClaudeSourceTransitionProposal{}, err
	}
	next.SourceStateRev++
	next.CursorVector[cursorIndex].RawByteEnd = record.RawByteEnd
	proposal := ClaudeSourceTransitionProposal{
		BaseGeneration:     current.SourceGeneration,
		BaseSourceStateRev: current.SourceStateRev,
		Next:               next,
		Result:             ClaudeTransitionAcceptedSourceOnly,
	}
	if record.LogicalRecordUUID == "" {
		if record.StructuralKind != "queue-operation" && record.StructuralKind != "last-prompt" {
			return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: logical UUID missing", ErrProjectionCheckpointInvalid)
		}
		if record.StructuralKind == "queue-operation" && record.ControlOperation == "" {
			return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: queue operation missing", ErrProjectionCheckpointInvalid)
		}
		return proposal, nil
	}
	occurrence := ClaudeGraphOccurrence{
		SourceStateRev:    next.SourceStateRev,
		ParentUUID:        record.ParentUUID,
		StructuralKind:    record.StructuralKind,
		GraphResolvedTurn: record.GraphResolvedTurn,
		SegmentStableKey:  record.SegmentStableKey,
		SourceGeneration:  record.SourceGeneration,
		RawByteStart:      record.RawByteStart,
		RawByteEnd:        record.RawByteEnd,
	}
	next.GraphNodes[record.LogicalRecordUUID] = append(next.GraphNodes[record.LogicalRecordUUID], occurrence)
	if claudeGraphOnlyStructuralKind(record.StructuralKind) {
		return proposal, nil
	}
	blockIDs := make([]string, 0, len(record.ContentBlocks))
	for ordinal, block := range record.ContentBlocks {
		canonical, err := canonicalClaudeJSON(block)
		if err != nil {
			return ClaudeSourceTransitionProposal{}, err
		}
		contentHash := sha256.Sum256(canonical)
		identityHash := sha256.Sum256([]byte(fmt.Sprintf(
			"%s\x00%d\x00%s",
			record.LogicalRecordUUID, ordinal, hex.EncodeToString(contentHash[:]),
		)))
		blockIDs = append(blockIDs, hex.EncodeToString(identityHash[:]))
	}
	contentSequenceHash := hashClaudeStrings(blockIDs)
	lifecycle, err := canonicalClaudeJSON(record.SemanticLifecycle)
	if err != nil {
		return ClaudeSourceTransitionProposal{}, err
	}
	lifecycleHash := sha256.Sum256(lifecycle)
	semanticLifecycleHash := hex.EncodeToString(lifecycleHash[:])
	existing, seen := next.LogicalRecords[record.LogicalRecordUUID]
	if !seen {
		if record.Contribution.TurnID == "" || record.Contribution.PartID == "" {
			return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: first Claude contribution missing", ErrProjectionCheckpointInvalid)
		}
		next.LogicalRecords[record.LogicalRecordUUID] = ClaudeLogicalRecord{
			ContentSequenceHash:   contentSequenceHash,
			SemanticLifecycleHash: semanticLifecycleHash,
			BlockOccurrenceIDs:    blockIDs,
			Contribution:          record.Contribution,
		}
		proposal.Result = ClaudeTransitionAcceptedProjection
		proposal.NewBlockOccurrenceIDs = append([]string(nil), blockIDs...)
		return proposal, nil
	}
	if existing.ContentSequenceHash == contentSequenceHash &&
		existing.SemanticLifecycleHash == semanticLifecycleHash {
		return proposal, nil
	}
	if !strictClaudePrefix(existing.BlockOccurrenceIDs, blockIDs) {
		return ClaudeSourceTransitionProposal{}, fmt.Errorf("%w: non-monotonic Claude logical record", ErrProjectionCheckpointInvalid)
	}
	existing.ContentSequenceHash = contentSequenceHash
	existing.SemanticLifecycleHash = semanticLifecycleHash
	existing.BlockOccurrenceIDs = append([]string(nil), blockIDs...)
	next.LogicalRecords[record.LogicalRecordUUID] = existing
	proposal.Result = ClaudeTransitionAcceptedProjection
	oldCount := len(current.LogicalRecords[record.LogicalRecordUUID].BlockOccurrenceIDs)
	proposal.NewBlockOccurrenceIDs = append([]string(nil), blockIDs[oldCount:]...)
	return proposal, nil
}

func claudeGraphOnlyStructuralKind(kind string) bool {
	switch kind {
	case "attachment", "system.stop_hook_summary", "system.api_error", "system.informational":
		return true
	default:
		return false
	}
}

func CommitClaudeSourceTransition(
	current *ClaudeSourceState,
	proposal ClaudeSourceTransitionProposal,
) error {
	if current == nil ||
		current.SourceGeneration != proposal.BaseGeneration ||
		current.SourceStateRev != proposal.BaseSourceStateRev {
		return fmt.Errorf("%w: stale Claude source transition CAS", ErrProjectionCheckpointInvalid)
	}
	next, err := cloneClaudeSourceState(proposal.Next)
	if err != nil {
		return err
	}
	*current = next
	return nil
}

// newInitialClaudeSourceState builds the Mac-private source ledger for a freshly committed Claude
// hydrate / relay startup (no checkpoint to restore from): one segment cursor at the admission
// complete-record cut, empty graph/logical maps. Cursor identity matches claudeSourceCorrelation.
// Observe at the same cut so the first live source batch clears the Kernel cursor/gap/generation
// fence. Source identity never leaves the Kernel, never enters a wire payload (guardrails #1/#3/#5).
func newInitialClaudeSourceState(segmentStableKey, segmentGeneration string, admissionCut int64) (ClaudeSourceState, error) {
	if segmentStableKey == "" || segmentGeneration == "" || admissionCut < 0 {
		return ClaudeSourceState{}, fmt.Errorf("%w: invalid initial Claude source state input", ErrProjectionCheckpointInvalid)
	}
	genSum := sha256.Sum256([]byte("claude-source-generation\x00" + segmentStableKey))
	membershipSum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", segmentStableKey, segmentGeneration, admissionCut)))
	return ClaudeSourceState{
		SchemaVersion:    ClaudeSourceStateSchemaVersion,
		SourceGeneration: hex.EncodeToString(genSum[:]),
		CursorVector: []ClaudeSourceCursor{{
			SegmentStableKey:  segmentStableKey,
			SegmentGeneration: segmentGeneration,
			RawByteEnd:        admissionCut,
			MembershipDigest:  hex.EncodeToString(membershipSum[:]),
		}},
		GraphNodes:     map[string][]ClaudeGraphOccurrence{},
		LogicalRecords: map[string]ClaudeLogicalRecord{},
	}, nil
}

func cloneClaudeSourceState(state ClaudeSourceState) (ClaudeSourceState, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return ClaudeSourceState{}, err
	}
	var cloned ClaudeSourceState
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return ClaudeSourceState{}, err
	}
	return cloned, nil
}

func canonicalClaudeJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: invalid Claude semantic JSON", ErrProjectionCheckpointInvalid)
	}
	return json.Marshal(value)
}

func hashClaudeStrings(values []string) string {
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func strictClaudePrefix(oldValues, newValues []string) bool {
	if len(oldValues) >= len(newValues) {
		return false
	}
	for index := range oldValues {
		if oldValues[index] != newValues[index] {
			return false
		}
	}
	return true
}
