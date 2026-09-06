package dshweb

// 并行子代理 workflow 折叠——单一折叠真值，live codec 与 history 冷拉共用
// （command_fold.go 同构先例）。
//
// 官方源码锚点（/Users/jacklee/Projects/deepseek-harness，HEAD d347e70 =
// release/dsh-0.1.3-alpha.1，2026-09-06 核验）：
//   - 折叠 packages/client/ui-workflow-run/src/client/workflow-definition.ts
//     workflowRunDefinition：四事件按 runId 聚成一个 keyed chat 节点；state
//     {name, stopReason?, members[]}，member {seq, label, phase?, childId,
//     outcome?}；updateAgentStart 追加（phase undefined → 不落字段）、
//     updateAgentEnd 按 seq 置 outcome、run-end 置 stopReason。
//   - 视图投影 projectWorkflow：成员按 phase 身份分组（workflowPhaseKey：
//     undefined→null→"missing"，""→"value:0:" 独立身份），成员状态 = outcome
//     映射（completed/failed/cancelled）或 running；run 状态 = stopReason 映射
//     （completed/cancelled/error→completed/cancelled/failed）或 running。
//     interrupted 是视图期 locationClosed 推断（锚点 turn/step 闭合且无终因）——
//     折叠层不知 turn 闭合，本包不产 interrupted；CordCode 由投影 reducer 在
//     turn 终态注入（记录差异：CordCode turn 粒度，官方 step/turn 粒度）。
//   - 不变量 packages/workflow/tool-workflow/src/invariant.ts：run-start
//     runId/name 非空且 run 不重复；agent-start 需开放 run、seq 正整数、label
//     string、phase 存在时必须 string、childId 非空、seq 不重复；agent-end 需
//     成员存在且未结算、outcome ∈ completed|failed|cancelled；run-end 需
//     stopReason ∈ completed|cancelled|error 且无未结算成员。官方在 append 时
//     保证；此处镜像为 codec resetf 依据。
//   - 真实 journal 样本 session-3eacd40e（2026-09-06 owner 事故会话，6 runs，
//     含 failed 成员与重试 run；全部 agent-start 无 phase 字段）。

