package gobridge

// S-2 物化存储（bbolt；方案 v30 §3.2，格式冻结见
// docs/2026-10-19-s2-materialization-format-freeze.md）。
//
// 以行级差分写替换「每次保存整棵 34MB JSON checkpoint」：head/segments/turns 同一
// bbolt Update 事务原子提交；重启恢复按四态来源分类（全等零读 / prefix digest /
// 失效重建），并接受 cursor ≤ 当前的推进——恢复基线携带 stored cursors，由 kernel
// 既有 hydrate 机制折叠 suffix（单源 range 遍历现成；composite 走
// CompositeRichHistoryOffsetProvider 续读）。
//
// 不变量：Mac 单真相 writer 不变（store 是 kernel 的派生持久层）；旧
// checkpoints/*.json 保留不读不删（OD-5）；OD-9 = 默认档 (a)——推进路径必经
// prefix digest，无跳过校验通道。

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	// 2026-10-21 评审 BLOCKER-1 bump 1→2：v1 期间组合源后缀折叠写入的 checkpoint
	// 可能已损坏（行号派生 turn ID 重编号碰撞 → 早期 turn 被覆盖/尾部虚构，
	// docs/2026-10-21-long-session-projection-loading-session-code-review-r1.md）。
	// bump 使全部 v1 存档失效 → 冷开诚实全量重建。
	materializationSchemaVersion = 2
	// materializationFilename 冻结于格式文档 §1。
	materializationFilename = "materialization.bbolt"

	materializationHeadKey     = "head"
	materializationSegmentsBkt = "segments"
	materializationTurnsBkt    = "turns"
	materializationItemsBkt    = "items"
)

// materializationProductionEnabled 是 S-2 的发布门（镜像 PERF-S4D 纪律）。
// Rollback = 置 false → SetDataDir 装配旧 JSON checkpoint store，行为逐字节回到
// 现状；materialization.bbolt 文件独立，可直接弃用（旧 checkpoints/*.json 从未
// 被触碰，无需迁移——OD-5）。
const materializationProductionEnabled = true

// materializationSegmentRow 是 segments/<ordinal> 的行形状（frozen §3；membership
// 按 identity 序判定——与 JSON store 同纪律，物理路径由当前 source descriptor
// 提供，行内 Path 仅诊断 provenance 用）。
type materializationSegmentRow struct {
	Identity       string `json:"identity"`
	Path           string `json:"path,omitempty"`
	Cursor         int64  `json:"cursor"`
	PrefixSHA256   string `json:"prefixSha256"`
	ObservedSize   int64  `json:"observedSize"`
	ObservedMTimeN int64  `json:"observedMTimeN"`
}

// materializationHeadRow 是 head 行形状（frozen §2）。
type materializationHeadRow struct {
	SchemaVersion      int                    `json:"schemaVersion"`
	BackendID          string                 `json:"backendId"`
	SessionID          string                 `json:"sessionId"`
	SyncRev            int                    `json:"syncRev"`
	BridgeEpoch        string                 `json:"bridgeEpoch,omitempty"` // provenance only
	UpdatedAt          time.Time              `json:"updatedAt"`
	ScalarStateJSON    json.RawMessage        `json:"scalarStateJson"`
	Sources            []materializationSegmentRow `json:"sources"`
	ClaudeSourceState  *ClaudeSourceState     `json:"claudeSourceState,omitempty"`
	CodexProducerState *CodexProducerState    `json:"codexProducerState,omitempty"`
}

// materializationScalars 是 scalar_state_json 的载荷（SessionProjection 的非 turn 字段）。
type materializationScalars struct {
	Execution         ExecutionView          `json:"execution"`
	BridgeEpoch       string                 `json:"bridgeEpoch,omitempty"`
	UpdatedAt         int64                  `json:"updatedAt,omitempty"`
	PlanMode          *PlanModeView          `json:"planMode,omitempty"`
	SessionMode       *SessionModeView       `json:"sessionMode,omitempty"`
	CollaborationMode *CollaborationModeView `json:"collaborationMode,omitempty"`
	CodexGoal         *CodexGoalView         `json:"codexGoal,omitempty"`
	Goal              *GoalView              `json:"goal,omitempty"`
}

// ProjectionMaterializationStore 实现 projectionCheckpointPersistence（bbolt 后端）。
// 空 baseDir = 禁用（与 JSON store 同语义）。
type ProjectionMaterializationStore struct {
	baseDir string
	db      *bolt.DB
}

