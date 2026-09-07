# Grok Build 斜杠命令面板 + 计划模式官方通道 接入方案

> 状态：**方案 v1.2（2026-09-07，未实施）**。v1.0 经[第一轮实施评审](2026-09-07-grok-build-slash-command-panel-implementation-review.md)不通过（6 P1 + 2 P2）修订为 v1.1；v1.1 经[第二轮评审](2026-09-07-grok-build-slash-command-panel-implementation-review-r2.md)仍不通过（4 P1 + 2 P2：S1-S6），本版逐项修订，两轮响应见下方「评审响应与修订记录」。dsh 方案 §11 的十轮返工教训（尤其 §11.2 ⑤⑧⑨⑩与 §11.3 四拍纪律、§11.4 迁移检查单）仍是本方案的**前置必读**。事实基线：调研
> [2026-09-04-slash-command-skill-cross-backend-survey.md](2026-09-04-slash-command-skill-cross-backend-survey.md)
> §7（两轮独立评审通过）。

## 评审响应与修订记录

### 第一轮（v1.0 → v1.1）：R1-R8 响应

8 项全部吸收，无整项不采纳；两处部分采纳理由随行注明。第二轮复核认定
R2/R3 方案层关闭，R1/R4/R5/R6/R7/R8 **响应但未闭环**——残留缺口即第二轮
S1-S6，全部在本版处理。「已响应」不等于「已闭环」。

| 评审项 | 级别 | 响应 | 落点 |
| --- | --- | --- | --- |
| R1 `SetMode` 接口无 sessionID/ctx/error | P1 | 新增 `SessionModeSwitcher` 接口 + handler 优先分支；第二轮指出 resident 归属与权限键路由未定义 → v1.2 §6.2.0/§6.2.4 补齐 | §5.1、§6.2.1 |
| R2 仅放宽门控泄漏 dsh `/plan` 回退 | P1 | 六项 iOS 清单 + 三态呈现 + per-backend 白名单（第二轮认定方案层关闭） | §5.2、§7 |
| R3 「input 与 dsh 同构」被 pager 源码否定 | P1 | 撤回官方同构结论；改标移动端规则（D4，第二轮认定可裁决） | §4.1.1、§3.2 D4 |
| R4 CMU 作 ack / 落盘未证 | P1 | settle 改 RPC response；第二轮指出持久化屏障仍不成立 → v1.2 §6.2.2 三态 + durable 读回重写 | §4.3、§6.2.2 |
| R5 缓存缺失放行 + 静默降级 | P1 | 双 fail-closed + 删 builtins 兜底；第二轮指出 fresh 无失效定义 → v1.2 §6.1 缓存规则补齐 | §4.4、§6.1 |
| R6 `_meta.mode` 陈旧回写 | P1 | 删除 prompt 携带；第二轮指出冷恢复与排序未完成 → v1.2 §6.2.0/§6.2.3 重写 | §2、§6.2.3 |
| R7 「Grok 无 /plan」/目录混淆 | P2 | 三集合表述 + 范围统一；第二轮指出反馈覆盖仍阻断 → v1.2 §7.1a 矩阵 | §4.2、§7.1a |
| R8 探针越界 / 终态映射 | P2 | 证据矩阵 + settle 四分法；第二轮细化业务成功语义 → v1.2 §4.4/§6.1 | §0.1、§6.1 |

### 第二轮（v1.1 → v1.2）：S1-S6 响应

6 项全部采纳，无不采纳项。

| 评审项 | 级别 | 响应 | 落点 |
| --- | --- | --- | --- |
| S1 CMU/短延迟不是持久化屏障；软失败留下成功假象（写盘失败后新 actor 本就是 default，不会再发 CMU 纠正 chip，观测不自然收敛） | P1 | **采纳**：删除「CMU/短延迟屏障」「软失败无污染、观测自然收敛」；settle 定义三态（RPC 已接受 / durable 已确认 / 未确认）；durable = 有界等待内权威读回（`plan_mode.json` 或 P7 证明的等价官方路径）；读回超时 = 真实 RPC error「持久化未确认」，chip 不翻、不自动重试、不伪造反向状态；P7 增关闭方式/失败判定/故障注入边界；1.0.13 无法建立可靠读回 → **Phase 2 切换能力整体阻断** | §6.2.2、§0.1 P7、§10 |
| S2 「单写者」缺 resident 所有权与并发规则（`session.go:99` 每 child 都是 `--no-leader` 独立进程；set_mode 改收到请求的 actor；外部 TUI 是另一 actor，仅 spawn 时读文件） | P1 | **采纳**：新增 §6.2.0 resident 路由三态（idle / CordCode 活跃 turn / 外部 resident）；per-session 锁扩展覆盖 Send/Execute/SetSessionMode（活跃 turn 时切换等待，超时报错不并发）；外部 resident 场景明示「两个独立 actor、文件仅 spawn 时同步、TUI 热更新不承诺」，跨进程并发写文件 = 上游行为由 P4(b)/P7(e) 取证，未证明前不作产品承诺；「单写者」改精确表述：CordCode 自身模式声明通道收敛为一个（删 `_meta.mode`），actor/外部一致性另行保证 | §6.2.0、§6.2.3、§0.1 P4/P7、§9 #5 |
| S3 冷恢复不存在于「既有 tailer 路径」（tailer 从 EOF 起，leader 丢 isReplay）；到达序不保证不回退 | P1 | **采纳**：冷 hydrate 改为**明确新增实现**（入口 = 会话打开/bridge 重启后首个状态请求；来源 = P8 裁决：summary.json mode / plan_mode.json 快照 / updates.jsonl 尾部有界回扫——均为新代码；失败/空 = mode 未知中性态，不伪造）；response patch 纳入 bridge 单点到达序 last-wins；同 epoch（per-session 锁窗口内自有子进程 CMU vs response patch）去重；重订阅（重连/重启）重新 hydrate 重基线、丢弃陈旧 live；跨源无序号时旧通知晚到翻回 = 明示残差（P5 取证是否可比较序号），重基线兜底；验收含重启后静止会话恢复 + 旧通知晚到不翻回（注入序可控验证 bridge 排序逻辑） | §6.2.3、§6.3、§0.1 P5/P8、§8 |
| S4 两类输入走查不覆盖全目录反馈；`/context` 已是反例（shell `ok_end_turn(0,None)` 无详情；pager 是 ShowContextInfo 本地面） | P1 | **采纳**：新增 §7.1a **反馈覆盖矩阵**（语义组 → 请求 → 官方输出/副作用 → iPhone 呈现 → 验证样本），按反馈类型分组替代逐命令四拍；本地呈现面组（context 类）第一期不纳入面板（接入本地 surface 另案），是否列出由 D1 依矩阵裁决；`ResultKind:"success"` 语义重定义 = 仅 turn 正常完成，**不代表业务成功**；正文承载的业务失败原样上屏，禁止 NLP 猜测错误；错误弹窗仅 RPC 层失败，正文错误不承诺弹窗；P6 扩无输出组 + 正文业务失败组样本 | §7.1a、§6.1、§4.4、§0.1 P6 |
| S5 handler 优先分支只接 plan/default 与「其余五键维持现状」冲突（`PermissionModes()` 仍广告 6 键，iOS 统一走 `setPermissionMode`）；default 双语义 | P2 | **采纳**：§6.2.4 权限键路由表逐键定义（plan/default → SessionModeSwitcher；其余 5 键 → D3 二选一：推荐从 `PermissionModes()` 移除（现状是空转假成功）或备选保留走 legacy 分支维持既有空转广播）；default 获得真实退出 plan 效果作为产品意图明示进 D3；参数化测试锁 6 键分派/错误/广播 | §6.2.4、§3.2 D3、§8 |
| S6 fresh 目录只有 fetchedAt 无失效定义（fresh 可以是任意旧快照） | P2 | **采纳**：§6.1 缓存规则定稿——身份键 `(backend, sessionID, cwd)`（fetchedAt 仅诊断元数据）；写入 = ACU/list 成功即**整表替换**（含空表）；失效 = bridge 重启全清 + cwd 变更换键；无 TTL（每个 per-turn 子进程 session/load 都推 ACU 自动刷新）；iOS 面板**每次打开**发 List（Mac 命中缓存即回，未命中走通道 2），替换 §2「冷拉一次」表述；测试覆盖失效重取/空表替换/跨 cwd 不复用 | §6.1、§2、§8 |

