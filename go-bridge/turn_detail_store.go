package gobridge

// Mac-side detail store (§11.8 "detail store 事务模型", frozen F1.1 2026-08-30;
// F2.1 revision 2026-08-31: ordered-entry model, slim tool cards, strict
// recovery). Owns persistence for turn-detail content: manifest (summary +
// resume), an append-only items transaction log, blob files, and the global
// cache budget. The batch engine (F4) maps upstream pages into ORDERED
// entries; this store atomically accepts them under the FROZEN commit order:
//
//   1. blob temp write → fsync → rename (into blobs/);
//   2. items transaction record append → fsync (items.log);
//   3. manifest temp write → fsync → rename  ← COMMIT POINT (resume state
//      only ever advances past steps 1-2);
//   4. startup sweep (SweepUncommitted): only a LEGAL uncommitted suffix
//      (tx > TxApplied, or one torn FINAL line) is rolled back; ANY defect in
//      the committed range 1..TxApplied quarantines the whole turn dir —
//      the turn re-hydrates from official pagination, never "repaired".
//
// Path safety (F1.1 P1-5): disk segments are ALWAYS hex(sha256(rawID)) — raw
// session/turn IDs never appear in paths; the manifest keeps raw IDs for
// audit. Blob handles additionally bind generation + content hash (F2.1):
// blobs are immutable; a stale handle can never overwrite a newer blob.
// Budget (P1-6): core.TurnDetailCacheBudgetBytes covers the WHOLE store;
// eviction granularity is a whole per-turn directory (LRU by manifest
// UpdatedAtMs).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var (
	ErrDetailStoreNotFound  = errors.New("detail-store: turn detail not found")
	ErrDetailStoreCorrupt   = errors.New("detail-store: turn detail cache corrupt")
	ErrDetailDuplicateItem  = errors.New("detail-store: duplicate canonical item id")
	ErrDetailPageOrder      = errors.New("detail-store: page out of order")
	ErrDetailGeneration     = errors.New("detail-store: turn generation mismatch")
	ErrDetailBlobMissing    = errors.New("detail-store: blob file missing")
	ErrDetailBlobUnref      = errors.New("detail-store: handle not referenced by manifest")
	ErrDetailInlineOversize = errors.New("detail-store: item exceeds blob threshold; stage as oversize")
	ErrDetailChunkIndex     = errors.New("detail-store: chunk index out of range")
	ErrDetailBadID          = errors.New("detail-store: unsafe id segment")
)

// turnDetailMappingVersion fences persisted ProjectionPart semantics. Version 1
// is the first cache generation that preserves the official agentMessage phase
// (commentary -> progress, final_answer -> final). Manifests written before the
// field existed decode as zero and are rebuilt from official pagination instead
// of replaying incorrectly classified text forever after a runtime upgrade.
const turnDetailMappingVersion = 1