// NewProjectionMaterializationStore 打开（或创建）物化库。baseDir 为空返回禁用实例
// （Save/Load 返回 ErrProjectionCheckpointDisabled）。
func NewProjectionMaterializationStore(dataDir string) *ProjectionMaterializationStore {
	if dataDir == "" {
		return &ProjectionMaterializationStore{}
	}
	dir := filepath.Join(dataDir, "session-projection")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &ProjectionMaterializationStore{}
	}
	db, err := bolt.Open(filepath.Join(dir, materializationFilename), 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		slog.Error("materialization: open failed (disabled for this run)", "error", err)
		return &ProjectionMaterializationStore{}
	}
	return &ProjectionMaterializationStore{baseDir: dir, db: db}
}

// Close 关闭底层数据库（进程退出路径）。
func (s *ProjectionMaterializationStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func materializationTurnKey(ordinal int) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, uint64(ordinal))
	return key
}

func materializationSegmentKey(ordinal int) []byte {
	return materializationTurnKey(ordinal)
}

// Save 以单个 Update 事务差分写入 checkpoint：head/segments 覆盖，turns 按行 diff
//（新增/变更 upsert、消失 delete），items Phase A 恒空。
func (s *ProjectionMaterializationStore) Save(checkpoint ProjectionCheckpoint) error {
	if s == nil || s.db == nil {
		return ErrProjectionCheckpointDisabled
	}
	if checkpoint.BackendID == "" || checkpoint.SessionID == "" {
		return fmt.Errorf("%w: empty backend/session identity", ErrProjectionCheckpointInvalid)
	}
	if checkpoint.Projection.SessionID != checkpoint.SessionID {
		return fmt.Errorf("%w: projection session identity mismatch", ErrProjectionCheckpointInvalid)
	}

	scalars := materializationScalars{
		Execution:         checkpoint.Projection.Execution,
		BridgeEpoch:       checkpoint.Projection.BridgeEpoch,
		UpdatedAt:         checkpoint.Projection.UpdatedAt,
		PlanMode:          checkpoint.Projection.PlanMode,
		SessionMode:       checkpoint.Projection.SessionMode,
		CollaborationMode: checkpoint.Projection.CollaborationMode,
		CodexGoal:         checkpoint.Projection.CodexGoal,
		Goal:              checkpoint.Projection.Goal,
	}
	scalarRaw, err := json.Marshal(scalars)
	if err != nil {
		return err
	}
	head := materializationHeadRow{
		SchemaVersion:      materializationSchemaVersion,
		BackendID:          checkpoint.BackendID,
		SessionID:          checkpoint.SessionID,
		SyncRev:            checkpoint.Projection.SyncRev,
		BridgeEpoch:        checkpoint.Projection.BridgeEpoch,
		UpdatedAt:          checkpoint.UpdatedAt,
		ScalarStateJSON:    scalarRaw,
		ClaudeSourceState:  checkpoint.ClaudeSourceState,
		CodexProducerState: checkpoint.CodexProducerState,
	}
	sources := checkpoint.Sources
	if len(sources) == 0 {
		sources = []ProjectionSourceCheckpoint{checkpoint.Source}
	}
	for _, src := range sources {
		head.Sources = append(head.Sources, materializationSegmentRow{
			Identity:       src.Identity,
			Cursor:         src.Cursor,
			PrefixSHA256:   src.PrefixSHA256,
			ObservedSize:   src.ObservedSize,
			ObservedMTimeN: src.ObservedMTimeN,
		})
	}
	headRaw, err := json.Marshal(head)
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		sessionBkt, err := materializationSessionBucket(tx, checkpoint.BackendID, checkpoint.SessionID, true)
		if err != nil {
			return err
		}
		if err := sessionBkt.Put([]byte(materializationHeadKey), headRaw); err != nil {
			return err
		}
		// segments 全量覆盖（先清后写——段数可能收缩）。
		segments := sessionBkt.Bucket([]byte(materializationSegmentsBkt))
		if segments == nil {
			segments, err = sessionBkt.CreateBucketIfNotExists([]byte(materializationSegmentsBkt))
			if err != nil {
				return err
			}
		}
		if err := segments.ForEach(func(k, _ []byte) error { return segments.Delete(k) }); err != nil {
			return err
		}
		for index, row := range head.Sources {
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if err := segments.Put(materializationSegmentKey(index), raw); err != nil {
				return err
			}
		}
		// turns 行级差分。
		turns := sessionBkt.Bucket([]byte(materializationTurnsBkt))
		if turns == nil {
			turns, err = sessionBkt.CreateBucketIfNotExists([]byte(materializationTurnsBkt))
			if err != nil {
				return err
			}
		}
		live := make(map[int][]byte, len(checkpoint.Projection.Turns))
		for index := range checkpoint.Projection.Turns {
			turn := &checkpoint.Projection.Turns[index]
			raw, err := json.Marshal(turn)
			if err != nil {
				return err
			}
			live[index] = raw
		}
		var staleKeys [][]byte
		if err := turns.ForEach(func(k, v []byte) error {
			ordinal := int(binary.BigEndian.Uint64(k[:8]))
			want, ok := live[ordinal]
			if !ok {
				staleKeys = append(staleKeys, append([]byte(nil), k...))
				return nil
			}
			if bytes.Equal(v, want) {
				delete(live, ordinal) // 未变行零写
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range staleKeys {
			if err := turns.Delete(k); err != nil {
				return err
			}
		}
		for ordinal, raw := range live {
			if err := turns.Put(materializationTurnKey(ordinal), raw); err != nil {
				return err
			}
		}
		// items Phase A 预留（恒空）。
		if _, err := sessionBkt.CreateBucketIfNotExists([]byte(materializationItemsBkt)); err != nil {
			return err
		}
		return nil
	})
}

// LoadValidated 按四态来源分类校验并重建 checkpoint（frozen §4）：
// membership/truncation/retreat → 失效重建（ErrProjectionCheckpointInvalid）；
// (iii) 全等零读；(ii) 推进或 mtime 变化 → digest [0, stored.cursor)。
// 返回的 checkpoint 携带 stored cursors（kernel 以其为折叠基线）。
func (s *ProjectionMaterializationStore) LoadValidated(
	backendID, sessionID string,
	source ProjectionSourceDescriptor,
) (ProjectionCheckpoint, error) {
	if s == nil || s.db == nil {
		return ProjectionCheckpoint{}, ErrProjectionCheckpointDisabled
	}
	var head materializationHeadRow
	var turnBlobs [][]byte
	err := s.db.View(func(tx *bolt.Tx) error {
		sessionBkt, err := materializationSessionBucket(tx, backendID, sessionID, false)
		if err != nil {
			return err
		}
		if sessionBkt == nil {
			return os.ErrNotExist
		}
		raw := sessionBkt.Get([]byte(materializationHeadKey))
		if raw == nil {
			return os.ErrNotExist
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return fmt.Errorf("%w: head decode: %v", ErrProjectionCheckpointInvalid, err)
		}
		turns := sessionBkt.Bucket([]byte(materializationTurnsBkt))
		if turns == nil {
			return fmt.Errorf("%w: turns bucket missing", ErrProjectionCheckpointInvalid)
		}
		return turns.ForEach(func(_, v []byte) error {
			turnBlobs = append(turnBlobs, append([]byte(nil), v...))
			return nil
		})
	})
	if err != nil {
		return ProjectionCheckpoint{}, err
	}
	if head.SchemaVersion != materializationSchemaVersion ||
		head.BackendID != backendID || head.SessionID != sessionID {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: schema/identity mismatch", ErrProjectionCheckpointInvalid)
	}
	// 仅 producer seed 的骨架行（无 committed 源）不是 checkpoint 命中——按未命中
	// 处理（冷开走完整 hydrate），与 JSON 侧「checkpoint 文件不存在」同语义。
	if len(head.Sources) == 0 {
		return ProjectionCheckpoint{}, os.ErrNotExist
	}
	if source.Identity == "" {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: source identity missing", ErrProjectionCheckpointInvalid)
	}

	// 来源四态分类（§4）：membership → truncation/retreat → (iii) 全等 → (ii) digest。
	var currentSegments []ProjectionSourceSegment
	if len(source.Segments) > 0 {
		currentSegments = source.Segments
	} else {
		currentSegments = []ProjectionSourceSegment{{Identity: source.Identity, Path: source.Path, Cursor: source.Cursor}}
	}
	if len(head.Sources) != len(currentSegments) {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: source membership mismatch", ErrProjectionCheckpointInvalid)
	}
	sources := make([]ProjectionSourceCheckpoint, 0, len(currentSegments))
	for index, segment := range currentSegments {
		stored := head.Sources[index]
		if stored.Identity != segment.Identity {
			return ProjectionCheckpoint{}, fmt.Errorf("%w: source membership mismatch at %d", ErrProjectionCheckpointInvalid, index)
		}
		// 2026-10-21 评审 BLOCKER-1 止血：恢复 main（f868af18）的组合源 cut 严格
		// 语义。组合源段级 cursor 推进时，后缀折叠依赖行号派生 turn ID 从续读
		// 起点重新编号（continuation.go），与存量 turn 确定性碰撞 → 早期 turn
		// 被新 prompt 覆盖、尾部 turn 虚构；损坏会写回本存储并持续叠加（生产
		// 18 次折叠 11 次分歧）。单源维持 main 语义不变：前缀 digest 校验 +
		// 接受 cursor 前进（transcript 纯追加，无重编号面）。组合源任何段
		// cursor 不等即 Invalid → 诚实全量重建。
		if len(source.Segments) > 0 && stored.Cursor != segment.Cursor {
			return ProjectionCheckpoint{}, fmt.Errorf(
				"%w: composite source cut mismatch at %d (stored=%d current=%d)",
				ErrProjectionCheckpointInvalid, index, stored.Cursor, segment.Cursor,
			)
		}
		validated, err := validateMaterializationSegment(segment.Path, stored)
		if err != nil {
			return ProjectionCheckpoint{}, err
		}
		sources = append(sources, validated)
	}

	// 重建投影：scalar（head）+ turns（ordinal 序）。
	var scalars materializationScalars
	if err := json.Unmarshal(head.ScalarStateJSON, &scalars); err != nil {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: scalar decode: %v", ErrProjectionCheckpointInvalid, err)
	}
	projection := SessionProjection{
		SessionID:        sessionID,
		SyncRev:          head.SyncRev,
		BridgeEpoch:      scalars.BridgeEpoch,
		UpdatedAt:        scalars.UpdatedAt,
		Execution:        scalars.Execution,
		PlanMode:         scalars.PlanMode,
		SessionMode:      scalars.SessionMode,
		CollaborationMode: scalars.CollaborationMode,
		CodexGoal:        scalars.CodexGoal,
		Goal:             scalars.Goal,
	}
	if err := validateTurnDetailCheckpointFields(projection.Turns); err != nil {
		return ProjectionCheckpoint{}, err
	}
	for _, blob := range turnBlobs {
		var turn TurnProjection
		if err := json.Unmarshal(blob, &turn); err != nil {
			return ProjectionCheckpoint{}, fmt.Errorf("%w: turn row decode: %v", ErrProjectionCheckpointInvalid, err)
		}
		projection.Turns = append(projection.Turns, turn)
	}
	if err := validateTurnDetailCheckpointFields(projection.Turns); err != nil {
		return ProjectionCheckpoint{}, err
	}

	checkpoint := ProjectionCheckpoint{
		SchemaVersion:      projectionCheckpointSchemaVersion,
		BackendID:          backendID,
		SessionID:          sessionID,
		Projection:         projection,
		ProjectionRev:      head.SyncRev,
		HydrateState:       ProjectionHydrateReady,
		UpdatedAt:          head.UpdatedAt,
		ClaudeSourceState:  head.ClaudeSourceState,
		CodexProducerState: head.CodexProducerState,
	}
	if len(sources) == 1 && len(source.Segments) == 0 {
		checkpoint.Source = sources[0]
	} else {
		checkpoint.Sources = sources
	}
	if checkpoint.ClaudeSourceState != nil {
		if err := ValidateClaudeSourceState(*checkpoint.ClaudeSourceState); err != nil {
			return ProjectionCheckpoint{}, err
		}
	}
	if checkpoint.CodexProducerState != nil {
		if err := ValidateCodexProducerState(*checkpoint.CodexProducerState); err != nil {
			return ProjectionCheckpoint{}, err
		}
	}
	return checkpoint, nil
}

// LoadCodexProducerState / SaveCodexProducerState：producer seed 并入 head 行
//（JSON store 的侧文件路径保留在其自身实现内，不共用）。
func (s *ProjectionMaterializationStore) LoadCodexProducerState(backendID, sessionID string) (*CodexProducerState, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var state *CodexProducerState
	err := s.db.View(func(tx *bolt.Tx) error {
		sessionBkt, err := materializationSessionBucket(tx, backendID, sessionID, false)
		if err != nil || sessionBkt == nil {
			return err
		}
		raw := sessionBkt.Get([]byte(materializationHeadKey))
		if raw == nil {
			return nil
		}
		var head materializationHeadRow
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil // producer seed 是加速器：head 损坏走全重建，不阻塞性失败
		}
		state = head.CodexProducerState
		return nil
	})
	if err != nil {
		return nil, nil
	}
	if state != nil {
		if err := ValidateCodexProducerState(*state); err != nil {
			return nil, nil
		}
	}
	return state, nil
}

