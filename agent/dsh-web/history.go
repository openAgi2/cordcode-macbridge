package dshweb

// Rich-history mapping: session.history rows → core.RichHistoryEntry.
// The turn/user/tool accumulation is COPIED from agent/dsh history.go
// (design §4.1/M3 copy-not-import; §7 owner 兜底许可 covers read-side reuse)
// and adapted to the HTTP history source: pages arrive NEWEST-FIRST
// (beforeSeq walks backwards), so pages are collected until the entry budget
// is met, then reversed to oldest-first for the wire.
//
// Part/step shapes follow the grokbuild catalog convention iOS already
// renders (reasoning → tool steps → narrative text).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// historyPageMessages bounds one session/page request. Official maxMessages
// counts user/assistant messages; each expands to thousands of
// assistant/chunk rows. 2000-message pages for a long Exec-plan session were
// 55MiB and blew the 32MiB unary cap → truncated JSON → projection.hydrate_
// failed on iPhone.
const historyPageMessages = 50

// pageEvents flattens one page's records into the journal events the
// accumulator consumes (ascending, ending at the page cut). Non-event
// records (assistant-stream frames) are never requested and dropped here.
func pageEvents(records []pageRecord) []sessionEventWire {
	out := make([]sessionEventWire, 0, len(records))
	for _, rec := range records {
		if rec.Type == "event" {
			out = append(out, rec.Event)
		}
	}
	return out
}

// headSeqOf returns the session's current log head — the projection block's
// asOfSeq. The page API's throughSeq must name a REAL log cut: the official
// client pages backwards from the follow snapshot's cursor (ui-plan
// plan-resource.ts), and -1 is the EMPTY-log cursor, not "latest"
// (live-probed 2026-09-23: throughSeq -1 returns an empty page).
func headSeqOf(ctx context.Context, client *Client, sessionID string) (int64, error) {
	var proj sessionProjectionsValue
	req := sessionProjectionsRequest{SessionID: sessionID}
	if err := client.Call(ctx, "session/projections", map[string]any{"request": req}, &proj); err != nil {
		return 0, err
	}
	return proj.AsOfSeq, nil
}

