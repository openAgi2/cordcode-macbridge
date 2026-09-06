package dshweb

// Host 斜杠命令 + 计划模式折叠——单一折叠真值，live codec 与 history 冷拉共用。
//
// 官方源码锚点（/Users/jacklee/Projects/deepseek-harness，HEAD dsh-v0.1.3-alpha.1
// = d347e70）：
//   - plan 投影单元 packages/plan/plan-mode/src/index.ts planProjectionDefinition：
//     state {active, wanted, running:{commandId, wanted}}；view {active, pending}
//     其中 wanted 取 running?.wanted ?? state.wanted，pending = wanted !== null &&
//     wanted !== state.active。request/header 只影响 narration，不入 view，不折叠。
//   - 命令卡节点 packages/client/ui-chat/src/client/conversation-nodes/command.ts：
//     commandFromRun（name/args 来自 run）/ commandFromDone（done-only 时 name 为
//     null，客户端按官方 locale 回退）。

import "strings"

// runningCommand 是已见 run、未见 done 的命令暂存（跨帧续 name/args）。
type runningCommand struct {
	name        string
	args        string
	argsPresent bool
}

// planRunningCommand 镜像官方 running:{commandId, wanted}。
type planRunningCommand struct {
	commandID string
	wanted    bool
}

// planFold 镜像官方 plan 投影单元 state（active / wanted / running）。
type planFold struct {
	active  bool
	wanted  *bool
	running *planRunningCommand
}

// onCommandRun 镜像官方 command/run && name==='plan' 分支：args 缺席（undefined）
// 不改变状态；wanted = args.trim() !== 'off'。
func (p *planFold) onCommandRun(commandID, name string, args string, argsPresent bool) {
	if name != "plan" || !argsPresent {
		return
	}
	wanted := strings.TrimSpace(args) != "off"
	p.running = &planRunningCommand{commandID: commandID, wanted: wanted}
}

// onCommandDone 镜像官方 command/done 分支：仅当 commandId 匹配 running 时结算；
// wanted = kind==='success' && running.wanted !== state.active ? running.wanted : null。
func (p *planFold) onCommandDone(commandID, kind string) {
	if p.running == nil || p.running.commandID != commandID {
		return
	}
	w := p.running.wanted
	p.running = nil
	if kind == "success" && w != p.active {
		p.wanted = &w
	} else {
		p.wanted = nil
	}
}

// onPlanMode 镜像官方 plan/mode 分支：active 整值替换，wanted 清空。
func (p *planFold) onPlanMode(active bool) {
	p.active = active
	p.wanted = nil
}

// view 镜像官方 wire.view：pending = wanted !== null && wanted !== state.active。
func (p *planFold) view() (active, pending bool) {
	var wanted *bool
	if p.running != nil {
		w := p.running.wanted
		wanted = &w
	} else {
		wanted = p.wanted
	}
	pending = wanted != nil && *wanted != p.active
	return p.active, pending
}
