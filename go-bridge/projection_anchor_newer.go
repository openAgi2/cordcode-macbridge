package gobridge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// anchor-newer 对账增量核心（S-7，方案 v30 §3.8；canonical =
// docs/protocol/bridge-v1.md §Anchor-Newer Incremental Reconciliation — FROZEN SPEC）。
//
// 不变量 6：Mac 对客户端进度完全无状态——全部判定输入（anchorTurnId、完整复查集、
// 续页标记）随请求携带；同一 cursor 重放幂等（upsert 覆盖）。

// 预算常量（frozen §预算与确定性打包；OD-7 冻结数值）。
const (
	// anchorNewerTotalHardCapBytes 是响应全字段序列化合计的硬顶（< 50MiB transport 解压上限留余量）。
	anchorNewerTotalHardCapBytes = 48 << 20
	// anchorNewerMandatoryReserve 为 envelope/scalar/metadata/removedTurnIds/分页元数据预留。
	anchorNewerMandatoryReserve = 2 << 20
	// anchorNewerNeverSplitQuota 是 anchorRefresh 与单个 changedTurn 的独占额度上限；超出 → typed oversized。
	anchorNewerNeverSplitQuota = 46 << 20
	// anchorNewerPageBudget 是每页对账预算（turns + changedTurns + scalar/metadata；anchorRefresh 不计入）。
	anchorNewerPageBudget = 8 << 20
	// anchorNewerMaxDigestEntries 是复查集容量上限（受缓存上限约束，frozen）。
	anchorNewerMaxDigestEntries = 256
	// anchorNewerDefaultTurns 是 limit 缺省（协议默认 100 turns / 4MiB；4MiB 由 growWindowRange 家族上限承接）。
	anchorNewerDefaultTurns = 100
)

var (
	errAnchorNewerStale     = errors.New("anchor turn left the kernel retention window")
	errAnchorNewerOversized = errors.New("anchorRefresh or a changedTurn exceeds the 46MiB never-split quota")
	errAnchorNewerCursor    = errors.New("correction cursor could not be decoded or its chain position left the kernel")
)

// AnchorNewerParams 是 get_session_projection_anchor_newer 请求（首包与续页共用；
// CorrectionCursor 为续页身份标记——无标记 = 首包携带 anchorRefresh，有标记 = 续页不携带）。
type AnchorNewerParams struct {
	SessionID        string              `json:"sessionId"`
	BackendID        string              `json:"backendId"`
	AnchorTurnID     string              `json:"anchorTurnId"`
	BaseDigests      map[string]*string  `json:"baseDigests"`
	Limit            int                 `json:"limit,omitempty"`
	CorrectionCursor string              `json:"correctionCursor,omitempty"`
}

// AnchorNewerCorrectionsPending 是剩余修正的续拉标记（cursor = 续页请求回传的修正游标）。
type AnchorNewerCorrectionsPending struct {
	Cursor    string `json:"cursor"`
	Remaining int    `json:"remaining"`
}

// AnchorNewerResult 是对账增量页（frozen 响应形状；wire 冻结样本
// samples/projection-window-v1/anchor-newer-response-spec.json）。
type AnchorNewerResult struct {
	Turns              []TurnProjection              `json:"turns"`
	AnchorRefresh      *TurnProjection               `json:"anchorRefresh,omitempty"`
	ChangedTurns       []TurnProjection              `json:"changedTurns"`
	RemovedTurnIDs     []string                      `json:"removedTurnIds"`
	ScalarState        AnchorNewerScalarState        `json:"scalarState"`
	CorrectionsPending *AnchorNewerCorrectionsPending `json:"correctionsPending,omitempty"`
	TurnTokens         map[string]string             `json:"turnTokens,omitempty"`
	SyncRev            int                           `json:"syncRev"`
}

