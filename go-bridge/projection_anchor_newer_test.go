package gobridge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// anchor-newer 切片行为测试（S-7，方案 v30 §7.1.5 核心；纯函数级——
// sliceAnchorNewerReconciliation 在一个 SessionProjection 上验证打包/对账/预算/
// 分页纪律，handler 级（fence/held/trace/capability gate）由 spec 与既有同族
// 机制测试覆盖）。

// anchorNewerTurnWithSerializedSize 构造序列化后精确等于 target 字节的 turn
// （两遍法：先测零 padding 基线，再按差值补 'a'——纯 ASCII 无转义，1 字符 = 1 字节）。
func anchorNewerTurnWithSerializedSize(t *testing.T, id string, target int) TurnProjection {
	t.Helper()
	build := func(pad int) TurnProjection {
		return TurnProjection{
			TurnID: id,
			Status: "completed",
			Assistant: &MessageProjection{
				ID:   id + "-assistant",
				Role: "assistant",
				Parts: []ProjectionPart{
					{Type: "text", Text: strings.Repeat("a", pad)},
				},
			},
		}
	}
	base := build(0)
	raw, err := marshalAnchorNewerTurnForTest(base)
	if err != nil {
		t.Fatal(err)
	}
	pad := target - len(raw)
	if pad < 0 {
		pad = 0
	}
	turn := build(pad)
	raw2, err := marshalAnchorNewerTurnForTest(turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw2) != target {
		// 结构性偏差（字段序/嵌套）时按线性关系修正一次。
		turn = build(pad + (target - len(raw2)))
	}
	return turn
}

func marshalAnchorNewerTurnForTest(turn TurnProjection) ([]byte, error) {
	return json.Marshal(turn)
}

type anchorNewerFixtureProjection struct {
	proj   SessionProjection
	ids    []string
	tokens map[string]string
}

// anchorNewerProjection 构造 N 个指定序列化大小的 turns（顺序即 kernel 顺序）。
func anchorNewerProjection(t *testing.T, sizes ...int) anchorNewerFixtureProjection {
	t.Helper()
	proj := SessionProjection{
		SessionID: "anchor-test",
		SyncRev:   531,
		Execution: ExecutionView{Phase: "running"},
	}
	tokens := make(map[string]string, len(sizes))
	ids := make([]string, 0, len(sizes))
	for i, size := range sizes {
		id := fmt.Sprintf("turn-%03d", i)
		turn := anchorNewerTurnWithSerializedSize(t, id, size)
		proj.Turns = append(proj.Turns, turn)
		ids = append(ids, id)
		token, err := AnchorNewerTurnToken(turn)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = token
	}
	return anchorNewerFixtureProjection{proj: proj, ids: ids, tokens: tokens}
}

func digestPtr(v string) *string { return &v }

// ① 锚点命中：turns 严格后继 + anchorRefresh 仅首包 + token 同帧。
func TestAnchorNewerAnchorHitStrictSuccessorAndFirstPageOnlyRefresh(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000, 1_000, 1_000, 1_000)
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[2],
		BaseDigests: map[string]*string{
			f.ids[0]: digestPtr(f.tokens[f.ids[0]]),
			f.ids[1]: digestPtr(f.tokens[f.ids[1]]),
			f.ids[2]: digestPtr(f.tokens[f.ids[2]]),
		},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 2 || result.Turns[0].TurnID != f.ids[3] || result.Turns[1].TurnID != f.ids[4] {
		t.Fatalf("turns = %v, want strict successors [3 4]", turnIDsOfAnchorNewer(result.Turns))
	}
	if result.AnchorRefresh == nil || result.AnchorRefresh.TurnID != f.ids[2] {
		t.Fatalf("anchorRefresh missing or wrong turn on first page")
	}
	if len(result.ChangedTurns) != 0 {
		t.Fatalf("matching digests must not produce changedTurns, got %v", turnIDsOfAnchorNewer(result.ChangedTurns))
	}
	for _, id := range []string{f.ids[2], f.ids[3], f.ids[4]} {
		if result.TurnTokens[id] == "" {
			t.Fatalf("turnTokens missing for delivered turn %s", id)
		}
	}
	if result.CorrectionsPending != nil {
		t.Fatalf("fully delivered page must not carry correctionsPending")
	}
	if result.SyncRev != 531 {
		t.Fatalf("syncRev = %d, want kernel head", result.SyncRev)
	}

	// 续页（即使无剩余，用 cursor 重放验证幂等形态：不携带 anchorRefresh）。
	cursor := encodeAnchorNewerCursorForTest(t, anchorNewerCursorState{V: 1, LastTurnID: f.ids[4]})
	params2 := AnchorNewerParams{AnchorTurnID: f.ids[2], BaseDigests: params.BaseDigests, CorrectionCursor: cursor}
	result2, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	if result2.AnchorRefresh != nil {
		t.Fatalf("continuation page must not repeat anchorRefresh")
	}
	if len(result2.Turns) != 0 {
		t.Fatalf("replayed cursor at tail must produce zero turns")
	}
}

