# 监工指令 5 号（C2–C7 集中收敛实施）

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`
> **范围**：双仓按需；OpenCode 1.18.18 evidence、Mac `opencode-web`/Kernel/control-plane、必要的 iOS 既有协议接线与定向测试
> **下发时间**：2026-08-20T15:48:22Z
> **状态**：已由 directive-006 取代；不得执行本指令的产品实施阶段
> **kind**：implementation（owner 明确授权集中批次；包含并取代 directive-004）

> **2026-08-20 supersession note**：canonical 设计经 source-shape 审计后确认 E1–E7 与 WP evidence ownership 尚未闭环。本指令原授予的 C2 decoder、C3–C7 translator/capability/安装权限已撤销。当前唯一有效工作为 directive-006 的 evidence-only capture/checker；样本返回并由设计 owner 更新 canonical §6 后，才会另发集中产品实施指令。

## 0. 执行方式变更

owner 已明确决定：不再按每个小功能“实施 → 停工 → 监工审计 → 再授权”。本指令把 directive-004 的 C2 补洞和后续 C3–C7 合并为一个连续实施批次。

- directive-004 标记 `superseded`；其全部补洞要求原样成为本指令 **Stage 0 硬门**。
- Stage 0 本地 checker/tests 通过后，开发 agent **无需等待监工回复**，直接进入 Stage 1–3。
- 各 stage 必须独立 commit、保留证据和内部验收，但中途不向 owner 请求“是否继续”。
- 全部批次完成后只提交一份集中完成报告，由监工做一次集中审计；审计前不得宣称 verified/done。
- 只有本指令“强制暂停条件”命中时才停止并提交证据给 owner；普通编译/单测失败由开发 agent 在批次内定位、修复、回归。

此流程调整只减少人工往返，不降低 source-first、capture-before-translator、SSV2 single-writer、fail-closed 和真实验证标准。

## 1. 总目标

以 OpenCode 1.18.18 official Web/source/live sample 为事实源，连续完成：

1. C2 evidence/decoder 补洞；
2. C3 首条消息、follow-up、稳定 messageID、agent/model/parts；
3. C4 direct SSE、nested sync skip、流式输出、history hydrate、reconnect/error/abort 与唯一 Kernel ingest；
4. C5 provider/model/agent catalog 与五级 fallback；
5. C6 permission、question、todo control-plane；
6. C7 rename/archive/delete；
7. 最终 capability 只按真实已完成路径收敛，Release 构建/安装一次，统一交付审计。

不追求“所有 API 都覆盖”。Gate B OD-3 的 fork/share/unshare/children/command/shell/summarize/revert/diff/VCS/file 等仍为 future/unsupported。

## 2. 永久架构护栏

整个批次始终遵守 Gate S 和 iOS `CLAUDE.md` 的 Session Sync v2 路线：

- OpenCode serve 是 upstream facts；不是 CordCode 第二 timeline writer。
- Mac 每个 `(backendId, sessionId)` 的 `ProjectionKernel` 是唯一 timeline truth。
- live 唯一路径：official direct SSE → `handleRawEvent/handleServerEvent` 唯一 pre-Kernel normalization → `EventPublisher.publish` → `ProjectionKernel.IngestLive`。
- hydrate 唯一路径：private hydrate transaction → commit；history/status 只能提供 hydrate facts，不能绕过 Kernel。
- iOS `ProjectionStore.applyFrame` 是唯一 SSV2 `messages[]` writer；composer placeholder 仅 presentation。
- nested `sync` 在唯一 normalization point 显式 skip；禁止 direct+sync 双 ingest、consumer referee、similarity merge、raw history merge、timeout completion。
- HTTP 200/204 只表示 request admission/control success，不表示 turn completed。
- permission/question 的 canonical event 可进 Kernel；todo 是 control-plane，禁止写 SessionProjection timeline 或发明 todo ID。
- catalog/model/project/session mutation 都是 request/control metadata，禁止制造 turn/message。

任何实现若需要改变以上 truth owner / only writer / transaction domain，命中强制暂停，不得自行改路线。

## 3. Stage 0 — C2 补洞（完整吸收 directive-004）

### 3.1 WP 真实 HTTP evidence

1. pinned official OpenCode=`2cba7e227d` / 1.18.18，隔离 HOME/XDG、4398/4399、harness-owned 临时 git worktree；不得触碰 4096。
2. 重新捕获 WP。每个 GET `/project` 的 `http[]` 保存实际 request/query/status/**response**；after-create/after-delete status 不得硬编码。
3. `check_workspace_project.py` 只从 `http[]` 的 GET `/project` status+response 推导 top-level、id/worktree、field presence、growth、deleted-still-registered。不得以 `rawPayloads` 或 summary 为 evidence owner。
4. 若保留旁路副本，checker 必须证明篡改副本不改变 derived result，同时 mismatch 会显式失败，禁止双真相。
5. self-test 至少捕获五类 HTTP 篡改：status、top-level、project id、worktree isolation、deleted-worktree presence。

### 3.2 `/project` fail-closed

- generation-118 project decoder 只接受样本坐实的 bare array；`{"data":…}`、object/null/string 全部失败。
- 每行必须是 object，`id`/`worktree` 必须是非空 string；坏类型/缺字段带 row index 失败，不能静默裁剪。
- unknown extra fields 允许；global pseudo-project `worktree="/"` 是合法 row，decode 后由 CordCode visibility overlay 排除。
- global aggregation和 `ListProjectSuggestions` 使用同一严格 decoder。
- 修正“official desktop 会隐藏 ghost”之类未证实注释；missing-worktree 明确是 CordCode overlay，server registry仍是 truth owner。

### 3.3 roots/limit 证据诚实度

- 删除 `TestOfficialRootsLimit` 中未真正进入 fixture 的第 101 行假证明。
- adapter unit test只证明发送 directory+roots=true+limit=100。
- A10 live checker证明 official serve 行为：4 roots 下 limit 1/2/3 → 1/2/3；limit10→4；child 不进 roots。
- 不为 synthetic server增加第二层 local truncation。

Stage 0 checker/tests PASS 并独立 commit 后自动进入 Stage 1，不等待审计。

## 4. Stage 1 — 一次性补齐剩余 source-only 样本

在任何对应 translator 实现前，用同一个 isolated official 1.18.18 harness 批量完成剩余七项：

| Evidence ID | 归属 | 必须回答 |
|---|---|---|
| SV | C3 selected variant | selected variant 的真实请求字段、persist/reload 行为；不能从 omit-when-unset 推导 |
| R | C4 reasoning | SSE/persisted reasoning part 的实际 type/字段/ordering |
| ET | C4 external turn | official Web 第二客户端发起时 global SSE、status、message、terminal/reload |
| P | C5 providers | `/provider` top-level/connected/catalog model shape |
| DM | C5 default model | configured default 的真实来源和缺失行为，不能把 first connected 冒充 default |
| RN | C7 rename | PATCH path/body/response、list/by-ID/event refresh |
| DL | C7 delete | DELETE response、list/by-ID 404、session.deleted/catalog invalidation |

共同要求：

- 每项 raw HTTP/SSE/reload + sanitized sample + official client/server `file:line` + 独立 checker/self-test。
- checker 从 raw transport fields 独立推导；summary 不能当 evidence。
- 可以一次启动 sandbox 连续捕获，样本仍须场景隔离、可单独 replay；最终精确回收 4398/4399，4096 不动。
- fake server 只用于 translator unit test，不替代 official sample。
- 若某项无法安全产生：标 `blocked` 并将**仅对应功能**保持 unsupported/unadvertised；继续不依赖该项的其他功能。不得猜 JSON，也不得让一个非关键 blocked 阻塞整个批次。
- 若真实样本推翻 Gate B/S3 的请求 shape、ownership 或产品决策：触发强制暂停，先交证据，不自行改设计。

Stage 1 统一 evidence commit/checker通过后自动进入 Stage 2。

## 5. Stage 2 — Turn/SSV2 核心集中实施（C3+C4+C5）

这三个 slice 共享 prompt/model/event/Kernel 链路，必须作为一个闭环实现，禁止分别造局部状态机。

### 5.1 C3 submit

- first/follow-up 均发送 authoritative stable `messageID`、selected `agent`、`model{providerID,modelID}`、有样本的 `variant`、A1/A2/A9 已支持 parts。
- text/file/image-persist/file mention/agent mention 只按样本转换；vision仍 unsupported，不能把 persist成功冒充 provider视觉理解。
- unsupported part 在任何 network I/O 前失败，capability-consistent error，零 prompt POST。
- messageID 只做 request ↔ persisted user message ↔ projection correlation；不得成为 iOS optimistic writer/referee。
- create继续 body `{}`；HTTP 204仅admission，真实 persisted user + assistant stream必须由C4回到同一Kernel。
- 若必须新增 send-RPC wire field，触发强制暂停；默认路径是 Mac adapter 生成 authoritative messageID，避免协议改动。

### 5.2 C4 event/hydrate/reconnect

- 在 `handleServerEvent` 命名、独占地 skip nested `sync`；unknown type log+drop，不视为成功。
- A1–A5完整序列 replay 到同一Kernel：healthy、follow-up、retry/error、abort、disconnect/reconnect；重复输入不得重复 syncRev/message。
- text/reasoning/tool/error/permission/question distinctions只按样本映射。
- seal/delete `kernel==nil → projection.Apply` 第二 reducer路径；不得新加 fallback/referee。
- history/status只进入 hydrate facts；reconnect validate/invalidate/rehydrate同一Kernel，push/pull同一head。
- A3/A4失败终态不能变 healthy stop；A5断线 busy、重连 `server.connected → delta → idle`，不得从空status推断成功。

### 5.3 C5 model/agent

- provider/model/agent catalog只按P/DM样本；connected-only、无recursive JSON search、无invented ID。
- 五级 fallback逐级独立：current choice → agent model → configured default → recent → connected fallback。样本未支持的级别明确 excluded，不得塌缩冒充。
- selected model/agent通过C3 body端到端携带；unavailable/unconnected选择零prompt POST。
- catalog refresh不写timeline。

### Stage 2 内部验收（不暂停）

- A1–A5/A9/SV/R/ET/P/DM完整 replay；create/send/reopen real sandbox cycle。
- stable messageID one action → one persisted user + one turn。
- direct+sync不双 ingest；kernel-nil fallback sealed；history/status不绕Kernel。
- 五级 fallback命名 fixtures；unavailable/unsupported零POST。
- Mac owning tests + iOS targeted architecture/writer-seal tests通过后，自动进入 Stage 3，不等待监工。

## 6. Stage 3 — Interaction 与 mutation（C6+C7）

### 6.1 C6 permission/question/todo

- permission严格按A6 once/always/reject：raw control不写messages；canonical permission request/resolved走唯一Kernel；reject不是healthy completion。
- question严格按A7 asked/replied/rejected，reply body `answers:string[][]`；映射canonical `user_input_requested/user_input_resolved`，禁止发明 `question_resolved`。
- legacy question presentation不得进入Kernel或SSV2 raw。
- todo严格按A8 `{content,status,priority}` 无id；只走现有control-plane `todos_updated`。禁止hash/content/position造ID，禁止进SessionProjection。
- external answer/reject、reconnect、cold reload均需测试；first server resolution wins。

### 6.2 C7 rename/archive/delete

- rename只在RN sample green时实现；archive沿用A10；delete只在DL sample green时实现。
- HTTP success只更新catalog metadata/by-ID refresh/list invalidation；不重写timeline。
- archive默认列表隐藏但by-ID可读；delete后list移除/by-ID 404；错误忠实暴露。
- OD-3十四项继续future/unsupported，不夹带generic API coverage。

### 6.3 Capability 最终激活

- 只使用 protocol pack 已存在的 capability/event/RPC；不新增 wire field/schema。
- 每个 capability 必须在完整 request + event/reload/error + Mac/iOS owning tests全部通过后才advertise。
- blocked、partial、fake-only 或仅HTTP成功的功能保持unadvertised。
- 更新 WireDescriptor 属最终activation commit，必须逐项附“样本→实现→测试”证据；若发现现有protocol无法诚实表达，触发强制暂停，不得私加字段。

Stage 3 完成后进入 Final Gate。

## 7. Final Gate — 一次性验证、安装和交付

### 7.1 测试原则

- 日常内部迭代只跑相关Go/Swift定向测试，不因一行改动反复跑全部iOS 2072用例。
- 最终统一运行：所有新sample checkers+self-tests、A1–A10 checker/self-test、`go test ./agent/opencode-web`、受影响go-bridge/core tests、`go vet`、`go build ./...`。
- iOS如有修改：定向 build + 受影响的 `CCCodeTests/<TestClass>`；禁止裸 `xcodebuild test`、CCCodeUITests、snapshot/simulator automation。
- UI tests、真机自动点击仍未授权。

### 7.2 任务 watchdog

- Go定向 test默认180s；iOS定向 build/test默认5分钟；全量unit确有必要才10分钟。
- shell管道 `set -o pipefail`，保存真实主命令exit code。
- 测试出现最终summary后立即收尾；diagnostics/simctl diagnose超过60s就终止本任务创建的诊断进程。
- 只回收本任务创建的process group；不得按名称全局kill。
- 测试hang/后台任务中断时保留日志并诊断，不得等待数小时。

### 7.3 最终安装一次

- Mac产品代码完成后只在Final Gate统一构建Release、覆盖安装并启动 `/Applications/CordCodeLink.app`；验证8777/runtime commit/路径，无临时app。
- iOS产品代码若有修改：用`devicectl`探测connected/available paired iPhone，Final Gate统一执行`scripts/run.sh device`安装启动一次；不操作UI。
- 4096 owner serve不得写/停/重启；真实managed-serve只做本任务已授权的read-only探测，sandbox验证不能冒充owner E2E。

### 7.4 集中完成报告

报告按 stage 给出：

1. evidence commits、product commits、activation/closeout commits及file list/diff stat；
2. 每个sample的raw来源、source file:line、checker/self-test、blocked项；
3. C2–C7 capability matrix：supported/blocked/unsupported、request shape、translator、truth owner、only writer、owning tests、advertised与否；
4. A1–A10+新增样本 replay、sandbox E2E、Go/iOS定向测试的真实命令/输出/timeout；
5. protocol/WireDescriptor/iOS变更逐项列出；若无则明确零diff；
6. Mac Release/iOS安装（如适用）、8777/4096/4398/4399、残留进程证明；
7. `git status --short`干净；不把exec-plan proven冒充监工verified或owner UI验收；
8. owner后续只需执行一次的最终真机测试矩阵草案。

提交报告后停止，等待一次集中独立审计；不得自行进入owner UI验收或继续OD-3。

## 8. 强制暂停条件（仅这些情况停工）

1. 同版本raw sample与设计文档/Gate B owner decision/SSV2 ownership冲突；
2. 必须新增或改变protocol schema/wire field才能诚实实现；
3. 必须改变Kernel/iOS only-writer或增加第二reducer/referee/fallback；
4. official真实路径无法用隔离harness捕获且实现会依赖猜测/真实账号；
5. 第一轮真实sandbox修复对目标症状完全无影响——立即升级为证据捕获+official对照，不在同一状态机猜第二轮；
6. 测试主体hang、后台任务失联或诊断收集无法在规定上限终止；先回收本任务进程并提交现场。

以下**不是**暂停理由：普通compile/test失败、可局部修正的实现错误、某个互不依赖feature被sample gate标blocked。开发agent应自行修复或诚实排除后继续批次。

## 9. Commit 纪律

建议但不强制逐功能停工的commit序列：

1. Stage 0 C2 evidence；
2. Stage 0 decoder/tests；
3. Stage 1 remaining evidence pack；
4. Stage 2 C3+C4+C5 implementation/tests；
5. Stage 3 C6+C7 implementation/tests；
6. capability activation（仅真实完成项）；
7. final regression/exec-plan/handoff/报告。

每个commit保持单一审计主题、工作树阶段性干净。不得把未验证WIP夹入Final Gate安装包。

## 10. 不授权

- 不授权OD-3 future功能、generic API coverage、provider auth/key收集。
- 不授权猜测样本、fake server替代official capture、legacy fallback、recursive decoder、第二writer/referee。
- 不授权未经强制暂停裁决的protocol新字段或SSV2路线变化。
- 不授权UI tests、snapshot、simulator automation、真机点击/输入/视觉验收。
- 不授权生产Relay/VPS、真实外部账号/provider、停止或修改owner 4096 serve。
- 不授权开发agent中途以tests green/exec-plan proven宣称最终done；只在全部批次后交集中报告。
