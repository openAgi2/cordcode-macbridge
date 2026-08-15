package codex

// user_input_unit_test.go 覆盖结构化用户输入的纯逻辑：稳定 ID 派生（§6.1）、
// question 规范化（§8.1 step3）、JSON-RPC envelope 分类（§8.1）以及 pending registry
// 状态机 + first-writer-wins + 幂等（§7/§8/§12）。这些不变量是 P1 adapter 接线后端到端
// 行为的基础，先在纯函数层锁死，避免在 transport 集成层才发现漂移。
//
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// --- ID 派生（§6.1）---

func TestDeriveCodexInteractionIDDeterministic(t *testing.T) {
	a := deriveCodexInteractionID("string", "req-1", "th", "tu", "it")
	b := deriveCodexInteractionID("string", "req-1", "th", "tu", "it")
	if a != b {
		t.Fatalf("相同输入应派生相同 interactionId: %q != %q", a, b)
	}
}

func TestDeriveCodexInteractionIDShape(t *testing.T) {
	id := deriveCodexInteractionID("string", "req-1", "th", "tu", "it")
	if !strings.HasPrefix(id, suiInteractionPrefix) {
		t.Fatalf("interactionId 应有 %q 前缀: %q", suiInteractionPrefix, id)
	}
	hex := strings.TrimPrefix(id, suiInteractionPrefix)
	if len(hex) != suiHexLen {
		t.Fatalf("interactionId hex 部分应 %d 字符，实际 %d (%q)", suiHexLen, len(hex), hex)
	}
	for _, c := range hex {
		isLowerHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isLowerHex {
			t.Fatalf("interactionId 必须全小写十六进制，含 %q (%q)", c, hex)
		}
	}
}

// 关键护栏（§6.1）：requestIdType 区分 string|int64，避免数字 1 与字符串 "1" 碰撞。
func TestDeriveCodexInteractionIDRequestTypeDisambiguates(t *testing.T) {
	asStr := deriveCodexInteractionID("string", "1", "th", "tu", "it")
	asInt := deriveCodexInteractionID("int64", "1", "th", "tu", "it")
	if asStr == asInt {
		t.Fatalf("requestIdType 必须参与 hash：string \"1\" 与 int64 \"1\" 不得派生同一 interactionId")
	}
}

func TestDeriveCodexInteractionIDInputSensitivity(t *testing.T) {
	base := deriveCodexInteractionID("string", "req-1", "th", "tu", "it")
	for _, mod := range []struct {
		name                 string
		typ, rid, th, tu, it string
	}{
		{"requestID", "string", "req-2", "th", "tu", "it"},
		{"threadID", "string", "req-1", "thX", "tu", "it"},
		{"turnID", "string", "req-1", "th", "tuX", "it"},
		{"itemID", "string", "req-1", "th", "tu", "itX"},
	} {
		got := deriveCodexInteractionID(mod.typ, mod.rid, mod.th, mod.tu, mod.it)
		if got == base {
			t.Fatalf("改动 %s 必须改变 interactionId", mod.name)
		}
	}
}

func TestCodexRequestIDTypeClassification(t *testing.T) {
	cases := []struct {
		raw           any
		wantType      string
		wantCanonical string
		wantOK        bool
	}{
		{"abc", "string", "abc", true},
		{float64(42), "int64", "42", true},
		{float64(1.5), "int64", "1.5", true},
		{int64(7), "int64", "7", true},
		{nil, "", "", false},
		{[]string{"x"}, "", "", false},
	}
	for _, c := range cases {
		typ, canon, ok := codexRequestIDType(c.raw)
		if typ != c.wantType || canon != c.wantCanonical || ok != c.wantOK {
			t.Fatalf("codexRequestIDType(%v) = (%q,%q,%v) want (%q,%q,%v)",
				c.raw, typ, canon, ok, c.wantType, c.wantCanonical, c.wantOK)
		}
	}
}

func TestQuestionAndOptionIDFormat(t *testing.T) {
	iid := "ui_abcd"
	if got := deriveQuestionID(iid, 0); got != "ui_abcd_q_0" {
		t.Fatalf("questionId 格式错: %q", got)
	}
	if got := deriveOptionID("ui_abcd_q_0", 3); got != "ui_abcd_q_0_o_3" {
		t.Fatalf("optionId 格式错: %q", got)
	}
}

