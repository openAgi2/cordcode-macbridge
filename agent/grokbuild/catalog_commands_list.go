package grokbuild

// catalog_commands_list.go — List 通道（2026-09-07 重做，P3 决策被 owner 延迟
// 报障推翻后的 source-first 修正）：
//
// 旧通道（每次面板打开 spawn 专用 child → session/load → 等 ACU 完整波 +
// 1500ms 静默窗）生产实测 8-10s（initialize 1.2s + session/load 3.1s + MCP
// 初始化波 + settle），owner 报障「点 ➕ 十几秒才出选项」。
//
// 官方事实（grok-build 72a61251）：
//   - `x.ai/commands/list` 带 `cwd` 参数 = grok-desktop 在 session start 后的
//     拉取通道（session_admin.rs handle_commands_list cwd 分支注释原文），按
//     cwd 现算插件注册表（folder-trust + 有效 plugins 配置 + 发现），**不需要
//     该会话已加载进本进程**；
//   - 官方 pager 客户端消费 ACU 的方式是被动存进 AgentSession.available_commands
//     （tracker.rs take_pending_acp_commands 单一 drain 点），打开斜杠菜单不发
//     任何 RPC、不起任何进程；
//   - ListCommandsRequest {sessionId?, cwd?, kind?}（camelCase）；kind 省略 =
//     完整 Build 目录（slash_commands.rs）。
//
// 本仓对位：dsh-web 的 commands/list 也是官方常驻服务上的单次 RPC（无缓存、
// 无子进程）。Grok 侧复用既有进程级单例 catalog 子进程（catalog_session_list.go，
// 与 list_sessions 同一 `grok agent --no-leader stdio` 常驻进程）调一次 ext RPC。
// 隔离探针（scripts/grokbuild-phase0，p9）：无会话进程 + cwd 参数 = 43ms、
// compact/goal 在表。红线不变：每次 List 仍是一次真实官方拉取，绝不返回缓存
// 冒充刷新；失败 fail-closed（身份标记不可用，Execute 拒绝旧表）。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// catalogCommandsListTimeout 单次 `_x.ai/commands/list` ext RPC 的硬超时。
// 常驻进程上实测 43ms（p9）；冷启动只付一次单例 spawn+initialize（生产中单例
// 由 list_sessions 保活）。必须留在 bridge list_session_commands 的 15s 预算内。
const catalogCommandsListTimeout = 12 * time.Second

// catalogCommandsListRequest mirrors the official ListCommandsRequest camelCase
// wire (slash_commands.rs)：只带 cwd（desktop 通道）；kind 省略 = 完整 Build
// 目录；sessionId 分支要求会话已加载进本进程，对无会话的单例不适用，禁用。
type catalogCommandsListRequest struct {
	Cwd string `json:"cwd"`
}

// catalogCommandsListResult mirrors the official ListCommandsResponse envelope:
// {"commands":[AvailableCommand…]}（元素形状与 ACU availableCommands 相同，
// 复用 availableCommandW 解码——同一 wire 类型保证与 ACU 缓存条目同形）。
type catalogCommandsListResult struct {
	Commands []availableCommandW `json:"commands"`
}

// catalogListCommands calls `_x.ai/commands/list {cwd}` on the persistent
// catalog singleton and maps the official descriptors to core.SessionCommand
// (empty-name entries dropped, input.hint flattened; 与 dsh-web 相同的中间
// wire→core 映射纪律——不直接把官方 JSON 解进 core 类型)。
func (c *grokCatalogClient) catalogListCommands(ctx context.Context, cwd string) ([]core.SessionCommand, error) {
	id := c.idCounter.next()
	raw, err := c.callRPCWithCtx(ctx, id, "_x.ai/commands/list", catalogCommandsListRequest{Cwd: cwd}, catalogCommandsListTimeout)
	if err != nil {
		return nil, err
	}
	var res catalogCommandsListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("grokbuild catalog: decode _x.ai/commands/list result: %w", err)
	}
	cmds := make([]core.SessionCommand, 0, len(res.Commands))
	for _, c := range res.Commands {
		if c.Name == "" {
			continue
		}
		cmds = append(cmds, core.SessionCommand{
			Name:        c.Name,
			Description: c.Description,
			Hint:        c.Hint(),
		})
	}
	return cmds, nil
}
