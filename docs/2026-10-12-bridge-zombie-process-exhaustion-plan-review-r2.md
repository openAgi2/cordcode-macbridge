# 2026-10-12 bridge 僵尸进程耗尽事故档案 — 方案评审 r2

## 结论

- verdict: **APPROVED**
- plan: `docs/incidents/2026-10-12-bridge-zombie-process-exhaustion.md`
  - SHA-256: `7e7a505a1fe537c9882c3f10dc9fc3b4f8cca2c372efab3f8c3f32473f25f2af`（相对 r1 的 `d8a8f981…` 已变）
  - 本轮：**复审** r1 四条意见闭合 + 修订回归（门控、引用、旧表述残留）。未重做全量源码普查。
- blockers: **0**；advisories: **0**
- implementation_readiness:
  - **设计通过。** P0 三处收尸可作为已授权切片开工（用户仍未授权改产品代码）。
  - P0 实施完成门：定向测试绿 + 覆盖安装后关 Desktop 跑 N 分钟，`token_refreshed`≥N 且 runtime 名下僵尸=0。
  - P1 仍被上游 token 刷新语义证据门挡住；P2 独立，不阻塞 P0。
  - 评审通过 ≠ 批准写代码 / 发版 / 再杀 runtime。

评审模式：同一评审员延续（r1 作者）。独立性限制已披露；本轮按命题核验修订文本与相关源码依赖，不把「全部采纳」当闭合证据。

## 来源与覆盖

| 项 | 值 |
| --- | --- |
| 仓库 | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` |
| 分支 | `feat/ios-native-message-timeline` |
| HEAD | `bc311b6cb7591edb72bbb931acfc1f7e395714a4`（与 r1 相同；产品代码未改） |
| 工作树 | 未跟踪：本事故档案 + r1 评审报告 |
| 专项审计 | N/A |

本轮亲核：修订后的 §3.1 调用方清单、§3.5 dsh-web、§4.3、§5.1-3、§5.4、§6 矩阵 dsh 行；以及 r1 已核、修订所依赖的 `resolver.go:194-196/:789`、`proc_unix.go:58-67`、`pairing_persist.go:23/28/:37-71/:79-93`。其余 r1 已核锚点（`loadChatGPTAuth` Kill 不 Wait、restore 循环、backoff、oauth `Start`、grok/claude Wait 等）源码未变，按命题复用。

未核：官方 `codex` checkout（P1 待捕）；本轮未复采 `ps`。

## 跨轮核验复用

| 命题 | r1 位置 | 本轮 | 依据 |
| --- | --- | --- | --- |
| `loadChatGPTAuth` 只 Kill 不 Wait | r1 锚点表 | 复用 | 源码未改，HEAD 同 |
| restore 无限循环 + 每轮 spawn | r1 锚点表 | 复用 | 同上 |
| backoff 1s/30s 重数 | r1 锚点表 | 复用 | 同上 |
| `Start` 守卫 already-has-a-process | r1 F-2 证据 | 复用源码，亲核文档已改写 | `resolver.go:194-196` 未变 |
| `loseSeatLocked` 不 Stop | r1 F-2 | 复用源码，亲核文档 | `:789` 未变 |
| `killProcessGroupNoWait` 已存在 | r1 F-4/F-2 | 复用源码，亲核 §5.1-3 引用 | `proc_unix.go:58-67` 未变 |
| `maybeRefreshCtrlToken` 在线仍 spawn | r1 F-3 | 复用源码，亲核 §3.1/§4.3 | `pairing_persist.go:23/28/:80` 未变 |
| P0 vs P2 完成门 | 新修订 | 亲核文档 §5.4 | 无源码依赖 |

## 原意见闭合

| ID | r1 严重性 | 本轮 | 说明 |
| --- | --- | --- | --- |
| F-1 | 阻塞 | **闭合** | §5.4 拆成 P0 完成门（收尸 + 测试绿 + 覆盖安装 + 关 Desktop 对账僵尸=0）与 P2 完成门（watchdog / 缺席状态机独立）。P0 段不再要求 watchdog 特征输出。 |
| F-2 | 建议 | **闭合** | §3.5 / §5.1-3 / §6 均改为「1 僵尸滞留 + Start 守卫挡住 respawn」。残留「每次 respawn」仅出现在勘正否定句中。P0-3 明确复用 `killProcessGroupNoWait`，测试不再要求 N 次 respawn → N 僵尸。 |
| F-3 | 建议 | **闭合** | §4.3 改为「大幅降频但非零，约每 10 分钟量级」；§3.1 补录 `runCtrlTokenRefresh`。 |
| F-4 | 建议 | **闭合** | 注入点改为实际 exec 的 bin；CI 硬断言改为 Wait 契约，明确 `ps` 不作 CI 硬断言。 |

## 修订回归

- 门控：P0 对账数字可独立勾选；P2 不再卡死 P0。与 CLAUDE.md 产物门（对账数字，非 watchdog）一致。
- 旧表述：检索「不再周期性 spawn」「包一层 candidates」「watchdog 特征输出」作为 P0 核验项——无作为现行要求的残留。
- §5.1-1 括号仍写「restoreOnce / pairing / diagnostics」，未再列 `runCtrlTokenRefresh`。因修复点是 `loadChatGPTAuth` 一处，在线刷新路径仍被覆盖，**不重开意见**。
- 无新阻塞；不为继续循环制造建议。

## 意见

无。

## 复审处置与剩余门

| 门 | 状态 | 挡住什么 |
| --- | --- | --- |
| P0 设计 | 满足 | 无 |
| P0 实施完成（测试绿 + 覆盖安装对账） | 待实施后验证 | 宣称已修复 / 发版 |
| P1 上游刷新语义 | pending | 不得定缓存窗口、不得当 P0 |
| P2 缺席/预算/watchdog | 可选 | 不阻塞 P0 |
| 写代码 / 发版授权 | 未授 | 本评审不增加授权 |