// ①' anchor == tail：零新增空 turns + anchorRefresh 仍携带终态。
func TestAnchorNewerAnchorAtTailEmptyTurnsWithRefresh(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000)
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[1],
		BaseDigests:  map[string]*string{f.ids[0]: digestPtr(f.tokens[f.ids[0]]), f.ids[1]: digestPtr(f.tokens[f.ids[1]])},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 0 {
		t.Fatalf("anchor at tail must yield empty turns")
	}
	if result.AnchorRefresh == nil || result.AnchorRefresh.TurnID != f.ids[1] {
		t.Fatalf("anchor==tail must still carry anchorRefresh terminal state")
	}
}

// ①'' 锚点离开保留区 → typed stale（errAnchorNewerStale）。
func TestAnchorNewerAnchorStale(t *testing.T) {
	f := anchorNewerProjection(t, 1_000)
	params := AnchorNewerParams{AnchorTurnID: "turn-gone", BaseDigests: map[string]*string{}}
	if _, err := sliceAnchorNewerReconciliation(f.proj, params); err == nil {
		t.Fatal("missing anchor must error")
	}
}

// ② 对账：token 不一致 → changedTurns；null → 必须回传；移除 → removedTurnIds。
func TestAnchorNewerDigestReconciliation(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000, 1_000, 1_000)
	staleToken := "stale-token-value"
	var nullToken *string
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[3],
		BaseDigests: map[string]*string{
			f.ids[0]: digestPtr(staleToken), // 离线修改（digest 不一致）
			f.ids[1]: nullToken,             // token 失效（null 必须回传）
			f.ids[2]: digestPtr(f.tokens[f.ids[2]]), // 命中：无修改
		},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	changed := turnIDsOfAnchorNewer(result.ChangedTurns)
	// changedTurns 按 turnId 升序。
	if len(changed) != 2 || changed[0] != f.ids[0] || changed[1] != f.ids[1] {
		t.Fatalf("changedTurns = %v, want [0 1] ascending", changed)
	}
	if result.TurnTokens[f.ids[1]] == "" {
		t.Fatalf("null-digest turn must be returned with a fresh token")
	}
}

func TestAnchorNewerRemovedTombstones(t *testing.T) {
	f := anchorNewerProjection(t, 1_000)
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[0],
		BaseDigests: map[string]*string{
			"turn-compacted": digestPtr("whatever"),
			f.ids[0]:         digestPtr(f.tokens[f.ids[0]]),
		},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedTurnIDs) != 1 || result.RemovedTurnIDs[0] != "turn-compacted" {
		t.Fatalf("removedTurnIds = %v, want [turn-compacted]", result.RemovedTurnIDs)
	}
}