两处**部分采纳沿用**（第二轮已裁定接受）：(a) 不逐命令重复四拍，以
§7.1a 反馈类型分组矩阵替代；(b) RPC 失败沿用现有弹窗（inline 化跨 backend
另案）——但按 S4 细化：**正文错误不是 RPC 失败**，走 transcript 原样上屏，
不承诺弹窗。

## 0. 来源清单（P0）与开工门

| 仓库 | 工作树 | 分支 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| grok-build（上游，只读） | `/Users/jacklee/Projects/grok-build` | detached @ `72a61251` | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9`（1.0.16） | 干净 |
| **目标运行版本**（本机安装二进制） | `~/.grok/bin/grok` | — | **1.0.13（自报 `5e9a58528b76`，不在 checkout 历史中）** | — |
| cordcode-macbridge | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（本方案工作树） | `plan/approval-layer` | `9efaa5a` + 本 v1.2 修订提交（产品源码与 `de17e6f` 零差异） | 干净（本文档与两轮评审报告除外） |
| cordcode-ios | `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 |

来源注记：v1.0 调研期锚点在 `a04095e`（本 worktree）读取、`fbb4940`（main
checkout）复核零差异。v1.1 修订轮复核了第一轮全部锚点。**v1.2 修订轮
（2026-09-07）**：第二轮报告新增锚点已逐条复核属实——macbridge
`agent/grokbuild/session.go:99`（`--no-leader stdio` 子进程启动）、
`updates_file_tailer.go:60`（EOF 起尾随，不回放）、`leader_subscriber.go:573`
（isReplay 丢弃）、`grokbuild.go:585`（PermissionModes 6 键）；grok
`slash_exec.rs:60-90`（FlushMemory skip / Dream 无输出 / ContextInfo
`ok_end_turn(0,None)`）、`slash_exec.rs:90-180`（Hooks 文本经
`send_host_turn_slash_command_output` 后正常 end_turn）、pager
`slash/commands/context.rs:21`（`Action::ShowContextInfo`）、
`acp_session.rs:1365-1405`（AgentMessageChunk + HOST_TURN_META_KEY 正文通道）；
iOS `ChatUIKitContainerView.swift:6193`（权限菜单由 catalog 构建）、`:6236`
（`applyPermissionMode` 统一入口）。iOS 锚点读自 main checkout，实施若换 iOS
工作树须按门点规则重新登记。

执行本方案前，三个门点（读源码分析前 / 首次改文件前 / 构建前）必须按
CLAUDE.md P0 重新生成本清单；本文锚点行号以 grok-build @ `72a61251` 与
macbridge @ `de17e6f` 为准，换基线须逐条复核。

### 0.1 版本漂移门：Phase 0 证据矩阵（本方案特有的 P0 前置）

think.md 2026-09-02 条目已证明：**checkout ≠ 目标二进制**（安装版 1.0.13 的
commit 不在 checkout 历史）。本方案锚点在 1.0.16 源码上核验；**编码开始前必须
先在安装版 1.0.13 上活体取证**。零 API 探针（fake client：`grok agent stdio`
+ 只发 initialize/session/load 等非 turn 方法）只能覆盖 P1/P2/P3/P5/P7/P8；
**P4/P6 必须真实 turn**——成本与副作用逐项列出，执行前向 owner 报备。**样本
不足的依赖项保持阻断，不得移到 §11 开放项后继续编码；本节各探针的拟定机制
不能保证其承诺时，对应能力同样阻断（第二轮 S1 裁定）。**

| # | 取证项 | 前置与方法序列 | 模型调用 | 副作用与复位 | 决定什么 |
| --- | --- | --- | --- | --- | --- |
| P1 | initialize `_meta.availableCommands` 真实形状 | fake client → initialize | 无 | 无 | 中间解码类型（R5 后仅诊断用途） |
| P2 | session/load 后 `available_commands_update` 全形状 | fake client → initialize → session/load | 无 | 无 | 全目录解码与展示字段 |
| P3 | catalog 单例子进程调 `x.ai/commands/list {cwd}` | 进程级 catalog 子进程 → initialize → ext list | 无 | 无 | 冷/缓存缺失时 List 拉取腿 |
| P4 | **（真实 turn）prompt 不带 `_meta.mode` 的继承 + 已有 resident 时切换**：(a) 置 plan → 退子进程 → 新子进程 load → 发不带 mode 的 prompt，验证按 plan 行为；(b) **Mac TUI 打开同会话（外部 resident 在场）时手机切换，随后发送**，观察 TUI 行为、文件写序、gateway CMU | (a) 依赖 P7 先置 plan；(b) 需 Mac 端 TUI 配合（owner 在场） | **有**（一条 turn） | 会话内多一条 turn；可弃会话；(b) 后 TUI 自行复位 | **官方继承链承重事实 + 外部 resident 路由规则**（§6.2.0 case c）：1.0.13 上独立 `--no-leader` 子进程 set_mode 与外部 TUI actor 的文件写序、TUI 是否热更新、后续 turn 从哪个模式恢复 |
| P5 | `current_mode_update` 三路形状 + **可比较的序号/身份元数据** + 冷重放条目 replay/live 标记 | P7 顺带捕获 stdout 路；leader 路订阅 gateway；直接读 `updates.jsonl` | 无 | 无 | 观测三路消费 + 去重排序终稿（§6.2.3：跨源是否有可比较序号；无则按单点到达序 + 重基线，晚到翻回作为明示残差） |
| P6 | **（真实 turn）反馈类型矩阵取证**：`/compact`、`/always-approve`（状态变更组）、`/dream` 或 `/flush`（无输出组）、`/hooks add <非法路径>`（正文业务失败组）、`/context`（shell ACP 分支）；记录终态 stopReason、是否产生 AgentMessageChunk 行（HOST_TURN_META_KEY）、可透传反馈文本 | 真实会话逐条执行 | **有**（compact 压缩可能调模型） | **always-approve 测后必须切回**；compact 压缩不可逆；可弃会话 | §7.1a 矩阵各组的实际输出形状 + settle 四分法映射 + `ResultText` 是否存在 |
| P7 | mode-only 子进程 set_mode 的 **ack、durable 与失败判定**：(a) response → 有界等待读回 `plan_mode.json`（或等价官方读回）→ 优雅关闭 → 新子进程 load 验证；(b) 重复 plan→plan / default→default（幂等）；(c) **关闭方式对比**（优雅 EOF 关闭 vs 强杀）下持久结果差异；(d) **写盘异常/超时场景判定**（隔离测试注入，仅验证内部行为，不作安装版协议证据）；(e) 已有外部 resident 时切换（与 P4(b) 同场） | mode-only 短命子进程若干轮 | 无（set_mode 不调模型） | `plan_mode.json` 变化；测后切回 default | **durable 读回机制可行性**（§6.2.2）：有界读回是否可靠、优雅关闭是否 flush、失败如何呈现；不可建立 → **Phase 2 切换能力整体阻断** |
| P8 | `summary.json` 是否持久化当前 session mode | 直接读会话目录文件 | 无 | 无 | 冷 hydrate 权威来源选择（§6.2.3：summary.json mode / plan_mode.json 快照 / updates.jsonl 尾部回扫） |

任何一项活体形状与 1.0.16 源码冲突时，**以 1.0.13 活体为准**并在实现说明里记录
漂移（dsh §11.2 ① 教训）。

### 0.2 开工源码优先门

1. 上游只读 checkout `72a61251`；目标二进制 1.0.13。动手改 `agent/grokbuild` /
   `go-bridge` / iOS **之前**，至少读并在实现说明里写下符号锚点（§4 已给全，逐条
   对号）。
