package gobridge

// Mac-side detail store (§11.8 "detail store 事务模型", frozen F1.1 2026-08-30;
// plan §3F-F2). Owns persistence for turn-detail content: manifest (summary +
// resume), an append-only items transaction log, blob files, and the global
// cache budget. The batch engine (F4) maps and classifies upstream pages; this
// store atomically accepts them under the FROZEN commit order:
//
//   1. blob temp write → fsync → rename (into blobs/);
//   2. items transaction record append → fsync (items.log);
//   3. manifest temp write → fsync → rename  ← COMMIT POINT (resume state
//      only ever advances past steps 1-2);
//   4. startup sweep (SweepUncommitted) rolls back records/blobs the manifest
//      never committed.
//
// Path safety (F1.1 P1-5): disk segments are ALWAYS hex(sha256(rawID)) — raw
// session/turn IDs never appear in paths. The manifest keeps raw IDs for
// audit. Budget (P1-6): core.TurnDetailCacheBudgetBytes covers manifests +
// item logs + blobs + temp files of the WHOLE store; eviction granularity is
// a whole per-turn directory (LRU by manifest UpdatedAtMs).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var (
	ErrDetailStoreNotFound  = errors.New("detail-store: turn detail not found")
	ErrDetailDuplicateItem  = errors.New("detail-store: duplicate canonical item id")
	ErrDetailPageOrder      = errors.New("detail-store: page out of order")
	ErrDetailGeneration     = errors.New("detail-store: turn generation mismatch")
	ErrDetailBlobMissing    = errors.New("detail-store: blob file missing")
	ErrDetailBlobUnref      = errors.New("detail-store: handle not referenced by manifest")
	ErrDetailInlineOversize = errors.New("detail-store: inline item exceeds blob threshold; stage as oversize")
	ErrDetailChunkIndex     = errors.New("detail-store: chunk index out of range")
	ErrDetailBadID          = errors.New("detail-store: unsafe id segment")
)

// safeBackendSeg / safeHandleSeg: the only two raw-ish segments allowed in
// paths (backend ids come from internal config, handles are store-derived —
// both still validated; session/turn segments are always sha256 hex).
var (
	safeBackendSeg = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	safeHandleSeg  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

func hashSeg(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TurnDetailItemSummary is one accepted item's manifest entry.
type TurnDetailItemSummary struct {
	ItemID     string `json:"itemId"`
	Type       string `json:"type"`
	Bytes      int64  `json:"bytes"`
	Page       int    `json:"page"`
	BlobHandle string `json:"blobHandle,omitempty"`
}

// TurnDetailResume is the owner-frozen resume state, advanced only at the
// manifest commit point: upstream next cursor, accepted boundary (count +
// last canonical item id), page count, EOF.
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
	BackendID    string                  `json:"backendId"`
	SessionID    string                  `json:"sessionId"` // raw, audit only (path is hashed)
	TurnID       string                  `json:"turnId"`    // raw, audit only
	Generation   int                     `json:"generation"`
	ManifestRev  int                     `json:"manifestRev"`
	TxApplied    int                     `json:"txApplied"`
	ChunkSeqNext int                     `json:"chunkSeqNext"`
	State        string                  `json:"state,omitempty"`
	ReasonCode   string                  `json:"reasonCode,omitempty"`
	ItemCount    int                     `json:"itemCount"`
	TotalBytes   int64                   `json:"totalBytes"`
	Items        []TurnDetailItemSummary `json:"items"`
	Resume       TurnDetailResume        `json:"resume"`
	UpdatedAtMs  int64                   `json:"updatedAtMs"`
}

// detailTxRecord is one line of items.log — the complete content needed to
// REBUILD the accepted page's chunk frames deterministically (same splitter,
// same chunkSeq range) without upstream.
type detailTxRecord struct {
	Tx            int                     `json:"tx"`
	Page          int                     `json:"page"`
	NextCursor    string                  `json:"nextCursor"`
	EOF           bool                    `json:"eof"`
	ChunkSeqFirst int                     `json:"chunkSeqFirst"`
	ChunkSeqLast  int                     `json:"chunkSeqLast"`
	Items         []ProjectionPart        `json:"items,omitempty"`
	Oversize      []TurnDetailOversizeRef `json:"oversize,omitempty"`
}

// DetailOversizeStaged is one oversize item (>256KB serialized) handed to the
// store WITHOUT a handle: the store derives the handle deterministically and
// computes TotalBytes/TotalChunks from the ACTUAL content offset table.
type DetailOversizeStaged struct {
	ItemID  string
	Type    string
	Preview string // store truncates to the frozen preview budget, rune-aligned
	Content string // full content; persisted only into blobs/<handle>.bin
}

// DetailPageAccept is one successfully fetched upstream page, already mapped
// and classified by the batch engine (F4): inline items each ≤ the blob
// threshold, oversize items staged with full content.
type DetailPageAccept struct {
	BackendID  string
	SessionID  string
	TurnID     string
	Generation int
	Page       int // 1-based; must be Resume.Pages+1
	NextCursor string
	EOF        bool
	Items      []ProjectionPart
	Oversize   []DetailOversizeStaged
}

// DetailAcceptedPage returns everything the batch engine needs after a
// committed accept: the new manifest revision, the chunkSeq range the page
// occupies (both 0 when the page packed into zero chunks), the completed
// oversize refs (handle/totalBytes/totalChunks filled by the store), and the
// manifest snapshot for the kernel manifest op.
type DetailAcceptedPage struct {
	ManifestRev   int
	ChunkSeqFirst int
	ChunkSeqLast  int
	OversizeRefs  []TurnDetailOversizeRef
	Manifest      *TurnDetailManifest
}

// TurnDetailStore is the process-wide detail cache. One mutex serializes
// accepts/sweeps/evictions — page accepts are ~500ms apart per turn and chunk
// reads are user-driven, so contention is not a concern at evidence scale.
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

func syncFile(f *os.File) error { return f.Sync() }

func syncDir(path string) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	_ = dir.Sync()
	_ = dir.Close()
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
		return nil, fmt.Errorf("detail-store: manifest corrupt: %w", err)
	}
	return &m, nil
}