// getRichHistory maps session/page pages to rich entries (oldest first).
// limit<=0 means unlimited.
func (a *Agent) getRichHistory(ctx context.Context, client *Client, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	// Collect pages newest→older until the entry budget is met or exhausted.
	// budget is counted in MAPPED entries; over-fetch events per page because
	// many rows (chunks, control-plane) map to nothing.
	budget := limit
	if budget <= 0 {
		budget = 500
	}
	pageSize := historyPageMessages

	head, err := headSeqOf(ctx, client, sessionID)
	if err != nil {
		return nil, err
	}
	if head < 0 {
		return nil, nil // empty log: nothing to fold
	}

	var pages [][]sessionEventWire
	collected := 0
	var before *int64
	for collected < budget {
		req := sessionPageRequest{
			Address:    sessionAddress{Kind: "session", SessionID: sessionID},
			ThroughSeq: head, // the real log cut (official client: snapshot cursor)
		}
		if before != nil {
			seq := *before
			req.BeforeSeq = &seq
		}
		max := pageSize
		req.MaxMessages = &max
		var val sessionPageValue
		if err := client.Call(ctx, "session/page", map[string]any{"request": req}, &val); err != nil {
			if isUnaryOversize(err) && pageSize > 1 {
				pageSize = pageSize / 2
				if pageSize < 1 {
					pageSize = 1
				}
				continue
			}
			return nil, err
		}
		events := pageEvents(val.Records)
		if len(events) == 0 {
			break
		}
		pages = append(pages, events)
		collected += countMappableEntries(events)
		if !val.HasMore {
			break
		}
		// Next page walks backwards from this page's first (oldest-in-page) seq.
		oldest := events[0].Seq
		if oldest <= 0 {
			break
		}
		n := oldest // beforeSeq is exclusive of that seq
		before = &n
	}

	// Flatten pages (newest page first) into one oldest-first slice.
	var total int
	for _, p := range pages {
		total += len(p)
	}
	flat := make([]sessionEventWire, 0, total)
	for i := len(pages) - 1; i >= 0; i-- {
		flat = append(flat, pages[i]...)
	}

	entries := mapHistoryEvents(sessionID, flat)
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// countMappableEntries estimates mapped-entry yield: turn boundaries, user
// messages, and settled slash commands (each folds to one system row).
//
// Pages without any boundary event — a mid-turn chunk window; reasoning-heavy
// turns expand to thousands of assistant/chunk + step rows per 50-message
// page — map to ZERO new entries: they only extend the tail of the turn whose
// turn/end row is counted by its own page. 2026-09-06 incident: the previous
// len(evs)/8 fallback estimated such a page (8417 events, 0 boundaries) at
// 1052 phantom entries, blew the walk budget after two pages, and iPhone
// cold-open showed only the final turn of a 10-turn session.
func countMappableEntries(evs []sessionEventWire) int {
	n := 0
	for _, e := range evs {
		switch e.Type {
		case "turn/end", "user/message", "command/done":
			n++
		}
	}
	return n
}

// mapHistoryEvents maps a oldest-first event slice onto rich entries — the
// copied agent/dsh accumulator flow: pass 1 tool outputs by callId, pass 2
// turn accumulation.
func mapHistoryEvents(sessionID string, evs []sessionEventWire) []core.RichHistoryEntry {
	// Pass 1: tool/result outputs by callId.
	results := map[string]dshToolResultInfo{}
	for _, e := range evs {
		if e.Type != "tool/result" {
			continue
		}
		var d dshToolResultData
		if jsonUnmarshal(e.Data, &d) != nil {
			continue
		}
		callID := strings.TrimSpace(d.Message.ToolCallID)
		if callID == "" && d.Message.Source != nil {
			callID = strings.TrimSpace(d.Message.Source.CallID)
		}
		if callID == "" {
			continue
		}
		var sb strings.Builder
		for _, block := range d.Message.Content {
			// 官方形状（llm ContentBlockMap + repair.ts；alpha.1 journal 实测）：
			// tool/result 的 content 是顶层 text 块，"tool-result" 块标签已退役。
			if block.Type != "text" || block.Text == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(block.Text)
		}
		results[callID] = dshToolResultInfo{text: sb.String(), isError: d.Message.IsError, meta: d.Meta}
	}

	var entries []core.RichHistoryEntry
	acc := &dshTurnAccumulator{sessionID: sessionID}
	// host 斜杠命令 + 计划模式折叠（command_fold.go；与 live codec 同一官方
	// 折叠，冷拉/直播同形）。run→done 按 commandId 续接 name/args；plan 投影
	// 只出末尾一条 {active, pending} 快照（非时间线节点，与官方一致）。
	runningCommands := map[string]runningCommand{}
	plan := &planFold{}
	// goal 投影整值快照（goal/change 全量替换；live codec 同一语义）。
	goal := (*core.GoalEvent)(nil)
	flushTurn := func(endSeq int64, endTime int64) {
		if entry, ok := acc.flush(endSeq, endTime); ok {
			entries = append(entries, entry)
		}
	}
	appendCommandEntry := func(commandID, name, args, kind, text string, at int64) {
		part := map[string]any{
			"type":      "command",
			"commandId": commandID,
			"kind":      kind,
		}
		if name != "" {
			part["name"] = name
		}
		if args != "" {
			part["args"] = args
		}
		if text != "" {
			part["text"] = text
		}
		if name == "goal" {
			// 官方 goalCommandText（goal 专属输入行气泡；与 live codec 同式）。
			part["line"] = "/goal" + strings.TrimRight(args, " \t\n\r\v\f")
		}
		entries = append(entries, core.RichHistoryEntry{
			ID:        fmt.Sprintf("%s:cmd:%s", sessionID, commandID),
			Role:      "system",
			Parts:     []map[string]any{part},
			Timestamp: dshLogTime(at),
		})
	}
	for _, e := range evs {
		switch e.Type {
		case "turn/start":
			// 官方 turn 号（journal ground truth {"turn": N}）：冷拉 entry 身份
			// 与 live codec adoptTurn 同式（dshw-<prefix>-t<N>），冷基线与
			// live 事件在一个身份上合并。缺失/非法时保持 fallback 身份。
			var d struct {
				Turn int `json:"turn"`
			}
			turnNum := 0
			if jsonUnmarshal(e.Data, &d) == nil && d.Turn >= 1 {
				turnNum = d.Turn
			}
			acc.start(e.Seq, e.Time, turnNum)
		case "turn/end":
			flushTurn(e.Seq, e.Time)
		case "user/message":
			var d dshUserMessageData
			if jsonUnmarshal(e.Data, &d) != nil {
				continue
			}
			if d.Source != nil && d.Source.Kind == "subagent-settled" {
				// 官方 settle 通知（同 live codec 分支）：busy 注入（turn 进行中）
				// 落进该 turn 的 parts（journal 原位 = 官方内联位），与 live 同 id
				//（"ctxinj:<seq>"）+ 同 turnId，reducer 幂等合并；idle 注入（turn
				// 外）保持独立 entry 原位（官方回合间位）。Summary 空 = 未知形状，
				// 静默丢（fail-open）。
				if strings.TrimSpace(d.Source.Summary) == "" {
					continue
				}
				if acc.open {
					acc.parts = append(acc.parts, map[string]any{
						"type":            "context_injection",
						"itemId":          fmt.Sprintf("ctxinj:%d", e.Seq),
						"kind":            d.Source.Kind,
						"form":            d.Source.Form,
						"summary":         d.Source.Summary,
						"text":            joinTextBlocks(d.Content),
						"senderSessionId": d.Source.SenderSessionID,
					})
					continue
				}
				entries = append(entries, core.RichHistoryEntry{
					ID:      fmt.Sprintf("ctxinj:%d", e.Seq),
					Role:    "context_injection",
					Content: joinTextBlocks(d.Content),
					ContextInjection: &core.ContextInjectionEvent{
						ItemID:          fmt.Sprintf("ctxinj:%d", e.Seq),
						Kind:            d.Source.Kind,
						Form:            d.Source.Form,
						Summary:         d.Source.Summary,
						Text:            joinTextBlocks(d.Content),
						SenderSessionID: d.Source.SenderSessionID,
					},
					Timestamp: dshLogTime(e.Time),
				})
				continue
			}
			if d.Source == nil || d.Source.Kind != "user" {
				continue // plugin/system injections are not conversation turns
			}
			text := joinTextBlocks(d.Content)
			atts := eventAttachments(d.Content)
			if strings.TrimSpace(text) == "" && len(atts) == 0 {
				continue
			}
			// user 行落在 turn 内 → 归属所属 dshw turn（live applyUserMessage
			// 同式：TurnID=activeTurnID）；turn 外（attach 前残留形）保持
			// sessionID:seq fallback。
			userID := fmt.Sprintf("%s:%d", sessionID, e.Seq)
			if acc.open && acc.turnNum >= 1 {
				userID = dshwTurnID(sessionID, acc.turnNum)
			}
			entries = append(entries, core.RichHistoryEntry{
				ID:        userID,
				Role:      "user",
				Content:   text,
				// S4: journal image/file blocks ride as descriptors（A4a/A4b
				// 证据形状；图片字节经 get_attachment 懒取）。
				Attachments: atts,
				Timestamp:   dshLogTime(e.Time),
			})
		case "assistant/message":
			var d dshAssistantData
			if jsonUnmarshal(e.Data, &d) != nil {
				continue
			}
			acc.addMessage(e.Seq, e.Time, d, results)
		case "command/run":
			var d dshCommandRunData
			if jsonUnmarshal(e.Data, &d) != nil {
				continue
			}
			commandID := strings.TrimSpace(d.CommandID)
			name := strings.TrimSpace(d.Name)
			if commandID == "" || name == "" {
				continue
			}
			args := ""
			argsPresent := false
			if d.Args != nil {
				args = *d.Args
				argsPresent = true
			}
			runningCommands[commandID] = runningCommand{name: name, args: args, argsPresent: argsPresent}
			plan.onCommandRun(commandID, name, args, argsPresent)
		case "command/done":
			var d dshCommandDoneData
			if jsonUnmarshal(e.Data, &d) != nil {
				continue
			}
			commandID := strings.TrimSpace(d.CommandID)
			if commandID == "" || (d.Kind != "success" && d.Kind != "error") {
				continue
			}
			name, args := "", ""
			if run, ok := runningCommands[commandID]; ok {
				name, args = run.name, run.args
				delete(runningCommands, commandID)
			}
			text := ""
			if d.Text != nil {
				text = *d.Text
			}
			plan.onCommandDone(commandID, d.Kind)
			appendCommandEntry(commandID, name, args, d.Kind, text, e.Time)
		case "plan/mode":
			var d struct {
				Active bool `json:"active"`
			}
			if jsonUnmarshal(e.Data, &d) != nil {
				continue
			}
			plan.onPlanMode(d.Active)
		case "goal/change":
			// 官方全量快照替换（domain.ts）：snapshot 形状整值覆盖，clear 墓碑
			// 置空。解码失败跳过该行（冷拉容错：下一快照会整值覆盖）。
			var d struct {
				Operation string          `json:"operation"`
				Goal      *core.GoalEvent `json:"goal"`
			}
			if jsonUnmarshal(e.Data, &d) != nil {
				continue
			}
			switch d.Operation {
			case "create", "edit", "pause", "resume", "complete", "block":
				if d.Goal != nil {
					goal = d.Goal
				}
			case "clear":
				goal = nil
			}
		case "tool-workflow/run-start", "tool-workflow/agent-start", "tool-workflow/agent-end", "tool-workflow/run-end":
			// 并行子代理 workflow 折叠（workflow_fold.go；与 live codec 同一折叠）。
			// run-start 在 journal 原位 append workflow part（官方 keyed chat 节点
			// 锚定 run-start 位置），后续事件原地改同一 part。turn 未开 → 整 run
			// 跳过（无锚定）。interrupted 由 reducer turn 终态 fixup 注入，不在此处理。
			acc.foldWorkflowEvent(e.Type, e.Data)
		}
	}
	flushTurn(0, 0) // torn tail: serve the committed prefix
	// Torn-tail runs without done keep one running row each (official unsettled
	// card; settle rows were already emitted inline at their done). Sorted for
	// deterministic order.
	pendingIDs := make([]string, 0, len(runningCommands))
	for id := range runningCommands {
		pendingIDs = append(pendingIDs, id)
	}
	sort.Strings(pendingIDs)
	for _, id := range pendingIDs {
		run := runningCommands[id]
		appendCommandEntry(id, run.name, run.args, "running", "", 0)
	}
	// Plan-mode snapshot: one trailing part consumed by the hydrate router into a
	// session_plan_mode event (official folds to inactive when the log has none).
	active, pending := plan.view()
	entries = append(entries, core.RichHistoryEntry{
		ID:   fmt.Sprintf("%s:plan-mode", sessionID),
		Role: "system",
		Parts: []map[string]any{{
			"type":    "plan_mode",
			"active":  active,
			"pending": pending,
		}},
	})
	// Goal snapshot: one trailing part consumed by the hydrate router into a
	// session_goal event (official projection is whole-snapshot; absent goal
	// carries phase "none" so the far side clears any stale banner).
	goalPart := map[string]any{"type": "goal", "phase": "none"}
	if goal != nil {
		goalPart = map[string]any{
			"type":      "goal",
			"id":        goal.ID,
			"revision":  goal.Revision,
			"objective": goal.Objective,
			"phase":     goal.Phase,
		}
		if goal.BlockedReason != nil {
			goalPart["blockedReason"] = map[string]any{
				"code":    goal.BlockedReason.Code,
				"message": goal.BlockedReason.Message,
			}
		}
		if goal.MaxGoalRounds > 0 {
			goalPart["maxGoalRounds"] = goal.MaxGoalRounds
		}
	}
	entries = append(entries, core.RichHistoryEntry{
		ID:    fmt.Sprintf("%s:goal", sessionID),
		Role:  "system",
		Parts: []map[string]any{goalPart},
	})
	return entries
}

// jsonUnmarshal is a small alias to keep the copied logic tidy.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// dshAssistantData is assistant/message's data payload.
type dshAssistantData struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Message struct {
		Role    string            `json:"role"`
		Content []dshContentBlock `json:"content"`
		Source  *dshModelSource   `json:"source,omitempty"`
	} `json:"message"`
}

