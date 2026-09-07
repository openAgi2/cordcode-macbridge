# Grok Build 斜杠命令面板 + 计划模式官方通道 接入方案

> 状态：**方案 v1.3（2026-09-07，未实施；三轮评审 R1-R8/S1-S6/T1-T3 已全部响应，D1-D4 已裁决并入）**。v1.0 经[第一轮实施评审](2026-09-07-grok-build-slash-command-panel-implementation-review.md)不通过（6 P1 + 2 P2）修订为 v1.1；v1.1 经[第二轮评审](2026-09-07-grok-build-slash-command-panel-implementation-review-r2.md)仍不通过（4 P1 + 2 P2：S1-S6）修订为 v1.2；v1.2 经[第三轮评审](2026-09-07-grok-build-slash-command-panel-implementation-review-r3.md)仍不通过（2 P1 + 1 P2：T1-T3，第三轮并受权代行 D1-D4 裁决），本版逐项修订，三轮响应见下方「评审响应与修订记录」。dsh 方案 §11 的十轮返工教训（尤其 §11.2 ⑤⑧⑨⑩与 §11.3 四拍纪律、§11.4 迁移检查单）仍是本方案的**前置必读**。事实基线：调研
> [2026-09-04-slash-command-skill-cross-backend-survey.md](2026-09-04-slash-command-skill-cross-backend-survey.md)
> §7（两轮独立评审通过）。

> 第三轮：用户授权「继续评审，并替我做裁决」，D1–D4 已裁决（§3.2）并自本版起正文统一为已裁决表述（清理遗留「推荐/备选/待确认」措辞）；技术残留 T1-T3 已在本版回填——**显示状态唯一写者改为权威读、通知一律降级为验证读触发（T1/T2）**、**面板每次打开改为权威拉取（T3）**。历史响应表仅说明各轮演变。真实 turn 与外部 TUI 协作取证（P4/P6/P4(b)/P7(e)）仍待 owner 成本报备后启动；产品裁决不等于批准实施。

## 评审响应与修订记录

### 第一轮（v1.0 → v1.1）：R1-R8 响应

8 项全部吸收，无整项不采纳；两处部分采纳理由随行注明。第二轮复核认定
R2/R3 方案层关闭，R1/R4/R5/R6/R7/R8 **响应但未闭环**——残留缺口即第二轮
S1-S6，全部在 v1.2 处理。「已响应」不等于「已闭环」。

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
| S1 CMU/短延迟不是持久化屏障；软失败留下成功假象（写盘失败后新 actor 本就是 default，不会再发 CMU 纠正 chip，观测不自然收敛） | P1 | **采纳**：删除「CMU/短延迟屏障」「软失败无污染、观测自然收敛」；settle 定义三态（RPC 已接受 / durable 已确认 / 未确认）；durable = 有界等待内权威读回（`plan_mode.json` 或 P7 证明的等价官方路径）；读回超时 = 真实 RPC error「持久化未确认」，chip 不翻、不自动重试、不伪造反向状态；P7 增关闭方式/失败判定/故障注入边界；1.0.13 无法建立可靠读回 → **Phase 2 切换能力整体阻断**；第三轮指出 CMU 仍可先于确认翻转 chip → v1.3 §6.2.2/§6.2.3 事务化 + 通知降级 | §6.2.2、§0.1 P7、§10 |
| S2 「单写者」缺 resident 所有权与并发规则（`session.go:99` 每 child 都是 `--no-leader` 独立进程；set_mode 改收到请求的 actor；外部 TUI 是另一 actor，仅 spawn 时读文件） | P1 | **采纳**：新增 §6.2.0 resident 路由三态（idle / CordCode 活跃 turn / 外部 resident）；per-session 锁扩展覆盖 Send/Execute/SetSessionMode（活跃 turn 时切换等待，超时报错不并发）；外部 resident 场景明示「两个独立 actor、文件仅 spawn 时同步、TUI 热更新不承诺」，跨进程并发写文件 = 上游行为由 P4(b)/P7(e) 取证，未证明前不作产品承诺；「单写者」改精确表述：CordCode 自身模式声明通道收敛为一个（删 `_meta.mode`），actor/外部一致性另行保证 | §6.2.0、§6.2.3、§0.1 P4/P7、§9 #5 |
| S3 冷恢复不存在于「既有 tailer 路径」（tailer 从 EOF 起，leader 丢 isReplay）；到达序不保证不回退 | P1 | **采纳**：冷 hydrate 改为**明确新增实现**（入口 = 会话打开/bridge 重启后首个状态请求；来源 = P8 裁决：summary.json mode / plan_mode.json 快照 / updates.jsonl 尾部有界回扫——均为新代码；失败/空 = mode 未知中性态，不伪造）；第三轮指出到达序 last-wins 与「晚到不翻回」验收矛盾 → v1.3 §6.2.3 重写为通知降级 + 权威读唯一写者 | §6.2.3、§6.3、§0.1 P5/P8、§8 |
| S4 两类输入走查不覆盖全目录反馈；`/context` 已是反例（shell `ok_end_turn(0,None)` 无详情；pager 是 ShowContextInfo 本地面） | P1 | **采纳**：新增 §7.1a **反馈覆盖矩阵**（语义组 → 请求 → 官方输出/副作用 → iPhone 呈现 → 验证样本），按反馈类型分组替代逐命令四拍；本地呈现面组第一期不纳入面板（D1 已裁决排除）；`ResultKind:"success"` 语义重定义 = 仅 turn 正常完成，**不代表业务成功**；正文承载的业务失败原样上屏，禁止 NLP 猜测错误；错误弹窗仅 RPC 层失败，正文错误不承诺弹窗；P6 扩无输出组 + 正文业务失败组样本 | §7.1a、§6.1、§4.4、§0.1 P6 |
| S5 handler 优先分支只接 plan/default 与「其余五键维持现状」冲突（`PermissionModes()` 仍广告 6 键，iOS 统一走 `setPermissionMode`）；default 双语义 | P2 | **采纳**：§6.2.4 权限键路由表逐键定义；第三轮 D3 裁决定稿：仅保留 plan/default，其余四键移除、不广告、拒绝调用返回不支持（无空转兼容路径）；参数化测试锁分派/错误/广播；另补 `list_permission_modes` 当前值 per-session 读路径（r3） | §6.2.4、§3.2 D3、§8 |
| S6 fresh 目录只有 fetchedAt 无失效定义（fresh 可以是任意旧快照） | P2 | **采纳**：§6.1 缓存规则定稿——身份键 `(backend, sessionID, cwd)`、整表替换、bridge 重启全清、cwd 换键、无 TTL；第三轮指出「面板每次打开仍可命中旧缓存」= 无界陈旧 → v1.3 §6.1 改每次打开权威拉取（T3） | §6.1、§2、§8 |

两处**部分采纳沿用**（第二轮已裁定接受）：(a) 不逐命令重复四拍，以
§7.1a 反馈类型分组矩阵替代；(b) RPC 失败沿用现有弹窗（inline 化跨 backend
另案）——但按 S4 细化：**正文错误不是 RPC 失败**，走 transcript 原样上屏，
不承诺弹窗。

### 第三轮（v1.2 → v1.3）：T1-T3 响应 + D1-D4 裁决并入

3 项技术项全部采纳，无不采纳项；第三轮代行的 D1-D4 产品裁决（§3.2）与
六键路由定稿（§6.2.4）一并并入，正文遗留「推荐/备选/待确认」措辞清理为
已裁决表述。