// safeBackendSeg: the only raw-ish segment allowed in paths (backend ids come
// from internal config; handles are store-derived hex — both still validated;
// session/turn segments are always sha256 hex).
var safeBackendSeg = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func hashSeg(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TurnDetailItemSummary is one accepted entry's manifest row, in official
// page order. Oversize rows carry the persisted blob offset table so chunk
// reads never rescan content (F2.1 P1-3).
type TurnDetailItemSummary struct {
	ItemID      string `json:"itemId"`
	Type        string `json:"type"`
	Bytes       int64  `json:"bytes"`
	Page        int    `json:"page"`
	BlobHandle  string `json:"blobHandle,omitempty"`
	BlobOffsets []int  `json:"blobOffsets,omitempty"`
}

// TurnDetailResume is the owner-frozen resume state, advanced only at the
// manifest commit point: upstream next cursor, accepted boundary (count +
// LAST entry's canonical item id — official page order), page count, EOF.
type TurnDetailResume struct {
	NextCursor         string `json:"nextCursor"`
	LastAcceptedItemID string `json:"lastAcceptedItemId"`
	AcceptedCount      int    `json:"acceptedCount"`
	Pages              int    `json:"pages"`
	EOF                bool   `json:"eof"`
}

// TurnDetailManifest is the per-turn persisted summary + resume state. The
// authoritative detailLoadState lives in the kernel; State/ReasonCode here
// mirror the last committed terminal state for restart fast-path decisions.
type TurnDetailManifest struct {
	MappingVersion int                     `json:"mappingVersion"`
	BackendID      string                  `json:"backendId"`
	SessionID      string                  `json:"sessionId"` // raw, audit only (path is hashed)
	TurnID         string                  `json:"turnId"`    // raw, audit only
	Generation     int                     `json:"generation"`
	ManifestRev    int                     `json:"manifestRev"`
	TxApplied      int                     `json:"txApplied"`
	ChunkSeqNext   int                     `json:"chunkSeqNext"`
	State          string                  `json:"state,omitempty"`
	ReasonCode     string                  `json:"reasonCode,omitempty"`
	ItemCount      int                     `json:"itemCount"`
	TotalBytes     int64                   `json:"totalBytes"`
	Items          []TurnDetailItemSummary `json:"items"`
	Resume         TurnDetailResume        `json:"resume"`
	UpdatedAtMs    int64                   `json:"updatedAtMs"`
}

// detailStoredEntry is one ordered entry of a committed page — the complete
// content needed to REBUILD its chunk frames deterministically (same
// splitter, same chunkSeq range) without upstream.
type detailStoredEntry struct {
	ItemID      string                 `json:"itemId"`
	Part        ProjectionPart         `json:"part"` // slim tool card for oversize entries
	Ref         *TurnDetailOversizeRef `json:"ref,omitempty"`
	BlobOffsets []int                  `json:"blobOffsets,omitempty"`
}

// detailTxRecord is one line of items.log.
type detailTxRecord struct {
	Tx            int                 `json:"tx"`
	Page          int                 `json:"page"`
	NextCursor    string              `json:"nextCursor"`
	EOF           bool                `json:"eof"`
	ChunkSeqFirst int                 `json:"chunkSeqFirst"`
	ChunkSeqLast  int                 `json:"chunkSeqLast"`
	Entries       []detailStoredEntry `json:"entries,omitempty"`
}

// DetailOversizeStaged is one oversize entry (>256KB serialized output)
// handed to the store: the SLIM tool card (command/cwd/status/exitCode/
// duration/title intact, huge output stripped — F2.1 P0-2) plus the full
// blob content. iOS renders the full card in position; only the output is a
// lazy load.
type DetailOversizeStaged struct {
	Part    ProjectionPart // slim card; ItemID/Type must be set
	Preview string         // store truncates to the frozen preview budget, rune-aligned
	Content string         // full output; persisted only into blobs/<handle>.bin
}

// DetailPageEntry is ONE item of an accepted page in OFFICIAL upstream order
// (F2.1 P0-1). Exactly one of Inline/Oversize is set.
type DetailPageEntry struct {
	ItemID   string
	Inline   *ProjectionPart       // pure inline item (≤ blob threshold)
	Oversize *DetailOversizeStaged // slim card + blob content
}

// DetailPageAccept is one successfully fetched upstream page, already mapped
// and classified by the batch engine (F4) into ordered entries.
type DetailPageAccept struct {
	BackendID  string
	SessionID  string
	TurnID     string
	Generation int
	Page       int // 1-based; must be Resume.Pages+1
	NextCursor string
	EOF        bool
	Entries    []DetailPageEntry // official page order
}

// DetailAcceptedPage returns everything the batch engine needs after a
// committed accept: the new manifest revision, the chunkSeq range the page
// occupies (both 0 when the page packed into zero chunks), and the manifest
// snapshot for the kernel manifest op.
type DetailAcceptedPage struct {
	ManifestRev   int
	ChunkSeqFirst int
	ChunkSeqLast  int
	Manifest      *TurnDetailManifest
}

// blobChunkTouchMinInterval throttles LRU manifest rewrites on chunk reads
// (F2.1 P1-3: 9 chunk reads must not mean 9 manifest rewrites).
const blobChunkTouchMinInterval = 10 * time.Second

// TurnDetailStore is the process-wide detail cache. One mutex serializes
// accepts/sweeps/evictions — page accepts are ~500ms apart per turn, and
// blob chunk reads hold the lock only for the manifest lookup (file reads
// happen outside it).
type TurnDetailStore struct {
	root   string
	budget int64
	mu     sync.Mutex
	now    func() time.Time
}

func NewTurnDetailStore(root string) *TurnDetailStore {
	return &TurnDetailStore{
		root:   root,
		budget: core.TurnDetailCacheBudgetBytes,
		now:    time.Now,
	}
}

// SetBudget overrides the cache budget (tests only; production uses the
// core const).
func (s *TurnDetailStore) SetBudget(bytes int64) { s.mu.Lock(); s.budget = bytes; s.mu.Unlock() }

func (s *TurnDetailStore) turnDir(backendID, sessionID, turnID string) (string, error) {
	if !safeBackendSeg.MatchString(backendID) || sessionID == "" || turnID == "" {
		return "", ErrDetailBadID
	}
	return filepath.Join(s.root, backendID, hashSeg(sessionID), hashSeg(turnID)), nil
}

func syncDir(path string) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	_ = dir.Sync()
	_ = dir.Close()
}