2. 官方 UI 层（pager）交互判决必须读：官方 pager 有 pager-local `/plan
   [description]`（`plan.rs:1-31`）与 `/context`（`context.rs:1-26`，本地呈现面
   ShowContextInfo）；TUI `/` 补全 = shell 可执行目录 ∪ pager-local 命令。交互
   语义在 UI 源码不在 RPC schema（dsh §11.2 ⑤ 教训）。
3. CordCode 只做 bridge 接线：官方 JSON 用中间类型解码再映射；`core.SessionCommandCatalog`
   的 dsh 实现是**先例不是真值**。实施时把 core 与 handler 里的 dsh 专属注释
   同步改为分 backend 契约（第一轮评审 §4 裁定，Phase 1 交付物）。
4. 测试 fixture 必须来自 §0.1 活体样本或官方仓 fixture；手写 fake server / 故障
   注入只验证内部行为，不能反向证明外部协议形状。
5. 违反本门 = 本方案未执行，评审按未通过。

## 1. 目标

1. iPhone **Grok Build 会话**的 `＋` 菜单出现「命令」节（与 dsh 同入口形态）：
   列出该会话官方**可执行目录**（builtins + skills + workflows，availability 已
   被官方过滤），点选按 D4 移动端规则分流执行，反馈按 §7.1a 矩阵呈现。
2. **Plan 模式 iOS 入口接官方通道**：think.md「Grok iOS Plan 只写 agent 内存」
   的现状终结——CordCode 侧模式写入通道收敛为官方 `session/set_mode`（经
   `SessionModeSwitcher` 接口，§6.2）；**prompt 恒不携带 `_meta.mode`**；切换
   以 RPC response + durable 读回双确认为准（§6.2.2）；模式变化经官方
   `current_mode_update` 三路与冷 hydrate 回流 iPhone 展示（Plan chip）。外部
   resident（Mac TUI）与 CordCode 子进程是各自独立 actor，一致性由 §6.2.0 路由
   规则约束——本方案**不宣称**「单写者消除外部多写者竞争」，只收敛 CordCode
   自身的写入通道。
3. 已交付的 plan 审批卡（`plan_review`，leader 广播 `x.ai/exit_plan_mode`，
   think.md §25）继续工作，本项不改审批语义。

## 2. 明确不做（第一期）

| 项 | 理由 |
| --- | --- |
| **prompt 携带 `_meta.mode`** | R6：手机缓存陈旧时携带 mode 会经官方 `reconcile_plan_mode_with_prompt`（`session_mode.rs:191-216`，对传入值直接 set）覆盖外部切换。官方已有继承链（`plan_mode.json` + `spawn.rs:625-648` 恢复 + `acp_agent.rs:1113-1129` prompt 无 mode 查本进程 actor），删除该通道即收敛 CordCode 自身写入口（actor/外部一致性另由 §6.2.0 处理） |
| **initialize `_meta.availableCommands` 作 List 兜底** | R5：builtins-only 冒充全目录是静默降级；List fail-closed（§6.1），该字段仅诊断日志 |
| **`/context` 等本地呈现面命令的 iOS 呈现面** | S4：shell ACP 分支 `ok_end_turn(0, None)` 不返回详情（`slash_exec.rs:90`），官方详情是 pager-local `ShowContextInfo` 本地面（`context.rs:21`）——「列表全上 + prompt 执行」无法等价替代。第一期不接入本地 surface；是否仍在面板列出由 D1 依 §7.1a 矩阵裁决（推荐不列出） |
| 键入 `/` 自动补全弹层 | owner dsh 裁决沿用：只要菜单入口 |
| prompt-only 命令 `loop` 的 loop_fire_mode 特殊面 | `PROMPT_COMMANDS` 特例重写 blocks + displayText，产品面窄；目录展示但走通用通道，特化另案 |
| workflow 进度投影（`workflow_projection`） | 命令进目录、可执行；workflow 专属过程投影是独立官方 surface，另案 |
| skills 的 `kind:"chat"` 产品通道 | Grok Chat 产品 lane，CordCode 不消费 |
| MCP elicit / `x.ai/queue/*` interjection | think.md：interjection 后置 Phase B |
| 合成全 backend Plan 按钮 | think.md「iOS 进入计划模式」行 owner 明令禁止 |
| 客户端自行合并/去重三条目录通道的结果 | 官方顺序（builtins-first）与 availability 过滤都是官方事实；客户端合并=自造目录（§4.1） |
| `permission_mode` 其余 5 键接真 | Phase 2 只接 `plan`↔SessionMode；其余键处置是 D3 裁决点（§6.2.4 路由表），非「维持现状」默认 |
| 目录刷新 UI（下拉重拉等） | 面板**每次打开**发 List（Mac 缓存命中即回）+ ACU 推送刷新已覆盖官方语义（§6.1 缓存规则）；手动刷新自造 |

## 3. Owner 决策

### 3.1 已锁定（既往裁决，直接生效）

| # | 来源 | 裁决 |
| --- | --- | --- |
| L1 | think.md「iOS 进入计划模式」行 | 三条 Plan 入口分别接各自官方通道，**禁止合成全 backend 按钮**；Codex 入口挂起、DSH 已交付、Grok 即本方案 |
| L2 | think.md §25 | plan 审批两键卡程度可接受；完整体验属跨 backend 通用另案，本项不动 |
| L3 | dsh 方案 §11.1 #3（2026-09-05 owner 裁决） | 面板白名单收窄**是 dsh 专属裁决**；对 grok 不是既成事实——grok 产品白名单是本方案 D1 待裁决项，iOS 白名单机制必须 per-backend（§5.2） |
| L4 | dsh 方案 §12 | 命令入口 = ＋ 菜单「命令」节（不是独立 `/` 按钮） |

### 3.2 本方案待 owner 裁决（开工前问一次，一次问完；第二轮确认 D1-D4 均仍待裁决）

| # | 决策点 | 推荐 | 备选 |
| --- | --- | --- | --- |
| D1 | iPhone 产品白名单（C 集，作用于官方全目录的**展示**过滤）——**裁决须建立在 §7.1a 反馈覆盖矩阵之上**（第二轮关闭条件 3） | **不收窄 + 排除本地呈现面组**：全上官方可执行目录，但 `/context` 这类 shell 执行无详情、官方反馈在 pager 本地面的命令第一期**不列出**（列出即误导）；其余各组按矩阵呈现 | 全上不排除（接受 context 列出后无详情输出）；或对齐 dsh 最小面（须逐条点名） |
| D2 | Plan 入口位置与动作 | **输入框 Plan chip**（对齐 dsh §11.1 #1 已交付形态）；点击 = `set_permission_mode`（模式通道，不发 `/plan` 文本） | ＋ 菜单「计划模式」行 |
| D3 | `permission_mode` 六键路由（S5 冲突的裁决） | **plan/default 两键路由 SessionModeSwitcher；其余 5 键从 `PermissionModes()` 移除（不再广告）**——现状 5 键是空转假成功，广告假键不诚实；iOS grok 权限菜单将只剩两键（可见变化）。**default 双语义明示**：路由 SessionMode 后 default 获得真实「退出 plan」效果，此为产品意图 | 保留 5 键走 legacy `ModeSwitcher` 分支维持既有空转广播（无可见变化，但继续广告假键） |
| D4 | 带 hint 命令的移动端交互 | **统一 claim**：点选 → 认领输入框 `/name ` + 官方 hint 幽灵提示，空参直接回车即执行（官方 optional-args Enter=Executes）；零 per-command 特判 | 个别「常用无参」命令点选即执行（per-command 硬编码，不推荐：`args_required` 不在目录下发） |

## 4. 官方不变量（上游核验 @ `72a61251`，实施必须镜像）

### 4.1 命令目录：三条官方通道，各司其职

| 通道 | 时机 | 内容 | 锚点 |
| --- | --- | --- | --- |
| `initialize` 响应 `_meta.availableCommands` | 握手后、session 前 | **仅 builtins**（availability 过滤） | `acp_agent.rs:543` + `slash_commands.rs:837-851` |
| `sessionUpdate.available_commands_update` | session/new 或 session/load 后 | **builtins + skills + workflows**，builtins 在前，cwd-scoped | `session_setup.rs:247`；`slash_commands.rs:445` |
| ext 方法 `x.ai/commands/list` | pull | `{sessionId?, cwd?, kind?}` → `{commands, tools?}`，camelCase | `slash_commands.rs:852-871` |

