# 本轮任务完成情况：Grok Build Leader 模式开关（B 路线，v10 APPROVE）

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader`（单仓任务，iOS / protocol pack / SSV2 零修改）
- Plan: `docs/2026-08-28-grokbuild-leader-mode-design.md`（v10 owner APPROVE）
- Canonical State File: `.exec-plan/state/plan-60f196abb855.json`
- Completion Report Verdict: **`proved-complete (owner-verified 2026-09-02)`**（产品代码、单测、Release 安装、§7.3 owner 真机矩阵全部收口；12 行中 11 行 owner 实测通过 + 诊断卡确认，行 3「未安装态」为可选行未测，已在 exec-plan regression 证据中注明）
- Queue Summary: **54/54 todos done**（B 路线原 33 项收官后，2026-09-02 晚间 owner 授权追加后续批次 12 项：`followup-roster-consume`、`followup-model-effort`、`rfx-effort-gate`、`rfx-model-id-softening` 四个三元组，全部 done 带 proof；owner 矩阵类 regression p2-dg1/p2-dg2/p3-ui/p4-diagnostics/p4-release 全部回填；p4-doc-sync-regression 为 D0 无运行面，na 见队列；验收期新增 rfx-row6-user-echo 三元组闭环；2026-09-03 追加 §23 会话 rename/delete（`followup-session-admin` 三元组，owner 矩阵 rename/delete ✅、archive 接受现状）与 §23.8 rename 标题瑕疵修复（`rfx-rename-title` 三元组，owner 复测 ✅）；2026-09-03 追加 §24 follower permission 应答（`perm-follower` 三元组，owner 真机矩阵四步全过，见 §9））
- Related Commits: `69f3b31`（TOML 依赖）`1676df5`（开关管理器 T1–T33）`9baa1e5`（D-G1）`3089a95`（D-G2）`4cfddfd`（Phase 3 UI）`0f4e5e8`（Phase 4 诊断行）`f92cec6`（doc-sync + D-3 帮助文案）`98ae793`（验收修复 ①：scope 切换退订旧观察键）`05152aa`（验收修复 ②：读路径订阅键改观察语义）`39e29a8`（验收修复 ③：iPhone 自有 turn 的 user echo 补 turn 身份）`c77bb80`（后续批次：消费 leader roster 广播）`c1dfa81`（后续批次：model/effort 全链路）`da17648`（后续修复 ④：镜像官方 effort 门）`a0b0f11`（后续修复 ⑤：set_model unknown model id 软化）；另有 Phase 0 基线修复 `6dc9353`（D-G0b 终态通知白名单）与 cwd 缺口修复（见执行日志）；2026-09-03 批次：`441706c`/`dc30edc`（§23 方案与修复指针）`5f37692`（§23 rename/delete 实现）`eaf5703`（§23.8 display_title 标题修复）`5f24679`（§23.8 文档三件套）
- Generated At: `2026-09-02T21:04:00+08:00`（owner 验收收官后更新）；`2026-09-02T22:21:00+08:00`（后续批次 12 todos 入账后更新，见 §1.1 与 §2 追加行）；`2026-09-03T18:52:00+08:00`（§23 session-admin + §23.8 标题修复 6 todos 入账后更新，见 §8）；`2026-09-03T22:05:00+08:00`（§24 follower permission 三元组收官、owner 矩阵四步全过后更新，见 §9）；`2026-09-03T22:50:00+08:00`（§25 exit_plan_mode + 选项透传批次入账，Mac 侧交付完成待 owner 验收，见 §10）

## 1. Overall Verdict (总体结论)

B 路线 Leader 模式的产品代码全部落地并已安装到 `/Applications`（最终 runtime commit
`39e29a879f2f`，运行态身份门全过），§7.3 owner 真机矩阵 2026-09-02 验收收官。产品语义
红线全程保持：**只读共存**（不 spawn leader、不抢 flock、observer 永不回答 agent→client
请求）、**关 = 删键保留节头**、**config.toml 外科文本编辑零重排**（CRLF/注释/其他键
逐字节保留）、**fail-visible 禁止 fallback**（F1/F2 禁用+提示；版本漂移症状可见不加伪装）。

验证分级声明（CLAUDE.md 部署后运行态验证第 4 条）：

- **组件级已验证**：Go 单测（go-bridge 包级回归 + D-G2 13 用例 + G4 互锁 + user echo
  5 用例）、Swift 单测（GrokLeaderModeTests 49/49：T1–T33 + rowState 5 例 + 诊断 5 例）、
  增量 Debug 编译、Release 构建。
- **生产路径级已验证**：Release 覆盖安装（3 次，最终 `39e29a879f2f`）+ 进程代际 + 8777
  监听者 + runtime.json / management runtimeIdentity 一致 + relay 重连 + grokbuild
  目录种子；§7.3 十二行真机矩阵中 11 行 owner 实测通过 + 诊断卡确认（行 3 可选未测）。
- **验收期暴露并修复的真实缺口**（三个，均非新分支引入的回归，已带测试与文档回写）：
  D-G2 两轮订阅键语义（`98ae793` + `05152aa`）、iPhone 自有 turn 的 user echo 缺身份
  （`39e29a8`，预存缺口，2026-08-05 起）。详见设计 §0.2-11。

### 1.1 后续改进批次（2026-09-02 晚间，owner 授权「先把条件成熟的做了」）

B 路线收官后同日追加四个三元组（12 todos，见队列 followup-/rfx- 前缀）：

1. **roster 广播消费**（`c77bb80`）：leader 进程的 roster 广播 → sessions_changed，
   iPhone 会话列表实时刷新；`leader_subscriber_roster_test.go` 192 行。
2. **model/effort 全链路**（`c1dfa81`）：initialize `_meta.modelState` 模型目录 →
   `list_models` capability → session/new `_meta.{modelId,reasoningEffort}` 初始选择 →
   `session/set_model` 切换；`session_model_switch_test.go` +517 行。owner 首轮真机验收
   1✅2✅4✅（目录显示/默认会话/grok-4.5 切换）。
3. **官方 effort 门镜像**（`da17648`，首轮真机 GLM+high 残留 → -32602 的修复）：
   `effectiveEffortForModel` 镜像上游 `resolve_effort_for_model`，目录证明无效的
   (model, effort) 组合不上 wire。
4. **双模型 id 形态软化**（`a0b0f11`，二轮真机修复）：官方目录/请求用条目 id、持久化/
   应答用底层 id，iOS 把 transcript 底层 id 回发 → 目录外 → -32602。修复为 unknown
   model id 软化（WARN + 会话保持当前模型继续 turn，此时会话本就在用户所选模型上）+
   RPC 错误携带 data + set_model 参数日志。owner 终验「测试结果符合预期✅」。

组件级：`go test ./agent/grokbuild/ -count=1` 全绿（每轮跑）；生产路径级：Release 覆盖
安装（最终 `a0b0f110f76e`）四门核对 + management API grokbuild available；owner 三轮
真机反馈全部收口。根因复盘与 iOS 条目 id 另案见 `think.md` 2026-09-02 复盘节。

## 2. Gate Completion Matrix (门完成矩阵)

| Gate | 内容 | Verdict | 证据 |
| --- | --- | --- | --- |
| Phase 0 | 十步来源门（版本/flag/env/拓扑/订阅/6a/6b/7/8/9/10） | `owner-verified (2026-09-02)` | `.exec-plan/artifacts/grokbuild-leader-p0/execution-log.md` 十步总收口表；两处实测修正已回写设计 §0.2/§2.2 |
| P1a TOML 依赖 | mattt/swift-toml 2.0.0 exact 冻结 | `proven-done (re-verified)` | commit `69f3b31`；project.yml + Package.resolved + source identity 留档（§3.6） |
| P1b 开关管理器 | 路径/symlink/语义 parser/canonical locator/交叉矩阵/外科写入/备份/原子/并发/回滚 | `proven-done (re-verified)` | commit `1676df5`；T1–T33（含 CRLF、多 scope 同名键、symlink 链、link swap、converge 失败 fail-closed、崩溃窗口 ≤3 备份） |
| P2 D-G1 | leader 订阅建立失败（未转发任何事件即断开）回退 updates.jsonl tailer + INFO | `proven-done (re-verified)` | commit `9baa1e5`；G1 三路（正常/失败回退/ctx 取消不回退）+ G4 互锁 |
| P2 D-G2 | 无订阅者 ≥60s 主动取消 + registry 第四状态 unknown + claim CAS | `proven-done (re-verified)` | commit `3089a95`；`grok_leader_relay_dg2_test.go` 13 用例（G5 锚点/59.9s/区间/清零、G6 闪断、G7 synthetic+real、G8 五路 unknown 不冒充 + claim 三态）；go-bridge 包级回归 73.9s 全绿 |
| P3 UI | agentRow 行内 Toggle + 九态副文案 + 失败回弹 alert + onChange 刷新 | `proven-done (re-verified)` | commit `4cfddfd`；rowState 5 例（优先级裁决：失败态 > #2/#3 > #4 > #5 > #6）+ xcresult 逐用例核对 44/44 |
| P4a 诊断行 | DiagnosticsSheet grok 组：配置三分 + socket 路径与存在性 + 安装版本 | `proven-done (re-verified)` | commit `0f4e5e8`；诊断 5 例（三分映射语言中立断言、F1/F2 fail-visible、三段结构、版本探测优先级/回退/nil）49/49 |
| P4b 帮助文案 | D-3 interaction 提示 + 四因 + D-G2 副作用 + chat 互斥（ON 态 hover） | `proven-done` | commit `f92cec6`；`grok_leader_mode_notes` 双语 |
| P4c Release | 构建 + 覆盖安装 + 运行态身份门 | `proven-done (re-verified)` | 本文 §4；两次安装（`0f4e5e8`→`f92cec6`）均过四门 |
| P4d doc-sync | CHANGELOG / think.md / 设计实测回写 / 本报告 | `proven-done` | commit `f92cec6` + 验收期收口提交 + 本文件 |
| §7.3 owner 矩阵 | 12 行真机验收 | **`owner-verified (2026-09-02)`** | 本文 §6 清单（11 行过 + 诊断卡确认；行 3 可选未测）；日志佐证：D-G2 取消 elapsed=1m0s、D-G1 回退行 20:59:03、SYNTHESIZE 21:00:47 → 干净终态 21:00:53 |
| 验收修复 ①② | D-G2 订阅键语义两轮修复 | `proven-done (re-verified)` | commits `98ae793`（scope 切换退订旧观察键）+ `05152aa`（读路径记空目录观察键）；owner 复测行 12 过 |
| 验收修复 ③ | iPhone 自有 turn user echo 补 turn 身份 | `proven-done (re-verified)` | commit `39e29a8`；`session_user_echo_test.go` 5 用例 + go-bridge/grokbuild 全量绿；owner 复测行 6 过（「发送的消息保持在对话里」） |
| 后续批次 · roster 消费 | leader roster 广播 → sessions_changed 实时刷新 | `owner-verified (2026-09-02)` | commit `c77bb80`；`leader_subscriber_roster_test.go` 192 行；owner 首轮真机验收 |
| 后续批次 · model/effort | 模型目录 / 初始 `_meta` / `set_model` 切换全链路 | `owner-verified (2026-09-02)` | commit `c1dfa81`；`session_model_switch_test.go` +517 行；owner 首轮 1✅2✅4✅ |
| 后续修复 ④ | 官方 effort 门镜像（无效组合不上 wire） | `proven-done (re-verified)` | commit `da17648`；`TestEffectiveEffortForModel` 7 断言 + 2 场景测试；上游锚点 `model_state.rs` |
| 后续修复 ⑤ | 双模型 id 形态：unknown model id 软化 + RPC data | `owner-verified (2026-09-02)` | commit `a0b0f11`；`TestSendUnknownModelIdSoftensToCurrentModel`；owner 终验「测试结果符合预期✅」；Release `a0b0f110f76e` 四门核对 |

### 2.1 Upstream Anchors (上游锚点)

| 语义 | 上游锚点（`/Users/jacklee/Projects/grok-build` @ `bc7f02ed`；评审机实测 1.0.12） | CordCode 取用 |
| --- | --- | --- |
| `[cli] use_leader` user 层键 | config 持久化 + `resolve_leader_mode` 决策链（§2.1-1 四因） | 只写该键；读取走语义 parser + 仓内 canonical locator 交叉裁决 |
| 订阅观察（leader → 订阅者推送） | server.rs session/update 广播 + `session_driver.entry().or_insert`（首个 load 者为 driver，订阅者死亡不终止 turn） | observer 永不回答、只订阅；6a 实测一致 |
| live rail 终态通知 | `_x.ai/session_notification`（gateway ext 包裹形态） | D-G0b 白名单修复（`6dc9353`）；codec 零改动承接 |
| disconnect 语义 / flock / stale socket | `leader/mod.rs` connect_or_spawn + flock 随进程死亡释放 | 只读共存：stat 判据 + D-G1 回退；Phase 0 第 8 步实测自愈语义回写设计 |
| interaction 共享广播 | `server.rs:491-500` first-answer-wins + `[toolset.ask_user_question]` timeout | 不答、帮助文案披露（D-3）；6b 实测 56s 挂起 + replay-on-attach 回写 §6-6 |
| model/effort 契约与门 | `xai-grok-pager/src/acp/model_state.rs`：`resolve_effort_for_model`（gate on supports 标志，目录外 `unwrap_or(false)`）+ `_meta.modelState` 形态 | `effectiveEffortForModel` 逐项镜像；目录/初始 `_meta`/set_model 按 1.0.13 wire 契约实测样本实现（探针 fixture） |

## 3. Key File Changes (关键文件变更)

- `MacBridge/MacBridge/Services/GrokLeaderModeManager.swift`（新增）：`GrokLeaderPaths` 解析链（`GROK_HOME`→`~/.grok`；socket `GROK_LEADER_SOCKET`→默认）、`GrokLeaderSymlinkResolver`（悬空/循环/非普通目标 F1）、`GrokLeaderSemanticParser` + `GrokLeaderLocator` + `GrokLeaderCrossMatrix`（F2 等价形态）、`GrokLeaderConfigFileEditor`（开=safeAppend/inPlaceEdit，关=删键保节头）、`GrokLeaderConfigWriter`（备份滚动 ≤3、原子 rename、并发检测、写后校验、受限回滚）、`GrokLeaderRowState` 九态、`GrokLeaderVersionProbe`（镜像 RuntimeManager CLI 搜索链）、`diagnosticsSummary` 三段构建。
- `MacBridge/MacBridge/Views/WorkspaceView.swift`：grokbuild 行 Toggle 槽位（`.switch`/`.small`/172×32 对齐 codex-web 先例）、九态副文案（#2 橙色）、失败 alert（回弹 + 备份目录）、onAppear/onChange(agents) 刷新、ON 态 hover 长文案（D-3/四因/D-G2 副作用/chat 互斥）。
- `MacBridge/MacBridge/Views/DiagnosticsSheet.swift`：grok 组 Leader 状态卡（配置三分 + socket 路径与存在性 + 安装版本；状态点颜色复用 rowState 单一真值；task/刷新按钮重探测）。
- `MacBridge/MacBridge/Services/Localization.swift`：26 键双语（开关/九态/失败/诊断 12/帮助长文案）。
- `MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift`：`BackendAgentStatus` 补 `Equatable`（onChange 前提）。
- `agent/grokbuild/grokbuild.go`：D-G1 建立失败回退 tailer（§3.5.1）。
- `go-bridge/handlers_relay.go` + `go-bridge/types.go`：D-G2 订阅者守望（`grokLeaderSessionRelayLoop`、F-7 分流、sessionStateUnknown 第四状态、claim generation CAS、`isKnownActive` 守卫 idleTimer/claude TTL/codex 硬上限三路）。
- `agent/grokbuild/leader_subscriber.go`：D-G0b 终态通知白名单 wire 形态修复（Phase 0 基线，`6dc9353`）。
- `go-bridge/handlers_relay.go`（验收修复 ①②）：`set_observation_scope` 切换时 `ReconcileObservationSubscriptions` 退订旧观察键（`98ae793`）；读路径（iOS `get_session` 触发的观察订阅）统一记**空目录观察键**而非带 `currentSessionDirectory` 的键，幸存 reconcile 语义（`05152aa`）。
- `agent/grokbuild/session.go`（验收修复 ③）：新增 `emitTurnScoped` —— `user_message_chunk` 回显（上游 meta.rs 按设计不 stamp promptId）缓冲为 `pendingUserEcho`，同 turn 首个带身份事件（含 `turn_completed` 终态）到达时以其 TurnID/ItemID 补发，`Send` 开新 turn 时清残留；session/update 循环全部走该方法，观察与自有 turn 两条消费路径统一覆盖（`39e29a8`）。

## 4. Verification Evidence (验证证据，标注 attestation)

| # | 验证 | 命令/证据 | 结果 | attestation |
| --- | --- | --- | --- | --- |
| 1 | Go 全量（go-bridge 包级 + D-G2） | `go test ./go-bridge/... -count=1`（含 dg2 13 用例） | 全绿 73.9s | re-verified（本轮执行） |
| 2 | G4 互锁 | `go test ./agent/grokbuild/ -count=1` | 全绿 16.7s | re-verified |
| 3 | Swift 单测 | `xcodebuild … test -only-testing:CordCodeLinkTests/GrokLeaderModeTests` | 49/49（T1–T33 + rowState 5 + 诊断 5；xcresult `Test-CordCodeLink-2026.09.02_18-07-38` 逐用例名核对） | re-verified |
| 4 | Debug 增量编译 | `xcodebuild … build`（复用 `MacBridge/build/DerivedData`） | BUILD SUCCEEDED ×3 | re-verified |
| 5 | Release 构建 | `./scripts/build-unsigned-release.sh` ×2 | `Runtime … commit: f92cec622e3c, built: 2026-09-02T10:15:19Z`；`dist/CordCodeLink-0.1.0-macos-arm64-unsigned.zip` | re-verified |
| 6 | 部署运行态四门（最终安装） | killall+pkill 双进程名 → cp -R → open；`lsof -t 8777`=50571；`ps lstart`=18:16:22（晚于构建 18:15:19）；runtime.json pid=50571；management `runtimeIdentity.pid`=50571；pgrep 无 /Applications 之外残留 | 全过 | re-verified（生产路径级） |
| 7 | 重启后健康 | go-bridge.log 18:09/18:16 起滚动；relay `connected`；grokbuild `session discovery snapshot seeded sessionCount=19` | 正常 | re-verified（生产路径级） |
| 8 | UI 观感（Toggle 副文案/诊断行渲染/alert） | — | **owner 真机确认**（§6 矩阵 #1–#3、#7 过 + 诊断卡过） | owner-verified |
| 9 | secret scan | 提交链只含源码/测试/文档；config.toml GLM api_key 未入库（`git log --all --oneline -- '**/config.toml'` 无生产配置） | 通过 | self-attested |
| 10 | 验收期回归（Go） | `go test ./agent/grokbuild/ -count=1` + `go test ./go-bridge/... -count=1`（含 `session_user_echo_test.go` 5 用例） | grokbuild 16.7s / go-bridge 74.1s 全绿 | re-verified（39e29a8 后执行） |
| 11 | 部署运行态四门（最终安装 ③，`39e29a8`） | killall+pkill 双进程名 → cp -R → open；runtime stamp `39e29a879f2f`；pid/lstart/runtime.json 一致；无 /Applications 之外残留 | 全过（owner 行 6 复测即在该 runtime 上通过） | re-verified（生产路径级） |

## 5. P0 来源门记录（三个门点）

```
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader
分支=codex/grokbuild-leader-mode
提交=读源码/修改门: 39e29a879f2f…（验收修复 ③ 提交后）
       构建/安装门: 39e29a879f2f…（构建 stamp 与提交一致；最终安装即该产物）
       doc-sync 门: 验收收官后 D0 提交（本文件/CHANGELOG/think.md/设计 §0.2-11）