// dshToolResultData is tool/result's data payload.
type dshToolResultData struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Message struct {
		// 官方 ToolResultMessage（llm/src/message.ts）：toolCallId/isError 在
		// message 顶层；content 是顶层 ContentBlock（text 等）。
		ToolCallID string            `json:"toolCallId,omitempty"`
		IsError    bool              `json:"isError,omitempty"`
		Source     *dshSource        `json:"source,omitempty"`
		Content    []dshContentBlock `json:"content"`
	} `json:"message"`
	// Meta is the tool's private presentation payload (official
	// tool-calls.ts: persisted so a UI bridge reproduces the card on
	// replay — read {path, offset, lines, totalLines} / write·edit
	// {diffs}). Parity plan §5 S3 clean-detail slice consumes it; the
	// previous cold path dropped it.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// dshToolResultInfo is the cold-path per-call digest of one tool/result:
// the joined output text, the block-level error flag (status mapping ⑤)
// and the raw presentation meta (clean-detail branches ①②③⑥).
type dshToolResultInfo struct {
	text    string
	isError bool
	meta    json.RawMessage
}

// dshTurnAccumulator assembles one assistant turn entry in grokbuild's part
// order: reasoning flush → tool steps → narrative text.
type dshTurnAccumulator struct {
	sessionID string
	open      bool
	turnNum   int // 官方 turn 号（turn/start {"turn": N}）；0 = 未知 → fallback 身份
	startSeq  int64
	startTime int64
	thinking  strings.Builder
	pending   strings.Builder
	content   strings.Builder
	parts     []map[string]any
	steps     []map[string]any
	model     string
	provider  string
	hasData   bool
	// workflowFold 折叠状态（workflow_fold.go；live codec 同一折叠）+ parts 索引：
	// run-start 在 journal 原位 append 一个 workflow part，后续事件原地改该 part
	// （官方 keyed chat 节点语义——卡锚定 run-start 位置，成员随事件更新）。
	workflowFold workflowFold
	workflowIdx  map[string]int
}