不变量：

1. **`input`/hint 的官方语义只是 placeholder**（R3）：pager 对所有 ACP 目录命令
   恒 `has_args: true`（`acp_command.rs:147-164`）；发送完整性按
   `takes_args`/`args_required` 判断（`slash/mod.rs:1407-1435`），而
   `args_required` 不在目录下发。CordCode 的 `hint.isEmpty → execute / 否则
   claim` 是**自定的移动端规则**（D4）：grok 上 compact（hint「optional
   context about what to preserve」，`slash_commands.rs:66-76`）、
   always-approve（「on|off」）等 hint 非空命令一律 claim，空参回车即执行。
   iOS `SlashCommandSelection.route` 代码零改动，依据标注为移动端规则。
2. availability 过滤是**官方做的**。CordCode 拿到什么列什么，不得客户端再
   过滤或合并三条通道（D1 收窄是 owner 依 §7.1a 矩阵的产品裁决，性质不同且
   须记录终表）。
3. CordCode 现状：`initializeMeta` 只解 `modelState`（`acp_types.go:102-115`），
   ACU 在 codec known-drop 清单（`acp_codec.go:326-330`）——两条通道都已在
   收包路径上，只是没解。

### 4.2 执行 = 命令行作为 prompt 文本（与 dsh 相反的官方语义）

grok 官方执行通道就是 `session/prompt` 发 `/cmd args` 文本；shell 侧
`resolve_human_intent`（`slash_commands.rs:1574-1644`）**确定性**解析（非模型
中介），四层判决：

```text
HumanIntent 前缀解析 → ① PROMPT_COMMANDS(loop) 特化重写
                    → ② builtin（name|alias 命中 + gate 允许）→ BuiltinAction
                    → ③ skill 引用解析 → InvokeSkill 信封
                    → ④ workflow 目录命中 → WorkflowLaunch
                    → 都不中 → Ok(prompt_blocks)：作为普通 prompt 交给模型
```

不变量：

1. **目录内命令的执行是确定性的**。grok 的防线是**只允许目录内命令走 execute
   通道**（白名单语义），自由文本 `/` 整行仍走普通发送（落入 ④ 是官方预期）。
2. **命令在官方投影里就是 user message**。CordCode 不合成 user 气泡——transcript
   由官方历史/polling 自然出现命令行（P6 取证实际行形状）。
3. **三个命令集合必须分开**（R7）：**A = shell 可执行目录**（三条通道下发，
   prompt 文本确定性解析）；**B = pager-local 动作**（`/plan [description]` →
   SetPlanMode/EnterPlanMode（`plan.rs:1-31`）；`/context` → ShowContextInfo
   （`context.rs:21`）等——不在 A，当文本发落入 ④）；**C = iPhone 产品白名单**
   （D1 裁决，作用于 A 的展示过滤，依据 §7.1a 矩阵）。iPhone Plan 入口必须转换
   为模式动作（§6.2）；模型/effort 走既有 catalog 通道。
4. BUILTIN_COMMANDS 16 条（`slash_commands.rs:66`）——shell 目录没有 `plan`：
   plan 在官方是 SessionMode + pager-local 命令。TUI `/` 补全 = A ∪ B，不与 A
   逐一等同（§9 #1）。

### 4.3 模式（SessionMode）：独立于 permission 的官方体系

| 事实 | 锚点 |
| --- | --- |
| SessionMode 枚举 `default`/`plan`/`ask`（snake_case，未知→Default） | `xai-grok-tools/src/types/session_mode.rs` |
| `session/set_mode` 需要 resident session handle；RPC response 在处理完后必发（与 CMU 无关的独立 ack） | `acp_agent.rs:2229-2250`、`run_loop.rs:737` |
| **set_mode 改的是收到请求的 resident actor 的内存**；prompt 无 mode 时查询的也是**本进程 actor 内存**（`acp_agent.rs:1113-1129`），不是每次从文件同步；子进程仅在 spawn 时从持久化 tracker 恢复（`spawn.rs:625-648`） | S2 依据 |
| **CMU 仅真实转换时 enqueue**（`enter_pending()` 幂等返回 false → 无 CMU 不 persist） | `session_mode.rs:44-113` |
| **持久化与 CMU 是两条独立队列**：`persist_plan_mode_state()` 向 persistence 队列 send；写盘在异步 `PersistenceMsg::PlanModeState` 分支执行，**失败仅记 warning，不向上反馈**——RPC response 与 CMU 均不证明写盘完成或成功 | `session_mode.rs`、`updates.rs:348`、`persistence.rs:1945`（S1 依据） |
| `session/prompt._meta.mode` 存在但对传入值**直接 set**（`reconcile_plan_mode_with_prompt`，`session_mode.rs:191-216`）——陈旧值覆盖外部切换。本方案不用（§2） | `session_mode.rs` |
| PlanModeState 机持久化到会话目录 `plan_mode.json`，resume 恢复，`awaiting_plan_approval` 同样持久化 | `plan_mode.rs` |
| `CurrentModeUpdate` 真实转换时发出；持久化进 `updates.jsonl` 并转发 gateway | `updates.rs:348`、`notification_bridge.rs:201` |
| **命令正文反馈通道**：hooks 类命令的成功/失败文本经 `AgentMessageChunk`（带 `HOST_TURN_META_KEY` meta）进入会话 scrollback，随后 flush replay buffer，turn 仍正常 end_turn——**业务失败不改变 turn 终态** | `slash_exec.rs:90-180`、`acp_session.rs:1365-1405`（S4 依据） |
| **无输出分支**：`ContextInfo => ok_end_turn(0, None)`；Dream 注释「Intentionally no user-visible output」；FlushMemory 有 skip 路径 | `slash_exec.rs:60-90`（S4 依据） |
| `x.ai/exit_plan_mode` 批准 → notification_bridge 回发 CurrentModeUpdate 恢复 default | `notification_bridge.rs`（§25 已交付消费端 `leader_subscriber.go:680`） |

推论（v1.2 重写，S1/S2 修正后的精确表述）：模式跨进程持久（`plan_mode.json`，
**仅 spawn 时读取**）+ prompt 无 mode 查**本进程 actor 内存**——文件是跨进程
同步点但**不是热同步**：CordCode 每个 `--no-leader` 子进程与外部 resident
（Mac TUI）是**各自独立的 actor**，互相不读对方的内存，也**不**在文件变化时
热更新。因此：CordCode 可用 mode-only 子进程切换（写文件）与观测（CMU 三路），
但**不能宣称**「改了文件即所有活跃 actor 可见」；切换的持久生效必须以 durable
读回确认（§6.2.2），外部 resident 在场时的行为由路由规则与 P4(b)/P7(e) 取证
约束（§6.2.0）。

### 4.4 codec：fail-open + 终态三分 + 业务语义分离

grok codec 对未知 sessionUpdate 是 fail-open（`acp_codec.go:362-369`）。本方案
的 codec 工作是把 `available_commands_update` / `current_mode_update` 从
known-drop 升级为消费（§6.3），fail-open 默认保持。

终态处理是三分的（`acp_codec.go:331-360`：error → EventError、cancelled →
EventResult"cancelled"、其他 → EventResult）——命令 settle 映射沿用此区分，
**cancelled 不算成功**，未知 stopReason 保留原值（§6.1）。

**业务语义分离（S4）**：`end_turn` 正常 ≠ 业务操作成功——hooks 类业务失败的
文字经 AgentMessageChunk 正文输出后 turn 照常正常结束（§4.3）。因此
`ResultKind:"success"` 只能表示「turn 正常完成（transport/终态层面）」；
业务成败的呈现依赖官方正文通道原样上屏，**禁止**按自然语言猜测把正文映射成
RPC 错误（既会漏（无输出组）也会错（正文措辞多样））。

## 5. CordCode 现状与差距（macbridge `de17e6f` + iOS `c3b1d5b0`）

### 5.1 Mac（`agent/grokbuild/`）

