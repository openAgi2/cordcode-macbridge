# codex-remote 缺席语义加固方案（P2）评审 r4 通过结论元复核（r4-meta）

- 复核对象：r4 通过报告 `docs/2026-10-13-codex-remote-absence-hardening-p2-plan-review-r4.md`
  （verdict APPROVED，阻塞 0 / 建议 0）
- 复核范围：仅两项——(1) 锚点抽查（从通过报告随机抽 ≥3 处带行号引用，行号与语义
  逐项核对实际文件）；(2) 通过报告来源清单完整性（是否覆盖任务项目规则要求的全部
  来源组合，含配套工作树，无论是否实际引用）。
- 复核时点方案 SHA-256：
  `97a97624efda8d0e9c823b9d952cad2941b6b6d7c533deaaf0a4f327caf9ecf3`
  （本轮 `shasum -a 256` 实测，与派发哈希、r4 报告记载一致——评审对象未被改动）
- 结论：**confirm = true，两项复核范围内未发现问题**。

## 1. 本复核来源清单（P0 门）

```text
本仓仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
本仓分支=feat/ios-native-message-timeline
本仓提交=11ae09386c5dfc926127629ed3014563d1fdf116（本轮 rev-parse 实测）
本仓未提交状态=干净（未跟踪：方案 v4、r1/r2/r3/r4 评审报告、本 r4-meta，均为 docs）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline（只读，
  仅枚举 worktree 元数据并核对 r4 清单行，未读取其源码）
配套分支=feat/ios-native-message-timeline
配套提交=507a1e23b8c38847f1433921dbe65557965a52e8（本轮 rev-parse 实测，与 r4 一致）
配套未提交状态=脏（porcelain 277 行，本轮 wc -l 实测）
上游仓库=不适用（本复核不重开产品语义/上游镜像论证，仅核锚点行号/语义与清单覆盖；
  r4 对上游 N/A 的论证维持其记载）
本机运行态=defaults read org.openagi.cordcode.link（只读实测）
任务预期分支=纯 Mac 侧（承方案 §2.1，r4 已核其在评审时点仍准确）
预期产品特性=cordcode-bridge-runtime（drivers 不变；新增 watchdog 观测；承方案 §2.1）
```

## 2. 锚点抽查（随机 ≥3 → 实抽 6，全部通过）

抽样方法：枚举 r4 报告 §2.3 锚点核验表与 §3.2 差异核验表中全部带行号引用共 16 项，
`$RANDOM` 均匀抽 6 项，逐行读实际文件核对行号与语义。

| # | 抽中锚点（r4 报告原引用） | 实际文件核对 | 结果 |
| --- | --- | --- | --- |
| 1 | `RuntimeManager.swift:648-658`（引 `:649`/`:650`/`:651`/`:654`） | `MacBridge/MacBridge/Services/RuntimeManager.swift`：`:649` 缺省 `?? true`、`:650` `guard enabled else { return }`、`:651` 缺省 `?? 120`、`:654` `if status == .ready \|\| status == .readyNoAgents,`——引文逐字吻合；语义（缺省值口径、关闭时 guard 早退、仅 ready 计时）成立 | 通过 |
| 2 | `agent_descriptor.go:20-34`、`:253-270`（引 `:22`/`:23`/`:254`/`:266-269`） | `go-bridge/agent_descriptor.go`：`:22` not_detected、`:23` not_logged_in、`:253-254` codex-remote → detectStructuredInstanceReadiness、`:266-269` TrimSpace 空值→NotConfigured、非空 `return AgentStatus(status), detail`——吻合；「零新增枚举、descriptor 层无需改动」主张成立（注：r4 表内简写 `agent_descriptor.go`，全路径在方案 §2.2 为 `go-bridge/agent_descriptor.go`，仓内唯一，非歧义） | 通过 |
| 3 | `backoff.go:16-17` | `agent/codex-remote/backoff.go`：`:16` base=1s、`:17` cap=30s，头注释 `:4` 引 codex-rs `next_reconnect_delay`——吻合 | 通过 |
| 4 | P1 方案 `docs/2026-10-12-codex-remote-probe-economy-p1-plan.md:252` | 该行即 E-9 行：`exp 距今 **96h56m**`、`一次性取证、样本已销毁、不可复核…仅证明 exp 存在与量级，禁止当硬 TTL 或常量`——吻合；v4「~96h 量级、非常量」表述与 P1 证据纪律一致 | 通过 |
| 5 | `diagnostics.go:10-35`（引 `:11-13`/`:19`/`:28-32`） | `agent/codex-remote/diagnostics.go`：`:11-13` 三态常量、`:19` `func (a *Agent) StructuredInstanceReadiness() (string, string)`、`:28-32` paired 分支返回 ServiceNotRunning——吻合；「非 available/pairing_required 分支即 class 映射落点」成立 | 通过 |
| 6 | 方案 v4 `:330-338`（§6 条件化窗口上界） | 方案实际 `:330-338` 与 r4 §3.2 引文（默认配置 ~120min 兜底 / 关闭时 token exp ~96h 量级 / 本机 autoRestartEnabled=0 / 事故 pid 1917 7.7h / 84a573ef/2026-06-17 / 窗口内=现状非回归 / 主场景不受影响）逐点对应——吻合 | 通过 |

