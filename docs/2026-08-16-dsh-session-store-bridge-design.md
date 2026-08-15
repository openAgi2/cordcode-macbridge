# DSH 会话存储桥接设计（列表 + 历史 + file-backed 投影）

- 日期：2026-08-16
- 状态：owner 裁决升级为必做（本轮对话：「iOS 端无法显示 mac 端 DeepSeek harness 的 session 列表」「iOS 端 DeepSeek 模式可以新建 session…但是 session 列表始终为空」——不做完这两点不算完整功能，原 6 行验收矩阵挂起）
- 前置计划：`docs/2026-08-13-dsh-driver-design.md`（driver 全量 + 空态修复 + live-only 投影修复 + 会话写入用户存储，均已完成）
- 不变约束：CordCode 初衷（探测-复用-未启动、零迁移、双向接力、永不自托管）+ SSV2 十二条护栏，见前置计划 §0/本文 §7。

## 1. 目标（owner 两点）

1. iOS 端 DeepSeek 模式显示 Mac 端 DSH 会话列表（含 dsh web 创建的会话）。
2. iOS 端自建的 DeepSeek 会话出现在列表中（并可重新打开查看历史）。

随之而来的完整体验：点开任意已结束会话可查看完整历史；死会话内发消息得到诚实错误（SDK 限制）；活会话续聊不变。

## 2. 事实基线（源码取证，pin `47f9438` / npm `@deepseek-ai/dsh@0.1.0-rc.6`）

### 2.1 SDK 无 list/resume（「prompt-known-id=恢复」假设否定）

- SDK JSON-RPC 面仅 `initialize`/`session/prompt`/`shutdown`（vendor `dsh-sdk-protocol/lib/types/types.d.ts`）。`sessionId` 客户端自选："an unknown id lazily creates the agent+session pair"。
- server 实现 `getOrCreateSession` → `ctx.agents.create`（vendor `dsh-sdk-jsonrpc-server/lib/index.js`）——**只有 create，没有 resume**。
- harness core 有 `agents.resume`（"Load a persisted session and resume an agent on it"，源码 `packages/core/agent/src/index.ts:434`）但未暴露到任何 out-of-process 面：
  - headless bundle：一次性 `session-${randomUUID()}` 新会话，打印最终文本退出（`packages/bundle/headless/src/index.ts:111`）；
  - ACP server：create-only（`session/new`，无 load；且 npm 安装未携带 acp 包）；
  - `--resume` 仅存在于 tui/web 等 host 内交互面。
- 持久化防覆写：`session-persistence-jsonl/src/index.ts:602` "refusing to materialize: a log already exists on disk (load/resume it instead)"；coordinator `already exists`。→ 对已存在 id 发 prompt = 报错，**不覆写磁盘**。

结论：pin 版本内跨进程恢复不可能；诚实边界 + 发送守卫（§4.5）。未来 SDK 开放 resume 后再升级。

### 2.2 存储格式（实测 `$HOME/.dsh/sessions/`，7 个真实会话）

布局：`$DSH_HOME/sessions/<projectKey(cwd)>/<sessionId>/session.jsonl[.zstd]`。

- `projectKey`（源码 `session-persistence-jsonl/src/format.ts`）：`/ \ :` 分隔符连续运行合并为单个 `-`；`[A-Za-z0-9._-]` 直通（`~` 除外）；其余 UTF-16 码单元 `~XXXX`（大写十六进制）；去首部 `-`；空→`root`；包成 `--<≤251>--`。**有损**（分隔符运行合并）→ 读侧以头行 `cwd` 为准，不反解目录名。
- 头行（第一条记录）：`{type:"session",version,id,createdAt(ms),cwd?,parentSession?,seedLength?,origin?,delegationDepth,agentPreset?}`。
- 事件信封：`{type,seq,time(ms),data}`。实测分类学（owner 真实 17,600 行 web 会话）：
  - `user/message`：`data{content:[{type:"text",text}…],source:{kind},role,id}`——人类消息 `source.kind=="user"`；`<system-reminder>`/runtime-context 注入为其他 kind。
  - `assistant/message`：`data{turn,step,message{role:"assistant",content:[{type:"reasoning"|"text"|"tool-call",…}],source:{kind:"model",…}}}`。
  - `tool/result`：`data{turn,step,message{source:{kind:"tool",callId},content:[{type:"tool-result",toolCallId,content:[{type:"text",text}]}]}}`。
  - `turn/start{turn}`、`turn/end{turn,reason:{kind}}`、`step/start|end`。
  - `session/title`：`data{title,messageSeqs,source:{kind:"fallback"|…}}`——**标题在日志内**。
  - 控制面：`permission/preset`、`sandbox/mode`、`approval/policy`、`agent/inbox/spliced`、`request/header|context`、`subagent/descriptor`、`session/end-seed`。
  - 流式/打包行：`assistant/chunk`、`reasoning-chunks`、`text-chunks`、`tool-call-chunks`（`packChunks` 默认 true）——历史读取跳过（committed `assistant/message` 已携带完整内容）。