| 现状 | 锚点 | 差距 |
| --- | --- | --- |
| `SetMode(mode string)` 只写 agent 级内存，无 sessionID/ctx/error；handler `handleSetPermissionMode` 先 SetMode 后查 SessionID、失败也广播成功；`a.mode` 两会话共享 | `core/interfaces.go:897`、`handlers.go:4532`、`grokbuild.go:573-583` | 三重缺口（R1）+ **S5 冲突**：新优先分支若只接 plan/default，其余 5 键从「假成功」变「报错」≠ 行为不变 → §6.2.4 路由表 + D3 裁决 |
| `PermissionModes()` 返回 6 条，含 `{Key:"plan"}`；iOS 权限菜单由该 catalog 构建、统一走 `setPermissionMode` | `grokbuild.go:585`；iOS `ChatUIKitContainerView.swift:6193`/`:6236` | S5：键集与路由表（§6.2.4）不齐即互相矛盾 |
| **每个会话子进程都是独立 `grok agent --no-leader stdio`** | `session.go:99` | S2：mode-only 子进程与外部 resident 天然是两个 actor；路由三态（§6.2.0）必须定义 |
| `updates_file_tailer.go` **从当前 EOF 起尾随**，不回放 EOF 前的 CMU；`leader_subscriber` 在 codec 前丢弃 isReplay 通知 | `updates_file_tailer.go:60`、`leader_subscriber.go:573` | S3：**冷模式 hydrate 是新增实现**（§6.2.3），不存在「既有 tailer 回放路径」可复用；两处现状都不能提供冷恢复 |
| wire 层 `execute_session_command` 的 line 校验只查非空 | `handlers_session_commands.go:52` | `/` 前缀+单行校验由 grok 侧自实现（R5） |
| `sessionPromptParams` 仅 `{sessionId, prompt}` | `acp_types.go:240-243` | **保持无 `_meta`**（§2/R6） |
| codec 把 CMU/ACU 等归入 known-drop；`initializeMeta` 只解 `modelState` | `acp_codec.go:326-330`、`acp_types.go:102-115` | ACU/CMU 升级为捕获（§6.3）；availableCommands 仅诊断日志 |
| plan 审批卡已交付 | `leader_subscriber.go:680` | 无差距，复用 |
| 进程级单例 catalog 子进程现用于 session/list + 模型目录 | `catalog_session_list.go` | P3 取证后复用作 `x.ai/commands/list` 拉取 |
| core `SessionCommandCatalog` + wire RPC 两件套已在（dsh 期） | `core/session_commands.go:10-40`、`handlers.go:1642-1644` | grokbuild 实现之；dsh 注释改分 backend 契约；零新增 RPC |

### 5.2 iOS（六项清单 + 权限菜单随 D3 联动）

| # | 现状 | 锚点 | 差距 |
| --- | --- | --- | --- |
| 1 | ＋菜单「命令」节门控硬编码 `== .deepSeekWeb` | `ChatUIKitContainerView.swift:4565-4571` | 放宽为 capability 驱动 |
| 2 | `commandWhitelist`（compact/goal/plan）无条件过滤所有 backend | `SlashCommandRouting.swift:11` | 白名单 per-backend：仅 deepSeekWeb 套用；grok 按 D1（推荐不套用，但本地呈现面组不列出） |
| 3 | `AttachMenuPlanner` 对未加载/失败/空目录回退内置三条（含 `/plan`） | 同文件 | 回退三条仅对 deepSeekWeb；grok 三态真实呈现；绝不显示 dsh `/plan` |
| 4 | Plan chip 刷新仅 deepSeekWeb；点击发 `/plan off` | `ChatUIKitContainerView.swift:6583`/`:6682` | chip 数据源扩 grok 模式状态（含冷 hydrate §6.2.3）；grok 点击 = `set_permission_mode` 模式通道 |
| 5 | 通用执行失败走 `presentSlashCommandErrorAlert` 弹窗 | `ChatUIKitContainerView.swift:6670` 邻域 | 沿用（**仅 RPC 层失败**）；正文承载的业务失败走 transcript 原样上屏，不弹窗（S4 细分） |
| 6 | `route(hint.isEmpty)` 判决 generic | `SlashCommandRouting.swift:32-38` | 代码零改动；依据改标移动端规则（D4） |
| 7 | 权限菜单由 `PermissionModes()` catalog 构建 | `ChatUIKitContainerView.swift:6193` | D3 若采推荐（移除 5 键），grok 菜单自动缩为两键——iOS 无需代码改动，但属可见变化须 owner 确认 |

## 6. Wire 设计

### 6.1 `SessionCommandCatalog` 的 grok 实现（Mac）

**List（fail-closed，两条官方通道，不合并、无兜底）+ 缓存规则（S6 定稿）**：

- **通道 1（turn 期）**：codec 捕获 ACU（§6.3）写入 per-session 目录缓存。
- **通道 2（冷/缓存缺失）**：catalog 单例子进程带会话 cwd 调
  `x.ai/commands/list {cwd}`（P3；不可行则退化为 per-turn 子进程 handshake +
  session/load 后读 ACU）。
- 两通道都失败 → RPC error（iOS 呈现错误态），**无兜底**；initialize
  `availableCommands` 仅 Debug 日志。
- **缓存身份与失效规则**：身份键 = `(backend, sessionID, cwd)`；`fetchedAt` 与
  来源（acu/list）仅作诊断元数据，不参与身份。**写入** = ACU 或 list 成功即
  **整表替换**（含空表——官方空目录真实呈现为空态，不保留旧表）。**失效** =
  bridge 重启全清；会话 cwd 变更视为新键（旧条目不迁移）；**无 TTL**——每个
  per-turn 子进程的 session/load 都会推新 ACU，执行期新鲜度由「本 bridge 运行
  内曾收到 ACU/list」界定。**iOS 衔接**：面板**每次打开**发 List RPC（Mac 命中
  缓存即回，未命中走通道 2）；不设「每会话一次」假设。外部 reload/plugins/skills
  变化由下一次 ACU（下一 turn）或下一次面板打开的 list 反映；快照与执行的非原子
  窗口见下。
- 映射：`Name`/`Description` 顶层；`Hint` ← `input.hint`（缺席 → `""`）；中间
  wire 类型解码（`input` 指针/omitempty）。skills/workflows 额外字段（P2）第一
  阶段丢弃。
- **非原子边界（明示）**：目录快照与执行不原子——快照后 skill 被删/availability
  变化，execute 白名单仍命中但官方 resolve 落 ④ 普通 prompt。这是官方行为；
  「刚拉完后文件又变化」的不可消除窗口被接受，**但缓存不因此无限期不失效**
  （上述规则保证每次 turn/面板打开都会刷新）；不做执行时二次拉取。

**Execute（官方 prompt 通道）**：

- `ExecuteSessionCommand(ctx, sessionID, "/compact")` = 复用现有 per-turn
  `session/prompt` 管道发送该行，不合成 user 气泡、不挂 `_meta.mode`、不带附件。
- **line 校验**（grok 侧自实现）：`/` 开头、单行；未过 → RPC error。
- **白名单（fail-closed）**：命令名必须命中该会话目录缓存；缓存缺失 → 先走
  通道 2 拉取；拉不到 → RPC error「catalog unavailable」，不直接发 prompt。
- **settle 四分法（P6 定稿）**：stopReason = `error` → Go error；`cancelled` →
  Go error「cancelled」（**不算成功**）；其他已知正常终态 → `ResultKind:"success"`
  + `ResultText`（P6 证实有可透传文本则映射，否则空串）；未知 stopReason → 保留
  原值进错误信息，fail visible。
- **`success` 的语义边界（S4）**：`success` 仅表示 **turn 正常完成**，不代表业务
  操作成功——hooks 类业务失败的文字经官方正文通道（AgentMessageChunk，
  HOST_TURN_META_KEY）进入 transcript 原样上屏；无输出组（dream/flush-skip/
  context-shell）成功后仅有命令行 echo，**诚实接受无副产物反馈**。禁止按正文
  自然语言猜测错误（不映射 RPC error、不弹窗）；错误弹窗仅用于 RPC 层失败
  （§5.2-5）。反馈呈现全集见 §7.1a。

