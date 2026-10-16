# Claude programmatic session 过滤与 API error 通知修复方案

> 状态：**done / owner-accepted**。v4 方案已实施、测试、Release 部署并提交为 `135ba20ada5479719eebc48065d83791dd370e20`；owner 于 2026-10-16 手动确认 CordCode App 的 Claude Code session 列表恢复正常，问题 session 已消失。
>
> 本方案将此前偏复杂的 plugin 专用 prompt 匹配方案，收敛为官方 TypeScript SDK 已公开的 `listSessions({ includeProgrammatic: false })` 语义，并单独修复 Claude synthetic API error 的终态归类。
>
> **v4 改动范围（第 3 轮评审 F-12 采纳，逐条核实后处置）**：D-1 为 `entrypoint` 增加文件尾 64KB 回退（对齐官方 `nl(head) ?? jt(tail)` 双窗口；`sessionKind` 仍只用 head，与官方一致）——本机 22 条 programmatic 中 3 条大文件 reviewer（`0296f9ef`/`1eba385c`/`a9a158a0`，1.25–1.9MB，均为无 401 的成功安审 reviewer）head 512KB 内零 `"entrypoint"` 子串、仅 tail 可达，v3 的 head-only 规则会漏隐藏 3/22 并使 E-8 基线 22 必假；删除 v3「两条路径实测收敛」的虚假断言并重写差异清单（F-12）；§2.5 补字节偏移 census（16 条 head 可达 + 3 条 tail-only，可达性分界是首条 user 行字节数而非行号）；S-1 fixture 补 entrypoint 双窗口用例；§7 补大文件隐藏行；§8 风险表「大文件」行改为 tail 回退兜底。修订时已以本机 39 文件模拟 v4 双窗口规则：22 programmatic / 17 visible，与官方 SDK 0.3.301 实测（hidden=22、prog:false=17）逐项一致。

## 1. 摘要与范围

### 1.1 现状问题

2026-10-16 的 iPhone 截图与本机取证确认了两个相互关联但独立的问题：

1. **Claude session 列表污染**：`security-guidance` 插件通过 Python Agent SDK 启动的 commit 安审 reviewer 会在 `~/.claude/projects/<project>/` 写入新的 top-level JSONL transcript。CordCode 的 Claude compatibility catalog 直接扫描这些文件，因此把内部 reviewer 当成用户交互 session 展示。
2. **API error 被误报为完成**：失败 reviewer transcript 里有 `isApiErrorMessage=true`、`apiErrorStatus=401` 的 synthetic assistant 行，但 Claude mapper 只看 final `stop_reason`，把它映射为 `turn_completed`。Web Push 层随后生成“Claude code 任务已完成 / Failed to authenticate...”的矛盾通知。

官方 Claude App / VSCode session picker 不显示这些 reviewer session，不是因为磁盘上没有 transcript，而是因为它们属于 programmatic/headless session。官方 TypeScript SDK 已把该可见性规则公开为 `listSessions({ includeProgrammatic: false })`。

### 1.2 目标

| ID | 需求 |
| --- | --- |
| R-1 | Claude 默认 session 列表对齐官方 IDE session picker 的 `includeProgrammatic:false` 语义，隐藏 SDK/daemon 内部 session。 |
| R-2 | 被隐藏的 programmatic transcript 仍保留在磁盘，并可通过明确 session ID 的 detail 路径读取；默认列表和默认通知不得呈现它们。 |
| R-3 | Claude synthetic API error 行必须收口为 `turn_error`，不得产生 assistant 正文、`turn_completed` 或 completion Web Push。 |
| R-4 | 真实用户交互 session 的可见性不因标题、prompt 内容或缺少 custom title 被误过滤。 |
| R-5 | 修复范围限于 MacBridge Go runtime 与测试；不引入 Node/Python runtime 依赖，不改 iOS 协议。 |
| R-6 | 定向验证覆盖 catalog、Web Push watcher、Claude mapper/live relay 三条路径，并按仓库规则完成 Release 构建部署。 |

### 1.3 非目标

- 不修复或重新启用 `security-guidance 2.0.7` 的 Desktop bearer token 认证问题；该插件继续 disabled。
- 不删除、移动或改写历史 `~/.claude/projects` transcript。
- 不把 Claude App / VSCode 的私有 session store 变成 CordCode 的唯一 catalog 真值。
- 不给 MacBridge 增加 Node 或 Python sidecar 进程。
- 不启用 `WebPushKindError`；该通知类别仍受真实样本门约束。
- 不跑 UI test、snapshot test、simulator automation 或 agent-device。

## 2. 来源与现状调查

### 2.1 来源身份