func (s *TurnDetailStore) persistManifestLocked(dir string, m *TurnDetailManifest) error {
	raw, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "tmp", "manifest.json.tmp")
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	final := filepath.Join(dir, "manifest.json")
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	syncDir(final)
	return nil
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

func itemIDOfPart(p ProjectionPart) string { return p.ItemID }

func truncateRuneAligned(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := alignBackToRuneStart(s, limit)
	return s[:cut]
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
			BackendID: acc.BackendID, SessionID: acc.SessionID, TurnID: acc.TurnID,
			Generation: acc.Generation, ChunkSeqNext: 1,
			UpdatedAtMs: s.now().UnixMilli(),
		}
	}
	if manifest.Generation != acc.Generation {
		return nil, fmt.Errorf("%w: manifest %d != accept %d", ErrDetailGeneration, manifest.Generation, acc.Generation)
	}
	if acc.Page != manifest.Resume.Pages+1 {
		return nil, fmt.Errorf("%w: accept %d, manifest at %d", ErrDetailPageOrder, acc.Page, manifest.Resume.Pages)
	}

	known := make(map[string]bool, len(manifest.Items))
	for _, item := range manifest.Items {
		known[item.ItemID] = true
	}
	threshold := core.TurnDetailBlobThresholdBytes
	refs := make([]TurnDetailOversizeRef, 0, len(acc.Oversize))
	stagedByID := make(map[string]DetailOversizeStaged, len(acc.Oversize))
	var pageBytes int64
	for i := range acc.Items {
		part := acc.Items[i]
		id := itemIDOfPart(part)
		if id == "" {
			return nil, fmt.Errorf("%w: inline item[%d] missing canonical itemId", ErrDetailBadID, i)
		}
		if known[id] {
			return nil, fmt.Errorf("%w: %s", ErrDetailDuplicateItem, id)
		}
		known[id] = true
		if sz := partSize(part); sz > threshold {
			return nil, fmt.Errorf("%w: %s is %d bytes", ErrDetailInlineOversize, id, sz)
		} else {
			pageBytes += sz
		}
	}
	for i, staged := range acc.Oversize {
		if staged.ItemID == "" || !utf8.ValidString(staged.Content) {
			return nil, fmt.Errorf("%w: oversize[%d] bad id/content", ErrDetailBadID, i)
		}
		if known[staged.ItemID] {
			return nil, fmt.Errorf("%w: %s", ErrDetailDuplicateItem, staged.ItemID)
		}
		known[staged.ItemID] = true
		handle := hashSeg(acc.BackendID + "|" + acc.SessionID + "|" + acc.TurnID + "|" + staged.ItemID)
		if !safeHandleSeg.MatchString(handle) {
			return nil, fmt.Errorf("%w: handle %q", ErrDetailBadID, handle)
		}
		totalBytes := int64(len(staged.Content))
		pageBytes += totalBytes
		refs = append(refs, TurnDetailOversizeRef{
			ItemID:      staged.ItemID,
			Handle:      handle,
			Type:        staged.Type,
			TotalBytes:  totalBytes,
			Preview:     truncateRuneAligned(staged.Preview, core.TurnDetailBlobPreviewBytes),
			TotalChunks: DetailChunkCount(staged.Content),
		})
		stagedByID[staged.ItemID] = staged
	}

	chunks, err := SplitDetailChunks(acc.Items, refs)
	if err != nil {
		return nil, err
	}
	chunkSeqFirst, chunkSeqLast := 0, 0
	if len(chunks) > 0 {
		chunkSeqFirst = manifest.ChunkSeqNext
		chunkSeqLast = chunkSeqFirst + len(chunks) - 1
	}
	tx := manifest.TxApplied + 1

	// Step 1: blobs — temp write, fsync, rename.
	if len(acc.Oversize) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
			return nil, err
		}
	}
	for _, ref := range refs {
		staged := stagedByID[ref.ItemID]
		tmp := filepath.Join(dir, "tmp", ref.Handle+".tmp")
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := f.WriteString(staged.Content); err != nil {
			f.Close()
			return nil, err
		}
		if err := syncFile(f); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		final := filepath.Join(dir, "blobs", ref.Handle+".bin")
		if err := os.Rename(tmp, final); err != nil {
			return nil, err
		}
		syncDir(final)
	}

	// Step 2: items transaction record — append, fsync.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	record := detailTxRecord{
		Tx: tx, Page: acc.Page, NextCursor: acc.NextCursor, EOF: acc.EOF,
		ChunkSeqFirst: chunkSeqFirst, ChunkSeqLast: chunkSeqLast,
		Items: acc.Items, Oversize: refs,
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
	if err := syncFile(logF); err != nil {
		logF.Close()
		return nil, err
	}
	if err := logF.Close(); err != nil {
		return nil, err
	}

	// Step 3: manifest — temp write, fsync, rename (COMMIT POINT).
	for i := range acc.Items {
		part := acc.Items[i]
		manifest.Items = append(manifest.Items, TurnDetailItemSummary{
			ItemID: itemIDOfPart(part), Type: part.Type, Bytes: partSize(part), Page: acc.Page,
		})
	}
	for _, ref := range refs {
		manifest.Items = append(manifest.Items, TurnDetailItemSummary{
			ItemID: ref.ItemID, Type: ref.Type, Bytes: ref.TotalBytes, Page: acc.Page, BlobHandle: ref.Handle,
		})
	}
	manifest.ManifestRev++
	manifest.TxApplied = tx
	if len(chunks) > 0 {
		manifest.ChunkSeqNext = chunkSeqLast + 1
	}
	manifest.ItemCount += len(acc.Items) + len(acc.Oversize)
	manifest.TotalBytes += pageBytes
	lastID := manifest.Resume.LastAcceptedItemID
	if n := len(acc.Items); n > 0 {
		lastID = itemIDOfPart(acc.Items[n-1])
	} else if n := len(acc.Oversize); n > 0 {
		lastID = acc.Oversize[n-1].ItemID
	}
	manifest.Resume = TurnDetailResume{
		NextCursor: acc.NextCursor, LastAcceptedItemID: lastID,
		AcceptedCount: manifest.Resume.AcceptedCount + len(acc.Items) + len(acc.Oversize),
		Pages:         acc.Page, EOF: acc.EOF,
	}
	manifest.UpdatedAtMs = s.now().UnixMilli()
	if err := s.persistManifestLocked(dir, manifest); err != nil {
		return nil, err
	}

	// Budget enforcement is best-effort and never evicts the turn just committed.
	_ = s.enforceBudgetLocked(dir)

	return &DetailAcceptedPage{
		ManifestRev:   manifest.ManifestRev,
		ChunkSeqFirst: chunkSeqFirst,
		ChunkSeqLast:  chunkSeqLast,
		OversizeRefs:  refs,
		Manifest:      manifest,
	}, nil
}