func (s *ProjectionMaterializationStore) SaveCodexProducerState(backendID, sessionID string, state CodexProducerState) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		// producer seed 与 checkpoint 保存无序（冷开 commit 后立刻写 seed，checkpoint
		// 写是去抖异步）——JSON 侧文件独立于此；bbolt 同语义：head 不存在则先落一个
		// 仅携带身份 + seed 的骨架行（LoadValidated 对无 sources 的骨架返回
		// ErrNotExist，不会误当 checkpoint 命中）。
		sessionBkt, err := materializationSessionBucket(tx, backendID, sessionID, true)
		if err != nil {
			return err
		}
		var head materializationHeadRow
		if raw := sessionBkt.Get([]byte(materializationHeadKey)); raw != nil {
			if err := json.Unmarshal(raw, &head); err != nil {
				return fmt.Errorf("%w: head decode: %v", ErrProjectionCheckpointInvalid, err)
			}
		} else {
			head = materializationHeadRow{
				SchemaVersion: materializationSchemaVersion,
				BackendID:     backendID,
				SessionID:     sessionID,
				UpdatedAt:     time.Now(),
			}
		}
		head.CodexProducerState = &state
		next, err := json.Marshal(head)
		if err != nil {
			return err
		}
		if err := sessionBkt.Put([]byte(materializationHeadKey), next); err != nil {
			return err
		}
		_, err = sessionBkt.CreateBucketIfNotExists([]byte(materializationTurnsBkt))
		return err
	})
}