// writeFileSynced is the crash-safe replace primitive: temp write → fsync
// (file) → rename → fsync (dir).
func writeFileSynced(final string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(final), "tmp", filepath.Base(final)+".tmp")
	if err := os.MkdirAll(filepath.Join(filepath.Dir(final), "tmp"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	syncDir(final)
	return nil
}

// DropTurn removes the whole per-turn cache dir. The batch engine calls it on
// generation rotation (§11.8): a store manifest from a SUPERSEDED generation
// can never accept the new generation's pages, and the new truth rebuilds the
// cache from official pagination. Missing dir is a no-op.
func (s *TurnDetailStore) DropTurn(backendID, sessionID, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.turnDir(backendID, sessionID, turnID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *TurnDetailStore) loadManifestLocked(dir string) (*TurnDetailManifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrDetailStoreNotFound
		}
		return nil, err
	}
	var m TurnDetailManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrDetailStoreCorrupt, err)
	}
	return &m, nil
}

func (s *TurnDetailStore) persistManifestLocked(dir string, m *TurnDetailManifest) error {
	raw, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return writeFileSynced(filepath.Join(dir, "manifest.json"), raw)
}

// LoadManifest reads the committed manifest for a turn.
func (s *TurnDetailStore) LoadManifest(backendID, sessionID, turnID string) (*TurnDetailManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.turnDir(backendID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	return s.loadManifestLocked(dir)
}

func partSize(p ProjectionPart) int64 {
	raw, err := json.Marshal(p)
	if err != nil {
		return 1 << 30
	}
	return int64(len(raw))
}

func truncateRuneAligned(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:alignBackToRuneStart(s, limit)]
}

// blobHandle derives the immutable blob handle: generation + content hash
// are part of the identity (F2.1 P1-5) so a re-accepted item under a new
// generation or with changed content can never overwrite an older blob and
// a stale handle simply stops resolving.
func blobHandle(backendID, sessionID, turnID string, generation int, itemID, content string) string {
	return hashSeg(backendID + "|" + sessionID + "|" + turnID + "|" +
		strconv.Itoa(generation) + "|" + itemID + "|" + hashSeg(content))
}