// ReadRecords returns the COMMITTED transaction records (Tx ≤ TxApplied) in
// accept order — the deterministic replay source.
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
	raw, err := os.ReadFile(filepath.Join(dir, "items.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []detailTxRecord
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec detailTxRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("detail-store: items.log corrupt: %w", err)
		}
		if rec.Tx <= manifest.TxApplied {
			records = append(records, rec)
		}
	}
	return records, nil
}

// ReadBlobChunk serves one chunk of a manifest-referenced blob, cut by the
// frozen offset table (deterministic across reads). Binding (generation/
// manifestRev/itemId/handle) is the caller's job against LoadManifest; this
// validates handle ∈ manifest.
func (s *TurnDetailStore) ReadBlobChunk(backendID, sessionID, turnID, handle string, chunkIndex int) (string, int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.turnDir(backendID, sessionID, turnID)
	if err != nil {
		return "", 0, 0, err
	}
	manifest, err := s.loadManifestLocked(dir)
	if err != nil {
		return "", 0, 0, err
	}
	referenced := false
	for _, item := range manifest.Items {
		if item.BlobHandle == handle {
			referenced = true
			break
		}
	}
	if !referenced {
		return "", 0, 0, fmt.Errorf("%w: %s", ErrDetailBlobUnref, handle)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "blobs", handle+".bin"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, 0, fmt.Errorf("%w: %s", ErrDetailBlobMissing, handle)
		}
		return "", 0, 0, err
	}
	content := string(raw)
	offsets := DetailChunkOffsets(content)
	if chunkIndex < 0 || chunkIndex >= len(offsets)-1 {
		return "", 0, 0, fmt.Errorf("%w: %d of %d", ErrDetailChunkIndex, chunkIndex, len(offsets)-1)
	}
	// LRU honesty: a chunk read refreshes the turn's eviction priority.
	manifest.UpdatedAtMs = s.now().UnixMilli()
	_ = s.persistManifestLocked(dir, manifest)
	return content[offsets[chunkIndex]:offsets[chunkIndex+1]], len(offsets) - 1, int64(len(content)), nil
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