| 评审项 | 级别 | 响应 | 落点 |
| --- | --- | --- | --- |
| T1 CMU 可先于 durable 确认翻转 chip，绕过三态门（上游 actor 更新 → persistence 与 CMU 独立入队，无先写盘后通知保证；同值去重 ≠ 确认） | P1 | **采纳**：一切通知（含自有事务窗口内的 stdout CMU）**不再直接写显示状态**——显示状态 `confirmedMode` 唯一写者 = 权威读（§6.2.3）；切换事务化（§6.2.2）：待确认期间 chip 显示 pending/当前确认值而非请求值，自有 CMU 仅作「actor 已接受」旁证并触发验证读；读回值 ≠ requested = 被外部覆盖（显示文件现值）；超时 = error 不自动重试、不伪造反向状态；四种到达顺序验收（CMU先到+读回超时 / CMU先到+成功读回 / 无CMU幂等 / 待确认期间外部相反切换）全部不得把未确认值显示为成功 | §6.2.2、§6.2.3、§6.3、§7.2、§8 |
| T2 到达序 last-wins 不能实现「旧通知/旧 response 晚到不翻回」（无序时旧 plan 迟到必胜；同值去重不处理异值新旧；重基线只在重订阅时运行） | P1 | **采纳**（选第三轮给出的第三种机制：**通知作为权威读回的触发，而非直接写最终状态**——来源选择为实现判断）：通知一律降级为触发，验证读**串行读文件现值**——迟到旧通知读到的也是现值，结构上不可能翻回；per-session 串行处理（触发→读→比较→下发同一互斥内，发射序=读序）；订阅代际丢弃旧代际消息；response patch 只以 durable 确认值下发，迟到事务 response 无显示效应；P5 序号/身份降级为**优化用途**（同值去重节流、事务关联），机制不依赖；测试按机制真实处理路径断言，不得用恰好有序数据冒充乱序安全 | §6.2.3、§6.3、§0.1 P5、§8、§10 |
| T3 面板每次打开只重新请求 bridge，无 turn/无 cwd 变化/无重启时通道 2 永不触发 = 无界陈旧（bridge 自造，非官方原子性） | P2 | **采纳**：List RPC = **每次打开实际执行通道 2 权威拉取**，成功整表替换缓存并返回新表；失败即错误，**不回退旧缓存冒充刷新成功**；「命中缓存即回」从展示路径删除；缓存职责收窄 = Execute 白名单 + turn 期新鲜度记录 + 诊断；通道 2 不可行时替代方案同样实拉，仍不可行 → List 展示阻断；无每次 execute 二次拉取；验收「首开 A → 外部变 B → 无 turn/重启/cwd 变化 → 再开必得 B 或真实错误」进 Phase 1 | §6.1、§2、§8 |

## 0. 来源清单（P0）与开工门

