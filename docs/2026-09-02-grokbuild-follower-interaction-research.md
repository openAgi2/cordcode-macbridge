# Grok Build follower 交互升级——调研取证报告（2026-09-02）

> 性质：**只调研、只取证、只写文档**。本文不包含任何产品代码修改；全程 D0（无 build / 无 test / 无安装）。
> 目的：为「iPhone 作为 follower 能看到并回答 Grok Build 交互请求」的后续升级冻结前置事实。当前 CordCode Link Grok Build backend 已支持 Leader 模式（Mac 侧只读订阅官方 grok leader socket），红线是 observer 只读共存——iPhone 看不到也不能回答交互请求。

---

## 1. 来源清单（P0 格式）

### 1.1 本仓（MacBridge）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader
分支=codex/grokbuild-leader-mode
提交=a5c1912f4ef9d3066e8ea8f25568eac24fd310d0
未提交状态=干净（仅未跟踪 docs/2026-09-02-same-name-ios-device-eviction-fix-design.md，属用户另案，未触碰）
任务预期分支=codex/grokbuild-leader-mode（本任务工作树）
配套仓库路径/分支/提交=见 1.2 / 1.3
预期产品特性=无代码改动；本文为调研产出
```

CordCode 侧结论全部基于上述工作树读取，行号以该提交为准。

### 1.2 上游官方源码（grok-build，只读）

```text
仓库路径=/Users/jacklee/Projects/grok-build
分支=main
提交=72a61251fcffb464bcc687aeb5a998e5a98ec0c9（调研收尾时复核）
未提交状态=干净
```

**版本漂移声明（重要）**：

- 调研开始时 checkout HEAD 为 `bc7f02eddd3d84085849dc19ed216f11c23b0571`（2026-08-28 锚点），调研期间上游被更新到 `72a61251`（bc7f02ed 之后 2 个提交，fast-forward）。收尾时已在新 HEAD 上**逐符号复核**本文引用的全部语义锚点（is_interaction_request 四方法清单、first-answer-wins 注释、PendingInteractionGuard::drop 幂等语义、RESPONSE_TIMEOUT、replay/evict 逻辑、`result_tx.closed → Cancelled`），全部仍然成立；行号按新 HEAD 修正后引用。
- 本机安装版为 **grok 1.0.13（5e9a58528b76）**，该提交**不在 checkout 内**。因此：本文 §2 源码结论标注为 checkout（72a61251）锚点；§3 的 wire 样本全部来自**安装版 1.0.13 实测**，是 wire 形状的最终真值。两者在观测点上未发现分歧。

### 1.3 实测环境（探针）

```text
leader 进程=PID 23577（/Users/jacklee/.grok/bin/grok agent leader --no-exit-on-disconnect --relay-on-demand ...）
  ——owner 既有常驻 leader，调研全程未重启、未改动其配置
leader socket=~/.grok/leader.sock（4-byte big-endian 长度前缀 + JSON envelope）
安装版=grok 1.0.13（5e9a58528b76）
本机配置=~/.grok/config.toml 未做任何修改（含 api_key 明文，永不入库；显示时已 grep -vE "api_key|token"）
  ——注意该配置 [ui] permission_mode="always-approve"，直接决定了 §3 中 permission 方向样本缺失
探针=零 API 伪造 follower 客户端（纯 socket 订阅 + 脚本化 PTY TUI 注入击键），脚本与 capture 存于
  /tmp/grok-follower-probe/（临时目录；capture.jsonl 为原始证据，本文附录已全文转录关键帧）
探针会话=~/.grok/sessions/%2Ftmp%2Fgrok-follower-probe%2Fproj/01a06290-1a64-70e1-a125-824782ed79ff
  ——用完即删，已确认删除；探针 TUI 进程已全部 kill，无残留