// --- question 规范化（§8.1 step 3）---

func suiPtrBool(b bool) *bool { return &b }

func TestNormalizeCodexQuestionsWithOptionsIsSingle(t *testing.T) {
	iid := deriveCodexInteractionID("string", "r", "th", "tu", "it")
	raw := []codexRawQuestion{
		{
			ID: "q-backend-1", Header: "Color", Question: "Which color?",
			IsOther: suiPtrBool(true), Options: []codexRawOption{
				{Label: "Red", Description: "r"},
				{Label: "Blue"},
			},
		},
	}
	out := normalizeCodexQuestions(iid, raw)
	if len(out) != 1 {
		t.Fatalf("应产出 1 题，实际 %d", len(out))
	}
	q := out[0]
	if q.ID != deriveQuestionID(iid, 0) {
		t.Fatalf("questionId 应按 index 派生: %q", q.ID)
	}
	if q.AnswerMode != core.UserInputAnswerModeSingle {
		t.Fatalf("有 options 应为 single，实际 %q", q.AnswerMode)
	}
	if !q.AllowsCustomAnswer {
		t.Fatalf("allowsCustomAnswer 应取 question-level isOther=true")
	}
	if !q.Required {
		t.Fatalf("Codex 每题恒 required=true")
	}
	if q.Prompt != "Which color?" || q.Header != "Color" {
		t.Fatalf("header/question 映射错: header=%q prompt=%q", q.Header, q.Prompt)
	}
	if len(q.Options) != 2 {
		t.Fatalf("应保留 2 个 option，实际 %d", len(q.Options))
	}
	want0 := deriveOptionID(q.ID, 0)
	if q.Options[0].ID != want0 {
		t.Fatalf("option[0].id 应按 index 派生 %q，实际 %q", want0, q.Options[0].ID)
	}
	if q.Options[0].Label != "Red" || q.Options[0].Description != "r" {
		t.Fatalf("option[0] label/description 映射错: %+v", q.Options[0])
	}
}

func TestNormalizeCodexQuestionsMissingOptionsIsText(t *testing.T) {
	iid := deriveCodexInteractionID("string", "r", "th", "tu", "it")
	// options 缺失
	out := normalizeCodexQuestions(iid, []codexRawQuestion{
		{ID: "q1", Header: "Name", Question: "Your name?", IsSecret: suiPtrBool(true)},
	})
	if len(out) != 1 || out[0].AnswerMode != core.UserInputAnswerModeText {
		t.Fatalf("缺失 options 应为 text 题")
	}
	if len(out[0].Options) != 0 {
		t.Fatalf("text 题不应有 option row")
	}
	if !out[0].AllowsCustomAnswer {
		t.Fatalf("text 题固定 allowsCustomAnswer=true")
	}
	if !out[0].IsSecret {
		t.Fatalf("isSecret=true 应透传")
	}
}

func TestNormalizeCodexQuestionsEmptyOptionsIsText(t *testing.T) {
	iid := deriveCodexInteractionID("string", "r", "th", "tu", "it")
	// options=[] 与缺失/null 同义，不能判 malformed
	out := normalizeCodexQuestions(iid, []codexRawQuestion{
		{ID: "q1", Question: "q", Options: []codexRawOption{}},
	})
	if out[0].AnswerMode != core.UserInputAnswerModeText {
		t.Fatalf("空 options 应归一为 text，实际 %q", out[0].AnswerMode)
	}
}

func TestNormalizeCodexQuestionsIsOtherFalseDisablesCustom(t *testing.T) {
	iid := deriveCodexInteractionID("string", "r", "th", "tu", "it")
	out := normalizeCodexQuestions(iid, []codexRawQuestion{
		{ID: "q1", Question: "q", IsOther: suiPtrBool(false), Options: []codexRawOption{{Label: "a"}}},
	})
	if out[0].AllowsCustomAnswer {
		t.Fatalf("isOther=false 时 allowsCustomAnswer 必须 false")
	}
}

// --- envelope 分类（§8.1）---