// validateMaterializationSegment 执行单段四态分类（frozen §4）；返回带 stored cursor
// 的 checkpoint 行（推进被接受）。
func validateMaterializationSegment(path string, stored materializationSegmentRow) (ProjectionSourceCheckpoint, error) {
	out := ProjectionSourceCheckpoint{
		Identity:       stored.Identity,
		Cursor:         stored.Cursor,
		PrefixSHA256:   stored.PrefixSHA256,
		ObservedSize:   stored.ObservedSize,
		ObservedMTimeN: stored.ObservedMTimeN,
	}
	if stored.Cursor < 0 {
		return out, fmt.Errorf("%w: negative stored cursor", ErrProjectionCheckpointInvalid)
	}
	info, err := os.Stat(path)
	if err != nil {
		return out, err
	}
	if info.Size() < stored.Cursor {
		return out, fmt.Errorf(
			"%w: source truncated size=%d cursor=%d",
			ErrProjectionCheckpointInvalid, info.Size(), stored.Cursor,
		)
	}
	// (iii) 全等零读（stat-only fast path）。
	if info.Size() == stored.Cursor && stored.Cursor == stored.ObservedSize &&
		info.ModTime().UnixNano() == stored.ObservedMTimeN {
		return out, nil
	}
	// (ii) 默认：digest committed 前缀（OD-9 档 (a)：推进路径必经校验，无跳过通道）。
	digest, err := digestFilePrefix(path, stored.Cursor)
	if err != nil {
		return out, err
	}
	if digest != stored.PrefixSHA256 {
		return out, fmt.Errorf("%w: consumed prefix digest mismatch", ErrProjectionCheckpointInvalid)
	}
	return out, nil
}

func digestFilePrefix(path string, cursor int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digestProjectionSourcePrefix(f, cursor)
}

func materializationSessionBucket(tx *bolt.Tx, backendID, sessionID string, create bool) (*bolt.Bucket, error) {
	if create {
		backend, err := tx.CreateBucketIfNotExists([]byte(backendID))
		if err != nil {
			return nil, err
		}
		session, err := backend.CreateBucketIfNotExists([]byte(sessionID))
		if err != nil {
			return nil, err
		}
		return session, nil
	}
	backend := tx.Bucket([]byte(backendID))
	if backend == nil {
		return nil, nil
	}
	session := backend.Bucket([]byte(sessionID))
	if session == nil {
		return nil, nil
	}
	return session, nil
}