// AcceptPage runs the frozen-commit-order transaction for one page and
// returns the committed result. All validation happens BEFORE any write; on
// error nothing reached disk.
func (s *TurnDetailStore) AcceptPage(acc DetailPageAccept) (*DetailAcceptedPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.turnDir(acc.BackendID, acc.SessionID, acc.TurnID)
	if err != nil {
		return nil, err
	}
	manifest, err := s.loadManifestLocked(dir)
	if err != nil {
		if !errors.Is(err, ErrDetailStoreNotFound) {
			return nil, err
		}
		manifest = &TurnDetailManifest{
			MappingVersion: turnDetailMappingVersion,
			BackendID:      acc.BackendID, SessionID: acc.SessionID, TurnID: acc.TurnID,
			Generation: acc.Generation, ChunkSeqNext: 1,
			UpdatedAtMs: s.now().UnixMilli(),
		}
	}
	if manifest.MappingVersion != turnDetailMappingVersion {
		return nil, fmt.Errorf("%w: mapping version %d != runtime %d",
			ErrDetailStoreCorrupt, manifest.MappingVersion, turnDetailMappingVersion)
	}
	if manifest.Generation != acc.Generation {
		return nil, fmt.Errorf("%w: manifest %d != accept %d", ErrDetailGeneration, manifest.Generation, acc.Generation)
	}
	if acc.Page != manifest.Resume.Pages+1 {
		return nil, fmt.Errorf("%w: accept %d, manifest at %d", ErrDetailPageOrder, acc.Page, manifest.Resume.Pages)
	}

	// Single ordered validation + classification pass (F2.1 P0-1/P0-2).
	known := make(map[string]bool, len(manifest.Items))
	for _, item := range manifest.Items {
		known[item.ItemID] = true
	}
	threshold := core.TurnDetailBlobThresholdBytes
	stagedContent := make(map[string]string, len(acc.Entries))
	stored := make([]detailStoredEntry, 0, len(acc.Entries))
	packEntries := make([]DetailChunkEntry, 0, len(acc.Entries))
	var pageBytes int64
	lastItemID := manifest.Resume.LastAcceptedItemID
	for i, entry := range acc.Entries {
		inlineSet := entry.Inline != nil
		oversizeSet := entry.Oversize != nil
		if inlineSet == oversizeSet || entry.ItemID == "" {
			return nil, fmt.Errorf("%w: entry[%d] must set exactly one of Inline/Oversize with an id", ErrDetailBadID, i)
		}
		if known[entry.ItemID] {
			return nil, fmt.Errorf("%w: %s", ErrDetailDuplicateItem, entry.ItemID)
		}
		known[entry.ItemID] = true
		lastItemID = entry.ItemID
		if inlineSet {
			if entry.Inline.ItemID != entry.ItemID {
				return nil, fmt.Errorf("%w: entry[%d] id %q != part id %q", ErrDetailBadID, i, entry.ItemID, entry.Inline.ItemID)
			}
			if sz := partSize(*entry.Inline); sz > threshold {
				return nil, fmt.Errorf("%w: %s is %d bytes", ErrDetailInlineOversize, entry.ItemID, sz)
			} else {
				pageBytes += sz
			}
			stored = append(stored, detailStoredEntry{ItemID: entry.ItemID, Part: *entry.Inline})
			packEntries = append(packEntries, DetailChunkEntry{Part: *entry.Inline})
			continue
		}
		card := entry.Oversize.Part
		if card.ItemID != entry.ItemID || !utf8.ValidString(entry.Oversize.Content) {
			return nil, fmt.Errorf("%w: oversize[%d] card id / content", ErrDetailBadID, i)
		}
		if sz := partSize(card); sz > threshold {
			return nil, fmt.Errorf("%w: oversize[%d] slim card is %d bytes — strip the output", ErrDetailInlineOversize, i, sz)
		}
		content := entry.Oversize.Content
		offsets := DetailChunkOffsets(content)
		handle := blobHandle(acc.BackendID, acc.SessionID, acc.TurnID, acc.Generation, entry.ItemID, content)
		ref := TurnDetailOversizeRef{
			ItemID:      entry.ItemID,
			Handle:      handle,
			Type:        card.Type,
			TotalBytes:  int64(len(content)),
			Preview:     truncateRuneAligned(entry.Oversize.Preview, core.TurnDetailBlobPreviewBytes),
			TotalChunks: len(offsets) - 1,
		}
		pageBytes += ref.TotalBytes
		stagedContent[entry.ItemID] = content
		stored = append(stored, detailStoredEntry{
			ItemID: entry.ItemID, Part: card, Ref: &ref, BlobOffsets: offsets,
		})
		packEntries = append(packEntries, DetailChunkEntry{Part: card, Ref: &ref})
	}

	chunks, err := SplitDetailChunks(packEntries)
	if err != nil {
		return nil, err
	}
	chunkSeqFirst, chunkSeqLast := 0, 0
	if len(chunks) > 0 {
		chunkSeqFirst = manifest.ChunkSeqNext
		chunkSeqLast = chunkSeqFirst + len(chunks) - 1
	}
	tx := manifest.TxApplied + 1

	// Step 1: blobs — temp write, fsync, rename (immutable handles: a redo
	// of the same page writes byte-identical content under the same name).
	for _, entry := range stored {
		if entry.Ref == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
			return nil, err
		}
		final := filepath.Join(dir, "blobs", entry.Ref.Handle+".bin")
		if err := writeFileSynced(final, []byte(stagedContent[entry.ItemID])); err != nil {
			return nil, err
		}
	}

	// Step 2: items transaction record — append, fsync.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	record := detailTxRecord{
		Tx: tx, Page: acc.Page, NextCursor: acc.NextCursor, EOF: acc.EOF,
		ChunkSeqFirst: chunkSeqFirst, ChunkSeqLast: chunkSeqLast,
		Entries: stored,
	}
	line, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	logF, err := os.OpenFile(filepath.Join(dir, "items.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := logF.Write(append(line, '\n')); err != nil {
		logF.Close()
		return nil, err
	}
	if err := logF.Sync(); err != nil {
		logF.Close()
		return nil, err
	}
	if err := logF.Close(); err != nil {
		return nil, err
	}

	// Step 3: manifest — temp write, fsync, rename (COMMIT POINT).
	for _, entry := range stored {
		summary := TurnDetailItemSummary{
			ItemID: entry.ItemID, Type: entry.Part.Type, Bytes: partSize(entry.Part), Page: acc.Page,
		}
		if entry.Ref != nil {
			summary.Bytes = entry.Ref.TotalBytes
			summary.BlobHandle = entry.Ref.Handle
			summary.BlobOffsets = entry.BlobOffsets
		}
		manifest.Items = append(manifest.Items, summary)
	}
	manifest.ManifestRev++
	manifest.TxApplied = tx
	if len(chunks) > 0 {
		manifest.ChunkSeqNext = chunkSeqLast + 1
	}
	manifest.ItemCount += len(acc.Entries)
	manifest.TotalBytes += pageBytes
	manifest.Resume = TurnDetailResume{
		NextCursor: acc.NextCursor, LastAcceptedItemID: lastItemID,
		AcceptedCount: manifest.Resume.AcceptedCount + len(acc.Entries),
		Pages:         acc.Page, EOF: acc.EOF,
	}
	manifest.UpdatedAtMs = s.now().UnixMilli()
	if err := s.persistManifestLocked(dir, manifest); err != nil {
		return nil, err
	}

	// Budget enforcement is best-effort: the page is COMMITTED — failing the
	// accept here would mislead the caller into retrying a committed page.
	// Eviction errors surface in logs instead of being swallowed (F2.1 P1).
	if err := s.enforceBudgetLocked(dir); err != nil {
		slog.Warn("detail-store: budget enforcement failed after commit", "dir", dir, "err", err)
	}

	return &DetailAcceptedPage{
		ManifestRev:   manifest.ManifestRev,
		ChunkSeqFirst: chunkSeqFirst,
		ChunkSeqLast:  chunkSeqLast,
		Manifest:      manifest,
	}, nil
}