// SweepUncommitted is the startup recovery pass (frozen step 4): every turn
// dir is reconciled to its manifest commit point — items.log lines beyond
// TxApplied are truncated; blobs referenced by no committed record are
// deleted; a dir with content but no manifest is uncommitted garbage and
// removed whole.
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
				if _, statErr := os.Stat(filepath.Join(path, "items.log")); statErr == nil {
					return os.RemoveAll(path)
				}
				return nil
			}
			return mErr
		}
		raw, readErr := os.ReadFile(filepath.Join(path, "items.log"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		var kept []string
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var rec detailTxRecord
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue // torn tail write — uncommitted by definition
			}
			if rec.Tx <= manifest.TxApplied {
				kept = append(kept, line)
			}
		}
		want := strings.Join(kept, "\n")
		got := strings.TrimRight(string(raw), "\n")
		if want != got {
			tmp := filepath.Join(path, "tmp", "items.log.tmp")
			if err := os.MkdirAll(filepath.Join(path, "tmp"), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(tmp, []byte(want+"\n"), 0o644); err != nil {
				return err
			}
			final := filepath.Join(path, "items.log")
			if err := os.Rename(tmp, final); err != nil {
				return err
			}
			syncDir(final)
		}
		committedHandles := make(map[string]bool)
		for _, line := range kept {
			var rec detailTxRecord
			_ = json.Unmarshal([]byte(line), &rec)
			for _, ref := range rec.Oversize {
				committedHandles[ref.Handle] = true
			}
		}
		blobDir := filepath.Join(path, "blobs")
		entries, globErr := os.ReadDir(blobDir)
		if globErr == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				handle := strings.TrimSuffix(entry.Name(), ".bin")
				if !committedHandles[handle] {
					_ = os.Remove(filepath.Join(blobDir, entry.Name()))
				}
			}
		}
		return nil
	})
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