func TestClassifyRPCEnvelope(t *testing.T) {
	cases := []struct {
		name                    string
		method, id, result, err bool
		want                    rpcEnvelopeKind
	}{
		{"server request", true, true, false, false, envelopeServerRequest},
		{"server request may carry result-less", true, true, false, false, envelopeServerRequest},
		{"notification", true, false, false, false, envelopeNotification},
		{"response with result", false, true, true, false, envelopeResponse},
		{"response with error", false, true, false, true, envelopeResponse},
		{"id only no method no result/error", false, true, false, false, envelopeMalformed},
		{"nothing", false, false, false, false, envelopeMalformed},
		{"result without id or method", false, false, true, false, envelopeMalformed},
	}
	for _, c := range cases {
		got := classifyRPCEnvelope(c.method, c.id, c.result, c.err)
		if got != c.want {
			t.Fatalf("%s: classify(method=%v,id=%v,result=%v,err=%v)=%s want %s",
				c.name, c.method, c.id, c.result, c.err, got, c.want)
		}
	}
}

// 关键护栏：method+id 必须判为 server request，而非 response——这是当前 driver 误判的根因（§3.2/§8.1）。
func TestClassifyRPCEnvelopeServerRequestNotResponse(t *testing.T) {
	// item/tool/requestUserInput 携带 method+id+params；当前 handleRPCMessage 因“有 id”误当 response。
	got := classifyRPCEnvelope(true, true, false, false)
	if got != envelopeServerRequest {
		t.Fatalf("method+id 必须是 server_request（当前 bug 把它当 response），实际 %s", got)
	}
}

// --- pending registry 状态机（§7/§8/§12）---

func sampleEntry(iid string) pendingEntry {
	return pendingEntry{
		interactionID:      iid,
		requestIDCanonical: "req-1",
		rawRequestID:       json.RawMessage(`"req-1"`),
		rawQuestionID:      map[string]string{deriveQuestionID(iid, 0): "backend-q-1"},
		optionLabel:        map[string]string{deriveOptionID(deriveQuestionID(iid, 0), 0): "Red"},
		questionMode:       map[string]core.UserInputAnswerMode{deriveQuestionID(iid, 0): core.UserInputAnswerModeSingle},
		questionOrder:      []string{deriveQuestionID(iid, 0)},
	}
}

func TestRegistryRegisterAndStatus(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	if !r.Register(sampleEntry(iid)) {
		t.Fatalf("首次 Register 应成功")
	}
	if r.Register(sampleEntry(iid)) {
		t.Fatalf("重复 Register 同 interactionID 不得覆盖")
	}
	if r.Status(iid) != registryPending {
		t.Fatalf("Register 后应为 pending")
	}
	if r.Status("missing") != registryAbsent {
		t.Fatalf("不存在应 absent")
	}
	// 反查索引（serverRequest/resolved 只带 requestId）
	if got, ok := r.LookupByRequestID("req-1"); !ok || got != iid {
		t.Fatalf("LookupByRequestID(req-1) = (%q,%v) want (%q,true)", got, ok, iid)
	}
	if _, ok := r.LookupByRequestID("unknown"); ok {
		t.Fatalf("未知 requestId 不应命中")
	}
	r.Remove(iid)
	if _, ok := r.LookupByRequestID("req-1"); ok {
		t.Fatalf("Remove 后反查索引应清除")
	}
}

func TestRegistryClaimFirstWriterWins(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))

	d1 := r.Claim(iid, "client-A")
	if !d1.claimed || d1.snapshot == nil {
		t.Fatalf("首个 Claim 应成功并返回 snapshot")
	}
	if string(d1.snapshot.RawRequestID) != `"req-1"` {
		t.Fatalf("snapshot 应携带原始 request id 供写 wire envelope，实际 %s", d1.snapshot.RawRequestID)
	}
	if r.Status(iid) != registryClaimed {
		t.Fatalf("Claim 后应为 claimed")
	}

	// 第二个 client 并发提交：first-writer-wins，不再 claim。
	d2 := r.Claim(iid, "client-B")
	if d2.claimed {
		t.Fatalf("已被 claim 时第二个 client 不得再 claim（first-writer-wins）")
	}
	if d2.status != registryClaimed {
		t.Fatalf("竞争失败状态应为 claimed，实际 %v", d2.status)
	}
}

