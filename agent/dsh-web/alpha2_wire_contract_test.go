package dshweb

// alpha2_wire_contract_test.go — S2 跨版本防御契约门（方案 §5.2 第二项 + A2 证据）。
// alpha.2 活体样本（testdata/commands_goals_alpha2_wire.json，A2 Gate 证据的脱敏
// 副本）钉死七面在两代的共同 wire 形状：首参 agentId（漂移 1——TypeScript 源码参数
// 名 `agent` 不上 wire，`{agent}` 变体被拒 verbatim；任何人把 payload 按 source 改
// 「修」成 agent 这里先红）；execute 第三参 submittedAttachments；goal 四动词
// {agentId, ref}（edit 加 request.objective）；edit 业务负例的官方消息原文（漂移
// 2——线上错误码是 gateway/internal，不是 GOAL_INVALID_EDIT）。dsh 升级改变
// descriptor 时重新取证并更新夹具，不得放宽断言。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// alpha2Probe is one fixture probe row.
type alpha2Probe struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func loadAlpha2Fixture(t *testing.T) map[string]alpha2Probe {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "commands_goals_alpha2_wire.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var doc struct {
		Probes map[string]alpha2Probe `json:"probes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("fixture JSON: %v", err)
	}
	if len(doc.Probes) == 0 {
		t.Fatal("fixture has no probes")
	}
	return doc.Probes
}

func TestAlpha2WireContractSevenSurfaces(t *testing.T) {
	probes := loadAlpha2Fixture(t)

	// 1. 七面 {agent} 变体被拒 verbatim（漂移 1：wire 名两代都是 agentId）。
	negatives := map[string]string{
		"commands/list":    "commands/list {agent} (descriptor negative)",
		"commands/execute": "commands/execute {agent,line,submittedAttachments} (descriptor negative)",
		"goals/get":        "goals/get {agent} (descriptor negative)",
		"goals/pause":      "goals/pause {agent,ref} (descriptor negative)",
		"goals/resume":     "goals/resume {agent,ref} (descriptor negative)",
		"goals/clear":      "goals/clear {agent,ref} (descriptor negative)",
		"goals/edit":       "goals/edit {agent,ref,request} (descriptor negative)",
	}
	for surface, key := range negatives {
		p, ok := probes[key]
		if !ok {
			t.Fatalf("alpha.2 fixture missing negative probe %q", key)
		}
		if p.OK || p.Error == nil {
			t.Fatalf("%s: {agent} variant must be a recorded rejection, got %+v", surface, p)
		}
		if !strings.Contains(p.Error.Message, `missing "agentId"`) ||
			!strings.Contains(p.Error.Message, `unexpected "agent"`) {
			t.Fatalf("%s: rejection must keep verbatim fragments (missing \"agentId\"; unexpected \"agent\"), got %q", surface, p.Error.Message)
		}
	}

	// 2. 桥的序列化键集 == alpha.2 接受的键集（两代一致形状）。
	execArgs, err := json.Marshal(commandsExecuteArgs{
		AgentID: "s", Line: "/plan", SubmittedAttachments: []commandSubmitAttachment{},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertKeySet(t, "commandsExecuteArgs", execArgs, []string{"agentId", "line", "submittedAttachments"})
	var execRaw map[string]json.RawMessage
	_ = json.Unmarshal(execArgs, &execRaw)
	if string(execRaw["submittedAttachments"]) != "[]" {
		t.Fatalf("submittedAttachments must serialize as [] (official no-attachment invocation), got %s", execRaw["submittedAttachments"])
	}

	mutateNoReq, _ := json.Marshal(goalsMutateArgs{AgentID: "s", Ref: goalsRefWire{ID: "g", Revision: 1}})
	assertKeySet(t, "goalsMutateArgs (pause/resume/clear)", mutateNoReq, []string{"agentId", "ref"})
	mutateEdit, _ := json.Marshal(goalsMutateArgs{AgentID: "s", Ref: goalsRefWire{ID: "g", Revision: 1}, Request: &goalsEditReq{Objective: "o"}})
	assertKeySet(t, "goalsMutateArgs (edit)", mutateEdit, []string{"agentId", "ref", "request"})
	var editRaw map[string]json.RawMessage
	_ = json.Unmarshal(mutateEdit, &editRaw)
	if !strings.Contains(string(editRaw["request"]), `"objective"`) {
		t.Fatalf("edit request must carry objective, got %s", editRaw["request"])
	}

	// 3. 正形探针必须在夹具里被接受（alpha.2 侧的接受证据）。
	for _, key := range []string{
		"commands/list {agentId}",
		"commands/execute {agentId,line:/zz-a2-probe-noop,submittedAttachments:[]} (unknown cmd = official no-op)",
		"commands/execute {agentId,line:/goal <objective>} (real command, creates the goal)",
		"goals/get {agentId}",
		"goals/pause {agentId,ref}",
		"goals/resume {agentId,ref}",
		"goals/edit {agentId,ref,request:{objective}}",
		"goals/clear {agentId,ref}",
	} {
		if p, ok := probes[key]; !ok || !p.OK {
			t.Fatalf("alpha.2 positive probe missing or not ok: %q => %+v", key, p)
		}
	}

	// 4. edit 业务负例：官方消息原文（漂移 2——错误码 gateway/internal，非
	// GOAL_INVALID_EDIT；契约钉消息，不钉不存在的 wire 错误码）。
	neg := probes["goals/edit {agentId,ref,request:{}} (business negative: empty request with live goal)"]
	if neg.OK || neg.Error == nil {
		t.Fatalf("edit business negative must be a recorded rejection, got %+v", neg)
	}
	if neg.Error.Code != "gateway/internal" ||
		neg.Error.Message != "goal edit requires objective and/or maxGoalRounds" {
		t.Fatalf("edit business negative must keep the official verbatim (gateway/internal: goal edit requires objective and/or maxGoalRounds), got %s: %q", neg.Error.Code, neg.Error.Message)
	}

	// 5. goal 生命周期收敛（夹具内容的字段级断言，A2 五要素之「字段级断言」）。
	assertGoalView := func(key string, wantPhase string, wantRevision int64) {
		p, ok := probes[key]
		if !ok || !p.OK {
			t.Fatalf("lifecycle probe missing/not ok: %q", key)
		}
		var v struct {
			Revision int64  `json:"revision"`
			Phase    string `json:"phase"`
		}
		if err := json.Unmarshal(p.Value, &v); err != nil {
			t.Fatalf("%s: decode view: %v", key, err)
		}
		if v.Phase != wantPhase || v.Revision != wantRevision {
			t.Fatalf("%s: want phase %q revision %d, got phase %q revision %d", key, wantPhase, wantRevision, v.Phase, v.Revision)
		}
	}
	assertGoalView("goals/get {agentId}", "active", 1)
	assertGoalView("goals/pause {agentId,ref}", "paused", 2)
	assertGoalView("goals/resume {agentId,ref}", "active", 3)
	assertGoalView("goals/edit {agentId,ref,request:{objective}}", "active", 4)
	// clear 返回墓碑 ref {id, revision+1}；get-after-clear 为 null。
	cleared := probes["goals/clear {agentId,ref}"]
	var tomb struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(cleared.Value, &tomb); err != nil || tomb.Revision != 5 {
		t.Fatalf("clear must return tombstone revision 5, got %s", cleared.Value)
	}
	if string(probes["goals/get {agentId} (after clear)"].Value) != "null" {
		t.Fatalf("get after clear must be null, got %s", probes["goals/get {agentId} (after clear)"].Value)
	}
}

func assertKeySet(t *testing.T, what string, raw []byte, want []string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: decode: %v", what, err)
	}
	if len(m) != len(want) {
		t.Fatalf("%s: key set must be exactly %v, got %v", what, want, payloadKeys(m))
	}
	for _, k := range want {
		if _, has := m[k]; !has {
			t.Fatalf("%s: missing key %q; key set = %v", what, k, payloadKeys(m))
		}
	}
}

// TestDescriptorStatusMessage pins the §2.4 version note: version + CLI path
// only (credential-free by construction), memoized across descriptor polls.
func TestDescriptorStatusMessage(t *testing.T) {
	old := runVersionProbe
	runs := 0
	runVersionProbe = func(bin string) (string, error) {
		runs++
		if bin != "/fake/dsh" {
			t.Fatalf("probe must run the resolved binary, got %q", bin)
		}
		return "0.1.7-alpha.2\n", nil
	}
	defer func() { runVersionProbe = old }()

	a := &Agent{}
	a.resolver = NewResolver()
	a.resolver.binPath = "/fake/dsh"

	msg := a.DescriptorStatusMessage()
	if msg != "dsh 0.1.7-alpha.2 (cli /fake/dsh)" {
		t.Fatalf("statusMessage = %q, want version + cli path only", msg)
	}
	// Memoized: the second descriptor poll must not re-pay the probe.
	if msg2 := a.DescriptorStatusMessage(); msg2 != msg || runs != 1 {
		t.Fatalf("second call must reuse the memoized verdict (runs=%d, msg2=%q)", runs, msg2)
	}

	// Probe failure degrades to the source-only note, never to an error state.
	runVersionProbe = func(string) (string, error) { return "", os.ErrDeadlineExceeded }
	a.version.mu.Lock()
	a.version.expiresAt = time.Now().Add(-time.Second) // force expiry past the 60s TTL
	a.version.mu.Unlock()
	if msg3 := a.DescriptorStatusMessage(); msg3 != "dsh (cli /fake/dsh)" {
		t.Fatalf("probe failure must degrade to source-only note, got %q", msg3)
	}
}