// validateCommittedRecords enforces the fail-closed recovery invariants on
// the COMMITTED range (F2.1 P0-3 / P1-4): tx 1..TxApplied contiguous and
// complete, page continuity from 1, chunkSeq continuity from 1, and the
// deterministic re-split reproduces each record's recorded span.
func validateCommittedRecords(manifest *TurnDetailManifest, records []detailTxRecord) error {
	if len(records) != manifest.TxApplied {
		return fmt.Errorf("%w: %d committed records, manifest TxApplied=%d",
			ErrDetailStoreCorrupt, len(records), manifest.TxApplied)
	}
	lastSeq := 0
	for i, rec := range records {
		if rec.Tx != i+1 {
			return fmt.Errorf("%w: record[%d].tx=%d, want %d", ErrDetailStoreCorrupt, i, rec.Tx, i+1)
		}
		if rec.Page != i+1 {
			return fmt.Errorf("%w: record[%d].page=%d, want %d", ErrDetailStoreCorrupt, i, rec.Page, i+1)
		}
		span := 0
		if len(rec.Entries) > 0 {
			entries := make([]DetailChunkEntry, 0, len(rec.Entries))
			for _, entry := range rec.Entries {
				entries = append(entries, DetailChunkEntry{Part: entry.Part, Ref: entry.Ref})
			}
			chunks, err := SplitDetailChunks(entries)
			if err != nil {
				return fmt.Errorf("%w: record[%d] re-split: %v", ErrDetailStoreCorrupt, i, err)
			}
			span = len(chunks)
		}
		if span == 0 {
			if rec.ChunkSeqFirst != 0 || rec.ChunkSeqLast != 0 {
				return fmt.Errorf("%w: record[%d] empty page must record 0-0", ErrDetailStoreCorrupt, i)
			}
		} else {
			if rec.ChunkSeqFirst != lastSeq+1 || rec.ChunkSeqLast != rec.ChunkSeqFirst+span-1 {
				return fmt.Errorf("%w: record[%d] chunkSeq %d-%d, want %d-%d",
					ErrDetailStoreCorrupt, i, rec.ChunkSeqFirst, rec.ChunkSeqLast, lastSeq+1, lastSeq+span)
			}
			lastSeq = rec.ChunkSeqLast
		}
	}
	if lastSeq != manifest.ChunkSeqNext-1 {
		return fmt.Errorf("%w: chunkSeq ends at %d, manifest ChunkSeqNext=%d",
			ErrDetailStoreCorrupt, lastSeq, manifest.ChunkSeqNext)
	}
	return nil
}