func (t *dshTurnAccumulator) start(seq int64, at int64, turn int) {
	t.open = true
	t.turnNum = turn
	t.startSeq = seq
	t.startTime = at
}

func (t *dshTurnAccumulator) flushPendingReasoning() {
	if t.pending.Len() == 0 {
		return
	}
	t.parts = append(t.parts, map[string]any{"type": "reasoning", "content": t.pending.String()})
	t.pending.Reset()
}

func (t *dshTurnAccumulator) addMessage(seq int64, at int64, d dshAssistantData, results map[string]dshToolResultInfo) {
	if !t.open {
		t.start(seq, at, 0)
	}
	t.hasData = true
	if d.Message.Source != nil {
		if t.provider == "" {
			t.provider = d.Message.Source.Provider
		}
		if t.model == "" {
			t.model = d.Message.Source.Model
		}
	}
	for _, block := range d.Message.Content {
		switch block.Type {
		case "reasoning":
			if block.Text == "" {
				continue
			}
			if t.thinking.Len() > 0 {
				t.thinking.WriteByte('\n')
			}
			t.thinking.WriteString(block.Text)
			if t.pending.Len() > 0 {
				t.pending.WriteByte('\n')
			}
			t.pending.WriteString(block.Text)
		case "tool-call":
			t.flushPendingReasoning()
			name := strings.TrimSpace(block.Name)
			if name == "" {
				continue
			}
			info, hasResult := results[strings.TrimSpace(block.ID)]
			output := ""
			if hasResult {
				output = info.text
			}
			if name == "ask_user_question" {
				// 冷拉重建结构化问答面（2026-09-15 owner 真机：重开带 pending
				// 问答的 dsh 会话，iOS 弹出卡只剩 L1 缓存首帧 ~1s 即被权威冷快照
				// 收起——冷拉把 ask_user_question 折成普通工具卡，user_input part
				// 丢失）。live 路径经 question/requested RPC 建 user_input part
				// （canRespond=true）；冷拉同形重建。pending（journal 无该 call 的
				// tool/result）只出 user_input part、不出工具 step——live codec 对
				// tool-call 块本就不发工具事件；answered（有 tool/result）工具
				// step 照旧（live 由 tool/result 补全）+ 终态 user_input part。
				// 解析失败 fail closed 落回普通工具卡，不造半张问答卡。
				if uiParts := askUserQuestionUserInputParts(block.Arguments, output != ""); len(uiParts) > 0 {
					if output == "" {
						t.parts = append(t.parts, uiParts...)
						continue
					}
					t.steps = append(t.steps, askUserQuestionToolStep(t.sessionID, seq, block, output))
					t.parts = append(t.parts, map[string]any{"type": "tool", "step": t.steps[len(t.steps)-1]})
					t.parts = append(t.parts, uiParts...)
					continue
				}
			}
			stepID := fmt.Sprintf("%s:%d:%s", t.sessionID, seq, strings.TrimSpace(block.ID))
			// 状态映射（§5 S3 分支⑤）：有 tool/result 的 step 按 block 级
			// isError 写 failed/completed（与 live codec 同形）；journal 无
			// 该 call 的 result（pending/中断残留）保持 unknown，不伪造终态。
			status := "unknown"
			if hasResult {
				if info.isError {
					status = "failed"
				} else {
					status = "completed"
				}
			}
			step := map[string]any{
				"id":                             stepID,
				"toolName":                       name,
				"status":                         status,
				"output":                         map[string]any{"kind": "inline", "text": output},
				"duration":                       nil,
				"requiresPermissionConfirmation": false,
				"availablePermissionOptions":     []any{},
			}
			if title := toolStepTitle(name, block.Arguments); title != "" {
				step["title"] = title
			}
			// clean-detail（§5 S3 分支①②③⑥）：read/write/edit 的成功
			// result 消费官方 presentation meta（此前整字段丢弃，iOS 冷拉
			// 只能渲染 XML 形 raw output）；错误 result（分支④）保持原始
			// 错误文本，不套结构化载荷。
			if hasResult && !info.isError {
				switch name {
				case "read", "write", "edit":
					fd, regions, diag := dshCleanToolDisplay(name, block.Arguments, info.meta)
					if fd != nil {
						step["fileDisplay"] = fd
					}
					if regions != nil {
						step["editRegions"] = regions
					}
					if diag != nil {
						step["detailUnavailable"] = diag
					}
				}
			}
			t.steps = append(t.steps, step)
			t.parts = append(t.parts, map[string]any{"type": "tool", "step": step})
		case "text":
			if block.Text == "" {
				continue
			}
			t.flushPendingReasoning()
			if t.content.Len() > 0 {
				t.content.WriteByte('\n')
			}
			t.content.WriteString(block.Text)
			t.parts = append(t.parts, map[string]any{"type": "text", "content": block.Text})
		}
	}
}

