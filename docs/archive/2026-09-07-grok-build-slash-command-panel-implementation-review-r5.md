# Grok Build 命令面板方案第五轮评审

日期：2026-09-07。对象：v1.4，提交 `7ef00c3ebe1f573fd6f6eea6247ff6ee36cfe0e2`。

## 结论

**v1.4 不能原样交给开发者实现。** 本轮按上游恢复语义、bridge 生命周期、wire/iOS 全链、异步状态、取证与回滚完整追踪，归并为 8 项（7 P1、1 P2）。已经直接重写[方案 v1.5](../2026-09-07-grok-build-slash-command-panel-implementation.md)，给出字段契约、路由、队列、依赖门与关闭条件，不要求开发者再从多轮响应表自行拼方案。

**可开始按 v1.5 §8 开发队列执行，不等于 Phase 2 可直接启用。** 新发现的上游 Pending 恢复行为否定了 1.0.16 上“短命 set_mode child 写好文件即可生效”的假设；目标 1.0.13 仍须取证。如果相同，模式切换方案必须停止，不能用文档修辞或本地 fallback 过门。独立目录/执行工作按各自证据继续。

D1–D4 沿用用户先前授权评审者的裁决，不再请求产品确认。本轮没有运行 binary、真实 turn、付费 API、UI/snapshot test 或 simulator automation；没有修改产品代码、安装、提交或 push。

## 来源清单

| 仓库绝对路径 | 分支 | 完整提交 | 编辑前未提交状态 | 预期与配对 |
| --- | --- | --- | --- | --- |
| `/Users/jacklee/Projects/cordcode-macbridge-plan-approval` | `plan/approval-layer` | `7ef00c3ebe1f573fd6f6eea6247ff6ee36cfe0e2` | 干净 | 用户指定评审工作树；配对按 v1.4 明定 iOS main |
| `/Users/jacklee/Projects/cordcode-ios` | `main` | `c3b1d5b0265d427ba30e0e72a371bba7063f63e5` | 干净 | v1.4 指定只读评审源；不是其他 iOS 工作树的实现/构建证明 |
| `/Users/jacklee/Projects/grok-build` | `main`（首次编辑前复核） | `72a61251fcffb464bcc687aeb5a998e5a98ec0c9` | 干净 | 上游 1.0.16；早前 detached 记录已按元数据更新，源码提交未变 |

Mac 产品源码与 `de17e6f782ecc1d72a29f7d4e81e6aed2405251d` 零差异。目标运行版为既有调研记录的 1.0.13（`5e9a58528b76`），本轮未重新执行核验，不把 1.0.16 源码当作该二进制的运行证据。预期产品特性：Grok 命令目录/执行、官方 Plan 模式和既有 plan_review；其他 backend 维持现有合同。

新枚举发现 `/Users/jacklee/Projects/cordcode-ios-plan-approval`（`plan/approval-layer-ios`，`a579cccadfb4b875f96432d7db370b1ae82ba781`）。本轮没有混入其源码；实施者必须刷新实际配对并复核差异。未授权修改 main 或集成分支。修改后仅方案与本报告发生文档变更。

源码路径说明：Mac 相对仓根；iOS 相对 `OpenCodeiOS/OpenCodeiOS/`；上游简写相对 `crates/codegen/xai-grok-shell/src/`，`spawn.rs` 指 `session/acp_session_impl/spawn.rs`。

## V1 · P1：durable 字节不等于下个 actor 的 Plan

上游 `crates/codegen/xai-grok-shell/src/session/acp_session_impl/session_mode.rs:44` 的 idle set plan 进入 Pending 并请求持久化；`session/plan_mode.rs:96` 的 `from_snapshot` 明确把 Pending 与 ExitPending 恢复为 Inactive，`:145` 的 `session_prompt_mode` 只有 Active 返回 Plan；`spawn.rs:625` 使用该恢复链。