### 6.2 模式通道（Phase 2，v1.2 重设计）

#### 6.2.0 resident 路由三态（S2——先于一切切换动作判定）

CordCode 每个会话子进程都是独立 `grok agent --no-leader stdio`
（`session.go:99`）；`session/set_mode` 修改**收到请求的 actor** 的内存，文件
仅在 spawn 时被其他进程读取。切换请求按会话当前状态路由：

| 态 | 判定 | 路由 | 效果承诺 |
| --- | --- | --- | --- |
| (a) idle，无外部 resident | 该会话无进行中 turn，且无外部 TUI 打开同会话（不可直接探测——按 P4(b) 证据界定可探测性） | mode-only 子进程 = **唯一活跃 actor**；set_mode 权威写文件 | durable 读回确认后可承诺「已持久生效」 |
| (b) CordCode 活跃 turn（per-turn 子进程在跑） | per-session 锁被 Send/Execute 持有 | 切换**等待**当前 turn 结束（与 Send/Execute/SetSessionMode 共用 per-session 锁串行）；ctx 超时 → RPC error（不并发切换、不插队） | 不与 turn 竞争；长 turn 期间切换可能超时失败，真实上报 |
| (c) 外部 resident（Mac TUI 打开同会话） | P4(b)/P7(e) 取证界定的探测方式（或无法探测则按未知处理） | mode-only 子进程与 TUI 是**两个独立 actor**：我们的 set_mode 写文件、改自己 actor；**TUI 的 actor 不热更新**（它仅 spawn 时读文件，prompt 查自身内存）；反向 TUI 切换 → 文件被写 + gateway CMU 观测 + 我们下一个子进程 spawn 时恢复 | **不承诺** TUI 即时可见；跨进程对 `plan_mode.json` 的并发写序 = 上游行为，P4(b)/P7(e) 取证前该场景标「行为未证明」，Phase 2 验收不含该场景的产品承诺（§9 #5 已改写） |

「单写者」的精确表述（替代 v1.1 的宽泛宣称）：**CordCode 自身的模式声明通道
收敛为一个**（删除 prompt `_meta.mode`，写入只剩 set_mode）；外部写者（TUI、
审批流）与多 actor 一致性**不是**由「一个 RPC 名称」自动解决的，由上表路由 +
durable 重基线（§6.2.3）+ 观测收敛处理，且 case (c) 的文件写序证据未取得前
保持未证明。审批动作（approve/quit）走 leader 通知链不经过 CordCode 子进程，
可能与切换并发：durable 以文件最后写者为准，显示按 §6.2.3 时序收敛，残差
明示。

#### 6.2.1 core 接口与 handler 分支

```go
// SessionModeSwitcher is an optional interface for agents whose permission
// modes are per-session state driven through the backend's official
// channel. Implementations must fail the call on official rejection
// (no memory-first mutation + fake success broadcast).
type SessionModeSwitcher interface {
    SetSessionMode(ctx context.Context, sessionID, mode string) error
    GetSessionMode(ctx context.Context, sessionID string) (string, error)
}
```

handler `handleSetPermissionMode` 优先分支（type-assert：`SessionModeSwitcher`
→ legacy `ModeSwitcher`）：先校验 sessionID 非空、mode ∈ {plan, default}，再
调用；error → RPC error 不广播。成功（= response + durable 读回双确认，下节）
→ 以请求值下发会话模式 patch，CMU 观测到达后同值幂等覆盖。grokbuild 实现：
per-session 模式缓存 + **per-session 互斥覆盖 Send/Execute/SetSessionMode**
（§6.2.0 case b）；`a.mode` 不再被新路径读写。**其余 5 键路由见 §6.2.4**——
优先分支不吞掉它们。

#### 6.2.2 settle 三态与 durable 读回（S1——删除软失败叙事）

- **三态定义**：① RPC 已接受（response OK = 收到请求的 actor 已切换，内存真实
  生效）；② durable 已确认（持久层已写入）；③ 未确认（超时/读回不符）。
- **durable 确认机制**：set_mode response 后，在同一 mode-only 子进程生命周期内
  **有界等待权威读回**——轮询会话目录 `plan_mode.json` 快照（或 P7 证明的等价
  官方读回/优雅关闭 flush 路径）直至反映新模式或超时（初稿 2s 上限，P7 定）。
  仅 response 不算持久生效。
- **对外呈现**：response + 读回双过 → RPC success（chip 翻转）；读回超时/不符 →
  RPC error「持久化未确认」（真实未知状态，iOS 按现有弹窗呈现，chip **不翻
  转**）；**不自动重试、不伪造反向状态、不向用户宣告已持久生效**。
- **已删除**（S1）：「等 CMU/短延迟作退出屏障」（CMU 幂等缺失 + 无因果保证）、
  「写盘失败是软失败、观测自然收敛」（写盘失败后下一个 actor 本就是 default，
  不会再发 CMU 纠正 chip——收敛不发生）。
- **P7 门槛**：若 1.0.13 上有界读回不可建立（写盘延迟无界、优雅关闭也不
  flush、读回形状不可靠），**Phase 2 切换能力整体阻断**（不交付带成功假象的
  切换），只保留观测面（chip 展示外部状态）。
- 残差（读回通过但磁盘随后损坏/被覆盖）：接受并明示；durable 以文件最后写者
  为准，冷 hydrate 重基线。

#### 6.2.3 状态模型：authoritative / 写入通道 / observed / 冷 hydrate / 时序

- **authoritative** = 会话目录 `plan_mode.json`（spawn 恢复源；文件最后写者胜）。
- **写入通道（CordCode 侧）** = 仅 `set_mode`（§6.2.0 路由 + §6.2.2 durable）；
  prompt 恒不带 mode。
- **observed** = CMU 三路（stdout/leader gateway/文件）+ response patch → chip
  展示，**永不回写** authoritative。
- **冷 hydrate = 新增实现（S3，不是既有 tailer 路径）**：现有
  `updates_file_tailer` 从 EOF 起尾随（`updates_file_tailer.go:60`）不回放旧
  CMU；`leader_subscriber` 在 codec 前丢弃 isReplay 通知
  （`leader_subscriber.go:573`）——两处都不能提供冷恢复。新增：
  - **入口**：iOS 打开 grok 会话请求会话状态 / bridge 重启后该会话首个状态
    请求时触发（一次，结果缓存）。
  - **来源**（P8 裁决优先级）：`summary.json` 的 mode 字段（若在）→
    `plan_mode.json` 快照直读 → `updates.jsonl` **尾部有界回扫**（新代码：从
    EOF 向后扫最近 N KB 找最后一条 `current_mode_update`）。
  - **空/失败语义**：来源都失败或无记录 → mode = 未知（chip 中性态），**不伪
    造**；不阻塞会话打开。
  - **与 live 衔接**：hydrate 完成前 live CMU 先到 → live 直接生效，hydrate
    结果退化为初始值校验；**重订阅（leader 重连/bridge 重启/会话重开）→ 重新
    hydrate 重基线**，丢弃可能陈旧的 live 值。
- **时序规则（bridge 单点）**：所有观测（三路 CMU + response patch）经 bridge
  单点串行，按 **bridge 到达序 last-wins**（response patch 纳入同一序，不再
  特殊）。per-session 锁窗口内自有子进程发出的 CMU 与本次 response patch 同
  epoch，同值幂等去重（防双跳）。`updates.jsonl` 冷重放/replay 标记条目只作
  hydrate 初始值，**不覆盖** live 已观测值（P5 取证标记形状）。**残差明示**：
  跨源无全局序号（P5 取证是否存在可比较序号/身份）时，极晚到的旧 live 通知
  理论上可翻回一拍——无法证明消除，靠重基线兜底；验收用可控注入顺序单测验证
  bridge 排序逻辑本身（旧通知/旧 response 晚到不翻回）。

#### 6.2.4 `permission_mode` 六键路由表（S5——D3 的裁决对象）

