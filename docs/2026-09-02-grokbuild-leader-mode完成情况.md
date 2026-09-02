# 本轮任务完成情况：Grok Build Leader 模式开关（B 路线，v10 APPROVE）

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader`（单仓任务，iOS / protocol pack / SSV2 零修改）
- Plan: `docs/2026-08-28-grokbuild-leader-mode-design.md`（v10 owner APPROVE）
- Canonical State File: `.exec-plan/state/plan-60f196abb855.json`
- Completion Report Verdict: **`proved-complete (owner-verified 2026-09-02)`**（产品代码、单测、Release 安装、§7.3 owner 真机矩阵全部收口；12 行中 11 行 owner 实测通过 + 诊断卡确认，行 3「未安装态」为可选行未测，已在 exec-plan regression 证据中注明）
- Queue Summary: **33/33 todos done**（owner 矩阵类 regression p2-dg1/p2-dg2/p3-ui/p4-diagnostics/p4-release 全部回填；p4-doc-sync-regression 为 D0 无运行面，na 见队列；验收期新增 rfx-row6-user-echo 三元组闭环）
- Related Commits: `69f3b31`（TOML 依赖）`1676df5`（开关管理器 T1–T33）`9baa1e5`（D-G1）`3089a95`（D-G2）`4cfddfd`（Phase 3 UI）`0f4e5e8`（Phase 4 诊断行）`f92cec6`（doc-sync + D-3 帮助文案）`98ae793`（验收修复 ①：scope 切换退订旧观察键）`05152aa`（验收修复 ②：读路径订阅键改观察语义）`39e29a8`（验收修复 ③：iPhone 自有 turn 的 user echo 补 turn 身份）；另有 Phase 0 基线修复 `6dc9353`（D-G0b 终态通知白名单）与 cwd 缺口修复（见执行日志）
- Generated At: `2026-09-02T21:04:00+08:00`（owner 验收收官后最终更新）

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

### 2.1 Upstream Anchors (上游锚点)

| 语义 | 上游锚点（`/Users/jacklee/Projects/grok-build` @ `bc7f02ed`；评审机实测 1.0.12） | CordCode 取用 |
| --- | --- | --- |
| `[cli] use_leader` user 层键 | config 持久化 + `resolve_leader_mode` 决策链（§2.1-1 四因） | 只写该键；读取走语义 parser + 仓内 canonical locator 交叉裁决 |
| 订阅观察（leader → 订阅者推送） | server.rs session/update 广播 + `session_driver.entry().or_insert`（首个 load 者为 driver，订阅者死亡不终止 turn） | observer 永不回答、只订阅；6a 实测一致 |
| live rail 终态通知 | `_x.ai/session_notification`（gateway ext 包裹形态） | D-G0b 白名单修复（`6dc9353`）；codec 零改动承接 |
| disconnect 语义 / flock / stale socket | `leader/mod.rs` connect_or_spawn + flock 随进程死亡释放 | 只读共存：stat 判据 + D-G1 回退；Phase 0 第 8 步实测自愈语义回写设计 |
| interaction 共享广播 | `server.rs:491-500` first-answer-wins + `[toolset.ask_user_question]` timeout | 不答、帮助文案披露（D-3）；6b 实测 56s 挂起 + replay-on-attach 回写 §6-6 |

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

follower 交互升级（D-3 已批另案，前置的 source-first 冻结条件见 think.md 总账）、
roster 通知消费、model/provider/effort 缺口——均已登记 think.md 顶部总账。