v1.4 §4.3、§6.2.0/2 仍推导“mode-only 写文件并读回 → 下次 prompt 继承 plan”。即使文件已经完全正确落盘，关闭 child 后也可能恢复 default。这是恢复语义问题，增加 flush 或延迟不能修复。

**回填**：v1.5 §2/P4/P7/P8、§5.2 把确认对象改为恢复后的 effective mode。明确 1.0.13 如同样 Pending→Inactive 则阻断短命路径；不得自动引入常驻兼容分支、假 prompt、直接写文件或 `_meta.mode`。Phase 2 功能放行仍待实证，未称关闭运行阻断。

## V2 · P1：Execute 缺 turn 基础设施，终态映射改错层会假成功

Mac `go-bridge/handlers_session_commands.go:52` 只是接口调用，缺 `handlers.go:2572` 起普通 Send 所做的 registry、admission、订阅与 relay。`agent/grokbuild/session.go:511` 写 prompt 即返回；`:1018` prompt response 只区分 JSON-RPC error，丢 `result.stopReason`；`:1073` cancel notification 也发普通 Done result。codec 对 session/update 的三分不是 prompt response 的三分。

若 Execute 私下 spawn 后只等 Done，流式反馈、权限响应路由可能断开；与 relay 两处竞争消费 Events 会丢事件；若继续复用当前 response 逻辑，cancelled/未知终态可误成功。握手 `session.go:214` drain 还会丢只靠 Events 保存的首次 ACU。

**回填**：v1.5 §4.1/2 把握手 side-state、共用 turn dispatcher、单一 Events 消费者、独立 terminal future、原始 stopReason 解码、request ID/replay/重复终态/EOF 与取消清理全部写成必做项。§9 给定向测试，不把这些留为“复用管道”的隐含工作。

## V3 · P1：锁、actor 寿命与请求预算未闭合

`go-bridge/handlers_relay.go:2795/:2945` 的 relay 完成不保证 child 退出；普通 Send 能复用 registry 里的 alive session。因此 v1.4 “每 turn spawn 后读文件”不是已成立事实。只锁 Send 函数或到 Done 为止，可能让仍活着的旧 actor 与 mode-only 写入竞争；反过来把权威读 I/O 放在事务同一 mutex 又会死锁或阻塞 CMU。

List handler 15s、Execute 300s 还必须传到底层，不能落入 catalog 60s 或 iOS 同时 300s 的竞争超时。模式请求未给全链时间分配也会让手机已超时而后台仍写。

**回填**：v1.5 §4.2/§5.2 定义 operation lease 在 load/spawn 前取得，覆盖真实终态与清理；状态短 mutex 锁外读取；明确 idle actor 的关闭/reap 与外部 actor 禁止关闭；失败清理保持 unavailable。List 15s、mode 25s/客户端30s、Execute 290s/客户端300s，全路径受 deadline，cwd/model 一次捕获。

## V4 · P1：权限 per-session 与 iOS 成功响应缺口

iOS `Services/Backend/BackendClient.swift:132` 与 `Services/Bridge/CCCodeBridgeBackendClient.swift:961` 的 fetchPermissionModes 没有 sessionId，单改 Mac `handlers.go:4505` 无法知道读哪个会话。`:978` 的 setPermissionMode 对缺失 appliesTo 使用 newSessions，`Services/Backend/BackendModels.swift:449` 有后续新会话语义。v1.4 “权限菜单无 iOS 代码改动”错误。

**回填**：v1.5 §5.1/§6 明确协议、所有 conformer、transport、模型和调用者 sessionId 全链；Grok 成功必须 current_session，缺字段不能乐观启动新会话；读取/写入响应均要 session fencing，不从迟到响应写显示状态。两键与四键拒绝路由不再依赖 legacy fallback。

## V5 · P1：三态没有投影载体，default chip 实际不可见

iOS `Models/SessionProjection.swift:32` 现有 planMode 使用 active/pending；其 pending 表达 dsh 切换方向，不能承担 Grok unknown/验证中。`App/ChatInputAccessoryView.swift:1126/:1641` 还在 default 隐藏 chip。单写“扩 chip 数据源”不能得到已裁决的灰色可点击入口。

