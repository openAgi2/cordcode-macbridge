# Grok Build 命令面板方案第二轮评审

日期：2026-09-07。对象：[方案 v1.1](../2026-09-07-grok-build-slash-command-panel-implementation.md)，本 worktree `plan/approval-layer`，提交 `9efaa5a`。对照：[第一轮报告](2026-09-07-grok-build-slash-command-panel-implementation-review.md)。

**结论：修订方向正确，但当前仍不通过实施评审。** 第一轮的事实错误大部分已修正，不能再沿用第一轮的问题描述；本轮仍有 **4 项 P1、2 项 P2**，集中于模式持久化、resident 所有权、冷恢复、命令反馈覆盖及权限兼容。可以继续修订和准备 Phase 0；不应把“8 项均响应”写成“8 项均闭环”。

本轮只读核验文档与源码并新增本报告；未运行真实 turn、付费 API、UI/snapshot tests 或真机自动化，未改产品代码。Grok 源码仍为 `72a61251`，iOS 为 `c3b1d5b0`；安装版 1.0.13 未在本轮探测。下述上游代码结论仅针对该 checkout，目标版结论仍受 Phase 0 门约束。**缺少 Phase 0 样本本身不算新的评审缺陷；本轮指出的是当前探针或拟定方案不能保证其承诺。**

## 1. 第一轮逐项复核

| 第一轮 | 本轮状态 | 说明 |
| --- | --- | --- |
| R1 会话模式接口 | 原接口缺口已修正，整体未闭环 | 新接口具备 ctx/sessionID/error；但 mode-only 与活跃 actor 的关系未定义，优先分支又与 D3 冲突，见 S2/S5 |
| R2 iOS dsh 泄漏 | 方案层主要关闭 | 六项清单覆盖白名单、回退和 chip action；不再要求仅改门控。接受沿用现有错误弹窗的范围选择 |
| R3 input 同构误判 | 方案层关闭，待 D4 裁决 | 已承认为移动端规则，compact 改 claim；不再要求“官方证明”产品选择 |
| R4 CMU ack/持久化 | ack 子项关闭，持久化仍阻断 | 改用 RPC response 正确；随后又用 CMU/短延迟作屏障并允许软失败，见 S1 |
| R5 校验/目录降级 | 主要修正，fresh 契约未闭环 | 缺缓存先拉、失败报错、删除 builtins 冒充全目录正确；fetchedAt 没有失效规则，见 S6 |
| R6 陈旧 mode 回写 | 删除 prompt 字段关闭该条回写路径；整体状态一致性未闭环 | 不能由“一个 RPC 名称”推出“一个写者/一个 actor”，冷恢复和排序也未完成，见 S2/S3 |
| R7 /plan/命令范围 | 事实修正关闭，反馈覆盖仍阻断 | 不必逐条重复四拍，但不能用两种输入方式代表所有输出，见 S4 |
| R8 取证成本/终态 | 成本与取消子项已修正，反馈语义仍需细化 | 真实 turn 已标明；正常 end_turn 不等于业务操作成功，见 S4。持久化探针标准还需修订，见 S1 |

## 2. 仍需修订的发现

### S1 · P1：CMU/短延迟仍不是持久化屏障，软失败会留下成功假象

**方案位置：§6.2.2，尤其“退出前等待 CMU 或短延迟”“response OK 但写盘失败：软失败”；§7.2.4、§10。延续 R4。**

当前上游有两条独立队列：`persist_plan_mode_state()` 将快照发给 persistence；`enqueue_current_mode_update()` 将通知发给 event pipeline。写文件发生在异步 `PersistenceMsg::PlanModeState` 分支，失败仅记录 warning。RPC response/CMU 均不证明该写入完成，更没有证明写入成功。

源码证据：`xai-grok-shell/src/session/acp_session_impl/session_mode.rs` 的 `persist_plan_mode_state`、`acp_session_impl/updates.rs:348`、`session/persistence.rs:1945`（均位于 `/Users/jacklee/Projects/grok-build/crates/codegen/`）。

明确失败序列：set plan 已更新 actor → response/CMU 都成功，iPhone 收到 plan patch → 写盘失败或进程提前终止 → 下一子进程恢复 default。由于新的 actor 本来就是 default，未必发生模式转换，**没有保证会再产生 default CMU 来纠正 chip**。所以“软失败后观测自然收敛”不是已建立的机制。

P7 的若干次“立即退出再 load 成功”也只能证明这些运行成功，不能排除调度窗口。固定短延迟不是因果屏障；等待 CMU还会重新引入幂等无通知的问题。

**修改要求：**

- 删除“CMU/短延迟足以保护退出”和“持久化失败无状态污染”的处理方案。
- 明确真实成功条件：使用已核实会 flush 的官方关闭路径，或具有明确边界的权威读回验证；若目标版本无法保证，保持该切换能力阻断，不能向用户宣告已持久生效。
- 分开 RPC 已接受、持久结果已确认、结果未知三种状态。超时或验证失败保留真实错误/未知状态，不自动重试、不伪造反向状态。
- P7 增加关闭方式、持久结果与超时/写盘异常的失败判定。故障注入可在隔离测试中验证内部行为，不得把 fake 样本当安装版协议证据。