| 来源 | 身份 | 状态 |
| --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `6ef1a10dacb258ba389162a5c87262e253d01f1b` | 本文首次编辑前干净 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline`，branch `feat/ios-native-message-timeline`，commit `02a46521678950fd1df47ac951505f3dccba0d4e` | 本文首次编辑前干净 |
| TypeScript SDK | `@anthropic-ai/claude-agent-sdk@0.3.301`；实现/类型锚定 **npm 包内 `sdk.mjs`/`sdk.d.ts`**（registry tarball 流式提取核对）。公开 repo `anthropics/claude-agent-sdk-typescript` 在 main commit `b0fc766997199a49f90c37f5ec056936c80c9491`（2026-10-15T19:21:55Z）仅含 docs/examples/scripts，无 `src/` 与 `sdk.d.ts`——repo 不是实现锚点，只作发布时间旁证；npm 精确 publish 时间未核 | npm 官方包实现/类型与本机行为实测 |
| Python SDK | repo main commit `aada02405ede575d7cbf8e6851a8179762560816`；本机 wheel/version `0.2.168` | 公开 API 与行为实测 |
| security-guidance | 本机 installed `2.0.7`，venv `claude-agent-sdk 0.2.137`，bundled CLI `2.1.229` | 继续禁用，不作为修复依赖 |

MacBridge 与 iOS 是当前任务唯一配对的同功能分支族；本文不形成 iOS 源码修改结论。实施开工前必须按 P0 来源门重新生成路径、分支、完整提交和 dirty 清单。

### 2.2 官方公开 API 调查

官方公开方法不是 `getSessions(projectName)`，而是：

```ts
import { listSessions } from "@anthropic-ai/claude-agent-sdk";