// ③ 预算边界：46MiB anchor 恰满合法（首包对账空间 0）；46MiB+1 → typed oversized。
func TestAnchorNewerBoundaryExactQuotaVsOversized(t *testing.T) {
	mk := func(size int) anchorNewerFixtureProjection {
		return anchorNewerProjection(t, size, 1_000)
	}
	ok := mk(46 << 20)
	params := AnchorNewerParams{AnchorTurnID: ok.ids[0], BaseDigests: map[string]*string{ok.ids[0]: digestPtr(ok.tokens[ok.ids[0]])}}
	result, err := sliceAnchorNewerReconciliation(ok.proj, params)
	if err != nil {
		t.Fatalf("exactly-46MiB anchor must be legal: %v", err)
	}
	if result.AnchorRefresh == nil || len(result.Turns) != 0 || result.CorrectionsPending == nil {
		t.Fatalf("exact-quota anchor: first page = anchor-only + correctionsPending")
	}
	// 续页交付 1KiB 后继（水位线前移）。
	cursor := decodeFirstPendingCursorForTest(t, result)
	params2 := AnchorNewerParams{AnchorTurnID: ok.ids[0], BaseDigests: map[string]*string{ok.ids[0]: digestPtr(ok.tokens[ok.ids[0]])}, CorrectionCursor: cursor}
	result2, err := sliceAnchorNewerReconciliation(ok.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Turns) != 1 || result2.Turns[0].TurnID != ok.ids[1] {
		t.Fatalf("46MiB anchor + 1KiB successor must deliver via continuation, turns = %v", turnIDsOfAnchorNewer(result2.Turns))
	}
	if result2.AnchorRefresh != nil {
		t.Fatalf("continuation must not repeat anchorRefresh")
	}

	over := mk((46 << 20) + 1)
	paramsOver := AnchorNewerParams{AnchorTurnID: over.ids[0], BaseDigests: map[string]*string{over.ids[0]: nil}}
	if _, err := sliceAnchorNewerReconciliation(over.proj, paramsOver); err == nil {
		t.Fatal("46MiB+1 anchor must fail typed")
	}
}

// ③' 单个 changedTurn 9MiB：超 8MiB 对账预算 → 本页独占（never-split ≤46MiB）。
// 真实场景：基底缓存中的旧 turn 离线修改（后继 turns 经 turn 阶段交付当前终态，
// 不重复进 changedTurns——合并为一条规则）。
func TestAnchorNewerOversizedChangedTurnOwnPage(t *testing.T) {
	// kernel: [A(1KB), B(9MiB), C(1KB)]；anchor = C；A/B 离线修改（stale digest）。
	f := anchorNewerProjection(t, 1_000, 9<<20, 1_000)
	anchor := f.ids[2]
	params := AnchorNewerParams{
		AnchorTurnID: anchor,
		BaseDigests: map[string]*string{
			f.ids[0]: digestPtr("stale"),
			f.ids[1]: digestPtr("stale"),
			anchor:   digestPtr(f.tokens[anchor]),
		},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 0 {
		t.Fatalf("anchor at tail: no successors")
	}
	// 首页：anchorRefresh(1KB) + changed A(1KB)（预算内）；B(9MiB) 超预算且本页
	// 对账内容非空 → 不装，correctionsPending 续拉。
	if len(result.ChangedTurns) != 1 || result.ChangedTurns[0].TurnID != f.ids[0] {
		t.Fatalf("first page changedTurns = %v, want [A]", turnIDsOfAnchorNewer(result.ChangedTurns))
	}
	if result.CorrectionsPending == nil {
		t.Fatal("pending must be set")
	}
	// 续页（对账内容为空）：B(9MiB) never-split 独占交付。
	cursor := decodeFirstPendingCursorForTest(t, result)
	digests := map[string]*string{
		f.ids[0]: digestPtr(result.TurnTokens[f.ids[0]]),
		f.ids[1]: digestPtr("stale"),
		anchor:   digestPtr(f.tokens[anchor]),
	}
	params2 := AnchorNewerParams{AnchorTurnID: anchor, BaseDigests: digests, CorrectionCursor: cursor}
	result2, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.ChangedTurns) != 1 || result2.ChangedTurns[0].TurnID != f.ids[1] {
		t.Fatalf("9MiB changedTurn must be alone on the empty continuation page, got %v", turnIDsOfAnchorNewer(result2.ChangedTurns))
	}
	if result2.CorrectionsPending != nil {
		t.Fatal("no further corrections expected")
	}
	if result2.AnchorRefresh != nil {
		t.Fatal("continuation must not repeat anchorRefresh")
	}
}