// ReadRecords returns the COMMITTED transaction records in accept order —
// the deterministic replay source — after fail-closed validation (F2.1 P1-4).
// A parseable uncommitted suffix (tx > TxApplied) is tolerated and skipped
// here; any unparseable line is NOT (post-sweep runtime state must be clean)
// — both fail closed.
func (s *TurnDetailStore) ReadRecords(backendID, sessionID, turnID string) ([]detailTxRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.turnDir(backendID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	manifest, err := s.loadManifestLocked(dir)
	if err != nil {
		return nil, err
	}
	records, err := readLogRecords(dir)
	if err != nil {
		return nil, err
	}
	if records.unparsed != 0 {
		return nil, fmt.Errorf("%w: items.log carries %d unparseable line(s)", ErrDetailStoreCorrupt, records.unparsed)
	}
	committed := make([]detailTxRecord, 0, len(records.committed))
	for _, rec := range records.committed {
		if rec.Tx <= manifest.TxApplied {
			committed = append(committed, rec)
		}
	}
	if err := validateCommittedRecords(manifest, committed); err != nil {
		return nil, err
	}
	return committed, nil
}

type logRecords struct {
	committed []detailTxRecord // parseable records, file order
	unparsed  int              // count of unparseable lines
	tornAtEnd bool             // the single unparseable line is the FINAL line
}

// readLogRecords parses items.log and classifies lines. A torn append can
// only ever damage the LAST line — an unparseable line anywhere else (or
// more than one) is corruption, and the caller must quarantine, not roll
// back (F2.1 P0-3).
func readLogRecords(dir string) (*logRecords, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "items.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return &logRecords{}, nil
		}
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	lastNonEmpty, unparsedIdx := -1, -1
	out := &logRecords{}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lastNonEmpty = i
		var rec detailTxRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			out.unparsed++
			unparsedIdx = i
			continue
		}
		out.committed = append(out.committed, rec)
	}
	out.tornAtEnd = out.unparsed == 1 && unparsedIdx == lastNonEmpty
	return out, nil
}