const sessions = await listSessions({
  dir: "/absolute/path/to/project",
  includeWorktrees: true,
  includeProgrammatic: false,
});
```

`@anthropic-ai/claude-agent-sdk@0.3.301` 的公开 `sdk.d.ts` 明确规定：

- `dir` 是项目目录路径，不是项目显示名；
- `limit` / `offset` 分页；
- `includeWorktrees` 默认 `true`；
- `includeProgrammatic` 默认 `true`，原因是 SDK 消费者需要枚举自己创建的 session；
- **IDE session picker 传 `false`**，以对齐 terminal `/resume`；
- 该开关只作用于本地文件系统读取，`sessionStore` 模式忽略它。

官方包实现的分类规则也已核对：

```text
entrypoint ∈ { sdk-cli, sdk-ts, sdk-py }
或
sessionKind ∈ { daemon, daemon-worker }
```

满足任一条件即为 programmatic/headless session，在 `includeProgrammatic:false` 下排除。

### 2.3 行为实测

用官方 TypeScript SDK `0.3.301` 对当前 iOS 项目目录执行：

```ts
listSessions({ dir, limit: 100, includeProgrammatic: true })
```

返回：

- 正常主 session `7afc3bd7-c69b-4582-85cf-556fa4f54d69`
- `security-guidance` reviewer：
  - `b19521e7-dfe2-4df8-9ebc-d75161e8aa92`
  - `9c5c888d-aa08-45f3-8efb-52128dabc769`
  - `1121093c-18d2-4bc0-abb2-a327fd4dec20`

再执行：

```ts
listSessions({ dir, limit: 100, includeProgrammatic: false })
```

上述 reviewer session 全部消失，正常主 session 保留。这与 Claude App / VSCode picker 的观察一致。

Python SDK `0.2.168` 虽有 `list_sessions(directory=...)`，但没有 `include_programmatic` 参数；对同一项目实测会返回上述 `sdk-py` reviewer。因此不能把 Python SDK 视为完整官方等效实现，也不能给 Go runtime 增加 Python sidecar；应在 Go catalog 中移植 TypeScript SDK 已公开的 programmatic 分类语义。

### 2.4 CordCode 现有实现

| 能力 | 当前锚点 | 现状 |
| --- | --- | --- |
| Claude catalog 候选来源 | `go-bridge/claude_session_catalog.go:5`、`:276-347` | 扫描 `~/.claude/projects` 下 top-level JSONL；这是无 public Claude catalog API 时的 compatibility 实现。 |
| metadata 扫描 | `go-bridge/handlers.go:4282` | 已有限读取 transcript 开头并解析 title/model/time；尚未保留 `entrypoint` / `sessionKind`。 |
| session 输出 | `go-bridge/claude_session_catalog.go:209-221` | 直接输出 `snapshot.Sorted`。 |
| Web Push watcher | `go-bridge/claude_web_push_watcher.go:93-137` | 遍历 `snapshot.Sorted`；programmatic session 若进入 Sorted 就会被 tail。 |
| Claude transcript mapper | `go-bridge/handlers_relay.go:2206-2451` | assistant final stop 一律映射 `turn_completed`；未解析 synthetic API error 字段。 |
| 错误终态 reducer | `go-bridge/projection_reducer.go:2481-2521` | 已支持 `turn_error`，并把 `message` 保留为 system diagnostic。 |
| iOS `turn_error` 解码 | iOS `CCCodeBridgeBackendClient.swift` | 已支持，无需协议或客户端改动。 |

### 2.5 生产样本

当前项目共有 19 条 `security-guidance` reviewer transcript：

- 11 条认证失败，其中 2026-10-16 下午 5 条对应截图中的重复 401；
- 8 条审查成功，标题形如 “I’ll read ...”；
- 全部为 `entrypoint:"sdk-py"`、`promptSource:"sdk"`、`isSidechain:false` 的 top-level JSONL；
- 均无 `<parent-session>/subagents/` 目录；
- `sessionKind` 在本项目全部 39 个 transcript 中零出现——`daemon`/`daemon-worker` 分类本机不可由样本观测，只能由官方 `sdk.mjs` 实现支撑（已支撑，见 §3.1），对应分支由单测 fixture 覆盖（见 E-8）；
- 正常主 session `7afc3bd7` 自身含 5 行 `isApiErrorMessage:true`、`apiErrorStatus:429` 的 synthetic assistant 行——交互 session 内出现 synthetic API error 是既有事实而非假设，D-4 的收口会改变这些行的投影（见 §3.4）。
- 同项目另含 3 条 `entrypoint:"sdk-cli"` programmatic session（`7310d850-d057-44ff-a6fe-e9ca1961bdce` / `a84cd7e8-e34d-422e-9dd3-d4a6bde761cb` / `f450f935-18ce-4d09-83de-0b3a192ec766`，ZCode vision-agent 识图委托，首条 user 均为「请直接用 Read 工具读取图片文件…」）——`sdk-cli` 同属官方隐藏集合，修复后一并从默认列表消失。本项目将被隐藏的 programmatic session 合计 **22 条**（19 `sdk-py` + 3 `sdk-cli`）；39 个 top-level transcript 的完整分布为 `sdk-py`×19、`claude-desktop-3p`×15、`sdk-cli`×3、`claude-desktop`×2。
- **entrypoint 字节可达性 census**：19 条 `sdk-py` 中 16 条的顶层 `entrypoint` 行在 512KB head 内可达；**3 条（`0296f9ef-02a6-4e5b-ab0c-15d893cb55a4`（1,906,241B）、`1eba385c-6c5f-439d-9c7a-919419b36ae4`（1,260,292B）、`a9a158a0-9794-4532-b855-873beb32b6e7`（1,251,451B），均为无 401 的成功安审 reviewer）的 head 512KB 内连 `"entrypoint"` 子串都不存在**——巨型首条 user 行（约 421–466KB）把带 entrypoint 的行推出窗口，仅文件尾 64KB 可取到（官方 `jt` tail 回退即兜住它们，官方 SDK 实测 hidden=22 含这 3 条）。entrypoint 的可达性分界是首条 user 行的字节数，不是行号；D-1 因此采用 head ∪ tail 双窗口（见 §3.1）。

这些样本证明需要过滤的是官方 SDK 定义的 programmatic session 类别，而不是“包含 401 的 session”或“标题像 subagent 的 session”。

## 3. 设计

### 3.1 D-1：移植官方 programmatic session 分类器

在 Claude metadata scan 中新增：

```go
Entrypoint  string
SessionKind string
```

解析规则（逐字对齐官方 `sdk.mjs` `0.3.301` 的 `YD` 分类器）：

1. 在现有 bounded head scan（512KB，`claudeSessionSummaryReadLimit`，handlers.go:59）中逐行读取 top-level JSON 字段；
2. `entrypoint`（对齐官方 `nl(head) ?? jt(tail)` 双窗口）：
   - 取 head 文本中**首次出现**的顶层 `entrypoint` 字段值；首次命中值为空串时**即停**——视同无 entrypoint，不回退、不续扫后续出现（对齐官方 `""` 非 nullish 不触发 `??` 回退的行为）；
   - head 顶层未命中时，读取**文件尾 64KB**（对齐官方 tail 窗口 `dn=65536`），跳过首个可能被截断的行，逐行顶层解析取**最后一次出现**的 `entrypoint`（对齐官方 `jt` 的最后命中语义）；
   - **tail 只服务 `entrypoint` 回退**：`sessionKind` 不读 tail（官方 `sessionKind` 仅来自 head，见规则 3）；
   - 本机依据：22 条 programmatic 中 3 条仅 tail 可达（§2.5），双窗口下 22 条全部可提取——修订时以本机 39 文件模拟本规则，得 22 programmatic / 17 visible，与官方 SDK 实测（hidden=22、prog:false=17）逐项一致；
3. `sessionKind`（对齐官方 `YD` 的三分支）：
   - head 内**存在**包含 `"parentUuid":` 子串的行（含 `"parentUuid":null` 行）时，在首个这样的行内读取 `sessionKind`，该行读不到即判非 daemon，不跨行继续找非空值；
   - head 内**不存在**任何包含 `"parentUuid":` 的行时，回退到**整个 head 文本**读取 `sessionKind` 首次出现（对齐官方 `e.split('\n').find(s => s.includes('"parentUuid":')) ?? e`——`?? e` 中的 `e` 即整个 head 字符串，而非「判非 daemon」）；
   - 读到的值为 `daemon` / `daemon-worker` 时判 programmatic，其余值（含读不到）判非 daemon；
4. 不根据 title、prompt、model、目录名或 message 内容猜测。

与官方实现的差异与边界声明（显式列出，避免 oracle 对账歧义）：

- **扫描窗口**：官方 `entrypoint` 覆盖窗口 = 文件头 64KB（首次，`nl`）∪ 文件尾 64KB（最后一次，`jt`）；Go = 文件头 512KB（首次顶层）∪ 文件尾 64KB（最后一次顶层，仅 head 未命中时读取）。Go 窗口 ⊇ 官方窗口（head 512KB ⊃ 64KB、tail 同窗），窗口维度不存在「官方取得到、Go 取不到」的缺口；`entrypoint` 的全部出现落在 `(512KB, size−64KB)` 中段时两侧一致地取不到、保持可见（fail-open，不按标题猜测）。本机 22 条 programmatic 在双窗口下全部可达（3 条经 tail 回退，§2.5）。
- **提取层**：官方 `nl`/`jt` 是原生子串匹配（`indexOf`），可命中嵌套 JSON 键（如 tool_use input 内作为 JSON 结构出现的 `"entrypoint":"sdk-py"`）；Go 在 head 与 tail 两侧都按行做**顶层** JSON 解析，只命中顶层键（tail 首行可能被截断，解析前跳过）。衍生分叉仅属构造性——如「head 内只有嵌套出现」时官方子串命中即不回退、Go 顶层未命中会回退 tail；本机 22 条经顶层解析全部命中，未依赖任何嵌套匹配。
- **空串即停**：head 首次命中值为空串时两侧都即停、不回退 tail（`""` 非 nullish 不触发 `??`）。

新增纯函数：

```go
func isClaudeProgrammaticSession(entrypoint, sessionKind string) bool {
	switch entrypoint {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return true
	}
	switch sessionKind {
	case "daemon", "daemon-worker":
		return true
	}
	return false
}
```

该集合必须与官方 TypeScript SDK `0.3.301` 的公开语义逐项对齐。未来扩展集合时，先核对当时官方 SDK 的 `includeProgrammatic` 实现，再更新 Go 常量和测试；不得因为看到未知 `entrypoint` 就自行归类。

### 3.2 D-2：catalog 默认隐藏 programmatic session，但保留缓存身份

`claudeSessionIndexEntry` 与 `claudeSessionScanResult` 增加：

```go
Programmatic bool
```

构造 snapshot 时：

1. `nextByKey` 保留所有物理 transcript entry，包括 programmatic entry 与 archived entry；
2. `Sorted` 相对现状**仅新增一条**过滤：候选先按 `Programmatic==false` 过滤，再交给既有 fork/compact lineage 处理。archived 条目维持现行契约——照常进入 `Sorted` 并经 `claudeSessionEntryToWire` 下发 `archivedAtMillis`，由客户端/消费侧隐藏（现行 `buildSnapshot` 本就把含 archived 的全部条目放进 `Sorted`，claude_session_catalog.go:401-404；隐藏是客户端行为，session_discovery.go:483-497）。本方案不改变 archived 可见性行为；
3. 过滤顺序定死：`Programmatic==false` 过滤在 fork/compact lineage **之前**应用——lineage 分组只见非 programmatic 条目，lineage 的 `delete(byKey, …)`（claude_session_catalog.go:487/:557）不可能触及 programmatic 条目。由此（a）programmatic 条目不会因 lineage 隐藏而离开 `ByKey`，缓存保留目标不被破坏；（b）programmatic 分支不可能成为 fork primary 而把真实分支从 `ByKey`/`Sorted` 删除；
4. 不把 programmatic entry 从 `ByKey` 删除。

理由：

- `list_sessions`、session discovery、recent view、Web Push watcher 与 pin 解析（`resolveClaudePin`，handlers_session_pin.go:224-236，经 `list()` 间接消费 `Sorted`）都消费 `Sorted`，过滤 Sorted 即统一得到官方 IDE picker 语义；
- 已声明的用户可见后果：先前误 pin 的 programmatic session（如 reviewer）退出 `Sorted` 后，pin 解析按「not in catalog => definitively gone」处理（handlers_session_pin.go:235-236）——该 pin 被判定为会话不存在；transcript 文件保留、明确 session ID 的 detail 读取不受影响；
- 保留 `ByKey` 让 fingerprint 未变的 programmatic transcript 继续命中缓存，避免 3 秒 Web Push sweep 反复解析隐藏文件；
- 明确 session ID 的 detail/get 路径仍可通过独立文件查找读取 transcript，不需要默认列表先展示它。

`subagentsAggregateDigest` 继续只遍历 `Sorted`，因此隐藏 session 的 subagent churn 不会触发可见 catalog discovery 广播。

### 3.3 D-3：Web Push watcher 采用同一可见性集合

`claudeWebPushWatcher.sweep` 当前遍历 `snapshot.Sorted`。D-2 生效后：

```text
programmatic session ∉ Sorted
→ watcher 不建立 tail state
→ 不读取新增行
→ 不产生 completion/error candidate
```

这不是用标题或 401 文本过滤通知，而是复用官方 session picker 的类别边界：默认对用户不可见的内部 programmatic session 也不产生默认通知。

需要补防御性测试：即使 fixture 的 synthetic 401 行存在，`sdk-py` session 也不能产生 WebPushCandidate。

### 3.4 D-4：synthetic API error 转 `turn_error`

扩展 `claudeTranscriptRelayEntry`：

```go
IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
APIErrorStatus    int    `json:"apiErrorStatus"`
Error             string `json:"error"`
RequestID         string `json:"requestId"`
```

在 assistant 分支进入普通 content 处理之前判断：

```text
type == assistant
&& isApiErrorMessage == true
```

满足时：

1. 不 emit `text_delta`；
2. 不把 synthetic 文本当 assistant 回复；
3. 不 emit `turn_completed`；
4. emit：

```go
projectionHydrateEvent{
	Event: "turn_error",
	Data: map[string]interface{}{
		"turnId":         turnID,
		"message":        errorMessage,
		"reason":         "claude_api_error",
		"apiErrorStatus": status,
		"requestId":      requestID,
	},
	TurnDone: true,
}
```

`errorMessage` 的优先级：

1. assistant text block 拼接后的非空文本；
2. top-level `error`。

该设计同时服务 cold hydrate / source batch、interactive file relay、unopened-session Web Push preview 三条路径。即使某个真实用户交互 session 发生 API 认证失败，它也会在投影中收口为 error，而不是伪装成功完成。

### 3.5 D-5：live relay 控制面识别错误终态

现有 `deliverClaudeLiveControlPlane` 只把 `turn_completed` 视为 idle 信号；legacy `deliverClaudeLegacyRow` 对 default event 保持 running。需要补齐：

- `turn_error` 后调用 `broadcastIdleState`；
- `runningObserved=false`；
- held terminal 逻辑不得把 API error 当“等待正文”的 completion 挂起；
- raw frame / projection patch 照常发送 `turn_error`。

Web Push producer 只对 `turn_completed` 开启 completion gate，因此 D-4 生效后 `turn_error` 不会生成 completion push。

## 4. 决定

| ID | 决定 | 推荐 | 状态 |
| --- | --- | --- | --- |
| OD-1 | programmatic session 是否默认显示？ | 不显示，对齐官方 `includeProgrammatic:false`；保留明确 ID detail 能力。 | decided，来源为 owner 要求借鉴官方公开方法与官方 SDK 语义 |
| OD-2 | 过滤依据用 prompt/title 还是结构化字段？ | 只用 `entrypoint` / `sessionKind`；prompt 仅作测试样本，不进生产判断。 | decided |
| OD-3 | 隐藏 session 是否保留在 snapshot cache？ | 保留 `ByKey`，不进 `Sorted`，避免重复解析。 | decided |
| OD-4 | synthetic API error 是否发 error push？ | 不发；映射 `turn_error` 供打开 session 时诊断。`WebPushKindError` 仍保持样本门关闭。 | decided |
| OD-5 | 是否调用 Node/TS SDK 作为 runtime sidecar？ | 不调用；移植官方语义到 Go，并用官方 SDK 行为做开发期 oracle。 | decided |

## 5. 证据与实施前置门

| ID | 命题 | 证据状态 | 缺项时阻塞 |
| --- | --- | --- | --- |
| E-1 | 官方 TS SDK `0.3.301` 公开 `listSessions({ dir, includeProgrammatic:false })`。 | verified：官方 `sdk.d.ts` 与 npm 包 | S-1、S-2 |
| E-2 | 官方 programmatic 分类集合是 `sdk-cli/sdk-ts/sdk-py` 与 `daemon/daemon-worker`。 | verified：官方包实现与类型说明 | S-1 |
| E-3 | 当前 `sdk-py` reviewer 在 `includeProgrammatic:true` 出现、`false` 消失，正常 session 保留。 | verified：本机官方 SDK 行为实测 | S-2 |
| E-4 | Python SDK `0.2.168` 无对应过滤参数。 | verified：源码与行为实测 | 阻止采用 Python SDK 作为“官方等效”实现 |
| E-5 | CordCode catalog/Web Push 当前消费 `Sorted`。 | verified：源码 | S-2、S-3 |
| E-6 | 实际 401 行携带 `isApiErrorMessage/apiErrorStatus`。 | verified：本机 transcript 样本 | S-4 |
| E-7 | reducer 与 iOS 已支持 `turn_error`。 | verified：源码 | S-4、S-5 |
| E-8 | 修改后官方 SDK oracle 与 Go catalog 在当前项目上的隐藏集合一致。 | pending：实施后运行对照 | S-6 完成门 |

E-8 最小对照（测量口径为可机械执行的集合运算）：

1. 官方 TS SDK 对同一 `dir` 执行 `includeProgrammatic:true` 与 `false` 两组查询，**两组都必须 `includeWorktrees:false` 且全量枚举**（limit ≥ 全集，或 offset 分页取到返回数 < limit 为止，不得截断）：
   - `includeWorktrees:true` 会把同仓兄弟工作树项目目录的 session 并入本 dir 清单——本机修订时实测（官方 SDK 0.3.301 复跑）：worktree 口径下全集 139、hidden=30，其中 8 条来自兄弟目录 `-Users-jacklee-Projects-cordcode-ios`，而 Go catalog 从不枚举兄弟目录，`GoHidden == TSHidden` 在该口径下必假；
   - `limit:100` 在全集 139 下会截断（§2.3 原问法即此形态），分页取全后不复存在；
   - 非 worktree 全量口径实测：39（prog:true）/ 17（prog:false），`TSHidden = IDs(true) − IDs(false)` = 22；
   - 含 worktree 的两组数同时留档备查，但**不进入断言**；
2. Go 侧在同一 snapshot 上测量：`GoVisibleAll` = programmatic 过滤关闭时 `Sorted` 的 SessionID 集合，`GoVisibleDefault` = 过滤开启（默认）时 `Sorted` 的 SessionID 集合（实施时以测试内开关或双扫描实现，不进生产路径），`GoHidden = GoVisibleAll − GoVisibleDefault`；
3. 断言 `GoHidden == TSHidden`，本机基线期望值为 **22**（19 `sdk-py` + 3 `sdk-cli`，§2.5；其中 3 条大文件 reviewer 的 `entrypoint` 仅 tail 64KB 可达，基线 22 依赖 D-1 的 head ∪ tail 双窗口提取规则），主 session `7afc3bd7` 必须保留在 `GoVisibleDefault`。fork/compact lineage 与 archived 在两次测量中同样作用、不进入差集（口径不使用 `ByKey − Sorted`，避免混入 lineage 隐藏与缓存语义）；若仍出现额外差异，逐项记录并解释；
4. 本机样本 `sessionKind` 零出现（§2.5），该对照只能覆盖 `entrypoint` 分支；`daemon`/`daemon-worker` 分支由 S-1 单测 fixture 覆盖，不依赖本机活体样本。

该对照是开发/实施验证证据，不把 Node SDK 加入生产依赖。

## 6. 实施切片

| 切片 | 可见结果 | 组件与复用方式 | 前置条件 | 最小验证 | 失败处理 |
| --- | --- | --- | --- | --- | --- |
| S-1 | Go 有官方 programmatic classifier | `handlers.go` metadata parser + 纯函数 | E-1、E-2 | 单测覆盖 5 个官方 entrypoint/kind、未知值、空值；sessionKind 三分支：首个含 `"parentUuid":` 行内命中 / 该行无 `sessionKind` 判非 daemon / head 无 `"parentUuid":` 行时回退整个 head 命中；entrypoint 双窗口：head 顶层命中时不读 tail / head 未命中时 tail 64KB 最后一次出现回退命中（含首行截断跳过）/ head 命中空串即停不回退 / sessionKind 不读 tail | 未知值保持 false，不猜测 |
| S-2 | 默认列表隐藏 programmatic session | scan result → index entry → `Sorted` | S-1、E-5 | `sdk-py` 隐藏；`claude-desktop` / `claude-vscode` / 普通 CLI 保留；archived 条目仍出现在 list wire 并携带 `archivedAtMillis`（回归锁定现行契约，F-1）；programmatic 先于 lineage 过滤的 fixture：programmatic 与真实分支同 customTitle/FirstUserAt 构成 fork 对时，真实分支保留、programmatic 仍在 `ByKey`（F-5）；大文件缓存命中测试 | 保留文件与 ByKey；只回退过滤变更 |
| S-3 | 默认 Web Push 不监听 programmatic session | watcher 继续消费过滤后的 `Sorted` | S-2 | `sdk-py` + `turn_completed` fixture，断言 0 candidate | 不改 push payload；暴露测试失败 |
| S-4 | synthetic API error 映射 `turn_error` | transcript struct + `claudeEntryToProjectionEvents` | E-6、E-7 | mapper 单测：无 `text_delta`、无 `turn_completed`、有 message/status/requestId | fail closed 保留诊断，不降级为 completion |
| S-5 | live relay 错误终态 idle | `deliverClaudeLiveControlPlane`、legacy row 分支 | S-4 | file relay 单测：error 后广播 idle，不挂起等待正文 | 控制面测试失败则不部署 |
| S-6 | 官方 oracle 对账 | 一次性开发命令，不进生产依赖 | S-2、E-1、E-3 | Go 隐藏集合 == TS SDK true/false 差异集合 | 记录差异并停止交付 |
| S-7 | 文档、测试与 Release 交付 | `GO_BRIDGE_ARCHITECTURE.md` 补规则；定向测试；Release 构建 | S-1..S-6 | `git diff --check`、定向 test、Release build、正式安装重启 | 保留失败日志，不伪造部署成功 |

### 6.1 定向测试命令

```bash
go test ./go-bridge -count=1 \
  -run 'TestClaude.*Programmatic|TestClaudeSessionCatalog|TestClaudeWebPushWatcher'