// AnchorNewerScalarState 镜像 SessionProjection 的 scalar 字段（execution 必选；
// nil = 该 backend 无此投影）。
type AnchorNewerScalarState struct {
	Execution         ExecutionView         `json:"execution"`
	PlanMode          *PlanModeView         `json:"planMode,omitempty"`
	SessionMode       *SessionModeView      `json:"sessionMode,omitempty"`
	CollaborationMode *CollaborationModeView `json:"collaborationMode,omitempty"`
	CodexGoal         *CodexGoalView        `json:"codexGoal,omitempty"`
	Goal              *GoalView             `json:"goal,omitempty"`
}

// anchorNewerCursorState 是修正游标的载荷（自包含：chain 位置可由当前 kernel 状态
// + 复查集重建；Mac 无 per-device 状态）。
type anchorNewerCursorState struct {
	V              int    `json:"v"`
	LastTurnID     string `json:"lastTurnId"`     // turn phase 交付链末端（"" 仅出现于零交付且 anchor 缺席的非法态）
	ChangedThrough string `json:"changedThrough"` // correction phase 已交付到的 changedTurnId（"" = 未开始）
}

func encodeAnchorNewerCursor(state anchorNewerCursorState) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeAnchorNewerCursor(encoded string) (anchorNewerCursorState, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return anchorNewerCursorState{}, fmt.Errorf("%w: %v", errAnchorNewerCursor, err)
	}
	var state anchorNewerCursorState
	if err := json.Unmarshal(raw, &state); err != nil {
		return anchorNewerCursorState{}, fmt.Errorf("%w: %v", errAnchorNewerCursor, err)
	}
	if state.V != 1 {
		return anchorNewerCursorState{}, fmt.Errorf("%w: unsupported cursor version %d", errAnchorNewerCursor, state.V)
	}
	return state, nil
}

// AnchorNewerTurnToken 生成 turn 的不透明摘要 token（token 生命周期不变量：与内容
// 来自同一权威交付；json.Marshal 字段序稳定 → 同一二进制内确定）。
func AnchorNewerTurnToken(turn TurnProjection) (string, error) {
	raw, err := json.Marshal(turn)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range sum {
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out), nil
}

// BuildAnchorNewerTurnTokens 为 full 种子（get_session_projection 全量响应）生成
// {turnId → token}（仅对已协商 anchor-newer 的连接下发；与内容同帧配对）。
func BuildAnchorNewerTurnTokens(turns []TurnProjection) (map[string]string, error) {
	tokens := make(map[string]string, len(turns))
	for _, turn := range turns {
		token, err := AnchorNewerTurnToken(turn)
		if err != nil {
			return nil, err
		}
		tokens[turn.TurnID] = token
	}
	return tokens, nil
}