### S2 · P1：“单写者”缺少 resident 所有权和与普通 turn 的并发规则

**方案位置：§6.2.1–3、§4.3 推论、§9 #5/#6。延续 R1/R6。**

mode-only 声称复用现有 load 路径，但现有 [agent/grokbuild/session.go:99](/Users/jacklee/Projects/cordcode-macbridge-plan-approval/agent/grokbuild/session.go:99) 明确启动 `grok agent --no-leader stdio`。上游 set_mode 修改收到请求的 resident actor；prompt 未传 mode 时也查询其所在 actor 的内存，而非每次从文件同步。

证据：上游 `agent/mvp_agent/acp_agent.rs:1113–1129`、`:2229–2250`；`session/acp_session_impl/spawn.rs:625–648` 是 spawn 时恢复；`session_mode.rs:44` 是 actor 内模式变更。

因此文档尚未回答：若同一会话已有手机普通 turn、后台 workflow 或 Mac TUI resident，mode-only 子进程是否与它共享 actor？若独立 load，会被官方拒绝、得到另一个 actor，还是有官方转发？在确定前，不能宣称“改了文件即 Mac TUI 同会话可见”或“单写者消除多写者竞争”。桥接层仅对两次 SetSessionMode 加锁，也没有与 Send/Execute/审批动作共同排序。

这里不是断言安装版一定允许两个 actor 并发写同一文件，而是指出：**当前方案既未证明它共享同一个 actor，也未规定不共享时的正确行为。**

**修改要求：**定义 idle 无 resident、CordCode 有活跃 turn、外部 TUI 有 resident 三种路由。明确何时复用 actor、何时拒绝 busy、何时允许独立 load，以及切换与 Send/Execute 的顺序。P7/P4 加入“已有 resident 时切换并随后发送”的证据；不能只验证完全串行的 spawn→close→spawn。将“单写者”改为精确描述：已删除 prompt 的模式声明通道，但 actor/外部写者一致性仍需另外保证。

### S3 · P1：冷恢复并不存在于“既有 tailer 路径”，到达序也不足以保证模式不回退

**方案位置：§6.2.1/3、§6.3“冷重放走 tailer 既有路径”、P5/P8、§9 #5。延续 R6。**

现有 [updates_file_tailer.go:60](/Users/jacklee/Projects/cordcode-macbridge-plan-approval/agent/grokbuild/updates_file_tailer.go:60) 明确从当前 EOF 起监听，初始 `offset = info.Size()`，不会恢复 EOF 之前最后一次 CMU；附加扫描只处理未完成 turn 的 user prompt。[leader_subscriber.go:573](/Users/jacklee/Projects/cordcode-macbridge-plan-approval/agent/grokbuild/leader_subscriber.go:573) 在 codec 之前直接丢弃 isReplay 通知。仅新增 codec CMU case 不会补上这两处状态恢复。

所以“GetSessionMode 缓存 miss 返回空，等待观测”在会话空闲且没有新 CMU 时可以一直未知。P8 可以决定从哪个官方来源读取，但**不管 P8 选哪种来源，冷 hydrate 都是明确要实现的新路径，不能写成现状已有**。

last-wins 按到达序还未处理：旧 stdout/leader 的 live CMU 延迟到达；先收到外部 default，再收到之前 set plan 的 response 请求值 patch；文件截断或重新订阅后如何建立新基线。replay 标记只区分回放，不证明三个 live 来源的先后关系。

**修改要求：**增加冷模式 hydrate 的调用入口、权威来源、空/错误语义及与 live 订阅的衔接边界。把 response patch 也纳入时序规则。依据 P5 的真实元数据选择可证明的顺序/身份；若没有可跨来源比较的序号，规定单一活动来源及切换时的重新基线流程，不能用任意到达序宣布全局顺序。验收必须包括 bridge/iPhone 重启后静止会话无新事件仍恢复，以及旧通知/旧 response 晚到不翻回。

### S4 · P1：两类输入走查不能覆盖全目录的反馈，/context 已是确定反例

**方案位置：文首 R7 部分采纳、D1、§7.1.4、P6。延续 R7/R8。**

不要求重复 16 份相同四拍；可以按语义分组。但“没有 per-command 分支，所以副产物都由现有通用投影覆盖”的推理不成立：缺少分支也可能正是功能缺口。

在本方案所引版本中：