抽样外顺带核验（来源清单支撑项，非锚点抽查计数）：

- 本仓 HEAD/分支/porcelain 与 r4 §2.1 一致；`git diff 95cbb3e0 HEAD -- '*.go' '*.swift'`
  为空（r4「方案全部代码锚点在 HEAD 逐字节有效」主张实测成立）。
- `git log -S autoRestartIntervalMinutes` → `84a573ef 2026-06-17 feat(app): add
  MacBridge auto-restart safety net`，与 r4 引文一致。
- `defaults read org.openagi.cordcode.link`：`autoRestartEnabled = 0`、
  `autoRestartIntervalMinutes` 键不存在——与 r4 §2.1 本机运行态行一致。
- 事故档案 `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md` 已入库
  （git ls-files 确认），`:3-9` 状态行含「owner 已预授权过审即实施」，与 r4 引文一致。

## 3. 来源清单完整性（通过）

r4 报告 §2.1 覆盖以下全部必需来源组合：

| 来源组合 | r4 覆盖情况 | 本轮实测 |
| --- | --- | --- |
| 本仓（MacBridge worktree） | 路径+分支+完整提交（HEAD 与锚点基线 95cbb3e0）+未提交状态四要素齐全；零代码差异主张附复核命令 | 一致 |
| 配套工作树（iOS，只读，实际引用为 E-3 证据） | 路径+分支+完整提交+未提交状态（脏 277 行）齐全，并经本轮 rev-parse/wc 复核 | 一致 |
| 上游 | 显式「不适用」+理由（P2 三项为 bridge 私有运维语义；r1/r2 已核），非静默缺省 | 记载在案 |
| 本机运行态（F-10 证据，易变项） | 列为独立来源行，注明只读实读、非复用 r3 | 一致 |
| 运行证据档案（build/p1-reconcile-*） | 列出并注明存在性经 r3 复核、本轮未重开 | 记载在案 |

配套工作树唯一性：iOS 仓全部 7 个 worktree 中，与任务分支族
`feat/ios-native-message-timeline` 同名配对的唯一为
`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`（其余在 main、
codex/chatgpt-codex-timeline-parity、codex/ios-widget-support 分支或游离 HEAD；
其 `output/` 下两个嵌套 detached worktree 为 baseline 副本，r4 已声明不作为 E-3
来源）。r4 所列配套组合即该唯一配对，不存在多候选未澄清。

字段完整性：r4 自身表格对每个涉及仓库均含 P0 门不可拆分的四要素（路径+分支+完整
提交+未提交状态）；「任务预期分支」「预期产品特性」两字段由其逐项核验为仍准确的
方案 §2.1（含全部 7 字段）承载（r4 显式引用并确认「与本仓方案 §2.1 逐项一致…§2.1
清单在本轮评审时点仍准确」），且 r4 保留了实施 kickoff 按门 2 用当时真实 HEAD 重新
生成清单的义务。来源组合覆盖无缺。

时序演化说明（非失实）：r4 所称「未跟踪恰为方案 v4 + r1/r2/r3 评审报告」在其自身
落盘前成立；当前 porcelain 另含 r4 报告与本文档，属评审链推进的正常演化。

## 4. 结论

- 锚点抽查：随机 6 处，6/6 行号与语义均吻合；抽样外支撑核验（git 状态、diff、
  84a573ef、defaults、事故档案）亦全部一致。
- 来源清单完整性：覆盖全部必需来源组合，配套工作树已列入且为唯一配对，易变运行态
  项为实读值。
- **无问题：confirm = true。**r4 通过结论在其复核范围内可信。