| mode 键 | 路由（D3 推荐） | 行为 | iOS 可见变化 |
| --- | --- | --- | --- |
| `plan` | SessionModeSwitcher | set_mode plan + durable 读回（§6.2.2） | chip 橙；权限菜单该键真实生效 |
| `default` | SessionModeSwitcher | set_mode default（= 退出 plan）——**default 双语义明示**：路由后该键获得真实「退出计划」效果，产品意图写入 D3 | 权限菜单 default 键真实退出 plan（原为空转假成功） |
| `acceptEdits`/`auto`/`dontAsk`/`bypassPermissions`/第 5 键 | **推荐：从 `PermissionModes()` 移除（不再广告）**；备选：保留并走 legacy `ModeSwitcher` 分支维持既有空转广播 | 推荐 = 诚实能力广告（现状是假成功）；备选 = 无行为变化但继续广告假键 | 推荐：grok 权限菜单缩为两键（可见变化，须 owner 确认）；备选：无变化 |

参数化测试锁定六键 ×（SessionModeSwitcher agent / legacy agent）的分派、错误
与广播行为（§8 Phase 2）。

### 6.3 codec 升级（Mac，Phase 1/2 各一半）

| update 类型 | 现状 | 改为 |
| --- | --- | --- |
| `available_commands_update` | known-drop | 解码 → **整表替换** per-session 目录缓存（§6.1 规则）；不产 timeline 事件 |
| `current_mode_update` | known-drop | 解码 SessionModeId → 会话级模式状态（observed）→ 经既有会话状态通道下发 iOS；时序按 §6.2.3；不产 timeline 事件 |
| `config_option_update` | known-drop | 维持 |
| 未知类型 | fail-open Debug | 维持 fail-open（§4.4） |

外部 turn 可观测性：CMU 经 leader gateway 转发 → leader_subscriber 增
`current_mode_update` 分支汇入同一状态；**注意** leader 路现状丢弃 isReplay
通知（`leader_subscriber.go:573`）——重放条目本就不进 live 流，冷恢复由 §6.2.3
hydrate 承担（新代码），二者分工：live 只走 gateway/stdout 新事件，冷值只走
hydrate。三路汇 bridge 单点排序（§6.2.3）。

### 6.4 协议同步

零新 RPC、零新 capability。`docs/protocol/` canonical pack 更新：
`session_commands` 实现者清单加 grokbuild；grok 模式状态快照/patch 字段（若
新增）按 optional-field 规则 additive。hello_ack capabilities 由类型断言自动
出现。core/handler 的 dsh 专属注释改分 backend 契约（Phase 1 交付）。

## 7. iOS 设计与四拍走查

**改动清单**（对应 §5.2 七项）：①门控放宽（唯一代码块级改动）；②白名单
per-backend；③回退三条仅 dsh、grok 三态真实呈现；④chip 数据源扩 grok（含冷
hydrate 中性态）+ 点击走模式通道；⑤RPC 失败沿用弹窗（正文错误走 transcript
不弹窗）；⑥routing 判决保留改标移动端规则；⑦权限菜单随 D3 自动缩键（无代码
改动，可见变化须确认）。

### 7.1a 反馈覆盖矩阵（S4——按反馈类型分组，替代逐命令四拍）

| 语义组（代表命令） | 请求 | 官方输出/副作用（源码依据 @72a61251） | iPhone 呈现 | 验证样本 |
| --- | --- | --- | --- | --- |
| **正文反馈组**（hooks-add/trust/list、feedback 类） | `/name args` prompt 文本 | 成功/失败文本经 `AgentMessageChunk`（HOST_TURN_META_KEY）进 scrollback，随后正常 end_turn（`slash_exec.rs:90-180`、`acp_session.rs:1365-1405`）——**业务失败不改 turn 终态** | 文本经官方 transcript/polling 自然上屏；无需新增组件；**不弹窗**（正文错误≠RPC 失败） | P6：`/hooks add <非法路径>`（失败文本）+ 一条成功样例 |
| **无输出组**（dream、flush 的 skip 路径、context 的 shell ACP 分支） | 同上 | `ok_end_turn(0, None)`，无正文、无副产物事件（`slash_exec.rs:60-90`：Dream「Intentionally no user-visible output」；ContextInfo 无详情） | 仅命令行 echo（官方 user message）；**诚实接受成功无副产物反馈**，不造 loading/成功 toast | P6：`/dream`（或 /flush skip 路径） |
| **本地呈现面组**（context 的 pager 面） | pager 内 `/context` | `Action::ShowContextInfo` 本地 UI（`context.rs:21`）——**shell ACP 执行拿不到详情** | **第一期不纳入面板**（D1 推荐）：列出即误导（执行后无详情）；接入 iOS 本地 surface 另案 | 无需样本（不纳入）；若 D1 改判需先取证该 surface |
| **状态变更组**（always-approve on/off、compact） | 同上 | 权限状态/上下文窗口变化；可能有简短正文；compact 压缩可调模型 | 正文（若有）上屏 + 既有 usage/permission 投影承接状态变化；命令行 echo | P6：`/always-approve on`（+测后复位）、`/compact` |
| **目录透传组**（skills、workflows、goal 等其余） | 同上 | 各自官方语义（skill 信封 / workflow 启动 / 参数命令）；输出形态以各自样本为准 | 按样本归类进上述组或标注「待样本」；未取证的命令类型不得承诺呈现形态 | P2/P6 样本 + 实施期增量取证 |

D1 裁决建立在本表之上：推荐全上（排除本地呈现面组）；矩阵中「待样本」条目在
Phase 0 完成前不进 D1 终表。

### 7.1 四拍走查：命令面板（统一规则 + 矩阵分组呈现）

1. **打开输入的地方**：Grok Build 会话（有 sessionId + `session_commands`
   capability）→ 输入框 `＋` → 「命令」节 → 官方可执行目录（name +
   description；D1 排除本地呈现面组；加载中/失败/空目录三态真实呈现，不回退
   dsh 三条）。
2. **输入**（D4 移动端规则）：hint 非空 → 认领输入框 `/name ` + 幽灵提示，空参
   回车即执行；无 input 命令（若存在）点选即执行；自由文本误触 `/` 未命中目录
   → 普通发送（官方 ④ 预期）。
3. **发出去**：`execute_session_command` → Mac line 校验 → fresh 目录白名单
   （缺失先拉，fail closed）→ per-turn `session/prompt` → 终态四分法 settle。
4. **把过程展示出来**：按 §7.1a 矩阵分组呈现——正文组文本上屏；无输出组仅
   echo；状态变更组正文 + 既有投影；执行中按命令名置灰「执行中…」；RPC 层失败
   按现有弹窗（正文业务失败不弹窗）。

### 7.2 四拍走查：Plan 切换

1. **打开**：Grok 会话输入框 Plan chip（default 灰、plan 橙、**未知中性**——
   冷 hydrate 失败时诚实呈现，不猜）。
2. **输入**：无参数（plan↔default 两态点按）。
3. **发出去**：`set_permission_mode {mode:"plan"|"default"}` → handler
   SessionModeSwitcher 分支 → §6.2.0 路由（活跃 turn 等待/超时报错）→
   mode-only 子进程 `session/set_mode` → **response + durable 读回双确认**
   （§6.2.2）；任一失败 → RPC error，chip 不翻转，不自动重试。
4. **过程展示**：chip 状态只来自官方回流（response patch/CMU 三路/冷 hydrate），
   按 §6.2.3 时序收敛；外部（Mac TUI/审批流）切换经观测同步；**不承诺 TUI 即时
   可见**（§6.2.0 case c）；plan 态下模型产出计划 → 已交付 `plan_review` 审批
   卡（§25）→ 批准后模式回 default（官方 CMU，chip 跟随）。不在本地假造状态
   （dsh §6.3 红线沿用）。

## 8. 分期落地