| 官方分支 | 实际行为 | 对现稿的影响 |
| --- | --- | --- |
| shell `BuiltinAction::ContextInfo` | `slash_exec.rs:89` 附近直接 `ok_end_turn(0, None)` | 不返回详细上下文信息；正常 settle 不能生成它 |
| pager `/context` | `slash/commands/context.rs:1–26` 返回 `Action::ShowContextInfo` | 官方有独立本地呈现面；“列表全上 + prompt 执行”无法等价替代 |
| shell Dream/FlushMemory | `slash_exec.rs:60–88` 包含跳过或刻意无用户可见输出的路径 | 需要明确手机是否仅有命令 echo；不能承诺所有成功都有副产物反馈 |
| shell HooksAdd/HooksTrust 等 | `slash_exec.rs:90–177` 中失败文本可经 AgentMessageChunk 输出，随后仍正常 end_turn | 四分法只分 transport/turn 终态，不能代表业务操作成功，也不能保证进入现有 RPC 错误弹窗 |

路径前缀同 S1；文本输出通道见 `xai-grok-shell/src/session/acp_session.rs:1370–1400`。这些是源码反例，目标版仍须选相应代表取证。

**修改要求：**用紧凑的“命令或语义组 → 请求 → 官方输出/副作用 → iPhone 呈现 → 验证样本”矩阵代替重复四拍即可。对 context 这类实际需要本地 surface 的条目，明确接入该 surface 或不纳入当前交付集合；D1 不收窄是推荐项，尚不能用它覆盖功能缺失。明确 ResultKind success 仅表示 turn 正常完成，业务失败文字按官方通道原样呈现，禁止按自然语言猜错误。P6 除 compact/always-approve 外应覆盖无输出和正文承载业务失败等不同反馈类型。

**对部分采纳的裁定：**接受压缩重复走查；不接受免除输出/副产物分类验证。接受错误弹窗另案，但区分“RPC 失败弹窗”和“正常 turn 内的官方错误正文”，不得将后者错误承诺成必有弹窗。

### S5 · P2：handler 优先分支只接 plan/default，与“其余五键维持现状”直接冲突

**方案位置：D3、§6.2.1、§6.2.4、§10。由 R1 修订引入。**

新 handler 对实现 SessionModeSwitcher 的 Grok 先验证 `mode ∈ {plan, default}`，按此规则 acceptEdits/auto/dontAsk/bypassPermissions 都会在优先分支被拒绝。但 `PermissionModes()` 仍向 iOS 广告它们，D3 又宣称行为保持不变。

源码：[grokbuild.go:585](/Users/jacklee/Projects/cordcode-macbridge-plan-approval/agent/grokbuild/grokbuild.go:585)；iOS `ChatUIKitContainerView.swift:6193` 用 catalog 建菜单、`:6236` 仍调用同一个 setPermissionMode。即使旧模式本来只是本地空转，“原来返回成功，新增优先分支后报错”也不是既有行为不变。

default 还同时是权限菜单键和 SessionMode 的退出 plan 值：新分支会使原权限菜单 default 获得真实的退出计划效果，需明确是否为产品意图。

**修改要求：**给每个现有键定义路由/展示语义，明确 SessionMode 与 PermissionMode 如何在同一 RPC 下区分。由 D3 决定缩减广告或保留哪些既有行为；不要新增未授权的降级逻辑来绕过冲突。加参数化测试锁五键与 plan 的分派、错误和广播行为。

### S6 · P2：fresh 目录仅有 fetchedAt，没有可执行的失效定义

**方案位置：§6.1 通道 1、白名单与非原子边界。延续 R5。**

方案给缓存写入 `(backend, sessionID, cwd, fetchedAt)`，但没有何时过期、进程结束是否失效、外部 reload/plugin/skills 变化如何刷新，也没有 iOS“每会话一次预取”与 Mac ACU 整表替换的衔接。因而执行中所谓 fresh 可以是任意旧快照。

非原子竞争窗口可以明示接受；**接受“刚拉完后文件又变化”的不可消除窗口，不等于接受缓存无限期不失效**。同时 §2“冷拉一次 + ACU 增量已覆盖官方语义”尚无 iOS 更新触发的设计支撑。

**修改要求：**定义缓存身份键与版本元数据（fetchedAt 不必作为身份键）、有效边界、失效触发和官方空表替换语义；明确 Mac 缓存更新后 iOS 如何更新，或者明确产品只在下次打开时重新拉取。无需强制每次 execute 二次拉取，也无需自造目录合并；测试覆盖失效后重新取证、空目录替换旧表、跨 cwd 不复用。

## 3. 第二轮关闭条件

1. S1/S2：将 RPC 完成、durable 完成、resident 路由分别写清；移除“CMU/短延迟屏障”“文件状态即等于所有活跃 actor”的推论。缺证能力继续阻断。
2. S3：冷 hydrate 必须明确新增实现点及与 live 的边界，不能继续称 tailer 已提供回放恢复。
3. S4：补按反馈类型分组的覆盖表即可，不要求冗长重复四拍；D1 全目录的产品裁决应建立在这张表之上。
4. S5/S6：写出模式键路由表与缓存有效期/失效规则，再进入编码。

D4 的输入规则已可以作为产品选择裁决；D1“是否全上”仍依赖反馈覆盖表。D2/D3 在正文仍标待裁决，不能因本轮摘要只提 D1/D4 就视为已定。该报告不代替 owner 作产品决策，也不启动 Phase 0 的真实 turn。