**回填**：v1.5 §5.1/§6 给出 additive `sessionMode {status,mode?,canSet,reason?}`，列出 core、reducer、snapshot/window/patch、Swift mapping/store 全链和 revision/epoch 约束。Grok default 也可见，pending/unknown 禁用，不借 dsh 的 active 反转。模式接口 typed 返回；readiness 不由类型断言单独决定。

## V6 · P1：认领失败退普通消息，每次打开也尚无调用入口

iOS `App/ChatUIKitContainerView.swift:1680` 起 slash 裁决路径在 List 错误后可落 normalSend；异步完成没有完整 A→B 会话隔离。`:6583` 的预取不是每次打开；`App/ChatInputAccessoryView.swift:382/:1076` 两种菜单仍是静态构建。v1.4 只修改 Mac List 并不能保证用户每次打开触发刷新。

**回填**：v1.5 §6 明确两个实际菜单打开 hook、loading/error/retry/empty，缓存成功不得替代当次请求；claim 身份独立保存，失败保留 draft 并报错。List/Execute/mode 都捕获 backend/session/cwd/generation；丢旧结果且不误清用户新输入。只有未认领、主动手写的未知 slash 才可 normalSend。

## V7 · P1：通知被降级后仍作为目标值，监听丢事件仍无界陈旧

v1.4 §6.2.3 一面说通知只触发读，一面规定旧 plan 通知在文件已 default 时“复查耗尽 → unknown”。这仍然把不权威的通知值作为必须见到的目标，导致真实 default 被旧通知拖入永久禁用。其残差又写“监听丢事件，显示停留上次确认值至下一入口”并声称不无界；没有下一入口就仍无界。

**回填**：v1.5 §5.3 分离 dirty 与自有 requested：非自有事务期间成功权威读即可 confirmed 当前有效值，不等通知声称值；只有未结束事务等待 requested。目录监听外增加有观察者时的周期只读重验与 freshness 失败转 unknown，后台取消后重新 hydrate。generation 在锁外读取前后比较，防触发丢失；所有复查只读。旧 plan+现 default 应 confirmed(default) 写入验收。

## V8 · P2：取证副作用、目录等价性与回滚仍缺明确边界

上游 `extensions/session_admin.rs:859` 的 cwd pull 使用进程配置/availability，`:885` 的 sessionId 分支要求该进程持有 session handle；不能推断两者天然同一会话目录。P3 必须证明其项目与会话等价范围，设计期选通道，不能运行期失败时静默换来源。

`session/acp_session_impl/slash_exec.rs:60` 起 dream/flush 会进入 memory 工作；可弃会话不等于全局记忆可复位。feedback 外发也不能只用“一条真实 turn”概括。v1.4 回滚段又明确恢复 D3 裁决前的六键空转，直接违背已采纳决策。

**回填**：v1.5 §2/P3/P6、§4.1、§7、§10 明确隔离配置与记忆、原权限复位、未经授权不发 feedback；不准入未证明反馈；回滚关闭 readiness/canSet 并拒绝不支持 RPC，绝不恢复六键或 dsh 内置菜单。阶段放行按依赖拆分，避免 Plan 门失败连独立目录工作一起锁死。

## 交付与验证

v1.5 已把上述方案层改动全部写入正文，重排为单一可执行契约，保留历史 Git 与报告引用。文档自检范围：章节链接、关键状态/预算/回滚一致性、差异与空白检查；源码事实均归属以上冻结提交。没有生成协议样本，没有运行产品测试。

开发者下一步是 v1.5 §8 的 0a：刷新实际双仓配对、验证目标 binary、归档零模型样本，优先判定 Pending 恢复。已有产品裁决不再重复询问；真实 turn 与副作用操作仍按已约定范围报明并确认后执行。不存在“保证所有未来问题都已发现”的承诺，实施证据门用于阻止剩余版本不确定性变成产品假成功。