- 标题规则（源码 `session-title` 包）：折叠取最新 `session/title` 事件；无则 fallback = 首条**人类** `user/message` 的 text 块首词（空白归一、UTF-8 字节上限截断、不切码点）。
- 压缩：web 默认 zstd（实测全 `.jsonl.zstd`）；driver cordis.yml `compression: none`（实测旧私有目录 `.jsonl` 明文）。**读侧双后缀都必须支持**。
- subagent 会话：头行 `origin:"subagent"` / `delegationDepth>0`（实测目录中存在）→ 列表过滤。

### 2.3 go-bridge 既有接线点

- 通用 list 路径（`go-bridge/handlers.go:2740`）：`agent.ListSessions` → `sessionsToWire`（title/messageCount/modifiedAt/directory）+ 运行态 enrich + pin + 分页。dsh 不在 `catalogCapabilityRequiredFor`（无 v2 协商门）。DSH 当前 `ListSessions` 返回 `core.ErrNotSupported`（`agent/dsh/dsh.go:248`）＝列表空根因。
- 历史读取：`RichHistoryProvider.GetRichSessionHistory` 全量解析路径（`handlers.go:3254`）；`TranscriptLocator` 分页为可选增强。
- 投影冷 hydrate：**opencode/grokbuild 先例 = pathless 全量重建**（`Path=""/Cursor=0`，仅要求 `RichHistoryProvider`；`handlers_projection.go:612-628`；re-open 强制重建集合 `:457-459`）。zstd 不可按行字节切割 → DSH 走此路径。
- live-only admission（上一轮过渡形态）：`backendUsesLiveOnlyProjection`（`handlers_projection.go:352`）、`ensureProjectionHydrated:439` 分支。
- iOS 门控：`BackendModels.isLiveOnlySessionList == .deepSeek`（`BackendModels.swift:188`）+ `liveOnlySessionListHint` + SidebarView / SessionsView / ChatViewModel+DirectoryPreferences / ChatViewModel+SessionManagement 调用点（iOS `fa371a3`/`9b7efd4`）。

## 3. 产品形态终态

- **列表**：iOS DeepSeek 模式显示全局 store 会话（mtime 倒序；title；directory=头行 cwd；过滤 subagent）。iOS 自建会话自 `624c6a4` 起写入 store → 自然入列（owner 点 2）；Mac web 会话在 store → 入列（owner 点 1）。
- **打开任意会话**：SSV2 投影 **file-backed 冷 hydrate**（pathless 全量重建）→ 完整历史渲染。
- **续聊**：活会话（本 bridge registry 内）正常续；死会话发送 → 快速失败诚实错误（§4.5）。
- **live-only admission 退役**：dsh 移出 `backendUsesLiveOnlyProjection`；死会话从 `projection.not_found` 升级为 file-backed 基线（not_found 仅剩「store 中也无此 id」的诚实语义）。SSV2 §8 栏记账为 file-backed（同一声明制路线内的能力升级，claudecode 同款模式）。
- **边界（如实声明）**：dsh web 正在运行的会话 v1 无实时 tail——iOS 下拉刷新触发 re-open 全量重建可见增量；不做 fork/seed 续聊；不 re-pin SDK；不改 vendor。

## 4. 实现清单（Mac 仓）