func TestRegistryConfirmResolved(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	r.Claim(iid, "client-A")
	if !r.ConfirmResolved(iid, "client-A", "ios") {
		t.Fatalf("claimed→resolved 应成功")
	}
	if r.Status(iid) != registryResolved {
		t.Fatalf("ConfirmResolved 后应为 resolved")
	}
	// 再次 ConfirmResolved 不应成功（已 resolved）。
	if r.ConfirmResolved(iid, "client-A", "ios") {
		t.Fatalf("已 resolved 时 ConfirmResolved 不应再次转移")
	}
}

func TestRegistryReleaseClaimOnWriteFailure(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	r.Claim(iid, "client-A")
	// 模拟 backend response 写失败：释放 claim 回 pending，允许重试。
	if !r.ReleaseClaim(iid) {
		t.Fatalf("claimed→pending 释放应成功")
	}
	if r.Status(iid) != registryPending {
		t.Fatalf("ReleaseClaim 后应回 pending")
	}
	// 释放后可重新 claim。
	d := r.Claim(iid, "client-A-retry")
	if !d.claimed {
		t.Fatalf("释放后应可重新 claim")
	}
}

func TestRegistryMarkExternallyResolvedIdempotent(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	// 外部先解决（serverRequest/resolved）。
	if !r.MarkExternallyResolved(iid) {
		t.Fatalf("pending→resolved(external) 应 changed=true")
	}
	// 再次外部解决：幂等 no-op，不产生第二 revision。
	if r.MarkExternallyResolved(iid) {
		t.Fatalf("已 resolved 时外部解决不得再 changed=true（幂等）")
	}
}

// 关键护栏（§8.3）：本端先回答成功后，迟到的 serverRequest/resolved 是幂等确认，不得产生第二 revision。
func TestRegistryExternalResolvedAfterLocalIsNoOp(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	r.Claim(iid, "ios-client")
	r.ConfirmResolved(iid, "ios-client", "ios")
	// 本端已 resolved；迟到的外部 resolved notification：
	if r.MarkExternallyResolved(iid) {
		t.Fatalf("本端已 resolved 时外部 resolved 不得 changed=true（幂等确认，无第二 revision）")
	}
}

func TestRegistryClientActionIdempotency(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	// 第一次提交（client-X）：claim → 写成功 → confirm。
	r.Claim(iid, "client-X")
	r.ConfirmResolved(iid, "client-X", "ios")
	// 同 clientActionID 重试（网络层重试）：返回缓存 outcome，不重复 claim/写。
	d := r.Claim(iid, "client-X")
	if d.claimed {
		t.Fatalf("幂等重试不得再次 claim/写 backend response")
	}
	if d.outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("幂等重试应返回缓存 outcome=accepted，实际 %q", d.outcome)
	}
}

func TestRegistryClaimAbsentAndResolved(t *testing.T) {
	r := newUserInputRegistry()
	if d := r.Claim("missing", "c"); d.status != registryAbsent {
		t.Fatalf("不存在的 interaction Claim 应 absent")
	}
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	r.MarkExternallyResolved(iid)
	d := r.Claim(iid, "late-client")
	if d.claimed || d.outcome != core.UserInputOutcomeAlreadyResolved {
		t.Fatalf("已 resolved 的 Claim 应返回 already_resolved，不 claim；claimed=%v outcome=%q", d.claimed, d.outcome)
	}
}

// 并发 first-writer-wins：N 个 goroutine 同时 Claim 同一 pending interaction，恰有一个成功。
func TestRegistryConcurrentClaimSingleWinner(t *testing.T) {
	r := newUserInputRegistry()
	iid := "ui_x"
	r.Register(sampleEntry(iid))
	const N = 32
	var wg sync.WaitGroup
	wins := make(chan bool, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			d := r.Claim(iid, "client")
			wins <- d.claimed
		}(i)
	}
	wg.Wait()
	close(wins)
	count := 0
	for w := range wins {
		if w {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("并发 Claim 应恰有 1 个 winner，实际 %d", count)
	}
}