func (t *dshTurnAccumulator) flush(endSeq int64, endTime int64) (core.RichHistoryEntry, bool) {
	if !t.open || (!t.hasData && t.content.Len() == 0 && t.thinking.Len() == 0 && len(t.parts) == 0) {
		t.reset()
		return core.RichHistoryEntry{}, false
	}
	// 身份与 live 同源（dshw-<prefix>-t<N>）：冷基线 assistant turn 与 live
	// 事件在一个身份上合并（goal 轮无 user 行，若沿用「上一个 user 行折叠」
	// 的平坦归属，多轮输出会全部折进同一 turn——owner 2026-09-06 01:49
	// rework ⑧）。turn 号未知（attach 前残留形）保持 sessionID:seq fallback。
	entryID := fmt.Sprintf("%s:%d", t.sessionID, t.startSeq)
	if t.turnNum >= 1 {
		entryID = dshwTurnID(t.sessionID, t.turnNum)
	}
	entry := core.RichHistoryEntry{
		ID:         entryID,
		Role:       "assistant",
		Content:    t.content.String(),
		Thinking:   t.thinking.String(),
		Parts:      t.parts,
		Steps:      t.steps,
		Timestamp:  dshLogTime(t.startTime),
		ModelID:    t.model,
		ProviderID: t.provider,
	}
	start := dshLogTime(t.startTime)
	entry.TurnStartedAt = &start
	if endTime > 0 {
		completed := dshLogTime(endTime)
		entry.TurnCompletedAt = &completed
	}
	t.reset()
	return entry, true
}