// ⑤' 42MiB anchor + 6MiB successor：首包剩余 4MiB < 6MiB → 不回填切页，续页交付。
func TestAnchorNewerLargeAnchorSuccessorPageBoundary(t *testing.T) {
	f := anchorNewerProjection(t, 42<<20, 6<<20, 500)
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[0],
		BaseDigests:  map[string]*string{f.ids[0]: digestPtr(f.tokens[f.ids[0]])},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	// 首包对账空间 = min(8, 48-2-42) = 4MiB < 6MiB → turn phase 不装（相位边界：
	// 装不下即止，不回填）；500B 的第三 turn 也不得越过未交付 turns 提前装页。
	if len(result.Turns) != 0 {
		t.Fatalf("6MiB successor must overflow to continuation (no backfill), got %v", turnIDsOfAnchorNewer(result.Turns))
	}
	if result.CorrectionsPending == nil {
		t.Fatal("pending expected")
	}
	cursor := decodeFirstPendingCursorForTest(t, result)
	params2 := AnchorNewerParams{AnchorTurnID: f.ids[0], BaseDigests: params.BaseDigests, CorrectionCursor: cursor}
	result2, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	// 续页对账预算 8MiB：6MiB + 500B 都装得下。
	if len(result2.Turns) != 2 || result2.Turns[0].TurnID != f.ids[1] {
		t.Fatalf("continuation must deliver both remaining turns, got %v", turnIDsOfAnchorNewer(result2.Turns))
	}
}

// ④' 打包确定性：同一输入两次生成的页序列一致。
func TestAnchorNewerDeterministicPacking(t *testing.T) {
	f := anchorNewerProjection(t, 3<<20, 3<<20, 3<<20, 3<<20)
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[0],
		BaseDigests: map[string]*string{
			f.ids[0]: digestPtr("stale"),
			f.ids[1]: nil,
		},
	}
	first, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	raw1, _ := json.Marshal(first)
	raw2, _ := json.Marshal(second)
	if string(raw1) != string(raw2) {
		t.Fatal("same input must produce byte-identical pages (deterministic packing)")
	}
}

// ⑧ 分页前进：limit=1 翻页仍前进；超大 anchor 不阻塞 turns。
func TestAnchorNewerLimitOneAdvances(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000, 1_000)
	digests := map[string]*string{f.ids[0]: digestPtr(f.tokens[f.ids[0]])}
	params := AnchorNewerParams{AnchorTurnID: f.ids[0], BaseDigests: digests, Limit: 1}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 1 || result.Turns[0].TurnID != f.ids[1] {
		t.Fatalf("limit=1 first page = 1 turn, got %v", turnIDsOfAnchorNewer(result.Turns))
	}
	cursor := decodeFirstPendingCursorForTest(t, result)
	digests[f.ids[1]] = digestPtr(result.TurnTokens[f.ids[1]])
	params2 := AnchorNewerParams{AnchorTurnID: f.ids[0], BaseDigests: digests, Limit: 1, CorrectionCursor: cursor}
	result2, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Turns) != 1 || result2.Turns[0].TurnID != f.ids[2] {
		t.Fatalf("limit=1 continuation must advance to next turn, got %v", turnIDsOfAnchorNewer(result2.Turns))
	}
}

// ⑥ 自包含 cursor：幂等重放（同 cursor 两次结果一致）；跨 cut 复查（早页确认项
// 页间被修改 → 全集复查捕获重发）。
func TestAnchorNewerCursorReplayIdempotent(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000, 1_000)
	params := AnchorNewerParams{AnchorTurnID: f.ids[0], BaseDigests: map[string]*string{f.ids[0]: nil}, Limit: 1}
	first, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	cursor := decodeFirstPendingCursorForTest(t, first)
	params2 := AnchorNewerParams{AnchorTurnID: f.ids[0], BaseDigests: map[string]*string{f.ids[0]: nil}, Limit: 1, CorrectionCursor: cursor}
	a, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	if string(rawA) != string(rawB) {
		t.Fatal("same cursor replay must be idempotent")
	}
}