```

取证纪律遵守 think.md 2026-09-02「错误指纹矩阵探针」结论：以安装版为准，checkout 只作语义参考，wire 细节必须实测。

---

## 2. 上游交互语义冻结表（源码结论，逐条 file:line）

以下结论全部来自 checkout 72a61251（见 §1.2 漂移声明）。标注【实测印证】的条目在 §3 有安装版 1.0.13 wire 证据。

### 2.1 共享交互反请求（shared interaction reverse-requests）

| # | 语义 | 锚点 |
|---|---|---|
| 2.1.1 | 共 4 种方法被识别为「交互反请求」：`session/request_permission`、`x.ai/ask_user_question`、`x.ai/exit_plan_mode`、`x.ai/mcp/elicit`。官方注释原文：广播给**每一个订阅者**，让任何客户端都能渲染并应答模态框，**first-answer-wins** | `crates/codegen/xai-grok-shell/src/leader/server.rs:494-507`（`is_interaction_request`；注释引用的 `SHARED_INTERACTIVE_MODALS.md` 在 checkout 中**不存在**，官方文档缺口如实记录） |
| 2.1.2 | 交互请求**广播给所有订阅者**（不校验 driver 身份、不校验会话成员资格） | `server.rs:2246-2277`（interaction/通知 → 广播分支）【实测印证 §3.1/§3.2：obs1/obs2 均收到】 |
| 2.1.3 | 非交互反请求与 `inject_prompt` 只路由给 **driver**（该 session 的首个带 sessionId 客户端） | `server.rs:2217-2245` |
| 2.1.4 | driver 身份与交互广播**无关**：任何带 sessionId 的客户端消息即 `or_insert` 成为 driver；纯订阅者（不发言）永远不是 driver 却能收到交互广播 | `server.rs:1854-1867`【实测印证：obs1 未发过任何带 sessionId 的请求，仅作为订阅者应答成功】 |
| 2.1.5 | gateway `_` 前缀 wrapped 形态兼容：ext 方法可能以顶层 `_x.ai/foo` 到达；`method_of`/`interaction_inner_params` 同时容忍「params 内嵌 {method,params} 的全包装」与「params 直接内联的半包装」两种形态 | `server.rs:440-477`【实测印证：1.0.13 wire 为**半包装**——顶层 `_x.ai/ask_user_question` + params 直接内联，无内嵌 method】 |

### 2.2 应答回程与 first-answer-wins 仲裁

| # | 语义 | 锚点 |
|---|---|---|
| 2.2.1 | `rewrite_request_id` 只改写 **request 方向**（有 `method` 字段）的 id 为 namespaced 字符串 `"client_id\|original_id_json"`；**Response 方向（有 result/error、无 method）原样透传给 agent**，由 agent 侧 pending oneshot 按原 id 匹配 | `server.rs:318-344`（rewrite）、`server.rs:345-357`（parse_response_id，要求 id 为字符串——数字 id 的 Response 不走客户端回程路由）、`server.rs:1918/1942`【实测印证：订阅者用广播里的原始数字 id 应答即可送达 agent】 |
| 2.2.2 | **仲裁实现 = agent 侧 oneshot**：每个 pending 交互请求持有一个 `result_tx/result_rx` oneshot；第一个到达的 Response 消费掉 sender，后续 Response 无 receiver 匹配、静默丢弃 | `crates/codegen/xai-acp-lib/src/channel.rs:34-48`；`crates/codegen/xai-grok-tools/src/implementations/grok_build/ask_user_question/mod.rs`（工具侧 oneshot）【实测印证 §3.5：已消费 id 的迟到应答无错误帧、无广播、turn 不受影响】 |
| 2.2.3 | **PendingInteractionGuard RAII 幂等**：guard 构造时插入 registry 并广播 `pending_interaction`；Drop 时仅当 `map.remove(&tool_call_id).is_some()`（真正移除到 entry）才广播 `interaction_resolved`。官方注释原文："First-answer-wins: only announce resolution if this guard actually owned the live entry"——重复/迟到 Drop 静默无操作 | `crates/codegen/xai-grok-shell/src/session/pending_interaction.rs:100-117`（Drop）、构造+广播在前文；registry 为内存态、**never persisted**【实测印证：pending_interaction 带 `kind`（permission/question），interaction_resolved 只带 `tool_call_id` 不带 kind】 |
| 2.2.4 | 官方 TUI 对迟到应答的表态： dismissed 模态丢弃 response_tx 无害——agent 已 resolve，迟到 response 被 gateway 忽略 | `crates/codegen/xai-grok-pager/src/app/agent_view/interactions.rs:1094-1101` |
| 2.2.5 | PendingKind 枚举：`Permission` / `Question` / `PlanApproval` / `McpElicitation`（对应 2.1.1 四方法；实测 wire 中出现 permission/question 两种，后两种本次未触发） | `pending_interaction.rs`（PendingKind 定义） |

### 2.3 缓存与 replay-on-attach

| # | 语义 | 锚点 |
|---|---|---|
| 2.3.1 | leader 按 `(session_id, tool_call_id)` 缓存 pending 交互请求 payload（`HashMap<String, HashMap<String, Arc<str>>>`） | `server.rs:1582` |
| 2.3.2 | 新客户端 `session/load` 完成后：flush 缓存的 live 事件 + **重放缓存的 pending 交互请求**给该客户端（原文语义：attach 即补发） | `server.rs:2046-2079`（replay 调用点 `:2069`）【实测印证 §3.4/§3.6：重放帧与原请求**同 id、同 payload 全文**；原客户端全死后新 attach 仍能收到并应答】 |
| 2.3.3 | 收到 `interaction_resolved` 广播即逐出对应缓存条目（已解决的交互不再重放） | `server.rs:2199-2216`【实测印证：run4 收到的重放只有仍 pending 的 id=3，不含已解决的 id=0/1/2】 |
| 2.3.4 | 缓存为 leader 进程内存态：leader 重启 = pending 交互丢失（agent 同进程，工具侧 oneshot 一并消失，等价于超时路径） | `server.rs:1582` + `pending_interaction.rs`（never persisted） |

### 2.4 ask_user_question 请求/应答形状与超时

| # | 语义 | 锚点 |
|---|---|---|
| 2.4.1 | 请求 params（camelCase）：`{sessionId, toolCallId, questions:[{question, options:[{label, description}], multiSelect}], mode}` | `crates/codegen/xai-grok-tools/src/implementations/grok_build/ask_user_question/types.rs`（AskUserQuestionExtRequest）【实测印证 §3.1：逐字段吻合，`multiSelect:null` 显式出现，`mode:"default"`】 |
| 2.4.2 | **envelope 不携带任何超时字段**——超时是 agent 工具侧预算，不在 wire 上可见 | types.rs（请求结构无 timeout 字段）+ §3.1 实测【实测印证】 |
| 2.4.3 | 应答 result 在 `outcome` 上 tag：`accepted{answers: question→[option]（IndexMap）, annotations?}` / `chat_about_this{partial_answers}` / `skip_interview{partial_answers}` / `cancelled`；answers 兼容旧版单值字符串与新版数组 | types.rs（AskUserQuestionExtResponse）【实测印证：`{"outcome":"accepted","answers":{"<question>":["<option>"]}}` 被接受并 resolve】 |
| 2.4.4 | 超时配置：`[toolset.ask_user_question]` `timeout_enabled`（默认 true）/ `timeout_secs`（默认 1800 = 30 分钟）；env `GROK_ASK_USER_QUESTION_TIMEOUT_SECS` 覆盖 | `crates/codegen/xai-grok-tools/src/implementations/grok_build/ask_user_question/mod.rs:61`（RESPONSE_TIMEOUT=30min）、`:69-77`（env）；`crates/codegen/xai-grok-shell/src/tools/config.rs`（AskUserQuestionToolConfig 默认值） |
| 2.4.5 | 超时到期行为：工具 future drop → coordinator `biased select` 看到 `request.result_tx.closed()` → 返回 `UserQuestionResponse::Cancelled`（**取消，非错误**），turn 以完成态收口 | `crates/codegen/xai-grok-shell/src/session/acp_session_impl/spawn.rs:2141-2146`【本次未实测（30 分钟窗口外），标注为源码结论】 |
| 2.4.6 | coordinator 发起：`ExtRequest::new("x.ai/ask_user_question", ...)` → `gateway.ext_method()`，由网关按 2.1.2 广播 | `spawn.rs:2087-2146` |

### 2.5 permission 请求形状（源码 + 本仓已冻结类型，双源）

| # | 语义 | 锚点 |
|---|---|---|
| 2.5.1 | `session/request_permission` 的 `tool_call_id` 嵌在 `params.toolCall` 下（区别于 ext 方法的直接 `params.toolCallId`） | `server.rs:508-527`（extract_interaction_tool_call_id 分支注释） |
| 2.5.2 | 请求 params 形状（CordCode 侧已冻结的自有 turn 类型，与本仓 `agent/grokbuild/acp_types.go:386-416` 一致）：`{sessionId, toolCall:{toolCallId, title?, kind?}, options:[{optionId, name, kind}]}`；应答 `{outcome:{outcome:"selected", optionId}}` 或 `{outcome:"cancelled"}` | 本仓 acp_types.go + 上游 server.rs 路由【**本机无法实测 request 方向**——owner 配置 always-approve 使 permission 在 agent 内部即被解决、从不广播给客户端（§3.7 实测证据）；实施前需补真实样本】 |

---

## 3. 真实 wire 样本附录（安装版 1.0.13 实测，脱敏全文）

实测方法：零 API 伪造 follower 客户端直连本机真实 leader socket（`~/.grok/leader.sock`），只 register/initialize/session/load 订阅 + 对广播 request 回 Response；TUI 侧用脚本化 PTY 注入击键触发真实 turn。探针会话已删除；原始全量证据在 `/tmp/grok-follower-probe/capture.jsonl`（临时，附录已转录关键帧）。

样本中 sessionId `01a06290-1a64-70e1-a125-824782ed79ff` 属已删除的探针会话，保留原值供交叉核对。

### 3.1 【核心】request 方向：ask_user_question 广播全文

leader → 订阅者（每个订阅者各收到一份），4 次实测 id 依次为 **0、1、2、3**（单调递增计数器，跨 turn 连续）：

```json
{"jsonrpc":"2.0","id":0,"method":"_x.ai/ask_user_question","params":{"sessionId":"01a06290-1a64-70e1-a125-824782ed79ff","toolCallId":"call_410dc27a15f64707b7f36ca2","questions":[{"question":"你偏好哪种配色主题?","options":[{"label":"深色主题","description":"界面以深色背景为主,适合弱光环境"},{"label":"浅色主题","description":"界面以浅色背景为主,适合明亮环境"}],"multiSelect":null}],"mode":"default"}}
```

要点：

1. **id 是 JSON 数字**（不是字符串、不是 namespaced 形态）——广播给客户端的请求保留 agent 原始 id；
2. 顶层 `_` 前缀半包装：`_x.ai/ask_user_question` + params 直接内联（无内嵌 method/params 双重包装）；
3. **envelope 无超时字段**（只有 sessionId/toolCallId/questions/mode）——订阅者无法从 wire 得知剩余等待时间；
4. `multiSelect` 显式 `null`；`mode:"default"`。

### 3.2 pending_interaction / interaction_resolved 广播全文

经由 `_x.ai/session_notification`（durable 通知轨，非 session/update）：

```json
{"jsonrpc":"2.0","method":"_x.ai/session_notification","params":{"sessionId":"01a06290-...","update":{"sessionUpdate":"pending_interaction","tool_call_id":"call_410dc27a15f64707b7f36ca2","kind":"question"}}}