// sliceAnchorNewerReconciliation 在一个 committed snapshot 上切出对账增量页。
//
// 打包纪律（frozen）：mandatory 2MiB 预留 →（首包）anchorRefresh 唯一一次 → turns
// （严格后继、limit/4MiB、never-split）→ changedTurns（按 turnId 升序）。相位边界：
// turn 装不下即切页、本页剩余不回填、changedTurns 不得越过未交付 turns；turns 到达
// kernel 尾部后 changedTurns 可与末个 turns 同页。对账事务内水位线 = 已交付链末端。
func sliceAnchorNewerReconciliation(
	proj SessionProjection,
	params AnchorNewerParams,
) (AnchorNewerResult, error) {
	turns := proj.Turns
	anchorIdx := indexOfTurn(turns, params.AnchorTurnID)
	if anchorIdx < 0 {
		return AnchorNewerResult{}, errAnchorNewerStale
	}
	sizes, err := turnEncodedSizes(turns)
	if err != nil {
		return AnchorNewerResult{}, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = anchorNewerDefaultTurns
	}

	isFirstPage := params.CorrectionCursor == ""
	var cursorState anchorNewerCursorState
	if isFirstPage {
		cursorState = anchorNewerCursorState{V: 1, LastTurnID: params.AnchorTurnID}
	} else {
		cursorState, err = decodeAnchorNewerCursor(params.CorrectionCursor)
		if err != nil {
			return AnchorNewerResult{}, err
		}
	}

	// turn phase 起点与链位置（续页位置失效 = 链断裂 → cursor_stale 族）。
	turnStart := 0
	if cursorState.LastTurnID != "" {
		pos := indexOfTurn(turns, cursorState.LastTurnID)
		if pos < 0 {
			return AnchorNewerResult{}, fmt.Errorf("%w: lastTurnId %s left the kernel", errAnchorNewerCursor, cursorState.LastTurnID)
		}
		turnStart = pos + 1
	}

	// 复查集比对（全集口径）：当前 kernel 每 turn 的 token 现算；removed = 复查项
	// 不在 kernel；changed = token 不一致或请求侧 null（失效必须回传）。
	currentTokens := make(map[string]string, len(turns))
	kernelMembership := make(map[string]bool, len(turns))
	for _, turn := range turns {
		token, tokenErr := AnchorNewerTurnToken(turn)
		if tokenErr != nil {
			return AnchorNewerResult{}, tokenErr
		}
		currentTokens[turn.TurnID] = token
		kernelMembership[turn.TurnID] = true
	}
	changedIDs := make([]string, 0, len(params.BaseDigests))
	removedIDs := make([]string, 0)
	// 合并为一条（frozen）：turn 阶段将交付当前终态的后继 turns、以及首包
	// anchorRefresh 已覆盖的 anchor，不再进 changedTurns（其当前状态与 token 经
	/// turn 阶段/anchorRefresh 同帧到达；续页中 anchor 的页间突变由下一请求的
	// 全集复查捕获——单调覆盖 ②）。
	coveredByTurnPhase := func(turnID string) bool {
		idx := indexOfTurn(turns, turnID)
		return idx >= turnStart
	}
	anchorCoveredOnFirstPage := isFirstPage
	for turnID, declared := range params.BaseDigests {
		current, present := currentTokens[turnID]
		if !present {
			removedIDs = append(removedIDs, turnID)
			continue
		}
		if declared != nil && *declared == current {
			continue
		}
		if coveredByTurnPhase(turnID) {
			continue
		}
		if anchorCoveredOnFirstPage && turnID == params.AnchorTurnID {
			continue
		}
		changedIDs = append(changedIDs, turnID)
	}
	sort.Strings(changedIDs)
	sort.Strings(removedIDs)

	result := AnchorNewerResult{
		Turns:          []TurnProjection{},
		ChangedTurns:   []TurnProjection{},
		RemovedTurnIDs: removedIDs,
		TurnTokens:     map[string]string{},
		SyncRev:        proj.SyncRev,
	}

	// （仅首包）anchorRefresh：never-split 独占额度，不计对账预算。
	pageBudget := anchorNewerPageBudget
	anchorSize := 0
	if isFirstPage {
		anchorTurn := turns[anchorIdx]
		if sizes[anchorIdx] > anchorNewerNeverSplitQuota {
			return AnchorNewerResult{}, errAnchorNewerOversized
		}
		anchorCopy := anchorTurn
		result.AnchorRefresh = &anchorCopy
		result.TurnTokens[anchorTurn.TurnID] = currentTokens[anchorTurn.TurnID]
		anchorSize = sizes[anchorIdx]
		// 首包对账空间 = min(8MiB, 48 − 2 − anchorRefresh)。
		remaining := anchorNewerTotalHardCapBytes - anchorNewerMandatoryReserve - anchorSize
		if remaining < pageBudget {
			pageBudget = remaining
		}
	}

	// neverSplitFits：本页对账内容尚空时，单个 ≤46MiB 的整 turn 可越过 pageBudget
	// 独占本页（总硬顶终检：anchor + 该 turn + mandatory ≤ 48MiB——大 anchor 与大
	// 后继不能同页，后者溢出至续页，frozen「46MiB anchor + 后继由续页交付」）。
	neverSplitFits := func(size int, pageHasReconciliationContent bool) bool {
		return !pageHasReconciliationContent &&
			size <= anchorNewerNeverSplitQuota &&
			anchorSize+size+anchorNewerMandatoryReserve <= anchorNewerTotalHardCapBytes
	}

	// turn phase：严格后继填充；装不下即切页（当前页 turn phase 终止，剩余不回填）。
	turnCursor := turnStart
	turnsOnPage := 0
	pageConsumed := 0
	for turnCursor < len(turns) {
		if turnsOnPage >= limit {
			break
		}
		size := sizes[turnCursor]
		if pageConsumed+size > pageBudget && !neverSplitFits(size, turnsOnPage > 0) {
			break
		}
		turn := turns[turnCursor]
		result.Turns = append(result.Turns, turn)
		result.TurnTokens[turn.TurnID] = currentTokens[turn.TurnID]
		pageConsumed += size
		turnsOnPage++
		turnCursor++
	}
	reachedTail := turnCursor >= len(turns)

	// correction phase：仅当 turns 已全部交付（到达 kernel 尾部）才进入；按 turnId
	// 升序填充剩余空间（可与末个 turns 同页）。未交付 turns 存在时 changedTurns
	// 不得提前装页（相位边界）。>8MiB 的单个 changedTurn 仅在本页对账内容为空时
	// 独占本页（≤46MiB）。
	if reachedTail {
		for _, changedID := range changedIDs {
			idx := indexOfTurn(turns, changedID)
			if idx < 0 {
				continue // 已并入 removedIDs 口径（并发窗口内的竞态以当前 kernel 为准）
			}
			size := sizes[idx]
			if size > anchorNewerNeverSplitQuota {
				return AnchorNewerResult{}, errAnchorNewerOversized
			}
			pageHasContent := turnsOnPage > 0 || len(result.ChangedTurns) > 0
			if pageConsumed+size > pageBudget && !neverSplitFits(size, pageHasContent) {
				break
			}
			turn := turns[idx]
			result.ChangedTurns = append(result.ChangedTurns, turn)
			result.TurnTokens[turn.TurnID] = currentTokens[turn.TurnID]
			pageConsumed += size
		}
	}

	// 全字段序列化 ≤48MiB 硬顶终检（防御性：构造路径按预算切片，此处兜底断言）。
	if anchorSize+pageConsumed+anchorNewerMandatoryReserve > anchorNewerTotalHardCapBytes {
		return AnchorNewerResult{}, fmt.Errorf("anchor-newer page exceeded the 48MiB total hard cap (anchor=%d content=%d)", anchorSize, pageConsumed)
	}

	// 剩余量与修正游标（自包含：位置由当前 kernel + 复查集重建）。
	remainingTurns := len(turns) - turnCursor
	deliveredChanged := make(map[string]bool, len(result.ChangedTurns))
	for _, turn := range result.ChangedTurns {
		deliveredChanged[turn.TurnID] = true
	}
	remainingChanged := 0
	for _, changedID := range changedIDs {
		if !deliveredChanged[changedID] && kernelMembership[changedID] {
			remainingChanged++
		}
	}
	if remainingTurns > 0 || remainingChanged > 0 {
		nextState := anchorNewerCursorState{
			V:              1,
			LastTurnID:     cursorState.LastTurnID,
			ChangedThrough: cursorState.ChangedThrough,
		}
		if len(result.Turns) > 0 {
			nextState.LastTurnID = result.Turns[len(result.Turns)-1].TurnID
		}
		if len(result.ChangedTurns) > 0 {
			// changedTurns 按 turnId 升序交付；游标记最后交付项。
			lastChanged := result.ChangedTurns[len(result.ChangedTurns)-1].TurnID
			nextState.ChangedThrough = lastChanged
		}
		cursor, cursorErr := encodeAnchorNewerCursor(nextState)
		if cursorErr != nil {
			return AnchorNewerResult{}, cursorErr
		}
		result.CorrectionsPending = &AnchorNewerCorrectionsPending{
			Cursor:    cursor,
			Remaining: remainingTurns + remainingChanged,
		}
	}

	result.ScalarState = AnchorNewerScalarState{
		Execution:         proj.Execution,
		PlanMode:          proj.PlanMode,
		SessionMode:       proj.SessionMode,
		CollaborationMode: proj.CollaborationMode,
		CodexGoal:         proj.CodexGoal,
		Goal:              proj.Goal,
	}
	return result, nil
}