// ReadBlobChunk serves one chunk of a manifest-referenced blob, cut at the
// offsets PERSISTED at accept time (F2.1 P1-3: ReadAt the target range, no
// full-file read, no content rescan; the store mutex covers only the
// manifest lookup; the LRU touch is throttled). Binding (generation/
// manifestRev/itemId/handle) is the caller's job against LoadManifest.
func (s *TurnDetailStore) ReadBlobChunk(backendID, sessionID, turnID, handle string, chunkIndex int) (string, int, int64, error) {
	s.mu.Lock()
	dir, err := s.turnDir(backendID, sessionID, turnID)
	if err != nil {
		s.mu.Unlock()
		return "", 0, 0, err
	}
	manifest, err := s.loadManifestLocked(dir)
	if err != nil {
		s.mu.Unlock()
		return "", 0, 0, err
	}
	var offsets []int
	var totalBytes int64
	for _, item := range manifest.Items {
		if item.BlobHandle == handle {
			offsets = item.BlobOffsets
			totalBytes = item.Bytes
			break
		}
	}
	if offsets == nil {
		s.mu.Unlock()
		return "", 0, 0, fmt.Errorf("%w: %s", ErrDetailBlobUnref, handle)
	}
	// Throttled LRU touch: 9 chunk reads ≈ 1 manifest rewrite, not 9.
	if now := s.now(); now.UnixMilli()-manifest.UpdatedAtMs >= blobChunkTouchMinInterval.Milliseconds() {
		manifest.UpdatedAtMs = now.UnixMilli()
		if err := s.persistManifestLocked(dir, manifest); err != nil {
			s.mu.Unlock()
			return "", 0, 0, err
		}
	}
	s.mu.Unlock()

	if chunkIndex < 0 || chunkIndex >= len(offsets)-1 {
		return "", 0, 0, fmt.Errorf("%w: %d of %d", ErrDetailChunkIndex, chunkIndex, len(offsets)-1)
	}
	f, err := os.Open(filepath.Join(dir, "blobs", handle+".bin"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, 0, fmt.Errorf("%w: %s", ErrDetailBlobMissing, handle)
		}
		return "", 0, 0, err
	}
	defer f.Close()
	buf := make([]byte, offsets[chunkIndex+1]-offsets[chunkIndex])
	if _, err := f.ReadAt(buf, int64(offsets[chunkIndex])); err != nil {
		return "", 0, 0, err
	}
	return string(buf), len(offsets) - 1, totalBytes, nil
}

// UpdateState mirrors a terminal kernel state into the store manifest (no
// progress revision bump) so restarts can fast-path without upstream.
func (s *TurnDetailStore) UpdateState(backendID, sessionID, turnID string, generation int, state, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.turnDir(backendID, sessionID, turnID)
	if err != nil {
		return err
	}
	manifest, err := s.loadManifestLocked(dir)
	if err != nil {
		return err
	}
	if manifest.Generation != generation {
		return fmt.Errorf("%w: manifest %d != update %d", ErrDetailGeneration, manifest.Generation, generation)
	}
	manifest.State = state
	manifest.ReasonCode = reason
	manifest.UpdatedAtMs = s.now().UnixMilli()
	return s.persistManifestLocked(dir, manifest)
}

// SweepUncommitted is the startup recovery pass (frozen step 4, strict F2.1
// P0-3):
//
//   - a turn dir WITHOUT a manifest is uncommitted garbage — removed whole
//     (a crash after blob rename but before items.log/manifest leaves such
//     dirs; nothing is committed without the manifest rename);
//   - within the committed range 1..TxApplied, ANY defect (missing/
//     duplicate/corrupt record, page or chunkSeq discontinuity) quarantines
//     the whole dir — the turn re-hydrates from official pagination;
//   - only a LEGAL uncommitted suffix is rolled back: parseable records with
//     tx > TxApplied, plus AT MOST ONE unparseable line, and only as the
//     FINAL line (a torn append cannot produce anything after it);
//   - the log rewrite itself is crash-safe (temp → fsync → rename → dir
//     fsync); blob/sync errors propagate, never swallowed.
func (s *TurnDetailStore) SweepUncommitted() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Walk(s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil || strings.Count(rel, string(filepath.Separator)) != 2 {
			return nil // only <backend>/<sessHash>/<turnHash> dirs qualify
		}
		manifest, mErr := s.loadManifestLocked(path)
		if mErr != nil {
			if errors.Is(mErr, ErrDetailStoreNotFound) {
				// Unconditional: no manifest ⇒ nothing committed ⇒ garbage.
				return os.RemoveAll(path)
			}
			return mErr
		}
		records, err := readLogRecords(path)
		if err != nil {
			return err
		}
		// A torn append damages only the FINAL line. Anything else unparseable
		// — more than one bad line, or a bad line with content after it — is
		// corruption of the log's structure: quarantine, never roll back.
		if records.unparsed > 1 || (records.unparsed == 1 && !records.tornAtEnd) {
			slog.Warn("detail-store: quarantining turn cache (unparseable line is not a final torn tail)",
				"dir", path, "unparsed", records.unparsed, "tornAtEnd", records.tornAtEnd)
			return os.RemoveAll(path)
		}
		kept := make([]detailTxRecord, 0, len(records.committed))
		suffix := 0
		for _, rec := range records.committed {
			if rec.Tx <= manifest.TxApplied {
				kept = append(kept, rec)
			} else {
				suffix++
			}
		}
		if err := validateCommittedRecords(manifest, kept); err != nil {
			// Committed range defective: quarantine whole dir (re-hydrate later).
			slog.Warn("detail-store: quarantining corrupt turn cache", "dir", path, "err", err)
			return os.RemoveAll(path)
		}
		if suffix > 0 || records.unparsed == 1 {
			slog.Info("detail-store: rolling back uncommitted suffix", "dir", path,
				"records", suffix, "tornTail", records.tornAtEnd)
			var buf strings.Builder
			for _, rec := range kept {
				line, err := json.Marshal(rec)
				if err != nil {
					return err
				}
				buf.Write(line)
				buf.WriteByte('\n')
			}
			if err := writeFileSynced(filepath.Join(path, "items.log"), []byte(buf.String())); err != nil {
				return err
			}
		}
		return s.sweepOrphanBlobs(path, kept)
	})
}