func TestAnchorNewerFullReviewSetCatchesMidTransactionChange(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000, 1_000)
	// 首页：确认全部 digest 命中（无修改）。
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[0],
		BaseDigests: map[string]*string{
			f.ids[0]: digestPtr(f.tokens[f.ids[0]]),
			f.ids[1]: digestPtr(f.tokens[f.ids[1]]),
		},
		Limit: 1,
	}
	first, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ChangedTurns) != 0 {
		t.Fatal("matching digests: no corrections expected")
	}
	// 续页前 turn-000 被 live patch 修改（kernel 内容变化 → token 变化）。
	f.proj.Turns[0].Assistant = &MessageProjection{
		ID:    f.ids[0] + "-assistant",
		Role:  "assistant",
		Parts: []ProjectionPart{{Type: "text", Text: "mutated offline"}},
	}
	cursor := decodeFirstPendingCursorForTest(t, first)
	// 复查集不收缩：早页确认项仍携带（客户端缓存 token）→ token 失效 → 重发新终态。
	params2 := AnchorNewerParams{
		AnchorTurnID: f.ids[0],
		BaseDigests: map[string]*string{
			f.ids[0]: digestPtr(first.TurnTokens[f.ids[0]]), // 早页确认项的已收 token
			f.ids[1]: digestPtr(first.TurnTokens[f.ids[1]]),
		},
		Limit:           1,
		CorrectionCursor: cursor,
	}
	second, err := sliceAnchorNewerReconciliation(f.proj, params2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, turn := range second.ChangedTurns {
		if turn.TurnID == f.ids[0] {
			found = true
		}
	}
	if !found {
		t.Fatal("full review set must catch the mid-transaction mutation of an earlier-confirmed turn")
	}
}

// helper: 取首页 correctionsPending 的 cursor（解码并回填 V 校验）。
func decodeFirstPendingCursorForTest(t *testing.T, result AnchorNewerResult) string {
	t.Helper()
	if result.CorrectionsPending == nil {
		t.Fatal("correctionsPending expected")
	}
	state, err := decodeAnchorNewerCursor(result.CorrectionsPending.Cursor)
	if err != nil {
		t.Fatalf("cursor decode: %v", err)
	}
	if state.V != 1 {
		t.Fatalf("cursor version = %d", state.V)
	}
	return result.CorrectionsPending.Cursor
}

func encodeAnchorNewerCursorForTest(t *testing.T, state anchorNewerCursorState) string {
	t.Helper()
	encoded, err := encodeAnchorNewerCursor(state)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func turnIDsOfAnchorNewer(turns []TurnProjection) []string {
	ids := make([]string, 0, len(turns))
	for _, turn := range turns {
		ids = append(ids, turn.TurnID)
	}
	return ids
}

// ⑪ 协商 echo（§7.1.5）：anchor 声明+ssv2 前置 → echo；缺前置 → hello 失败；
// 未声明 → 不 echo；scalar = window_v1 + scalar 双声明 + ok。
func TestAnchorNewerNegotiationEcho(t *testing.T) {
	publisher := NewEventPublisher("anchor-newer-test-epoch")
	conn := &scopeTestConn{}
	s := &Server{eventPublisher: publisher}

	// 未声明 → 不 echo。
	ack := &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2"}}, conn) {
		t.Fatal("ssv2-only hello must pass window negotiation")
	}
	if ack.Capabilities["projection_window_anchor_newer_v1"] || ack.Capabilities["projection_window_scalar_v1"] {
		t.Fatal("undeclared capabilities must not echo")
	}

	// 声明 anchor 缺 ssv2 → hello 失败（同族纪律）。
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"projection_window_anchor_newer_v1"}}, conn) {
		t.Fatal("anchor without ssv2 prerequisite must fail hello")
	}
	if ack.Ok {
		t.Fatal("ack must be marked failed")
	}

	// 声明 anchor + ssv2 → echo + per-conn 标记。
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2", "projection_window_anchor_newer_v1"}}, conn) {
		t.Fatal("declared anchor with ssv2 must pass")
	}
	if !ack.Capabilities["projection_window_anchor_newer_v1"] {
		t.Fatal("declared anchor must echo")
	}
	if !s.eventPublisher.ConnAnchorNewerV1(conn) {
		t.Fatal("per-conn anchor mark must be set")
	}

	// scalar：window_v1 + scalar 双声明 → echo（OD-6 W1 条件 2 协商面）。
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{
		"session_sync_v2", "projection_window_v1", "projection_window_scalar_v1",
	}}, conn) {
		t.Fatal("scalar declarations must pass")
	}
	if !ack.Capabilities["projection_window_scalar_v1"] {
		t.Fatal("scalar (with window declared) must echo")
	}
	if !s.eventPublisher.ConnProjectionWindowScalarV1(conn) {
		t.Fatal("per-conn scalar mark must be set")
	}

	// scalar 无 window_v1 → 不 echo（W1 echo 条件 = 现有 window 条件 + 落地）。
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	if !s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2", "projection_window_scalar_v1"}}, conn) {
		t.Fatal("scalar-only declaration must not fail hello")
	}
	if ack.Capabilities["projection_window_scalar_v1"] {
		t.Fatal("scalar without projection_window_v1 must not echo")
	}
}