| Phase | 内容 | 验收 |
| --- | --- | --- |
| 0 证据矩阵 | §0.1 P1-P8 样本归档（脱敏）；P4/P6 真实 turn + P4(b)/P7(e) 外部 resident 场景成本与副作用先报 owner | 每项真实样本 + 漂移记录；**P7 durable 读回不可建立 → Phase 2 切换能力整体阻断**；样本不足的依赖项保持阻断 |
| 1 Mac 目录+执行 | codec ACU 捕获、`SessionCommandCatalog` 实现（List fail-closed + §6.1 缓存规则、execute 校验+白名单、settle 四分法）、capability 出现、dsh 注释改分 backend 契约 | `go test ./agent/grokbuild ./go-bridge` 定向，须含：目录**空/失败/缺失**三态、`/` 前缀+单行校验、白名单 fail-closed、**cancelled 不算成功**、未知 stopReason 保留原值、compact claim 判决、**缓存失效规则**（重启全清/空表整表替换/跨 cwd 不复用）；capability 断言 dsh-web 与 grokbuild 有、三家无；fixture 用 Phase 0 样本 |
| 2 模式通道 | `SessionModeSwitcher` + handler 优先分支 + **六键路由表（§6.2.4）**、per-session 锁覆盖 Send/Execute/SetSessionMode、mode-only set_mode + **durable 读回**、CMU 三路消费、**冷 hydrate 新增**、`plan` 键迁移（D3） | 定向单测须含：**六键 × 两类 agent 参数化分派**（S5）、**跨会话隔离**、**幂等无 CMU 切换仍 settle**、**durable 读回超时 → error 不广播成功**（S1）、**活跃 turn 时切换等待/超时**（S2 case b）、**重启后静止会话冷恢复**（S3：无新事件仍 hydrate 成功）、**旧通知/旧 response 晚到不翻回**（S3：注入序验证 bridge 排序）、**外部切换后发送不回写**、陈旧 replay 只作初始值；真机 §9 #5-#7；plan 审批回归 |
| 3 iOS 面板 | §7 七项清单 | 定向单测：门控、白名单 per-backend、回退仅 dsh、chip action 模式通道 + **未知中性态**、routing 判决注释；三态验收（List 未返回/报错/空目录）；**权限菜单缩键**（D3 推荐时）确认呈现；真机 §9 |
| 4 收尾 | protocol pack、双仓 CHANGELOG、Release 覆盖安装（killall 两进程）、owner 真机 | §9 全矩阵 |

按仓库纪律：Phase 1/2 不装真机不跑全量；任何定向 build/test 超 5 分钟异常止损。

## 9. Owner 真机矩阵（产品语言）

环境：Mac 安装版 grok 1.0.13；iPhone 连已覆盖安装的 CordCode Link；Grok Build
会话（含至少一个带 project skills 的目录）。

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | 打开 Grok 会话 → `＋` | 「命令」节列官方可执行目录（builtins+skills+workflows；**不含 /context**（D1 推荐排除））；与 Mac TUI `/` 补全清单不必逐一等同（TUI 另含 pager-local 命令）；List 未返回/报错/空目录时三态真实呈现，**不出现 dsh 的 /plan**；其他 backend 不回归 |
| 2 | 点 compact（hint 非空） | 认领输入框 + hint 幽灵提示；空参回车即执行；命令行 echo 上屏；上下文用量随后更新 |
| 3 | 点 hooks 类命令（如 `/hooks add <非法路径>`） | 失败**文本经对话流上屏**（不弹窗——正文错误不是 RPC 失败）；turn 正常结束 |
| 4 | 输入框手打 `/compact` 普通发送 / 手打 `/foo` / 手打 `/plan` | 分别：确定性执行 / 模型普通回复（官方 ④）/ 模型普通回复且**不进计划模式**（官方 fail closed——chip 必须走模式通道的原因） |
| 5 | 点 Plan chip 进入计划模式 | chip 变橙（= response + durable 读回双确认后的真实状态）；**重启 CordCode Link 后 chip 仍橙**（durable 真实 + 冷 hydrate 生效）；**若 Mac TUI 正开着同会话：不承诺 TUI 即时可见**（两个独立 actor，TUI 重启后按文件恢复——P4(b) 取证前的诚实边界） |
| 6 | 计划模式下发消息 | 模型按计划模式行为；消息不携带模式信号（官方从持久化继承）；外部（Mac TUI）刚切回 default 后手机立即发消息：仍是 default（无陈旧回写） |
| 7 | 模型产出计划 → 审批卡批准 | 既有两键卡可批准；批准后 chip 回灰（官方 CMU 回流） |
| 8 | 在 Mac TUI 切 plan / 跑 `/compact` | iPhone 无操作时 chip/命令行随后同步（leader 广播 + polling 兜底） |

真机点击须 owner；agent 只做日志/Management 核验。#4 是官方语义锚点；#5 前半
是 durable + 冷 hydrate 锚点、后半是 §6.2.0 case c 诚实边界；#6 后半是单写者
锚点；#8 是三路可观测锚点。

## 10. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 1.0.13 活体与 1.0.16 源码漂移 | §0.1 门：活体优先，漂移记录；样本不足保持阻断 |
| **durable 读回机制不可建立**（P7：写盘延迟无界/优雅关闭不 flush） | **Phase 2 切换能力整体阻断**（S1：不交付成功假象）；只保留观测面；重新设计需官方关闭路径证据 |
| **外部 resident 并发写文件序未证明**（P4(b)/P7(e)） | case (c) 标「行为未证明」：不承诺 TUI 即时可见、验收不含该场景；取证后再定 |
| 跨源旧通知晚到翻回 | 无全局序号时不可证明消除（残差明示）；bridge 单点到达序 + 同 epoch 去重 + 重订阅重基线兜底；单测注入序验证 |
| 目录快照与执行非原子 | 官方 resolve 落 ④；§6.1 明示边界 + 缓存整表替换规则收窄窗口（每 turn ACU/每面板打开 list 刷新） |
| mode-only 子进程成本 | 与一次空 turn 同量级；切换低频；durable 读回在同进程生命周期内完成不额外 spawn |
| `permission_mode` 键集变化（D3 推荐） | iOS grok 权限菜单缩为两键（可见变化，owner 确认）；参数化测试锁分派；备选保留 5 键零可见变化 |
| iOS 门控放宽后其他 backend 误显示 | capability 断言三家无；白名单/回退 per-backend |
| 命令行 user message 与 transcript polling 投影冲突 | P6 取证行形状（含 HOST_TURN_META_KEY meta 是否进历史）；映射层处理，不改投影身份公式 |

回滚：grokbuild 去掉 `SessionCommandCatalog`/`SessionModeSwitcher` 实现 →
capability 消失/`handleSetPermissionMode` 落回 legacy 分支（含 D3 备选的 5 键
路径）→ iOS 门控与菜单自动退回（dsh 期形态）；codec ACU/CMU 消费与冷 hydrate
即使回滚面板也保留（状态捕获无副作用）。

## 11. 与后续案的边界 + 开放项

边界：本项关闭 think.md「Grok iOS Plan 只写 agent 内存」与调研 §7 的 Grok 命令
面板两个口子。不关闭：skills 深面、workflow 进度投影、`ask` 模式、permission
其余键接真（D3 备选下保留空转）、MCP elicit、interjection（Phase B）、
prompt-only `loop` 特化、`/context` 本地呈现面（另案）、错误反馈 inline 化
（跨 backend 另案）。

开放项（Phase 0 取证定夺，不猜；**取证完成前对应能力保持阻断**）：

1. P3 catalog 单例子进程调 `x.ai/commands/list` 可行性（cwd 传递）。
2. P4(b) 已有外部 resident 时切换 + 随后发送的写序/可见性——决定 §6.2.0
   case (c) 的效果承诺上限。
3. P5 三路 CMU 的可比较序号/身份 + replay 标记形状——决定 §6.2.3 时序终稿
   （无序号则残差明示）。
4. P6 反馈矩阵各组样本（含无输出组、正文业务失败组）——D1 终表前提。
5. P7 durable 读回可行性（有界等待/优雅关闭 flush/失败判定）——**不可建立
   则 Phase 2 切换能力阻断**。
6. P8 冷 hydrate 权威来源（summary.json mode / plan_mode.json / 尾部回扫）。
7. D1-D4 owner 裁决（D1 依 §7.1a 矩阵；D2/D3 正文仍标待裁决，不因摘要只提
   D1/D4 视为已定——第二轮明确要求）。