| 仓库 | 工作树 | 分支 | 提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| grok-build（上游，只读） | `/Users/jacklee/Projects/grok-build` | detached @ `72a61251` | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9`（1.0.16） | 干净 |
| **目标运行版本**（本机安装二进制） | `~/.grok/bin/grok` | — | **1.0.13（自报 `5e9a58528b76`，不在 checkout 历史中）** | — |
| cordcode-macbridge | `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（本方案工作树） | `plan/approval-layer` | `39b6f6f` + 本 v1.3 修订提交（产品源码与 `de17e6f` 零差异） | 干净（本文档与三轮评审报告除外） |
| cordcode-ios | `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 |

来源注记：v1.0 调研期锚点在 `a04095e`（本 worktree）读取、`fbb4940`（main
checkout）复核零差异。v1.1 修订轮复核了第一轮全部锚点。v1.2 修订轮复核了
第二轮全部锚点。**v1.3 修订轮（2026-09-07）**：第三轮报告新增锚点已逐条
复核属实——macbridge `go-bridge/handlers.go:4505`（`handleListPermissionModes`
当前选中值读 agent 级 `GetMode()`，非 per-session）、
`agent/grokbuild/session.go:511`（`Send` 写入请求后即返回，不等 turn 终态）。
既有锚点不变：macbridge `session.go:99`、`updates_file_tailer.go:60`、
`leader_subscriber.go:573`、`grokbuild.go:585`；grok `slash_exec.rs:60-180`、
pager `context.rs:21`、`acp_session.rs:1365-1405`；iOS
`ChatUIKitContainerView.swift:6193/6236`。iOS 锚点读自 main checkout，实施若换
iOS 工作树须按门点规则重新登记。

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
| P3 | catalog 单例子进程调 `x.ai/commands/list {cwd}` | 进程级 catalog 子进程 → initialize → ext list | 无 | 无 | **List 展示主通道**（§6.1 T3：每次面板打开实拉）可行性；不可行 → 替代 = 专用子进程 handshake + session/load 捕获 ACU（同样实拉）；仍不可行 → List 展示阻断 |
| P4 | **（真实 turn）prompt 不带 `_meta.mode` 的继承 + 已有 resident 时切换**：(a) 置 plan → 退子进程 → 新子进程 load → 发不带 mode 的 prompt，验证按 plan 行为；(b) **Mac TUI 打开同会话（外部 resident 在场）时手机切换，随后发送**，观察 TUI 行为、文件写序、gateway CMU | (a) 依赖 P7 先置 plan；(b) 需 Mac 端 TUI 配合（owner 在场） | **有**（一条 turn） | 会话内多一条 turn；可弃会话；(b) 后 TUI 自行复位 | **官方继承链承重事实 + 外部 resident 路由规则**（§6.2.0 case c）：1.0.13 上独立 `--no-leader` 子进程 set_mode 与外部 TUI actor 的文件写序、TUI 是否热更新、后续 turn 从哪个模式恢复 |
| P5 | `current_mode_update` 三路形状 + 可比较的序号/身份元数据 + 冷重放条目 replay/live 标记 | P7 顺带捕获 stdout 路；leader 路订阅 gateway；直接读 `updates.jsonl` | 无 | 无 | §6.2.3 机制**不依赖**序号（结构安全 = 读文件现值 + 串行 + 代际，T2）；取证结果用于**优化**：验证读同值去重节流、通知关联事务；replay 标记形状仍用于 hydrate |
| P6 | **（真实 turn）反馈类型矩阵取证**：`/compact`、`/always-approve`（状态变更组）、`/dream` 或 `/flush`（无输出组）、`/hooks-add <非法路径>`（正文业务失败组）、`/context`（shell ACP 分支）；记录终态 stopReason、是否产生 AgentMessageChunk 行（HOST_TURN_META_KEY）、可透传反馈文本 | 真实会话逐条执行 | **有**（compact 压缩可能调模型） | **always-approve 测后必须切回**；compact 压缩不可逆；可弃会话 | §7.1a 矩阵各组的实际输出形状 + settle 四分法映射 + `ResultText` 是否存在；D1 准入集合终表前提（状态变更组须实际权限状态呈现证据） |
| P7 | mode-only 子进程 set_mode 的 **ack、durable 与失败判定**：(a) response → 有界等待读回 `plan_mode.json`（或等价官方读回）→ 优雅关闭 → 新子进程 load 验证；(b) 重复 plan→plan / default→default（幂等）；(c) **关闭方式对比**（优雅 EOF 关闭 vs 强杀）下持久结果差异；(d) **写盘异常/超时场景判定**（隔离测试注入，仅验证内部行为，不作安装版协议证据）；(e) 已有外部 resident 时切换（与 P4(b) 同场） | mode-only 短命子进程若干轮 | 无（set_mode 不调模型） | `plan_mode.json` 变化；测后切回 default | **权威读机制可行性**（§6.2.2 事务读回 + §6.2.3 通知触发验证读共用同一读路径）：有界读回是否可靠、写盘延迟是否有界、优雅关闭是否 flush、失败如何呈现；不可建立 → **切换能力阻断** + **显示确认降级 unknown 中性（观测仅进诊断日志）** |
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
   被官方过滤）∩ D1 准入集合，每次打开由 Mac 权威拉取（§6.1），点选按 D4 移动端
   规则分流执行，反馈按 §7.1a 矩阵呈现。
2. **Plan 模式 iOS 入口接官方通道**：think.md「Grok iOS Plan 只写 agent 内存」
   的现状终结——CordCode 侧模式写入通道收敛为官方 `session/set_mode`（经
   `SessionModeSwitcher` 接口，§6.2）；**prompt 恒不携带 `_meta.mode`**；切换
   以 RPC response + durable 读回双确认为准（§6.2.2）；**显示状态唯一写者 =
   权威读**（事务读回 / 通知触发的验证读 / 冷 hydrate，§6.2.3），通知一律降级
   为触发，Plan chip 只显示已确认值或未知中性态。外部 resident（Mac TUI）与
   CordCode 子进程是各自独立 actor，一致性由 §6.2.0 路由规则约束——本方案
   **不宣称**「单写者消除外部多写者竞争」，只收敛 CordCode 自身的写入通道。
3. 已交付的 plan 审批卡（`plan_review`，leader 广播 `x.ai/exit_plan_mode`，
   think.md §25）继续工作，本项不改审批语义。

## 2. 明确不做（第一期）

| 项 | 理由 |
| --- | --- |
| **prompt 携带 `_meta.mode`** | R6：手机缓存陈旧时携带 mode 会经官方 `reconcile_plan_mode_with_prompt`（`session_mode.rs:191-216`，对传入值直接 set）覆盖外部切换。官方已有继承链（`plan_mode.json` + `spawn.rs:625-648` 恢复 + `acp_agent.rs:1113-1129` prompt 无 mode 查本进程 actor），删除该通道即收敛 CordCode 自身写入口（actor/外部一致性另由 §6.2.0 处理） |
| **initialize `_meta.availableCommands` 作 List 兜底** | R5：builtins-only 冒充全目录是静默降级；List fail-closed（§6.1），该字段仅诊断日志 |
| **`/context` 等本地呈现面命令的 iOS 呈现面** | S4：shell ACP 分支 `ok_end_turn(0, None)` 不返回详情（`slash_exec.rs:90`），官方详情是 pager-local `ShowContextInfo` 本地面（`context.rs:21`）——「列表全上 + prompt 执行」无法等价替代。第一期不接入本地 surface；D1 已裁决排除该组，接入另案 |
| 键入 `/` 自动补全弹层 | owner dsh 裁决沿用：只要菜单入口 |
| prompt-only 命令 `loop` 的 loop_fire_mode 特殊面 | `PROMPT_COMMANDS` 特例重写 blocks + displayText，产品面窄；目录展示但走通用通道，特化另案 |
| workflow 进度投影（`workflow_projection`） | 命令进目录、可执行；workflow 专属过程投影是独立官方 surface，另案 |
| skills 的 `kind:"chat"` 产品通道 | Grok Chat 产品 lane，CordCode 不消费 |
| MCP elicit / `x.ai/queue/*` interjection | think.md：interjection 后置 Phase B |
| 合成全 backend Plan 按钮 | think.md「iOS 进入计划模式」行 owner 明令禁止 |
| 客户端自行合并/去重三条目录通道的结果 | 官方顺序（builtins-first）与 availability 过滤都是官方事实；客户端合并=自造目录（§4.1） |
| **`permission_mode` 其余四键（D3 已裁决移除）** | `acceptEdits`/`auto`/`dontAsk`/`bypassPermissions` 从 `PermissionModes()` 移除、不广告；旧客户端发送已删键返回明确不支持、不改状态、不广播成功（§6.2.4）；各键接真另案 |
| 目录刷新 UI（下拉重拉等） | 面板**每次打开 = Mac 权威拉取**（§6.1，T3）+ ACU turn 期刷新已覆盖官方语义；手动刷新自造 |

## 3. Owner 决策

### 3.1 已锁定（既往裁决，直接生效）

| # | 来源 | 裁决 |
| --- | --- | --- |
| L1 | think.md「iOS 进入计划模式」行 | 三条 Plan 入口分别接各自官方通道，**禁止合成全 backend 按钮**；Codex 入口挂起、DSH 已交付、Grok 即本方案 |
| L2 | think.md §25 | plan 审批两键卡程度可接受；完整体验属跨 backend 通用另案，本项不动 |
| L3 | dsh 方案 §11.1 #3（2026-09-05 owner 裁决） | 面板白名单收窄**是 dsh 专属裁决**；对 grok 不是既成事实——grok 产品白名单是本方案 D1 待裁决项，iOS 白名单机制必须 per-backend（§5.2） |
| L4 | dsh 方案 §12 | 命令入口 = ＋ 菜单「命令」节（不是独立 `/` 按钮） |

### 3.2 D1–D4 已裁决（2026-09-07，用户授权评审者代行）

| # | 确定裁决 | 执行边界 |
| --- | --- | --- |
| D1 | **按反馈证据准入，不套 dsh 三条白名单。** 面板 = 当前官方目录 ∩ 已核验反馈类型的产品准入集合；保留官方顺序，排除 `context` 及需要未接入 pager-local surface 的命令。 | 正文反馈、已证实官方无输出仅 echo 的命令可纳入；状态变更命令须证明对应状态确实可在手机观察。skills/workflows/goal 等待样本条目先不显示，通过相应证据门后按此规则纳入，无需再问 owner；不能只因新版本出现在目录里就自动展示未知类型。`plan` 不作为 shell 命令补进目录。 |
| D2 | **Grok 输入框 Plan chip**，default 灰、plan 橙，点击通过 SessionMode 通道显式设置目标值；不发送 `/plan` 或 `/plan off`。 | 未知、持久化未确认、resident 归属无法安全确定时显示中性/不可切换状态，禁止猜测当前值进行 toggle；切换待确认时禁用重复操作。不接受“旧通知可把状态翻错、等用户重开修复”作为交付标准。Phase 2 证据门失败则只保留观测，明确禁用切换；不能回到旧的内存假成功。 |
| D3 | **只保留 `plan` / `default` 两键**；移除 `acceptEdits`、`auto`、`dontAsk`、`bypassPermissions` **四键**，不保留空转兼容路径。 | 原来共六键，保留两键应删除四键，无“第 5 键”。允许 Grok 菜单缩为两键。default 仅表示退出计划模式，**不承诺关闭 YOLO 或恢复逐工具权限询问**；Grok 文案须反映这一语义。旧客户端发送已移除键返回明确不支持，不改状态、不广播成功。其他 backend 不变。 |
| D4 | **hint 非空统一 claim**：点选收菜单、认领 `/name ` + 官方 hint；空参回车照常提交，由官方校验；hint 为空点选执行。 | 不增加 compact 等逐命令快捷执行特判。空参可提交不等于业务必成功；usage 或正文错误原样呈现。RPC 错误沿用现有弹窗。 |

以上产品裁决已完成。Phase 0 负责验证具体目录条目和协议路径，不能放宽准入规则或以假数据替代失败。真实 turn 和外部 TUI 协作取证不由本次评审自动启动。

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
   过滤或合并三条通道（D1 准入交集是 owner 依 §7.1a 矩阵的产品裁决，性质
   不同且须记录终表）。
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
   （`context.rs:21`）等——不在 A，当文本发落入 ④）；**C = iPhone 产品准入
   集合**（D1 裁决，作用于 A 的展示过滤，依据 §7.1a 矩阵）。iPhone Plan 入口
   必须转换为模式动作（§6.2）；模型/effort 走既有 catalog 通道。
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
| **持久化与 CMU 是两条独立队列**：`persist_plan_mode_state()` 向 persistence 队列 send；写盘在异步 `PersistenceMsg::PlanModeState` 分支执行，**失败仅记 warning，不向上反馈**——RPC response 与 CMU 均不证明写盘完成或成功；且二者入队相互独立，**CMU 可先于写盘到达观察者**（T1 依据） | `session_mode.rs`、`updates.rs:348`、`persistence.rs:1945`（S1/T1 依据） |
| `session/prompt._meta.mode` 存在但对传入值**直接 set**（`reconcile_plan_mode_with_prompt`，`session_mode.rs:191-216`）——陈旧值覆盖外部切换。本方案不用（§2） | `session_mode.rs` |
| PlanModeState 机持久化到会话目录 `plan_mode.json`，resume 恢复，`awaiting_plan_approval` 同样持久化 | `plan_mode.rs` |
| `CurrentModeUpdate` 真实转换时发出；持久化进 `updates.jsonl` 并转发 gateway | `updates.rs:348`、`notification_bridge.rs:201` |
| **命令正文反馈通道**：hooks 类命令的成功/失败文本经 `AgentMessageChunk`（带 `HOST_TURN_META_KEY` meta）进入会话 scrollback，随后 flush replay buffer，turn 仍正常 end_turn——**业务失败不改变 turn 终态** | `slash_exec.rs:90-180`、`acp_session.rs:1365-1405`（S4 依据） |
| **无输出分支**：`ContextInfo => ok_end_turn(0, None)`；Dream 注释「Intentionally no user-visible output」；FlushMemory 有 skip 路径 | `slash_exec.rs:60-90`（S4 依据） |
| `x.ai/exit_plan_mode` 批准 → notification_bridge 回发 CurrentModeUpdate 恢复 default | `notification_bridge.rs`（§25 已交付消费端 `leader_subscriber.go:680`） |

推论（v1.3 修正后的精确表述）：模式跨进程持久（`plan_mode.json`，
**仅 spawn 时读取**）+ prompt 无 mode 查**本进程 actor 内存**——文件是跨进程
同步点但**不是热同步**：CordCode 每个 `--no-leader` 子进程与外部 resident
（Mac TUI）是**各自独立的 actor**，互相不读对方的内存，也**不**在文件变化时
热更新。因此：CordCode 可用 mode-only 子进程切换（写文件）与观测（CMU 三路），
但**不能宣称**「改了文件即所有活跃 actor 可见」；切换的持久生效必须以 durable
读回确认（§6.2.2），显示状态必须以权威读（文件）为唯一写者、通知只作触发
（§6.2.3），外部 resident 在场时的行为由路由规则与 P4(b)/P7(e) 取证
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
| `SetMode(mode string)` 只写 agent 级内存，无 sessionID/ctx/error；handler `handleSetPermissionMode` 先 SetMode 后查 SessionID、失败也广播成功；`a.mode` 两会话共享 | `core/interfaces.go:897`、`handlers.go:4532`、`grokbuild.go:573-583` | 三重缺口（R1）+ S5 冲突 → §6.2.4 路由表 + D3 裁决定稿 |
| `PermissionModes()` 返回 6 条，含 `{Key:"plan"}`；iOS 权限菜单由该 catalog 构建、统一走 `setPermissionMode` | `grokbuild.go:585`；iOS `ChatUIKitContainerView.swift:6193`/`:6236` | D3 裁决移除四键；键集与路由表（§6.2.4）对齐 |
| `handleListPermissionModes` 的「当前选中值」读 agent 级 `GetMode()`，非 per-session | `handlers.go:4505` | r3：读路径改 per-session（§6.2.4）——否则 chip 为 plan 而菜单显示 default；未知时不选中任何键 |
| `Send` 写入请求后即返回，不等 turn 终态 | `session.go:511` | r3：§6.2.0 case (b)「活跃 turn 持锁」的锁持有期必须到**实际 turn 终态/取消清理**，不是函数返回 |
| **每个会话子进程都是独立 `grok agent --no-leader stdio`** | `session.go:99` | S2：mode-only 子进程与外部 resident 天然是两个 actor；路由三态（§6.2.0）必须定义 |
| `updates_file_tailer.go` **从当前 EOF 起尾随**，不回放 EOF 前的 CMU；`leader_subscriber` 在 codec 前丢弃 isReplay 通知 | `updates_file_tailer.go:60`、`leader_subscriber.go:573` | S3：**冷模式 hydrate 是新增实现**（§6.2.3），不存在「既有 tailer 回放路径」可复用；两处现状都不能提供冷恢复 |
| wire 层 `execute_session_command` 的 line 校验只查非空 | `handlers_session_commands.go:52` | `/` 前缀+单行校验由 grok 侧自实现（R5） |
| `sessionPromptParams` 仅 `{sessionId, prompt}` | `acp_types.go:240-243` | **保持无 `_meta`**（§2/R6） |
| codec 把 CMU/ACU 等归入 known-drop；`initializeMeta` 只解 `modelState` | `acp_codec.go:326-330`、`acp_types.go:102-115` | ACU/CMU 升级为捕获（§6.3）；availableCommands 仅诊断日志 |
| plan 审批卡已交付 | `leader_subscriber.go:680` | 无差距，复用 |
| 进程级单例 catalog 子进程现用于 session/list + 模型目录 | `catalog_session_list.go` | P3 取证后复用作 `x.ai/commands/list` 拉取（现为 List 展示主通道，§6.1） |
| core `SessionCommandCatalog` + wire RPC 两件套已在（dsh 期） | `core/session_commands.go:10-40`、`handlers.go:1642-1644` | grokbuild 实现之；dsh 注释改分 backend 契约；零新增 RPC |

### 5.2 iOS（六项清单 + 权限菜单随 D3 联动）

| # | 现状 | 锚点 | 差距 |
| --- | --- | --- | --- |
| 1 | ＋菜单「命令」节门控硬编码 `== .deepSeekWeb` | `ChatUIKitContainerView.swift:4565-4571` | 放宽为 capability 驱动 |
| 2 | `commandWhitelist`（compact/goal/plan）无条件过滤所有 backend | `SlashCommandRouting.swift:11` | 白名单 per-backend：仅 deepSeekWeb 套用；grok 不套 dsh 白名单——准入由 Mac 侧 D1 交集承担（§6.1） |
| 3 | `AttachMenuPlanner` 对未加载/失败/空目录回退内置三条（含 `/plan`） | 同文件 | 回退三条仅对 deepSeekWeb；grok 三态真实呈现；绝不显示 dsh `/plan` |
| 4 | Plan chip 刷新仅 deepSeekWeb；点击发 `/plan off` | `ChatUIKitContainerView.swift:6583`/`:6682` | chip 数据源扩 grok 模式状态（含冷 hydrate §6.2.3）；grok 点击 = `set_permission_mode` 模式通道 |
| 5 | 通用执行失败走 `presentSlashCommandErrorAlert` 弹窗 | `ChatUIKitContainerView.swift:6670` 邻域 | 沿用（**仅 RPC 层失败**）；正文承载的业务失败走 transcript 原样上屏，不弹窗（S4 细分） |
| 6 | `route(hint.isEmpty)` 判决 generic | `SlashCommandRouting.swift:32-38` | 代码零改动；依据改标移动端规则（D4） |
| 7 | 权限菜单由 `PermissionModes()` catalog 构建 | `ChatUIKitContainerView.swift:6193` | D3 已裁决移除四键：grok 菜单随 catalog 自动缩为两键（无 iOS 代码改动，可见变化已裁决）；菜单「当前选中值」读路径为 Mac 侧改动（§5.1 `handlers.go:4505`） |

## 6. Wire 设计

### 6.1 `SessionCommandCatalog` 的 grok 实现（Mac）

**List（fail-closed 权威拉取 + 职责收窄的缓存，S6 + T3）**：

- **iOS 面板每次打开的 List RPC = 每次实际权威拉取**：catalog 单例子进程带会话
  cwd 调 `x.ai/commands/list {cwd}`（P3），成功即**整表替换**缓存并返回新表
  （含空表——官方空目录真实呈现为空态，不保留旧表）；失败 → RPC error（iOS
  呈现错误态），**不回退旧缓存冒充刷新成功**（T3）。「命中缓存即回」从展示
  路径删除。
- **通道 2 不可行时的替代（P3 裁决）**：退化方案 = 专用子进程 handshake +
  session/load 捕获 ACU（同样每次真实拉取，非缓存命中）；仍不可行 → **List
  展示能力阻断**，不得回退缓存命中或 initialize builtins。
- **通道 1（turn 期 ACU）继续写缓存**，但不再作为 List 展示来源；缓存职责
  收窄为：① Execute 白名单校验的就近输入（缺失时先走一次权威拉取，仍失败 →
  fail closed）；② turn 期新鲜度与诊断元数据（`fetchedAt`、来源 acu/list）。
- **缓存身份与失效（S6）**：身份键 = `(backend, sessionID, cwd)`；bridge 重启
  全清；cwd 变更换键；无 TTL——展示路径已不依赖缓存新鲜度（每次打开实拉），
  T3 指出的「无 turn/无 cwd 变化/无重启时永命中旧缓存」的无界陈旧窗口随之
  消除。不做每次 execute 二次拉取。
- **D1 产品准入交集**：List 返回前与「已核验反馈类型准入集合」（§7.1a 矩阵
  驱动）取交集——排除 `context` 及依赖未接入 pager-local surface 的命令；待
  样本类型通过证据门后加入集合（无需再问 owner）；不能只因新版本出现在目录
  里自动展示未知类型。准入终表与排除理由记录进实现说明。
- 映射：`Name`/`Description` 顶层；`Hint` ← `input.hint`（缺席 → `""`）；中间
  wire 类型解码（`input` 指针/omitempty）。skills/workflows 额外字段（P2）第一
  阶段丢弃。
- **非原子边界（明示）**：目录快照与执行不原子——拉取后 skill 被删/availability
  变化，execute 白名单仍命中但官方 resolve 落 ④ 普通 prompt。这是官方行为且
  窗口以「每次打开/每 turn」为界；不做执行时二次拉取。

**Execute（官方 prompt 通道）**：

- `ExecuteSessionCommand(ctx, sessionID, "/compact")` = 复用现有 per-turn
  `session/prompt` 管道发送该行，不合成 user 气泡、不挂 `_meta.mode`、不带附件。
- **line 校验**（grok 侧自实现）：`/` 开头、单行；未过 → RPC error。
- **白名单（fail-closed）**：命令名必须命中该会话目录缓存；缓存缺失 → 先走
  一次权威拉取；拉不到 → RPC error「catalog unavailable」，不直接发 prompt。
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

### 6.2 模式通道（Phase 2，v1.3 重设计）

#### 6.2.0 resident 路由三态（S2——先于一切切换动作判定）

CordCode 每个会话子进程都是独立 `grok agent --no-leader stdio`
（`session.go:99`）；`session/set_mode` 修改**收到请求的 actor** 的内存，文件
仅在 spawn 时被其他进程读取。切换请求按会话当前状态路由：

| 态 | 判定 | 路由 | 效果承诺 |
| --- | --- | --- | --- |
| (a) idle，无外部 resident | 该会话无进行中 turn，且无外部 TUI 打开同会话（不可直接探测——按 P4(b) 证据界定可探测性） | mode-only 子进程 = **唯一活跃 actor**；set_mode 权威写文件 | durable 读回确认后可承诺「已持久生效」 |
| (b) CordCode 活跃 turn（per-turn 子进程在跑） | per-session 锁被 Send/Execute 持有 | 切换**等待**当前 turn 结束（与 Send/Execute/SetSessionMode 共用 per-session 锁串行；**锁持有期 = turn 实际终态/取消清理，不是 `Send` 函数返回**——`session.go:511` Send 写入请求后即返回）；ctx 超时 → RPC error（不并发切换、不插队） | 不与 turn 竞争；长 turn 期间切换可能超时失败，真实上报 |
| (c) 外部 resident（Mac TUI 打开同会话） | P4(b)/P7(e) 取证界定的探测方式（或无法探测则按未知处理；**归属未知 = 按 D2 禁止发起切换**） | mode-only 子进程与 TUI 是**两个独立 actor**：我们的 set_mode 写文件、改自己 actor；**TUI 的 actor 不热更新**（它仅 spawn 时读文件，prompt 查自身内存）；反向 TUI 切换 → 文件被写 + gateway CMU 触发验证读（§6.2.3）+ 我们下一个子进程 spawn 时恢复 | **不承诺** TUI 即时可见；跨进程对 `plan_mode.json` 的并发写序 = 上游行为，P4(b)/P7(e) 取证前该场景标「行为未证明」，Phase 2 验收不含该场景的产品承诺（§9 #5 已改写） |

「单写者」的精确表述（替代 v1.1 的宽泛宣称）：**CordCode 自身的模式声明通道
收敛为一个**（删除 prompt `_meta.mode`，写入只剩 set_mode）；外部写者（TUI、
审批流）与多 actor 一致性**不是**由「一个 RPC 名称」自动解决的，由上表路由 +
权威读重基线（§6.2.3）+ 观测收敛处理，且 case (c) 的文件写序证据未取得前
保持未证明。审批动作（approve/quit）走 leader 通知链不经过 CordCode 子进程，
可能与切换并发：durable 以文件最后写者为准，显示按 §6.2.3 权威读收敛。

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
→ legacy `ModeSwitcher`）：先校验 sessionID 非空、mode ∈ {plan, default}（其余
键按 §6.2.4 返回不支持），再调用；error → RPC error 不广播。成功（= response +
durable 读回双确认，下节）→ **以 durable 确认值（= 读回值）下发会话模式
patch**；显示状态只经 §6.2.3 权威读路径变化。`GetSessionMode` 返回
`confirmedMode`（miss → 触发冷 hydrate，§6.2.3）。grokbuild 实现：per-session
模式状态 + **per-session 互斥覆盖 Send/Execute/SetSessionMode**（§6.2.0 case b）；
`a.mode` 不再被新路径读写。**其余四键路由见 §6.2.4**——优先分支不吞掉它们。

#### 6.2.2 切换事务、settle 三态与 durable 读回（S1 + T1）

- **三态定义**：① RPC 已接受（response OK = 收到请求的 actor 已切换，内存真实
  生效）；② durable 已确认（持久层已写入）；③ 未确认（超时/读回不符）。
- **切换事务**：`SetSessionMode` = 路由判定（§6.2.0）→ 登记
  `pendingSwitch{requested, deadline}`（窗口内**拒绝同会话新切换请求**，D2：
  待确认时禁用重复操作——RPC 报 busy，不排队）→ mode-only 子进程
  `session/set_mode` → response（= ①，actor 接受证据）→ **有界权威读回窗口**
  （初稿 2s，P7 校准）：轮询权威读（`plan_mode.json` 或 P7 证明的等价官方
  读回）直至读到 requested 或超时。读到 requested = ② → `confirmedMode` =
  requested → RPC success → 以确认值下发 patch。
- **自有 CMU 在窗口内到达 ≠ durable（T1 核心）**：上游 actor 更新、persistence
  与 CMU 独立入队（§4.3），CMU 可先于写盘到达。自有 stdout CMU 一律经 §6.2.3
  通知处理路径降级为**验证读触发**，不写显示、不提前成功；在事务语境下它只
  作为「actor 已接受」的旁证。**同值去重只减少重复处理，不能把未确认变成已
  确认**。
- **对外呈现**：待确认期间 chip 显示 pending（或当前确认值），**不显示请求
  值**；response + 读回双过 → RPC success（chip 翻转）；读回持续 ≠ requested
  （外部并发写文件）→ 事务被覆盖：`confirmedMode` = 文件现值（外部确认值），
  RPC error（信息含实际值），不伪造反向状态；读回超时 → RPC error「持久化
  未确认」（真实未知状态，iOS 按现有弹窗呈现，chip **不翻转**）；**不自动
  重试、不向用户宣告已持久生效**。超时后迟到的落盘不再有通知（persistence
  静默），显示停留文件现值（诚实），下次触发/冷 hydrate/事务重基线。
- **幂等无 CMU 切换**（plan→plan / default→default）：官方无转换即无 CMU
  （§4.3）——事务**不依赖 CMU**，读回窗口照常，读到 requested 即成功。
- **P7 门槛（S1 + T1 扩展）**：权威读不可建立（写盘延迟无界、优雅关闭不
  flush、读回形状不可靠）→ **切换能力整体阻断**（不交付带成功假象的切换）；
  同时通知触发的验证读（§6.2.3）也不可靠 → **显示确认机制整体降级**：
  unknown 中性态 + 原始通知仅进诊断日志（D2：技术门失败只保留观测，且不得
  从未定序通知直接显示）。
- **四种到达顺序验收（T1，§8 Phase 2）**：CMU 先到 + 读回超时 / CMU 先到 +
  成功读回 / 无 CMU 幂等切换 / 待确认期间外部相反切换——**全部不得把未确认
  值显示为已成功切换**。
- 残差（读回通过但磁盘随后损坏/被覆盖）：接受并明示；durable 以文件最后写者
  为准，冷 hydrate 重基线。

#### 6.2.3 状态模型：权威读为显示唯一写者；通知一律降级为触发（S3 + T2）

- **authoritative** = 会话目录 `plan_mode.json`（spawn 恢复源；文件最后写者
  胜）。CordCode 的 iPhone turn 每次都从文件恢复，文件是对本产品路径的
  operative truth；TUI live actor 与文件的暂时分歧是上游 case (c) 现象，
  P4(b) 取证前不作产品承诺。
- **`confirmedMode`（显示状态，per-session）唯一写者 = 权威读**，四个入口：
  1. 自有切换事务的读回（§6.2.2）；
  2. 通知触发的验证读（下述）；
  3. 冷 hydrate（下述）；
  4. 重订阅重基线。
- **通知处理规则（T2 核心）**：三路 CMU（stdout / leader gateway / 文件
  tailer）与任何 response 都**不直接写 `confirmedMode`**；到达后触发一次
  **有界验证读**：串行读权威文件**现值**——与通知声称值一致 →
  `confirmedMode` = 该值；不一致（写盘竞争：通知先到、异步持久化未落盘）→
  短窗内重读（上限与事务读回同源，P7 校准）直至一致或小超时，仍不一致 →
  **以文件现值为准**（声称值未落盘 = 未持久化，不显示）。同值幂等节流：
  `confirmedMode` 已等于通知声称值时跳过验证读。
- **串行化与发射序**：per-session 单点串行处理（触发 → 读 → 比较 → 下发
  patch 在同一互斥内）；bridge → iOS 的模式事件按发射序到达（同连接有序），
  发射序 = 串行读序 → 终值 = 最新一次处理的文件现值。**迟到旧通知触发的
  处理读到的也是当前文件值**，最多产生一次冗余同值处理——结构上不可能翻回
  旧值（T2：不接受「翻错靠重开修复」）。
- **订阅代际**：leader 重连 / bridge 重启 / 会话重开建立新代际，旧代际消息在
  订阅层直接丢弃（不触发验证读）；新代际首条前重新 hydrate 重基线。
- **response patch**：由 §6.2.1 handler 在事务 durable 确认后以**确认值**
  （= 读回值）下发；迟到的事务 response 无显示效应（显示只经权威读路径，
  事务已终结的 response 不再参与）。
- **冷 hydrate（S3，新增实现——不是既有 tailer 路径）**：现有
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
  - **与 live 衔接**：hydrate 完成前触发的验证读先到 → 以验证读为准，hydrate
    结果退化为初始值校验；重订阅 → 重新 hydrate 重基线。
- **P5 角色改为优化（T2 裁定的实现选择）**：本机制**不依赖**跨源序号/身份
  ——结构安全来自「读文件现值 + 串行 + 代际」。P5 取得的序号/身份元数据用于：
  验证读同值去重节流、把通知关联到事务。P5 无果不阻断显示能力；阻断条件是
  P7 权威读可行性与 P4(b)/P7(e) 外部 resident 归属。
- **残差（明示，非乱序翻回）**：验证读小窗口结束时声称值仍未落盘、随后才
  落盘 → 显示停留在文件旧值直至下一个重基线点（下次通知触发 / 冷 hydrate /
  事务）。窗口上限由 P7 校准；P7 证明写盘延迟无界时按 §6.2.2 整体阻断。
  终值方向总是文件真值——这是有界收敛延迟，不是 T2 禁止的乱序翻回。

#### 6.2.4 模式键路由表（D3 已裁决）

| mode 键 | 路由 | 行为 |
| --- | --- | --- |
| `plan` | SessionModeSwitcher | set_mode plan + durable 确认；对外只声明进入计划模式 |
| `default` | SessionModeSwitcher | set_mode default + durable 确认；只声明退出计划模式，不改变或代证 YOLO/其他权限状态 |
| `acceptEdits` | 不广告，拒绝旧客户端调用 | 返回不支持；无本地状态变更、无成功广播 |
| `auto` | 不广告，拒绝旧客户端调用 | 同上 |
| `dontAsk` | 不广告，拒绝旧客户端调用 | 同上 |
| `bypassPermissions` | 不广告，拒绝旧客户端调用 | 同上 |

原六键移除四键，仅保留 plan/default；不走 Grok legacy 空转路径。两键文案和当前选中值必须来自同一会话模式语义，不能继续用 agent 级 `GetMode()` 回答当前会话状态（`handlers.go:4505` 的 `list_permission_modes` 读路径纳入 Phase 2 改动：当前值改 per-session、取自 `confirmedMode`）；iOS 菜单读取路径随之联动。未知时不选中任何键。其他 backend 保持既有行为。

参数化测试锁六键 × 两类 agent 的分派、错误与广播；再测 A/B 会话切换、外部 CMU 后重新打开菜单及未知状态不默认选中。切换能力未通过技术门时，两键不得广告为可执行。

### 6.3 codec 升级（Mac，Phase 1/2 各一半）

| update 类型 | 现状 | 改为 |
| --- | --- | --- |
| `available_commands_update` | known-drop | 解码 → **整表替换** per-session 目录缓存（§6.1 规则）；不产 timeline 事件 |
| `current_mode_update` | known-drop | 解码 SessionModeId → **触发 §6.2.3 有界验证读**（不直接写显示状态）；确认值经既有会话状态通道下发 iOS；不产 timeline 事件 |
| `config_option_update` | known-drop | 维持 |
| 未知类型 | fail-open Debug | 维持 fail-open（§4.4） |

外部 turn 可观测性：CMU 经 leader gateway 转发 → leader_subscriber 增
`current_mode_update` 分支汇入同一触发处理；**注意** leader 路现状丢弃 isReplay
通知（`leader_subscriber.go:573`）——重放条目本就不进 live 流，冷恢复由 §6.2.3
hydrate 承担（新代码），二者分工：live 只走 gateway/stdout 新事件（且一律降级
为验证读触发），冷值只走 hydrate。三路汇 per-session 串行处理 + 订阅代际丢弃
（§6.2.3）。

### 6.4 协议同步

零新 RPC、零新 capability。`docs/protocol/` canonical pack 更新：
`session_commands` 实现者清单加 grokbuild；grok 模式状态快照/patch 字段（若
新增）按 optional-field 规则 additive。hello_ack capabilities 由类型断言自动
出现。core/handler 的 dsh 专属注释改分 backend 契约（Phase 1 交付）。

## 7. iOS 设计与四拍走查

**改动清单**（对应 §5.2 七项）：①门控放宽（唯一代码块级改动）；②白名单
per-backend（grok 准入由 Mac 侧 D1 交集承担）；③回退三条仅 dsh、grok 三态真实
呈现；④chip 数据源扩 grok（含冷 hydrate 中性态与事务 pending 态）+ 点击走模式
通道；⑤RPC 失败沿用弹窗（正文错误走 transcript 不弹窗）；⑥routing 判决保留
改标移动端规则；⑦权限菜单随 D3 缩键（catalog 驱动，无 iOS 代码改动；当前值
per-session 读路径是 Mac 侧改动，§5.1）。

### 7.1a 反馈覆盖矩阵（S4——按反馈类型分组，替代逐命令四拍）

| 语义组（代表命令） | 请求 | 官方输出/副作用（源码依据 @72a61251） | iPhone 呈现 | 验证样本 |
| --- | --- | --- | --- | --- |
| **正文反馈组**（hooks-add/trust/list、feedback 类） | `/name args` prompt 文本 | 成功/失败文本经 `AgentMessageChunk`（HOST_TURN_META_KEY）进 scrollback，随后正常 end_turn（`slash_exec.rs:90-180`、`acp_session.rs:1365-1405`）——**业务失败不改 turn 终态** | 文本经官方 transcript/polling 自然上屏；无需新增组件；**不弹窗**（正文错误≠RPC 失败） | P6：`/hooks-add <非法路径>`（失败文本）+ 一条成功样例 |
| **无输出组**（dream、flush 的 skip 路径、context 的 shell ACP 分支） | 同上 | `ok_end_turn(0, None)`，无正文、无副产物事件（`slash_exec.rs:60-90`：Dream「Intentionally no user-visible output」；ContextInfo 无详情） | 仅命令行 echo（官方 user message）；**诚实接受成功无副产物反馈**，不造 loading/成功 toast | P6：`/dream`（或 /flush skip 路径） |
| **本地呈现面组**（context 的 pager 面） | pager 内 `/context` | `Action::ShowContextInfo` 本地 UI（`context.rs:21`）——**shell ACP 执行拿不到详情** | **D1 已裁决排除**：第一期不纳入面板（列出即误导——执行后无详情）；接入 iOS 本地 surface 另案 | 无需样本（不纳入）；若未来改判需先取证该 surface |
| **状态变更组**（always-approve on/off、compact） | 同上 | 权限状态/上下文窗口变化；可能有简短正文；compact 压缩可调模型 | 正文（若有）上屏 + 既有 usage/permission 投影承接状态变化；命令行 echo | P6：`/always-approve on`（+测后复位）、`/compact`；always-approve 进 D1 准入的前提 = 实际权限状态呈现证据成立（r3） |
| **目录透传组**（skills、workflows、goal 等其余） | 同上 | 各自官方语义（skill 信封 / workflow 启动 / 参数命令）；输出形态以各自样本为准 | 按样本归类进上述组或标注「待样本」；未取证的命令类型不得承诺呈现形态 | P2/P6 样本 + 实施期增量取证 |

D1 裁决建立在本表之上：按反馈证据准入——排除本地呈现面组；矩阵中「待样本」
条目在通过证据门前不进 D1 终表，过门后纳入无需再问 owner。

### 7.1 四拍走查：命令面板（统一规则 + 矩阵分组呈现）

1. **打开输入的地方**：Grok Build 会话（有 sessionId + `session_commands`
   capability）→ 输入框 `＋` → 「命令」节 → 官方可执行目录 ∩ D1 准入集合
   （name + description；加载中/失败/空目录三态真实呈现，不回退 dsh 三条；
   每次打开由 Mac 权威拉取，§6.1）。
2. **输入**（D4 移动端规则）：hint 非空 → 认领输入框 `/name ` + 幽灵提示，空参
   回车即执行；无 input 命令（若存在）点选即执行；自由文本误触 `/` 未命中目录
   → 普通发送（官方 ④ 预期）。
3. **发出去**：`execute_session_command` → Mac line 校验 → 白名单（缺失先权威
   拉取，fail closed）→ per-turn `session/prompt` → 终态四分法 settle。
4. **把过程展示出来**：按 §7.1a 矩阵分组呈现——正文组文本上屏；无输出组仅
   echo；状态变更组正文 + 既有投影；执行中按命令名置灰「执行中…」；RPC 层失败
   按现有弹窗（正文业务失败不弹窗）。

### 7.2 四拍走查：Plan 切换

1. **打开**：Grok 会话输入框 Plan chip（default 灰、plan 橙、**未知中性**——
   冷 hydrate 失败时诚实呈现，不猜）。
2. **输入**：无参数（plan↔default 两态点按）。
3. **发出去**：`set_permission_mode {mode:"plan"|"default"}` → handler
   SessionModeSwitcher 分支 → §6.2.0 路由（活跃 turn 等待/超时报错；待确认
   期间重复点按报 busy）→ mode-only 子进程 `session/set_mode` → **response +
   durable 读回双确认**（§6.2.2）；待确认期间 chip 显示「切换中」pending，
   **不显示请求值**；任一失败 → RPC error，chip 不翻转，不自动重试。
4. **过程展示**：chip 状态只来自权威读确认（事务读回 / 通知触发验证读 / 冷
   hydrate，§6.2.3）——**CMU 本身不翻转 chip**（T1）；外部（Mac TUI/审批流）
   切换经其 CMU 触发的验证读同步；**不承诺 TUI 即时可见**（§6.2.0 case c）；
   plan 态下模型产出计划 → 已交付 `plan_review` 审批卡（§25）→ 批准后模式回
   default（官方 CMU → 验证读 → chip 跟随）。不在本地假造状态（dsh §6.3 红线
   沿用）。

## 8. 分期落地

| Phase | 内容 | 验收 |
| --- | --- | --- |
| 0 证据矩阵 | §0.1 P1-P8 样本归档（脱敏）；P4/P6 真实 turn + P4(b)/P7(e) 外部 resident 场景成本与副作用先报 owner | 每项真实样本 + 漂移记录；**P7 权威读不可建立 → Phase 2 切换能力阻断 + 显示确认降级 unknown 中性**；样本不足的依赖项保持阻断 |
| 1 Mac 目录+执行 | codec ACU 捕获、`SessionCommandCatalog` 实现（List **每次打开权威拉取** + D1 准入交集 + 缓存职责收窄 §6.1、execute 校验+白名单、settle 四分法）、capability 出现、dsh 注释改分 backend 契约 | `go test ./agent/grokbuild ./go-bridge` 定向，须含：**每次打开权威拉取**（T3：首开 A → 外部目录变 B → 无 turn/无重启/无 cwd 变化 → 再开必得 B 或真实错误；拉取失败不回退旧表）、**D1 准入交集**（context 被排除）、目录**空/失败/缺失**三态、`/` 前缀+单行校验、白名单 fail-closed、**cancelled 不算成功**、未知 stopReason 保留原值、compact claim 判决、缓存身份规则（重启全清/空表整表替换/跨 cwd 不复用）；capability 断言 dsh-web 与 grokbuild 有、三家无；fixture 用 Phase 0 样本 |
| 2 模式通道 | `SessionModeSwitcher` + handler 优先分支 + 六键路由表（§6.2.4，D3 已裁决）、per-session 锁覆盖 Send/Execute/SetSessionMode（**锁持有期至 turn 实际终态/取消清理**）、mode-only set_mode + durable 读回事务（§6.2.2）、CMU 三路消费（降级为验证读触发 §6.2.3）、**冷 hydrate 新增**、`list_permission_modes` per-session 读路径、`plan` 键迁移 | 定向单测须含：**六键 × 两类 agent 参数化分派**（S5）、**跨会话隔离**、**幂等无 CMU 切换仍 settle**、**durable 读回超时 → error 不广播成功**、**T1 四顺序**（CMU 先到+读回超时 / CMU 先到+成功读回 / 无 CMU 幂等 / 待确认期间外部相反切换——均不得显示未确认值）、**T2 机制断言**（plan→default 通知逆序 → 显示=文件现值；旧代际通知订阅层丢弃不触发；迟到事务 response 无显示效应；hydrate 与验证读交错以验证读为准）——测试按机制真实处理路径断言，**不得注入恰好有序的数据冒充乱序安全**、**外部切换后发送不回写**、**list_permission_modes 当前值 per-session 且未知不选中**、重启后静止会话冷恢复；真机 §9 #5-#7；plan 审批回归 |
| 3 iOS 面板 | §7 七项清单 | 定向单测：门控、白名单 per-backend、回退仅 dsh、chip action 模式通道 + **未知中性态 + 事务 pending 态**、routing 判决注释；三态验收（List 未返回/报错/空目录）；**权限菜单缩为两键**（D3 已裁决）呈现确认；真机 §9 |
| 4 收尾 | protocol pack、双仓 CHANGELOG、Release 覆盖安装（killall 两进程）、owner 真机 | §9 全矩阵 |

按仓库纪律：Phase 1/2 不装真机不跑全量；任何定向 build/test 超 5 分钟异常止损。

## 9. Owner 真机矩阵（产品语言）

环境：Mac 安装版 grok 1.0.13；iPhone 连已覆盖安装的 CordCode Link；Grok Build
会话（含至少一个带 project skills 的目录）。

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | 打开 Grok 会话 → `＋` | 「命令」节列官方可执行目录 ∩ D1 准入集合（builtins+skills+workflows；**不含 /context**，D1 已裁决排除）；与 Mac TUI `/` 补全清单不必逐一等同（TUI 另含 pager-local 命令）；List 未返回/报错/空目录时三态真实呈现，**不出现 dsh 的 /plan**；其他 backend 不回归 |
| 2 | 点 compact（hint 非空） | 认领输入框 + hint 幽灵提示；空参回车即执行；命令行 echo 上屏；上下文用量随后更新 |
| 3 | 点 hooks 类命令（如 `/hooks-add <非法路径>`） | 失败**文本经对话流上屏**（不弹窗——正文错误不是 RPC 失败）；turn 正常结束 |
| 4 | 输入框手打 `/compact` 普通发送 / 手打 `/foo` / 手打 `/plan` | 分别：确定性执行 / 模型普通回复（官方 ④）/ 模型普通回复且**不进计划模式**（官方 fail closed——chip 必须走模式通道的原因） |
| 5 | 点 Plan chip 进入计划模式 | chip 变橙（= response + durable 读回双确认后的真实状态；**CMU 先到不提前翻转**）；确认前显示「切换中」不显示橙色；**重启 CordCode Link 后 chip 仍橙**（durable 真实 + 冷 hydrate 生效）；**若 Mac TUI 正开着同会话：不承诺 TUI 即时可见**（两个独立 actor，TUI 重启后按文件恢复——P4(b) 取证前的诚实边界） |
| 6 | 计划模式下发消息 | 模型按计划模式行为；消息不携带模式信号（官方从持久化继承）；外部（Mac TUI）刚切回 default 后手机立即发消息：仍是 default（无陈旧回写） |
| 7 | 模型产出计划 → 审批卡批准 | 既有两键卡可批准；批准后 chip 回灰（官方 CMU → 验证读 → chip 跟随） |
| 8 | 在 Mac TUI 切 plan / 跑 `/compact` | iPhone 无操作时 chip/命令行随后同步（leader 广播触发验证读 + polling 兜底） |

真机点击须 owner；agent 只做日志/Management 核验。#4 是官方语义锚点；#5 前半
是 durable + 冷 hydrate 锚点、后半是 §6.2.0 case c 诚实边界；#6 后半是单写者
锚点；#8 是三路可观测锚点。

## 10. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 1.0.13 活体与 1.0.16 源码漂移 | §0.1 门：活体优先，漂移记录；样本不足保持阻断 |
| **权威读机制不可建立**（P7：写盘延迟无界/优雅关闭不 flush/读回形状不可靠） | **Phase 2 切换能力整体阻断**（S1）+ **显示确认降级 unknown 中性，原始通知仅进诊断日志**（T1/D2）；重新设计需官方关闭路径证据 |
| **外部 resident 并发写文件序未证明**（P4(b)/P7(e)） | case (c) 标「行为未证明」：不承诺 TUI 即时可见、验收不含该场景；归属未知 = 按 D2 禁止发起切换；取证后再定 |
| **通知乱序/迟到**（T2） | 结构性消除：通知只触发权威读（读文件现值）+ per-session 串行处理 + 订阅代际丢弃（§6.2.3）；验收按机制真实处理路径断言；残留 = 写盘竞争小窗口的**有界收敛延迟**（P7 校准上限），非乱序翻回 |
| **面板每次打开权威拉取成本**（T3） | catalog 子进程无模型调用、与既有 session/list 同量级；打开面板低频；拉取失败真实报错不回退旧表 |
| 目录快照与执行非原子 | 官方 resolve 落 ④；§6.1 明示边界 + 窗口以「每次打开/每 turn」为界；不做执行时二次拉取 |
| mode-only 子进程成本 | 与一次空 turn 同量级；切换低频；durable 读回在同进程生命周期内完成不额外 spawn |
| `permission_mode` 键集变化（D3 已裁决） | iOS grok 权限菜单缩为两键（可见变化已裁决）；参数化测试锁分派；旧客户端发已删键返回明确不支持、不改状态、不广播成功；菜单当前值 per-session（§6.2.4） |
| iOS 门控放宽后其他 backend 误显示 | capability 断言三家无；白名单/回退 per-backend |
| 命令行 user message 与 transcript polling 投影冲突 | P6 取证行形状（含 HOST_TURN_META_KEY meta 是否进历史）；映射层处理，不改投影身份公式 |

回滚：grokbuild 去掉 `SessionCommandCatalog`/`SessionModeSwitcher` 实现 →
capability 消失 / `handleSetPermissionMode` 落回 legacy 分支（恢复 D3 裁决前的
六键空转路径）/ iOS 门控与菜单自动退回（dsh 期形态）；codec ACU/CMU 消费与
冷 hydrate 即使回滚面板也保留（状态捕获无副作用）。

## 11. 与后续案的边界 + 开放项

边界：本项关闭 think.md「Grok iOS Plan 只写 agent 内存」与调研 §7 的 Grok 命令
面板两个口子。不关闭：skills 深面、workflow 进度投影、`ask` 模式、permission
其余键接真（D3 已裁决移除四键，接真另案）、MCP elicit、interjection
（Phase B）、prompt-only `loop` 特化、`/context` 本地呈现面（另案）、错误反馈
inline 化（跨 backend 另案）。

开放项（Phase 0 取证定夺，不猜；**取证完成前对应能力保持阻断**）：

1. P3 catalog 单例子进程调 `x.ai/commands/list` 可行性（cwd 传递）——现为
   **List 展示主通道**（每次打开实拉，§6.1）；不可行时替代 = 专用子进程
   handshake + session/load 捕获 ACU（同样实拉）；仍不可行 → List 展示阻断。
2. P4(b) 已有外部 resident 时切换 + 随后发送的写序/可见性——决定 §6.2.0
   case (c) 的效果承诺上限。
3. P5 三路 CMU 的可比较序号/身份 + replay 标记形状——§6.2.3 机制**不依赖**
   （结构安全 = 读文件现值 + 串行 + 代际）；取证结果用于验证读去重节流与
   事务关联（优化），replay 标记用于 hydrate。
4. P6 反馈矩阵各组样本（含无输出组、正文业务失败组）——D1 准入集合终表前提
   （状态变更组须实际权限状态呈现证据）。
5. P7 权威读可行性（有界等待/写盘延迟上界/优雅关闭 flush/失败判定）——
   **不可建立则切换阻断 + 显示确认降级 unknown 中性**。
6. P8 冷 hydrate 权威来源（summary.json mode / plan_mode.json / 尾部回扫）。
7. D1–D4 已由用户授权评审者裁决，见 §3.2；剩余为技术证据门，不再重复请求
   产品确认。