// wire 键序（frozen Result 字段集运行时核对）。
func TestAnchorNewerResultWireKeys(t *testing.T) {
	f := anchorNewerProjection(t, 1_000, 1_000)
	params := AnchorNewerParams{
		AnchorTurnID: f.ids[1],
		BaseDigests:  map[string]*string{f.ids[0]: nil},
	}
	result, err := sliceAnchorNewerReconciliation(f.proj, params)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"turns", "anchorRefresh", "changedTurns", "removedTurnIds", "scalarState", "syncRev"} {
		if _, ok := object[key]; !ok {
			t.Fatalf("wire result missing frozen key %s (keys: %v)", key, mapKeysAnchorNewer(object))
		}
	}
	// token 同帧：changedTurn 的 turnTokens 覆盖。
	if result.TurnTokens[f.ids[0]] == "" {
		t.Fatal("changed turn must carry a fresh token")
	}
}

func mapKeysAnchorNewer(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// projectionMaxBytes 协商（F-53）：请求 → 授予 min(请求, 256MiB)；未请求 → 不回显
// （默认 50MiB）；越界请求被钳制。
func TestAnchorNewerProjectionMaxBytesNegotiation(t *testing.T) {
	publisher := NewEventPublisher("pmb-test-epoch")
	conn := &scopeTestConn{}
	s := &Server{eventPublisher: publisher}

	req := int64(100 << 20)
	ack := &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	hello := &HelloMessage{Capabilities: []string{"session_sync_v2"}, ProjectionMaxBytes: &req}
	if !s.negotiateProjectionWindowV1(ack, hello, conn) {
		t.Fatal("valid request must pass")
	}
	if ack.ProjectionMaxBytes == nil || *ack.ProjectionMaxBytes != req {
		t.Fatalf("granted = %v, want %d", ack.ProjectionMaxBytes, req)
	}
	if got := publisher.ProjectionMaxBytes(conn); got != req {
		t.Fatalf("per-conn cap = %d, want %d", got, req)
	}

	over := int64(1 << 30)
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	hello = &HelloMessage{Capabilities: []string{"session_sync_v2"}, ProjectionMaxBytes: &over}
	_ = s.negotiateProjectionWindowV1(ack, hello, conn)
	if ack.ProjectionMaxBytes == nil || *ack.ProjectionMaxBytes != projectionMaxBytesTierCap {
		t.Fatalf("over-tier request must clamp to 256MiB, got %v", ack.ProjectionMaxBytes)
	}

	freshConn := &scopeTestConn{}
	ack = &HelloAckMessage{Type: "hello_ack", Ok: true, Capabilities: map[string]bool{}}
	_ = s.negotiateProjectionWindowV1(ack, &HelloMessage{Capabilities: []string{"session_sync_v2"}}, freshConn)
	if ack.ProjectionMaxBytes != nil {
		t.Fatal("unrequested hello must not echo projectionMaxBytes")
	}
	if got := publisher.ProjectionMaxBytes(freshConn); got != projectionMaxBytesDefault {
		t.Fatalf("default cap = %d, want %d", got, projectionMaxBytesDefault)
	}
}