go test ./go-bridge -count=1 \
  -run 'TestClaude.*APIError|TestClaudeEntryToProjectionEvents|TestClaudeSessionFileRelay'
```

如实际测试名更具体，以实际测试名为准；上述命令表示风险分组，不要求逐字创建同名测试。

### 6.2 构建与部署纪律

本修复属于 D3 runtime 行为改动，不做全量测试。实施完成后：

1. 重新记录来源清单；
2. 运行两组定向 Go 测试；
3. 构建 unsigned Release；
4. 覆盖安装 `/Applications/CordCodeLink.app`；
5. 同时终止 `CordCodeLink` 与 `cordcode-bridge-runtime`（先 `killall CordCodeLink` 再 `pkill -f cordcode-bridge-runtime`，两进程名不同，前者匹配不到后者）；
6. 确认 8777 由新 runtime PID 监听；
7. 进程代际核对：`ps -o pid,ppid,lstart -p <runtime-pid>` 的启动时间必须晚于本次构建时刻；
8. 新版本特征输出：实施时新增一条本次提交独有的 INFO 日志行（catalog 扫描后输出被隐藏的 programmatic session 计数，如 `claude catalog programmatic hidden: N`），部署后日志必须出现该行——“日志仍在滚动”不算证据；隐藏集合生效须以日志/列表行为等运行态证据确认，`strings`/`-version` 等产物校验不能替代；
9. 不运行 UI automation。

## 7. 验证矩阵

| 场景 | 期望 |
| --- | --- |
| `entrypoint=sdk-py` security reviewer | 默认列表不显示；Web Push 不监听；transcript 文件保留 |
| `entrypoint=sdk-ts` / `sdk-cli` | 同上 |
| `sessionKind=daemon` / `daemon-worker` | 同上 |
| `entrypoint=claude-desktop` / `claude-vscode` / 正常 CLI | 默认列表保留 |
| 大文件成功安审 reviewer（`entrypoint` 仅 tail 64KB 可达，如 `0296f9ef` 形态，head 512KB 内零 `"entrypoint"` 子串） | 经 tail 回退判定 programmatic，默认列表隐藏 |
| 用户 session 首条 prompt 恰好是 “Review this change...” 但 entrypoint 不是 SDK | 保留，不用 prompt 猜测 |
| programmatic transcript 写入 401 synthetic row | 不产生任何 completion push |
| 交互 session 写入 401 synthetic row | reducer 收口 `turn_error`，执行态回 idle，无 completion push |
| 交互 session 正常 final assistant | 既有 `turn_completed` 行为不变 |
| archived/fork/compact session | 既有 lineage 规则继续运行；programmatic 不参与 fork 猜测 |
| archived session（`archivedAtMillis` 非零） | 仍出现在 list wire 并携带 `archivedAtMillis`，由客户端隐藏——回归锁定现行契约，本方案不改变 |
| programmatic 与真实分支构成 fork 对（同 customTitle/FirstUserAt） | programmatic 先被过滤、不参与 lineage 分组；真实分支保留在 `Sorted`/`ByKey`，programmatic 条目保留在 `ByKey` |

## 8. 风险与回退

| 风险 | 处理 |
| --- | --- |
| 官方未来扩大 programmatic entrypoint 集合 | Go 常量与官方 SDK oracle 对账绑定；未知值默认可见，不猜测 |
| 大文件 head 读不到 entrypoint（本机 3/22 条即此形态：首条 user 行约 421–466KB） | tail 64KB 回退兜住（官方同款双窗口，§3.1）；head 与 tail 都读不到时保持可见而不是按标题隐藏。测试覆盖 head 超窗 + tail 命中 fixture |
| `ByKey` 保留隐藏项后 snapshot 语义漂移 | 注释明确：`ByKey` 是缓存/身份集合，`Sorted` 是默认可见集合；测试锁定 |
| API error 行出现在 user 之前 | 以 assistant 自身 identity 兜底 turnId；仍 emit `turn_error`，不编造 completion |
| iOS 对 `turn_error` projection patch 有未知展示缺口 | 本方案只修 Mac 终态语义；若发现 iOS 既有解码/渲染缺口，另立任务，不引入假成功 |
| plugin 重新启用后仍 401 | 不属本修复；继续禁用 plugin，等待上游认证修复 |

回退边界：

- 回退 Go catalog/filter 与 mapper 相关 commits 即可；
- 不删除用户 transcript；
- 不需要数据迁移；
- Web Push subscription store 和 ledger 不变。

## 9. 完成标准

1. 当前 iPhone Claude session 列表不再出现 5 条重复 401 reviewer 和 “I’ll read...” 安审 reviewer；同批消失的还有 3 条 `sdk-cli` 识图委托 session（官方语义内预期隐藏，非缺陷，见 §2.5），owner 核对列表时不应视为多隐藏；
2. 正常主 session `7afc...` 仍显示；
3. 定向 Go 测试通过；
4. 官方 TS SDK oracle 对账通过；
5. 新 synthetic 401 fixture 不产生 WebPushCandidate；
6. Release runtime 部署并确认新进程代际（`lstart` 晚于本次构建时刻）与本次提交独有的隐藏计数日志行；
7. `security-guidance` 保持 disabled，不把插件认证问题误报为已修复。

## 10. 修订记录

| 版本 | 日期 | 变更 |
| --- | --- | --- |
| v1 | 2026-10-16 | 初版：以官方 TypeScript SDK `includeProgrammatic:false` 语义替代 plugin prompt 专用过滤；同时修复 synthetic API error 终态。 |
| v2 | 2026-10-16 | 按第 1 轮评审（F-1..F-6 全部采纳，逐条核实后处置）：D-2 `Sorted` 输出条件改为「相对现状仅新增 `Programmatic==false` 过滤」并定死先于 fork/compact lineage 应用，archived 维持现行 `archivedAtMillis` wire 契约，§6/§7 补 archived 回归与 lineage 顺序断言（F-1/F-5）；D-1 entrypoint/sessionKind 提取规则逐字对齐官方 `sdk.mjs`（含首次命中与单行读取语义），声明 512KB head vs 官方 64KB head+tail 的窗口差异（F-2）；§2.1 TS SDK 来源表注明实现/类型锚定 npm 包 `sdk.mjs`/`sdk.d.ts`、公开 repo 仅 docs/examples、npm publish 时间未核（F-3）；E-8 改为可机械执行的集合运算口径（`Sorted` 过滤开关差集），声明 daemon 分支本机无样本、由单测覆盖（F-4）；§6.2 补 `ps -o pid,ppid,lstart` 代际核对与本次提交独有的隐藏计数日志行（F-6）；§2.5 补本轮核实的样本事实：主 session `7afc3bd7` 自身含 5 行 `isApiErrorMessage:true`/429，`sessionKind` 本机零出现。 |
| v3 | 2026-10-16 | 按第 2 轮评审（F-7..F-11 全部采纳，逐条核实后处置）：D-1 规则 3 修正为官方 `YD` 三分支——head 无 `"parentUuid":` 行时回退整个 head 文本搜 `sessionKind`（v2 把 `?? e` 误读为「判非 daemon」，与官方相反），S-1 fixture 补该分支用例（F-7）；E-8 钉死 `includeWorktrees:false` + 全量枚举（limit/offset 取全），写入本机基线 22——修订时官方 SDK 0.3.301 复跑：非 worktree 口径 39/17、TSHidden=22，worktree 口径 139/109、hidden=30 含 8 条兄弟目录增量，`limit:100` 截断（F-8）；§2.5 census 补 3 条 `sdk-cli` 识图委托 session（`7310d850`/`a84cd7e8`/`f450f935`），合计 22 条将被隐藏，§9.1 同步（F-9）；D-2 消费方清单补 pin 解析（`resolveClaudePin`）及其「definitively gone」后果声明（F-10）；D-1 差异清单补提取层差异（官方 `nl` 原生子串可命中嵌套键 vs Go 按行顶层解析）并精确化空串命中即停语义（F-11）。 |
| v4 | 2026-10-16 | 按第 3 轮评审（F-12 采纳，逐条核实后处置）：D-1 为 `entrypoint` 增加文件尾 64KB 回退（head 顶层首次命中 ?? tail 顶层最后一次命中、跳过截断首行；`sessionKind` 仍只用 head）——本机 3/22 条 programmatic（`0296f9ef`/`1eba385c`/`a9a158a0`，1.25–1.9MB 成功安审 reviewer，首条 user 行 421–466KB）head 512KB 内零 `"entrypoint"` 子串、仅 tail 可达，v3 head-only 规则会漏隐藏 3 条并使 E-8 基线 22 必假（实际 19）；删除 v3「真实样本 entrypoint 出现在第 3 行、两条路径实测收敛」虚假断言，差异清单按双窗口重写（Go 窗口 ⊇ 官方窗口，残余分叉仅提取层构造场景）；§2.5 补字节可达性 census（16 head + 3 tail-only）；E-8 基线 22 补双窗口可达性依据；S-1 fixture 补双窗口用例、§7 补大文件隐藏行、§8 大文件风险行改为 tail 回退兜底。修订时以本机 39 文件模拟 v4 规则：22 programmatic / 17 visible，与官方 SDK 实测逐项一致。 |