1. **`agent/dsh/store.go`（新）**：store root 解析（复用 `dshHome()` 语义；HOME 失败 → 空结果而非错误）；`projectKey` Go 移植；`scanSessions`（全局、双后缀、头行、mtime、subagent 过滤、标题扫描=解压流式前缀预算 512KB 内找 `session/title` 或首条人类 user/message）；`resolveSessionFile(id)`（跨 project 目录按 id 查找）；解码器（plain bufio / zstd 流式，klauspost `github.com/klauspost/compress/zstd`，纯 Go 无 cgo）。
2. **`ListSessions`**：替换 `ErrNotSupported`，返回 `AgentSessionInfo{ID,Summary,Directory,ModifiedAt,MessageCount(标题扫描预算内如实，超预算 0)}`，mtime 倒序。**不读**旧私有目录（残留测试会话，前置计划 KI#4）。
3. **`GetRichSessionHistory`**（`RichHistoryProvider`）：全量解码映射——人类 `user/message`→user 条目；`assistant/message`→assistant（text→Content、reasoning→Thinking、tool-call 块→parts）；`tool/result`→tool_result part（按 callId 邻接）；`turn/start|end`→TurnStartedAt/TurnCompletedAt；稳定 ID=`<sessionId>:<seq>`（grokbuild 先例：物理 seq 派生，防 iOS 外部探测误报）；limit=尾部语义对齐 grokbuild；跳过 chunk/packed/控制面/非人类 user/message。
4. **投影接线**：`prepareProjectionHydrateSource` 增 `deepseek` pathless 分支；`backendSupportsProjectionHydrate` 增 `deepseek`；`:457-459` re-open 强制重建集合增 `deepseek`；`backendUsesLiveOnlyProjection` 移除 `deepseek`。核验 pathless 事务对 RichHistoryEntry 的投影转换对 dsh 通用（opencode/grokbuild 共用 machinery）；liveonly 测试类改造为 file-backed 语义。
5. **发送守卫**：`StartSession(已有 id)` 三态——registry 活→复用；store 存在该 id→返回新 wire 错误 `session_resume_not_supported`（快速失败，避免 spawn 后在首个 prompt 处 materialize 拒绝）；否则新会话。canonical `docs/protocol/bridge-v1.md` + iOS mirror 入册。
6. 文档：设计 §8 SSV2 栏 file-backed 记账、CHANGELOG、完成报告补轮。

## 5. 实现清单（iOS 仓）

1. `isLiveOnlySessionList` 移除 `.deepSeek`（含 `liveOnlySessionListHint` 与四处调用点；空态回退通用「暂无会话」语义；保留「列表返回 not_supported → 空态不报错」通用兜底）。
2. `session_resume_not_supported` 错误码 → 文案映射（「当前 DeepSeek SDK (0.1.0-rc.6) 不支持恢复已结束的会话；可发起新会话」语境）。
3. `LiveOnlySessionListTests` 改写为 store-list 语义；mirror 协议文档同步。测试仅 `-only-testing:CCCodeTests`。

## 6. 测试与构建

- Mac 单测：store（projectKey 向量对照 TS 逐例：普通路径/分隔符运行/`~`/`.`/`..`/空→root；zstd+plain fixture 测试内生成；标题折叠/fallback/预算截断；subagent 过滤；排序与 directory 字段）；rich history 黄金样本（自真实会话提炼小型 fixture）；hydrate dsh pathless 用例（死会话 file 基线达 ready / store 无 id → not_found / re-open 重建）；发送守卫三态；全量回归。
- 构建：`go build ./... && go test`（root 模块路径纪律）→ iOS `xcodebuild build` + `CCCodeTests` → Release 覆盖安装 `/Applications` + 重启核对（端口 8777 归属 /Applications、进程、日志）。

## 7. 约束合规

- 初衷：读取**用户自己的** `~/.dsh/sessions`（复用用户 harness 的存储与标题事件，零迁移、不自建真相源；等价 claudecode transcript 桥接模式）；不代装、不写入用户 store 之外的私有体系（写路径维持 `624c6a4` 形态）。
- SSV2 护栏：dsh 保持声明制 v2（capability 已广告）；死会话投影来自**真实文件数据**（非空壳）；not_found 仅用于 store 无此 id；iOS 零裁判（无超时收口/无 ownership 补丁/无 fallback）；hydrate 走既有 Kernel 事务域（pathless machinery）。
- 工程硬边界：SDK pin 不动、vendor 不改、真实 key turn 属 owner 授权（本计划 mock 测试）。

## 8. 验收（owner 真机，扩展矩阵——替代原 6 行，原行保留）

| # | 动作 | 应看到 |
|---|---|---|
| 1 | iPhone 切 DeepSeek 模式 | 列表显示 Mac 端会话（含 dsh web 会话，标题/目录正确） |
| 2 | iPhone 新建会话发消息 | 正常聊天，且会话出现在列表 |
| 3 | 点开任一已结束会话 | 完整历史渲染（含 thinking/工具调用） |
| 4 | 在已结束会话内发消息 | 诚实错误提示（SDK 不支持恢复），不卡死、不覆写 |
| 5 | 活会话续聊（本 bridge 内） | 正常续聊、流式、停止可用 |
| 6 | 发带图片的消息 | 提示不支持该附件 |
| 7 | 重启 Mac 端 CordCode Link 后重开任一会话 | 历史可见（file-backed hydrate） |
| 8 | 退出重开 iPhone App | 列表与历史一致 |

## 9. 边界与未来项

- dsh web 运行中会话的实时增量（文件 tail / SSE）→ 未来项。
- 死会话续聊（等 SDK 暴露 resume 或 owner 决定 re-pin）→ 未来项。
- prompt-known-id 恢复实验：**已由源码取证否定**（§2.1），不再实验。