import (
	"errors"
	"fmt"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// workflowMemberState 镜像官方 WorkflowMemberState（phase nil = 官方 undefined
// 缺席；outcome "" = agent-end 未到）。
type workflowMemberState struct {
	seq     int
	label   string
	phase   *string
	childID string
	outcome string
}

// workflowRunState 镜像官方 WorkflowState（stopReason "" = run-end 未到）。
type workflowRunState struct {
	name       string
	stopReason string
	members    []workflowMemberState
}

// workflowFold 持有全部 run 的折叠状态，按 runId 索引。
type workflowFold struct {
	runs map[string]*workflowRunState
}

// workflowRunStatusFromStopReason 镜像官方 statusFromStopReason（error→failed）。
func workflowRunStatusFromStopReason(stopReason string) string {
	switch stopReason {
	case "completed":
		return core.WorkflowStatusCompleted
	case "cancelled":
		return core.WorkflowStatusCancelled
	case "error":
		return core.WorkflowStatusFailed
	}
	return core.WorkflowStatusRunning
}

// workflowMemberStatusFromOutcome 镜像官方 statusFromOutcome（无 outcome = running）。
func workflowMemberStatusFromOutcome(outcome string) string {
	switch outcome {
	case "completed", "failed", "cancelled":
		return outcome
	}
	return core.WorkflowStatusRunning
}

// onRunStart 镜像官方 start + invariant（重复 run = 违规）。
func (f *workflowFold) onRunStart(runID, name string) error {
	if runID == "" || name == "" {
		return fmt.Errorf("tool-workflow/run-start missing runId/name")
	}
	if f.runs == nil {
		f.runs = map[string]*workflowRunState{}
	}
	if _, ok := f.runs[runID]; ok {
		return fmt.Errorf("tool-workflow/run-start repeats run %s", runID)
	}
	f.runs[runID] = &workflowRunState{name: name}
	return nil
}

// onAgentStart 镜像官方 updateAgentStart + invariant（开放 run、seq 正整数、
// 不重复成员）。
func (f *workflowFold) onAgentStart(runID string, seq int, label string, phase *string, childID string) error {
	run := f.runs[runID]
	if run == nil {
		return fmt.Errorf("tool-workflow/agent-start has no matching run-start for run %s", runID)
	}
	if run.stopReason != "" {
		return fmt.Errorf("tool-workflow/agent-start appears after run-end for run %s", runID)
	}
	if seq < 1 {
		return fmt.Errorf("tool-workflow/agent-start member seq %d must be a positive integer", seq)
	}
	if childID == "" {
		return fmt.Errorf("tool-workflow/agent-start missing childId")
	}
	for _, m := range run.members {
		if m.seq == seq {
			return fmt.Errorf("tool-workflow/agent-start repeats member seq %d in run %s", seq, runID)
		}
	}
	run.members = append(run.members, workflowMemberState{seq: seq, label: label, phase: phase, childID: childID})
	return nil
}

// onAgentEnd 镜像官方 updateAgentEnd + invariant（成员存在、未结算、outcome 枚举）。
func (f *workflowFold) onAgentEnd(runID string, seq int, outcome string) error {
	run := f.runs[runID]
	if run == nil {
		return fmt.Errorf("tool-workflow/agent-end has no matching run-start for run %s", runID)
	}
	if run.stopReason != "" {
		return fmt.Errorf("tool-workflow/agent-end appears after run-end for run %s", runID)
	}
	switch outcome {
	case "completed", "failed", "cancelled":
	default:
		return fmt.Errorf("tool-workflow/agent-end outcome %q is invalid", outcome)
	}
	for i := range run.members {
		if run.members[i].seq == seq {
			if run.members[i].outcome != "" {
				return fmt.Errorf("tool-workflow/agent-end repeats member seq %d in run %s", seq, runID)
			}
			run.members[i].outcome = outcome
			return nil
		}
	}
	return fmt.Errorf("tool-workflow/agent-end has no matching member seq %d in run %s", seq, runID)
}

// onRunEnd 镜像官方 run-end + invariant（stopReason 枚举、无未结算成员）。
func (f *workflowFold) onRunEnd(runID, stopReason string) error {
	run := f.runs[runID]
	if run == nil {
		return fmt.Errorf("tool-workflow/run-end has no matching run-start for run %s", runID)
	}
	if run.stopReason != "" {
		return fmt.Errorf("tool-workflow/run-end repeats run %s", runID)
	}
	switch stopReason {
	case "completed", "cancelled", "error":
	default:
		return fmt.Errorf("tool-workflow/run-end stopReason %q is invalid", stopReason)
	}
	for _, m := range run.members {
		if m.outcome == "" {
			return fmt.Errorf("tool-workflow/run-end leaves member seq %d open in run %s", m.seq, runID)
		}
	}
	run.stopReason = stopReason
	return nil
}

// snapshot 镜像官方 projectWorkflow（无 interrupted——locationClosed 由投影层
// 注入）：phase 分组按首现顺序，nil=未分阶段与 ""=空阶段名是两个身份。
func (f *workflowFold) snapshot(runID string) (core.WorkflowRunEvent, bool) {
	run := f.runs[runID]
	if run == nil {
		return core.WorkflowRunEvent{}, false
	}
	event := core.WorkflowRunEvent{
		RunID:  runID,
		Name:   run.name,
		Status: workflowRunStatusFromStopReason(run.stopReason),
	}
	phaseIndex := map[string]int{}
	for _, m := range run.members {
		key := workflowPhaseKey(m.phase)
		idx, ok := phaseIndex[key]
		if !ok {
			idx = len(event.Phases)
			phaseIndex[key] = idx
			event.Phases = append(event.Phases, core.WorkflowRunPhase{Phase: m.phase})
		}
		event.Phases[idx].Members = append(event.Phases[idx].Members, core.WorkflowRunMember{
			Seq:            m.seq,
			Label:          m.label,
			ChildSessionID: m.childID,
			Status:         workflowMemberStatusFromOutcome(m.outcome),
		})
	}
	return event, true
}

// workflowPhaseKey 镜像官方 workflowPhaseKey（collision-free，保住缺失 vs 空串
// 身份差异）。
func workflowPhaseKey(phase *string) string {
	if phase == nil {
		return "missing"
	}
	return fmt.Sprintf("value:%d:%s", len(*phase), *phase)
}

// foldWorkflowJournalEvent 把一条 journal 事件喂给折叠（history 冷拉共用 live
// 语义；解析失败返回 nil errObj 由调用方决定丢弃策略——冷拉 fail-open 跳过，
// live codec resetf）。
func foldWorkflowJournalEvent(f *workflowFold, eventType string, data []byte) error {
	switch eventType {
	case "tool-workflow/run-start":
		var d struct {
			RunID string `json:"runId"`
			Name  string `json:"name"`
		}
		if err := jsonUnmarshal(data, &d); err != nil {
			return err
		}
		return f.onRunStart(d.RunID, d.Name)
	case "tool-workflow/agent-start":
		var d struct {
			RunID   string  `json:"runId"`
			Seq     int     `json:"seq"`
			Label   string  `json:"label"`
			Phase   *string `json:"phase"`
			ChildID string  `json:"childId"`
		}
		if err := jsonUnmarshal(data, &d); err != nil {
			return err
		}
		return f.onAgentStart(d.RunID, d.Seq, d.Label, d.Phase, d.ChildID)
	case "tool-workflow/agent-end":
		var d struct {
			RunID   string `json:"runId"`
			Seq     int    `json:"seq"`
			Outcome string `json:"outcome"`
		}
		if err := jsonUnmarshal(data, &d); err != nil {
			return err
		}
		return f.onAgentEnd(d.RunID, d.Seq, d.Outcome)
	case "tool-workflow/run-end":
		var d struct {
			RunID      string `json:"runId"`
			StopReason string `json:"stopReason"`
		}
		if err := jsonUnmarshal(data, &d); err != nil {
			return err
		}
		return f.onRunEnd(d.RunID, d.StopReason)
	}
	return errors.New("not a tool-workflow event")
}