// foldWorkflowEvent 把一条 tool-workflow/* journal 事件折叠进当前开放 turn 的
// parts（run-start 在 journal 原位 append workflow part，后续事件原地改同一
// part——官方 keyed chat 节点锚定 run-start 位置的冷拉对位）。turn 未开
// （journal 残留形）或折叠违规 → false 跳过（冷拉 fail-open：官方 append 时已
// 保证不变量，违规=解析脱节，下一快照/直播路径仍是权威）。
// interrupted 不在此注入：converter 对该 entry 发 turn_completed，reducer 终态
// fixup（官方 locationCold 语义）统一处理冷热两路。
func (t *dshTurnAccumulator) foldWorkflowEvent(eventType string, data []byte) bool {
	if !t.open {
		return false
	}
	if err := foldWorkflowJournalEvent(&t.workflowFold, eventType, data); err != nil {
		return false
	}
	runID := workflowEventRunID(eventType, data)
	snapshot, ok := t.workflowFold.snapshot(runID)
	if !ok {
		return false
	}
	part := map[string]any{
		"type":           "workflow",
		"workflowId":     snapshot.RunID,
		"workflowName":   snapshot.Name,
		"workflowStatus": snapshot.Status,
		"workflowPhases": workflowPhasesToPartMaps(snapshot.Phases),
	}
	if idx, ok := t.workflowIdx[runID]; ok {
		t.parts[idx] = part
	} else {
		if t.workflowIdx == nil {
			t.workflowIdx = map[string]int{}
		}
		t.workflowIdx[runID] = len(t.parts)
		t.parts = append(t.parts, part)
	}
	t.hasData = true
	return true
}