未提交状态=仅 untracked docs/2026-09-02-same-name-ios-device-eviction-fix-design.md
          （另一任务设计文档，明确排除在全部提交与产物之外）
任务预期分支=codex/grokbuild-leader-mode（一致 ✓）
配套仓库=无（单仓任务，iOS 零修改 ✓；owner 真机矩阵使用 iPhone 客户端属验收动作非源码依赖）
预期产品特性=agentRow grokbuild Leader Toggle、九态副文案、DiagnosticsSheet grok 组
            状态行、D-G1 回退、D-G2 守望（含两轮订阅键修复）、user echo 身份补齐
            （全部在产物源码中核对 ✓）
```

## 6. Owner 验收清单（§7.3 十二行，产品语言）—— 已收官 2026-09-02

前提：Mac 上 CordCode Link 已是本轮构建（帮助与诊断里 grok 版本行可查）；iPhone 正常
连接。结果：**11 行 owner 实测通过 + 诊断卡确认；行 3 为可选行未测**。验收期暴露的
三个缺口已修复（见 §2 矩阵验收修复行），修复后相关行由 owner 复测通过。

1. **开** ✅：AI 工具列表 Grok Build 行打开「Leader 模式」→ 行内出现橙色「已开启，重启
   grok 后生效」；`~/.grok/config.toml` 出现 `use_leader = true`，其他内容原样。
2. **关** ✅：再关闭 → 键被删除（不是 false；空 `[cli]` 节头保留）；`~/Library/Application
   Support/CordCode Link/` 下备份目录存在（≤3 份，0700/0600）。
3. **未安装态**（可选，PATH 移除 grok 时）未测：开关置灰，悬浮提示「未检测到 grok CLI」。
4. **生效链** ✅：开关开着 → 启动 grok TUI 发一条消息 → iPhone 打开同一会话：内容实时
   可见（推送节奏，非 1 秒一次的轮询节奏）。
5. **长消息流式** ✅：TUI 发长消息 → iPhone 实时流式显示，turn 有唯一终态。
6. **iPhone 自己发起** ✅（修复后复测）：iPhone 对自己发起的 grok 会话发消息正常，且
   发送的 prompt 保持在对话里（owner 原话「测试符合预期✅，发送的消息保持在对话里」；
   修复 `39e29a8`，预存缺口非本分支引入）。
7. **未重启提示** ✅：开关开着但 grok 没重启 → 提示保持橙色「重启后生效」，不显示
   「检测到 socket」。
8. **关 TUI 不断流** ✅：开关开着、TUI 里一个没在等权限/提问的任务进行中、iPhone 会话
   保持打开 → 关闭 TUI：任务继续完成，iPhone 看到完整收尾。
9. **OFF 基线** ✅：开关关闭、grok 正常运行 → TUI 发消息、iPhone 打开同一会话：观察照旧
   （日志有 `falling back to updates.jsonl file tailer` 行）。
10. **leader 崩溃** ✅：开关开着、TUI 里任务进行中、iPhone 会话打开 → `kill -9` leader
    进程：iPhone 收到明确「已中断」并回到空闲，不残留「执行中」（agent 辅助执行，
    F-7 分流实测 17ms 内合成）。
11. **stale socket 回退** ✅：`kill -9` leader 留下 socket 文件 → iPhone 重开会话：观察
    经回退继续工作（事件按轮询节奏恢复），日志出现回退 INFO 行（agent 辅助三步执行：
    退 TUI → 杀 leader → iPhone 重开；日志 20:59:03 `leader subscribe failed, falling
    back to updates.jsonl tailer` → 21:00:47 恢复 → 21:00:53 干净终态）。
12. **无人观看自动下线** ✅（修复后复测）：某会话 observer 在场 → iPhone 关闭该会话
    并等 >70 秒：日志可见 observer 取消（elapsed=1m0s）、relay 退出；**无虚假「已中断」**；
    侧栏不再显示「运行中」；重开会话后观察恢复（修复 `98ae793`+`05152aa`，两轮订阅键
    语义修复）。

帮助与诊断 ✅：App 左侧「帮助与诊断」→「Grok Leader 状态」卡正常（配置三值、Socket
路径与存在性、grok 版本 `grok 1.0.12 (…)`）。

## 7. 后续另案（不在本验收内）

- follower 交互升级（D-3 已批另案，前置的 source-first 冻结条件见 think.md 总账）。
  question-only 起步已另计划交付（2026-09-03，owner 矩阵四行全过，见
  `2026-09-02-grokbuild-follower-question-implementation-plan完成情况.md`）；剩余
  permission/interjection 升级仍未开工。
- iOS 发送 Grok 模型改用 list_models 条目 id（transcript 底层 id 仅用于显示）——
  2026-09-02 登记；Mac 侧已部署 unknown model id 软化兜底（`a0b0f11`），iOS 根治另案。
- iOS automation open-session 冷启动 get_session 竞态（2026-09-03 §23.8 排查中发现；
  设计文档 §23.8 末尾有记录）——**已修复（2026-09-03，iOS 主树工作区）**：根因是冷启动
  hello_ack 未到达时 `resolveBackendClient` 返回 `BridgeUnavailableBackendClient` 桩，
  getSession 本地即抛被 `try?` 吞掉、无 wire RPC；修复 = ContentView.swift `openSession`
  的 Task 内有界等待（10s/200ms 轮询）真实 client 建立后再解析，等待窗口内用户切走则
  不回写。同路径的通知点开（notification-pending/live replay）一并受益。真机验证：
  env 注入重启后 Mac 日志出现 get_session（19:00:47 req_2）且 header 显示真实标题。
  （iOS 仓有他轮未提交改动，本修复暂留在工作区随 iOS 侧一并提交。）

（原列于此的 roster 通知消费、model/provider/effort 缺口已于 2026-09-02 晚间完成并
入账本报告 §1.1，think.md 总账对应行已删除。）

## 8. 2026-09-03 追加批次：§23 会话 rename/delete + §23.8 标题修复

owner 2026-09-03 授权按设计 §23 直接实施会话重命名/删除；验收后修复唯一瑕疵。
两个 proof-carrying 三元组入账（`followup-session-admin-*`、`rfx-rename-title-*`），
队列 51/51 done，queue hash `185719b9dbfe`。

**§23 rename/delete**（`5f37692`，方案 `441706c`/`dc30edc`）：经官方 session-admin
ext 方法（`_x.ai/session/rename` / `_x.ai/session/delete`，catalog rail 半包装形态，
grok 1.0.13 探针锚定）实现 `core.SessionRenamer`/`SessionDeleter`；官方 error data
透传；archive 有意不实现（官方无此功能）。owner 真机矩阵：rename ✅、delete ✅、
archive 报「not yet supported」owner 裁决接受现状。

**§23.8 标题修复**（`eaf5703`，文档 `5f24679`）：rename 后消息页标题停留旧名的
根因是官方 rename 只写 `generated_title` 不回写 `session_summary`，CordCode 详情链
只读后者、列表链经官方解析——两条链标题分裂，rename 后 50ms 的 get_session 刷新
把新标题覆盖回旧值。修复 = `parseSummaryFile` 镜像官方 `display_title()` 优先级。
验证：定向单测（磁盘实测 fixture 三例）+ 包全量全绿；Release 四门核对；生产路径
wire 探针（临时配对设备直连生产 8777 的真实 get_session）返回新标题，探针设备已
revoke；owner 真机复测「测试结果符合预期✅」（2026-09-03）。

## 9. 2026-09-03 追加批次：§24 follower permission 应答

owner 指令「先方案入档、扩展现有 plan JSON、继续 exec-plan」。一个 proof-carrying
三元组（`perm-follower-*`）入账，队列 54/54 done，queue hash `6d25a7906b1c`。

**实现**（`1661e91`，方案 `5511a84`，收口 `12a2abc`）：Mac 单仓、iOS/protocol pack
零改动——wire `resolve_permission` → 既有 `core.SessionPermissionResponder` 接口
（core/interfaces.go:66 早已存在）type assertion 即通。leader 订阅门加宽
（`session/request_permission` → `handlePermissionBroadcast`：registry 按 wireID 索引 +
emit `permission_request`）；`AnswerPermission`（getByWire 预检 → permissionOutcome
行为映射 → take → 原始 numeric id 回帧）；TUI 先答经 `interaction_resolved` 广播收口。
`permissionOutcome` 共享 helper（acp_codec.go）：allow/always→allow 选项（always 无
grok 对应降级）、deny→deny 选项、无 deny→cancelled。

**验证**：单测 5 例（`leader_permission_test.go`：允许应答/行为映射/TUI 先答广播/
未知 id/registry 双轴一致性）+ 包全量全绿（re-verified）；Release 四门核对（PID
18150 lstart 晚于构建、8777 = /Applications 内嵌 runtime、无残留）；owner 真机矩阵
四步全过（2026-09-03 21:52-21:54，grok 1.0.13 leader 会话，`permission_mode=ask` +
yolo 关闭）：TUI 发起权限 turn（`rm -rf /tmp/grok-perm-test`）→ iPhone 权限卡同步
出现；iPhone 允许 → 任务继续 + Mac 弹窗自动收口；iPhone 拒绝路径 ✅；Mac 先答 →
iPhone 卡自动消失。owner 原话「1✅2✅，测试结果符合预期」。

**范围裁决**（非缺陷）：grok TUI 5 档选项（always-approve 模式切换 / 按命令
Always allow / Never allow / 拒绝附反馈文字）vs iOS 允许/拒绝两键——iOS 权限卡为
跨后端通用组件，bridge-v1 权限语义即二元应答；选项集透传属范围扩展（需 wire
协议 + iOS UI 改造），另案。exit_plan_mode / mcp elicit 维持 ruling B observe-only。

**排障教训入账**：owner 初测「几十次不弹」的根因是 grok 侧自动放行（事件流实证
23 次权限请求全部毫秒级 allow）——Ctrl+O 为 yolo 全放行开关（TUI actions.rs:424）、
`[ui] permission_mode` 持久化且停在 `always-approve`；两者任一处于放行态即不询问。
测试前须确认 `permission_mode = "ask"` 且 yolo 关闭。

## 10. 2026-09-03 追加批次：§25 exit_plan_mode 开放 + 权限选项透传

owner 裁决「可以把剩下需要做的都做了吧，那个 plan mode……先把能开发的代码都开发了」
——本批次推翻 §9「范围裁决」中的两点搁置（exit_plan_mode observe-only、选项透传
另案），按设计文档 §25 实施。MCP elicit 仍 observe-only（维持 ruling B 原判）。
本批次为 owner 直指令快车道，未开 exec-plan 三元组；验收证据形态与 §9 同构。

**交付内容**（Mac 单仓，iOS 零改动）：

1. exit_plan_mode（plan 审批）：registry kind 三元（question/permission/plan）+
   `wireAnswerable` 统一 wire 轴；`handlePlanBroadcast` 表面化为「计划审批: 」权限卡
   （标题取计划首个标题行，80 rune 截断）；iPhone 允许→`{outcome:"approved"}`、
   拒绝→`{outcome:"cancelled"}`；interaction_resolved 双 kind 收口。
2. 权限选项透传：`permissionOptionActions`（实收 options kind 动态映射，规范序
   approve→approveAlways→reject；reject_always 有意不透传——iOS 应答无法区分持久
   拒绝与单次拒绝）；`grokPermissionKind`（execute→bash 等，空/未知→"grok" 占位）；
   leader/OFF 两轨 + plan 卡三路 emit 同步；`permissionOutcome` kind 精确化
   （always→allow_always 优先、deny→reject_once 优先、§24 降级路径全兼容）。
3. 上游锚点：grok-build @72a61251 prompter.rs（options 按 session 创建者档位广播，
   不按订阅者定制——故必须动态映射）+ exit_plan_mode/types.rs。

**验证**（self-attested，命令可复跑）：`leader_plan_test.go` 5 例 +
`leader_permission_options_test.go` 6 例 + §24 既有测试零改动通过；
`go test ./agent/grokbuild/ -count=1` ok 24.7s；`go build ./...` + relay-server 过；
Release 0699945+§25 构建产物 strings 含两个特征日志文案；覆盖安装四门核对过
（runtime PID 17642 lstart 22:44:12 晚于构建 22:42:51、8777 监听者
/Applications 内嵌 runtime、无违规残留、日志新代际）。

**待 owner 真机验收**（设计文档 §25.5）：① TUI plan 模式任务→iPhone 出「计划审批」
两键卡；② iPhone 允许/拒绝→TUI 计划批准/取消；③ bash 权限卡出现「总是允许」三键、
点后 grok 侧持久放行（后续同类不再询问）；④ Mac TUI 先答→iPhone 卡收口。