{"jsonrpc":"2.0","method":"_x.ai/session_notification","params":{"sessionId":"01a06290-...","update":{"sessionUpdate":"interaction_resolved","tool_call_id":"call_410dc27a15f64707b7f36ca2"}}}
```

要点：pending 带 `kind`（实测出现 `permission`/`question` 两种）；resolved **只带 tool_call_id、不带 kind**；同一 toolCallId 先后出现 permission 与 question 两个 pending（工具调用先过权限门再提问，auto-approve 下 permission pending 在 0-4ms 内即 resolved）。

### 3.3 【核心】answer 方向：follower（非 driver）应答 + 仲裁生效

时间线（round 3，S1）：REQUEST id=2 到达 obs1（t=783.5）→ obs2 挂载并收到 replay（784.2）→ obs2 断开 → **obs1（纯订阅者，从未发过带 sessionId 的消息、不是 driver）以原 id 应答**（789.2）→ **同毫秒** `interaction_resolved` 广播（789.2）→ turn 正常 `turn_completed` + `prompt_complete`（794.2）。

应答帧全文（obs1 → leader）：

```json
{"jsonrpc": "2.0", "id": 2, "result": {"outcome": "accepted", "answers": {"你偏好哪种饮品?": ["咖啡"]}}}
```

要点：id 用广播里的**原始数字 id**；result 即 AskUserQuestionExtResponse 的 accepted 形态；**订阅者应答端到端生效**（resolve + turn 收口），不需要任何 driver 身份。

### 3.4 replay-on-attach：挂载期间重放（同 id 同全文）

obs2 在请求 pending 期间 attach（register + initialize + session/load），load 完成即收到：

```json
{"jsonrpc":"2.0","id":2,"method":"_x.ai/ask_user_question","params":{"sessionId":"01a06290-1a64-70e1-a125-824782ed79ff","toolCallId":"call_d45f229bcd024f67b0ab9984","questions":[{"question":"你偏好哪种饮品?","options":[{"label":"咖啡","description":"含咖啡因的提神饮品"},{"label":"茶","description":"茶类饮品"}],"multiSelect":null}],"mode":"default"}}
```

与原请求**逐字节同构、同 id=2**（脚本断言 same_id=True）。重放与历史 session_update 回放一起在 load 后到达。

### 3.5 迟到/过期 id 应答：静默无操作

round 3 S2：REQUEST id=3（「你偏好哪种输入法?」，选项 拼音/五笔）pending 后，obs1 用**已消费的旧 id=2** 再发一次 accepted 应答（t=805.3）。结果：**无错误帧、无广播、无任何可见反馈**；turn 继续正常流式（tool_call_delta_chunk 连续输出），与源码 2.2.1/2.2.2 的「Response 无匹配即静默丢弃」完全一致。

另有一次 TUI 侧已答后 probe 侧迟到应答（round 1：TUI 回车 t=79.272 → resolved 79.277），行为相同——静默。

### 3.6 【核心】原客户端全部死亡后的 replay + 应答（离线恢复）

round 4：id=3 请求 pending 时，原 TUI 已被 kill、两个观察者均已断开。全新观察者 attach（register + initialize + session/load）：

- t=950.5（load 后 ~0.6s）：**收到 id=3 重放全文**（同 id、同 toolCallId、同 questions）；
- t=954.5：应答 `{"outcome":"accepted","answers":{"你偏好哪种输入法?":["拼音"]}}`；
- t=956.5：`interaction_resolved` + `turn_completed`（kinds: model_changed, interaction_resolved, hook_execution, response_completed, hook_execution, turn_completed）。

结论：**pending 交互缓存在 leader 进程内、不依赖任何客户端存活**；断线重连后 attach 即恢复，follower 应答照常生效。这正是 iPhone 离线再上线场景需要的行为。

### 3.7 permission 方向：本机不可实测（负样本也是证据）

三轮探针中每次工具调用都先出现 `pending_interaction kind=permission` → 4ms 内 `interaction_resolved` 的广播对，**但从未收到任何 `session/request_permission` REQUEST 帧**——owner 配置 `permission_mode="always-approve"` 使权限在 agent 内部即被解决，根本不进入客户端广播。这是 always-approve 的 wire 级签名，也意味着：

- permission REQUEST 的广播形状**只能**依赖源码 2.5.2（本仓 acp_types.go 已冻结类型 + 上游路由逻辑）推断，实施前需在非 always-approve 环境补一份实测样本；
- CordCode 未来实现时**不得**把「只收到 pending_interaction(permission) 而没有 REQUEST 帧」当作异常——在 always-approve 配置的 Mac 上这是常态。

### 3.8 request id 计数器与 replay id 语义汇总

| 观测 | 值 |
|---|---|
| id 类型 | JSON 数字 |
| 计数 | 0→1→2→3 单调递增，跨 turn、跨客户端断连连续（leader/agent 进程生命周期） |
| replay id | 与原请求相同（实测 same_id=True 两次） |
| 应答匹配 | 用原始数字 id 即可；已消费 id 再答 = 静默丢弃 |

---

## 4. CordCode 现状与差距分析

全部基于 §1.1 工作树（codex/grokbuild-leader-mode @ a5c1912）。

### 4.1 现状：observer 只读的三个丢弃点

| # | 位置 | 现状行为 |
|---|---|---|
| 4.1.1 | `agent/grokbuild/leader_subscriber.go:327-338` | **REQUEST 帧（id+method）在方法门被静默丢弃**：`isSessionUpdateMethod` 只认 `session/update`、`_x.ai/session/update`、`_x.ai/session_notification`；`_x.ai/ask_user_question` 等 4 种交互请求落 default return——**iPhone 看不到问题的现状根因**。同文件 :319-326 的 Response→pending 映射只服务于订阅者自己发起的 request |
| 4.1.2 | `agent/grokbuild/acp_codec.go:356-362` | `convertSessionUpdate` 对 `pending_interaction` / `interaction_resolved` 两个 sessionUpdate 落 default 分支丢弃（unmapped log）——即使收到官方的 pending/resolved 广播，也不会投影到 iOS |
| 4.1.3 | `agent/grokbuild/session.go:643-650` | `RespondQuestion` / `RejectQuestion` 返回 `ErrNotSupported`（注释 "ACP has no question protocol" **已过时**——上游 ask_user_question 就是 question 协议） |

已有的可复用资产：

- 自有 turn 的 permission 全链路已通：`session.go:793-800` handleRequest（只处理 session/request_permission）→ `:872-889` handlePermissionRequest（pendingPerms + EventPermissionRequest）→ `:584-628` RespondPermission（`{outcome:{outcome:"selected",optionId}}` / cancelled）；`acp_codec.go:76-95` encodeResponse 已能编码 ACP Response；
- bridge-v1 wire 与 reducer：`go-bridge/events.go:181-212`（EventPermissionRequest→`permission_request` / EventPermissionResolved→`permission_resolved`）；`go-bridge/handlers.go:4433-4484` handleResolvePermission（含 officialResolutionSource 豁免的乐观收口）、`:4493-4529` handleQuestionReply（core.SessionQuestionResponder 门）；`go-bridge/projection_reducer.go:970-1014` permission_request 投影要求 requestId（fallback itemId）+ 活跃 turn；
- leader rail 的 turn 边界：`go-bridge/handlers_relay.go:176-330` grokLeaderSessionRelayLoop 已合成 turn_started/turn_completed/F-7 turn_aborted 与 D-G2 守望取消。

### 4.2 follower 升级的差距清单

| # | 差距 | 需要什么 |
|---|---|---|
| 4.2.1 | 订阅侧不识别交互 REQUEST 帧 | leader_subscriber 方法门扩展：4 种方法（含 `_` 前缀半包装与全包装两种形态归一化，参照 §2.1.5）→ 登记 pending（id+method+params）→ 转发事件 |
| 4.2.2 | pending_interaction/interaction_resolved 广播未映射 | acp_codec 增加两个 sessionUpdate 的映射（含 kind 字段），作为「官方收口信号」与「pending 表清理信号」 |
| 4.2.3 | 无应答通道 | leader_subscriber 需要**写路径**：按登记的原 id 编码 ACP Response（`{"jsonrpc":"2.0","id":<原数字>,"result":...}`）经同一 socket 发回；ask_user_question 用 accepted/chat_about_this/skip_interview/cancelled，request_permission 用 selected/cancelled（形状复用自有 turn 已冻结类型） |
| 4.2.4 | 无 SessionQuestionResponder 实现 | grokbuild agent 实现 core 可选接口（bridge-v1 `question_reply` / handleQuestionReply 门已存在）；同时清理 ErrNotSupported 过时注释 |
| 4.2.5 | 重连恢复未覆盖交互 | 重连 subscriber 的 session/load 已会触发上游 replay（§3.6 实测）——需把 replay 的 REQUEST 帧同样登记进 pending 表并上报 iOS（复用 4.2.1 路径即可） |
| 4.2.6 | 投影身份 | reducer 要求 requestId + 活跃 turn：交互请求必须携带稳定的 requestId（建议 tool_call_id，与 interaction_resolved 广播天然对齐）并确保落在 relay loop 合成的活跃 turn 内 |
| 4.2.7 | 抢答/迟到收口 | 收到 interaction_resolved 广播即清 pending 表 + 广播 permission_resolved/question 已收口（官方仲裁结果落地）；本地乐观应答后也要容忍「resolved 由官方广播确认」的双路径（与现有 handleResolvePermission officialResolutionSource 豁免同构） |

### 4.3 上游免费提供的保证（不需要 CordCode 自建）

1. first-answer-wins 仲裁与迟到应答静默——上游 oneshot + guard 幂等已保证（§2.2），CordCode 只需不重复造仲裁；
2. pending 缓存与断线 replay——上游 leader 已做（§2.3/§3.6），CordCode 重连后正常 attach 即得；
3. turn 收口——应答生效后 turn 正常 completed（§3.3），relay loop 现有合成逻辑无需改。

---

## 5. 方案候选与取舍

### 候选 A：follower 全量交互（permission + question 双方法）

订阅侧识别 4 方法中的 `session/request_permission` 与 `x.ai/ask_user_question`，分别走 bridge-v1 既有 permission_request 与 question 通道；`exit_plan_mode` / `x.ai/mcp/elicit` 暂不实现（见裁决点 6）。

- 优点：一步到位；permission 链路 CordCode 已全通（自有 turn），question 通道 iOS 已有渲染先例（claude/codex 均有 question UI）；
- 风险：permission REQUEST 广播形状**无实测样本**（§3.7），需先补证或按源码实现后用真实样本回归；
- 工作量：4.2.1-4.2.7 全量。

### 候选 B：先 question only（推荐起步）

只订阅应答 `x.ai/ask_user_question`；permission 维持只读（iPhone 能看到 pending_interaction(permission) 的存在即可，不提供应答）。

- 优点：ask_user_question 是**全链路实测过**的方向（request/answer/replay/迟到全有样本）；无 permission 样本缺证风险；与「iPhone 回答问题」这一最直接的产品诉求吻合；
- 风险：permission 仍需 TUI/官方客户端代答，用户体验半吊子；
- 升级路径：A 是 B 的超集，B 不造成返工。

### 候选 C：等待官方 follower 参考实现

上游 `SHARED_INTERACTIVE_MODALS.md` 文档缺失（§2.1.1），官方仓库尚无 follower 客户端样例可镜像。

- 优点：最保守；
- 缺点：owner 已裁决升级要做，无限等待不符合节奏；本次实测已补齐官方文档缺口的 wire 真值，等待的边际价值低。

### 共同风险矩阵（无论 A/B）

| 风险 | 事实依据 | 处置 |
|---|---|---|
| 抢答冲突（Mac TUI 与 iPhone 同时答） | 上游 first-answer-wins（§2.2）+ 迟到者静默（§3.5） | 不自建仲裁；以 interaction_resolved 广播为准收口 iOS UI；本地乐观应答展示「已提交」而非「已生效」 |
| TUI 已答后 iPhone 仍显示 pending | resolved 广播实时到达（§3.2，实测 5ms 内） | 订阅 interaction_resolved 即清 pending；再加会话重载兜底（replay 只含未决项，§3.6） |
| 30 分钟无提示超时 | envelope 无超时字段（§3.1 要点 3）；默认 1800s；到期 = Cancelled 收口非错误（§2.4.5） | iOS 本地起表或显示「长等待」；到时由 turn 正常收口自愈（不悬挂） |
| iPhone 离线期间的交互 | leader 内存缓存 + attach 即 replay（§3.6 实测原客户端全死后仍可恢复） | 重连后 session/load 自动补发；唯一不可恢复窗口 = leader 进程重启（§2.3.4，官方同样无持久化，对齐即可） |
| always-approve Mac | permission request 根本不广播（§3.7） | 不视为异常；产品上此类 Mac 的 iPhone 端自然只有 question 面 |
| 订阅者应答被上游静默丢弃后的悬挂 | 已消费 id 静默（§3.5） | pending 表以 interaction_resolved 广播为唯一清理真值 + turn 收口时强制清理兜底 |

---

## 6. owner 裁决点清单

1. **范围**：候选 A（permission+question）还是候选 B（先 question only）？——B 风险更低（全实测），A 体验完整但 permission 方向缺实测样本。
2. **permission 样本补证方式**：是否安排一次受控采集（临时在测试环境关闭 always-approve 触发一次真实 permission REQUEST 广播，采完即恢复）？该动作涉及改 owner 的 `~/.grok/config.toml`，按任务红线本次未做。
3. **超时 UX**：wire 无超时字段，iOS 端按「本地 30 分钟假设显示倒计时」还是「无期限等待提示」？（上游 timeout_secs 可被用户配置改掉，本地假设可能不准。）
4. **抢答冲突提示**：iPhone 应答被 TUI 抢先（resolved 先到）时，iOS 显示「已被其他客户端应答」还是静默收口？
5. **协议面影响**：question 通道复用 bridge-v1 既有 question wire（无协议包变更）还是引入 grokbuild 专属事件字段（需升 protocol pack + iOS mirror）？——倾向前者，但 iOS question UI 对 grokbuild 场景的字段适配（multiSelect/mode/description）需要一次 iOS 侧确认。
6. **exit_plan_mode / mcp/elicit**：本次仅源码冻结（§2.1.1），未实测、iOS 无对应 UI——纳入本期还是显式移出 capability？

### 6.1 裁决记录（2026-09-02，owner 授权 agent 代定）

owner 当日裁决方式：「你自己看着办吧，我其实看不懂这些细节」——六点全部由 agent
按保守/子集优先原则代定，实施时按此执行；如 owner 后续推翻任何一条，从对应条目重做：

1. **范围 = 候选 B（question only 起步）**。理由：ask_user_question 是全链路实测方向，
   permission 方向无实测样本（§3.7）；候选 A 是 B 的严格超集，后补 permission 无返工。
2. **permission 样本补证 = 本期不做受控采集**。不改 owner 生产 `~/.grok/config.toml`
   （含 api_key，属外部环境操作红线）；question-only 实施不需要该样本。将来做候选 A
   时再议（届时优先在非 always-approve 环境自然采集，其次才考虑受控采集并单独请示）。
3. **超时 UX = 无期限等待提示，不做倒计时**。wire 无超时字段且 `timeout_secs` 可被
   用户配置/env 覆盖（§2.4.4），本地倒计时必然可能给出错误承诺；显示中性「等待回答中」，
   到期由 turn 正常收口自愈（§2.4.5，取消非错误）。
4. **抢答冲突 = 静默收口**。本地应答后显示「已提交」；`interaction_resolved` 广播到达即
   清 pending 收口 UI，不追加「已被其他客户端应答」提示——最常见的抢答者是 Mac TUI
   （用户自己刚答过），再弹提示是噪音。
5. **协议面 = 复用 bridge-v1 既有 question wire**。不升 protocol pack、不动 iOS mirror
   协议；multiSelect / options.description / mode 的适配放 iOS 渲染层。
6. **exit_plan_mode / mcp/elicit = 显式移出本期 capability**。源码语义已冻结（§2.1.1），
   将来有产品需求再评估；不在 capability 中广告未实现面。

---

## 7. 后续实施前置条件清单（只列不改）

### 7.1 测试 fixture（从本次 capture 归档）

- REQUEST 半包装帧（`_x.ai/ask_user_question` + 数字 id + 直接 params）——§3.1 全文即 fixture；
- pending_interaction / interaction_resolved 广播（含 kind 有无差异）——§3.2；
- follower 应答帧（原数字 id + accepted 形态）与 TUI 抢答时间线——§3.3/§3.5；
- replay-on-attach 同 id 重放（含原客户端死亡场景）——§3.4/§3.6；
- always-approve 签名（permission pending→resolved 对、无 REQUEST 帧）——§3.7（负样本，防误判）；
- 待补：非 always-approve 环境的 `session/request_permission` REQUEST 广播真身（裁决点 2）。

### 7.2 Mac 侧改动面（实施时逐项落地）

1. `agent/grokbuild/leader_subscriber.go`：方法门扩展 + REQUEST 登记 + 写路径（应答回 socket）；
2. `agent/grokbuild/acp_codec.go`：pending_interaction / interaction_resolved 映射 + 交互 REQUEST 的 params 归一化（半/全包装两形态）；
3. `agent/grokbuild/session.go`：RespondQuestion/RejectQuestion 实现（替换 ErrNotSupported）+ 交互 pending 表（以 tool_call_id 为键，resolved 广播清理）；
4. `go-bridge/events.go` / `handlers.go`：question 事件下发 + handleQuestionReply 接通（门已存在）；
5. `go-bridge/projection_reducer.go`：requestId=tool_call_id 的身份核对 + 活跃 turn 约束对 leader rail 合成 turn 的适配；
6. `go-bridge/handlers_relay.go`：relay loop 内交互事件的转发与收口（复用 D-G2 模式）。

### 7.3 iOS 侧涉及面（仅列出，本任务不改 iOS 仓）

- question 渲染路径复用确认（multiSelect / options.description / mode 的字段适配）；
- permission_request 渲染对 grokbuild leader rail 来源的兼容（若做候选 A）；
- protocol pack mirror 同步（若裁决点 5 选择新字段）。

### 7.4 版本管理前置

- 实施前重核上游 HEAD（本次 72a61251）与**安装版**版本；安装版升级时用本文 §3 样本做 wire 回归（重点：id 形态、半包装、replay 语义）；
- 上游 `SHARED_INTERACTIVE_MODALS.md` 若后续补上，以官方文档复核本文 §2 冻结表。

---

## 附：探针方法与清理记录

- 4 轮探针（1 轮初版谓词 bug + 1 轮重复 bug + 1 轮修复后完整 + 1 轮 post-death），全部零 prompt 伪造、零配置修改；模型 turn 共 4 次最小 ask_user_question 触发（探针会话内）。
- 已清理：探针会话目录 `~/.grok/sessions/%2Ftmp%2Fgrok-follower-probe%2Fproj/`（含 01a06290 会话）已删除；探针 TUI 进程已 kill（`pgrep` 确认无残留）；owner 的 leader 进程（PID 23577）与 `~/.grok/config.toml` 全程未动。
- 保留：`/tmp/grok-follower-probe/capture.jsonl`（原始证据，临时目录随系统清理；关键帧已全文转录进本文 §3）。
