# A1 三层作用域事实表（2026-09-05，只读取证；变量名级别，无值）

## 结论

RC 拒绝条件在**本机只在 user settings env 块一处生效**，且 shell env 与
project settings env 均无法覆盖它（两次实验取证，见 a1-doctor-project-
override.txt：面板不变）。

| 层 | 位置 | RC 相关变量（只列名） | 对 RC 的影响 |
| --- | --- | --- | --- |
| 当前 shell（agent 沙箱） | `env` | 无 anthropic/claude/telemetry 变量 | 无 |
| shell 启动文件 | `~/.zshrc` `~/.zshenv` `~/.zprofile` `~/.bashrc` `~/.bash_profile` | grep 无任何 ANTHROPIC/CLAUDE/TELEMETRY/GROWTHBOOK 导出 | 无 |
| user settings env 块 | `~/.claude/settings.json` → `env` | `ANTHROPIC_BASE_URL`（指向 bigmodel 网关）、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`（值=网关 key，不记录）、`ANTHROPIC_MODEL`、`ANTHROPIC_DEFAULT_*`（glm 映射族）、`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`、`DISABLE_AUTOUPDATER=1` | **两条 RC 硬拒绝**：非官方 base URL + feature-flag 评估被禁 |
| 本仓项目 settings | `.claude/settings.json` / `.claude/settings.local.json` | 文件不存在 | 无 |
| 测试目录 project settings（实验注入） | `/tmp/rc-probe/work/.claude/settings.json` | 尝试 `ANTHROPIC_BASE_URL=https://api.anthropic.com`、`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=0` | **无效**：doctor RC 面板不变（user env 块优先） |

## 关键事实

1. `~/.claude/settings.json` 另有备份 `settings-xirang-bak.json` /
   `settings-xirang-bak_副本.json`（owner 历史切换痕迹，只记录存在）。
2. keychain 无 `Claude Code-credentials` 条目；`~/.claude/.credentials.json`
   不存在 → 本机从未以 claude.ai OAuth 登录（A3：当前认证形态=网关 token）。
3. 干净 HOME（`HOME=/tmp/rc-probe/home`）下 doctor 面板只剩「未登录」
   族条目 → base URL / feature-flag 两条阻塞完全由 user settings env 块
   引起；解除的唯一路径是改该文件本身（owner 决策点）。