// sweepOrphanBlobs deletes blobs referenced by no committed record (step-1
// leftovers of rolled-back transactions). Errors propagate.
func (s *TurnDetailStore) sweepOrphanBlobs(dir string, committed []detailTxRecord) error {
	committedHandles := make(map[string]bool)
	for _, rec := range committed {
		for _, entry := range rec.Entries {
			if entry.Ref != nil {
				committedHandles[entry.Ref.Handle] = true
			}
		}
	}
	blobDir := filepath.Join(dir, "blobs")
	entries, err := os.ReadDir(blobDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		handle := strings.TrimSuffix(entry.Name(), ".bin")
		if committedHandles[handle] {
			continue
		}
		if err := os.Remove(filepath.Join(blobDir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// enforceBudgetLocked evicts oldest whole turn dirs (LRU by manifest
// UpdatedAtMs; manifest-less dirs count as oldest) until the store fits the
// budget. The dir named by keep (the just-committed turn) is never evicted.
func (s *TurnDetailStore) enforceBudgetLocked(keep string) error {
	type turnDir struct {
		path    string
		size    int64
		updated int64
	}
	var dirs []turnDir
	backendEntries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, backend := range backendEntries {
		if !backend.IsDir() {
			continue
		}
		sessEntries, err := os.ReadDir(filepath.Join(s.root, backend.Name()))
		if err != nil {
			continue
		}
		for _, sess := range sessEntries {
			if !sess.IsDir() {
				continue
			}
			turnEntries, err := os.ReadDir(filepath.Join(s.root, backend.Name(), sess.Name()))
			if err != nil {
				continue
			}
			for _, turn := range turnEntries {
				if !turn.IsDir() {
					continue
				}
				path := filepath.Join(s.root, backend.Name(), sess.Name(), turn.Name())
				if path == keep {
					continue
				}
				size := dirSize(path)
				var updated int64
				if manifest, err := s.loadManifestLocked(path); err == nil {
					updated = manifest.UpdatedAtMs
				}
				dirs = append(dirs, turnDir{path: path, size: size, updated: updated})
			}
		}
	}
	total := int64(0)
	for _, d := range dirs {
		total += d.size
	}
	if keep != "" {
		total += dirSize(keep)
	}
	if total <= s.budget {
		return nil
	}
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].updated != dirs[j].updated {
			return dirs[i].updated < dirs[j].updated
		}
		return dirs[i].path < dirs[j].path
	})
	for _, d := range dirs {
		if total <= s.budget {
			break
		}
		if err := os.RemoveAll(d.path); err != nil {
			return err
		}
		total -= d.size
	}
	return nil
}

// StoreUsage reports the current total size (tests/diagnostics).
func (s *TurnDetailStore) StoreUsage() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return dirSize(s.root)
}