// workflowPhasesToPartMaps 把折叠快照的 phase 分组转成 part map 的 wire 形状
// （键与 go-bridge ProjectionPart JSON 标签一致：phase/members/seq/label/
// childSessionId/status；phase nil → JSON null = 未分阶段身份）。
func workflowPhasesToPartMaps(phases []core.WorkflowRunPhase) []map[string]any {
	out := make([]map[string]any, 0, len(phases))
	for _, phase := range phases {
		var phaseValue any
		if phase.Phase != nil {
			phaseValue = *phase.Phase
		}
		members := make([]map[string]any, 0, len(phase.Members))
		for _, m := range phase.Members {
			members = append(members, map[string]any{
				"seq":            m.Seq,
				"label":          m.Label,
				"childSessionId": m.ChildSessionID,
				"status":         m.Status,
			})
		}
		out = append(out, map[string]any{"phase": phaseValue, "members": members})
	}
	return out
}

func (t *dshTurnAccumulator) reset() {
	*t = dshTurnAccumulator{sessionID: t.sessionID}
}

func joinTextBlocks(blocks []dshContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// toolStepTitle derives a short step title from common tool argument shapes
// (bash command / file path). DSH logs the arguments as a JSON-encoded
// *string* on tool-call blocks, so both object and string-wrapped-object
// forms are accepted.
func toolStepTitle(name string, arguments []byte) string {
	if len(arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := jsonUnmarshal(arguments, &args); err != nil {
		var wrapped string
		if err := jsonUnmarshal(arguments, &wrapped); err != nil {
			return ""
		}
		if err := jsonUnmarshal([]byte(wrapped), &args); err != nil {
			return ""
		}
	}
	// 键序 = 官方工具族的展示语义：coding 工具的主参数在前；
	// `description` 是官方 subagent 工具的展示摘要参数（tool-subagent
	// src/index.ts：3-5 词 "description of the delegated task, for display"，
	// 官方 Tool call 行 `Tool call · subagent · <description>` 的文本源）；
	// `prompt` 是 subagent 无 description 时的最后兜底（长文，截断展示）。
	// 2026-09-05 owner 报障：iOS subagent 工具行标题为空——旧键表不认这两个键。
	for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url", "description", "prompt"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			title := strings.TrimSpace(v)
			// rune 截断：字节截断会把 CJK 命令切成非法 UTF-8（live ticker 也走这里）。
			if runes := []rune(title); len(runes) > 80 {
				title = string(runes[:80])
			}
			return title
		}
	}
	return ""
}

func dshLogTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// askUserQuestionArguments is ask_user_question's arguments payload (official
// user-questions tool: header/id/multi_select/options/question per entry).
type askUserQuestionArguments struct {
	Questions []struct {
		ID          string `json:"id"`
		Header      string `json:"header"`
		Question    string `json:"question"`
		MultiSelect bool   `json:"multi_select"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// askUserQuestionUserInputParts folds one ask_user_question tool-call into
// user_input parts — one part per question, interactionId = the dsh question
// id (same identity as the live question/requested path, so resolve_user_input
// and reconnect replays upsert the same card). answered = the journal already
// carries this call's tool/result (the harness feeds the answer back to the
// model as the tool result). Question/options field mapping mirrors the live
// approvals.go construction; dsh options carry no ids — the label IS the
// identifier, echoed verbatim in the answer's selected[].
func askUserQuestionUserInputParts(arguments []byte, answered bool) []map[string]any {
	if len(arguments) == 0 {
		return nil
	}
	// DSH 把 tool-call 的 arguments 记为 JSON 编码的 *string*（toolStepTitle
	// 同款约束）：对象与字符串包裹对象两种形态都接受。
	raw := arguments
	var args askUserQuestionArguments
	if jsonUnmarshal(raw, &args) != nil {
		var wrapped string
		if jsonUnmarshal(raw, &wrapped) != nil || jsonUnmarshal([]byte(wrapped), &args) != nil {
			return nil
		}
	}
	status := "pending"
	if answered {
		status = "answered"
	}
	var parts []map[string]any
	for _, q := range args.Questions {
		qid := strings.TrimSpace(q.ID)
		prompt := strings.TrimSpace(q.Question)
		if qid == "" || prompt == "" {
			continue
		}
		mode := "single"
		if q.MultiSelect {
			mode = "multiple"
		}
		opts := make([]map[string]any, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, map[string]any{
				"id":          o.Label,
				"label":       o.Label,
				"description": o.Description,
			})
		}
		question := map[string]any{
			"id":                 qid,
			"prompt":             prompt,
			"answerMode":         mode,
			"options":            opts,
			"allowsCustomAnswer": true,
			"isSecret":           false,
			"required":           true,
		}
		// 空 header 落 nil（iOS 卡 eyebrow 按 nil 隐藏；空串会留一个空 label 位）。
		if header := strings.TrimSpace(q.Header); header != "" {
			question["header"] = header
		}
		parts = append(parts, map[string]any{
			"type":          "user_input",
			"interactionId": qid,
			"status":        status,
			"questions":     []map[string]any{question},
			"canRespond":    !answered,
			"canReject":     !answered,
		})
	}
	return parts
}

// askUserQuestionToolStep builds the (answered) ask_user_question tool step
// for the cold fold — same shape as the generic tool-call step, with the
// journal's tool/result output and a completed status (live parity: the step
// only materializes once tool/result arrives).
func askUserQuestionToolStep(sessionID string, seq int64, block dshContentBlock, output string) map[string]any {
	step := map[string]any{
		"id":                             fmt.Sprintf("%s:%d:%s", sessionID, seq, strings.TrimSpace(block.ID)),
		"toolName":                       "ask_user_question",
		"status":                         "completed",
		"output":                         map[string]any{"kind": "inline", "text": output},
		"duration":                       nil,
		"requiresPermissionConfirmation": false,
		"availablePermissionOptions":     []any{},
	}
	if title := toolStepTitle("ask_user_question", block.Arguments); title != "" {
		step["title"] = title
	}
	return step
}
