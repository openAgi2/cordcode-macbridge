# 终审报告（final）：OpenCode v2 独立接入与共享服务设计方案

- 评审轮次：**终审**（final review）；对象为方案 v1.8（r6 通过后的建议收编版）
- 评审日期：2026-10-21（来源门两次：20:22、20:39，Asia/Shanghai）
- verdict：**REVISION_REQUIRED（不通过，需修订）**
- blockers：**1**（F-18）；advisories：**7**（F-19 ~ F-25）
- 契约：plan-contract-v1.1（plan-review skill，接入 audit-plan 专项）
- 评审对象：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md`，v1.8，
  SHA-256 `34bd16fe62b360b39ca9262d58bbcda16f0a5436f3e268f86b41134a90da0e45`，git blob
  `bc84a29ed5621de9e38344a0d453704f48784d06`（提交 `cbd0a109` 之后未再变化）
- 评审员模式：**新会话、新评审员**。先按 A1~A8 独立建立风险清单，并对双仓与 remote-web 做机械化
  消费者扫描，再以 r6/r2 作回归清单。r1~r6 的结论一律按「待抽查的声明」处理。
- 边界：只写本报告。未改方案与历轮报告，未改业务代码，未 commit/push，未 build/test/install，
  49374/4096 两个服务均未探测、未启停，无 UI automation/截屏。**本结论不构成实施授权。**

## 0. 一句话结论

设计主体成立：独立包、ID 复用协商、手动启动、桶隔离、E/S 门控、退役门互相自洽，依赖 DAG 无环，
v1.8 增量诚实，F-16 处置正确。但作为「实施前冻结基线」不成立：§3.11 的身份复用消费者清单不完整。
其中漏列的 Swift `configureOpenCodeDesktopServerIfNeeded()` 以 `drivers.contains("opencode")` 为门，
新 v2 产品复用 `opencode` 后它会被激活，在主路径上把 v1 端点和密码写进 OpenCode Desktop 的 userData。
这正是附录 A.6 崩溃链的同类前提，也违反方案自己的非目标。Go、iOS、remote-web 另有一批有语义后果的
未列消费点，而 S-0/E-11a 没有要求机械化全量枚举与对账。修订量为文档级（v1.9），建议以定向复核闭合。

---

## 1. 来源身份表（本轮 fresh 记录）

| 别名 | 仓库/工作树 | 绝对路径 | 分支/状态 | 完整提交 | 未提交状态（porcelain） | 本轮角色 |
| --- | --- | --- | --- | --- | --- | --- |
| M | cordcode-macbridge（方案所在） | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `bd240b80ec6eceb61a99f23120086c388ebeed2b` | 空（20:22、20:39 两次一致） | 评审目标；Mac 源码锚点核验 |
| I | cordcode-ios（配套） | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `04fc269bdb8a297b1578ac4b9f0d4b82a98c7997` | 空（两次一致） | 唯一配套：`git worktree list` 中 iOS 仓该分支族只此一条；iOS 与 `remote-web/` 源码核验 |
| U | opencode-v2（上游，只读） | `/Users/jacklee/Projects/opencode-v2` | detached；`git describe --exact-match` = `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 空（两次一致） | v2 官方语义唯一锚点 |
| — | opencode（旧 checkout） | `/Users/jacklee/Projects/opencode` | — | — | — | 按指令不作 v2 锚点，本轮未读取 |

方案登记来源与本轮实测的差异：

- **M**：方案登记 `1fe9c65d734949981d032de5f57e9ffb43f6bde9`，它是当前 HEAD 的祖先（`cbd0a109` 的父提交就是它）。
  `1fe9c65d..HEAD` 共 65 个提交、58 个非文档文件变更，涉及锚点文件 `go-bridge/main.go`、`handlers.go`、
  `handlers_opencode.go`、`handlers_projection.go`、`projection_kernel.go`，并新增
  `projection_window_v2*.go` 等新的身份消费点（见 F-19）。
- **I**：方案登记 `89f0a3693c7ba2ee45cd9191a08cbc5f765aee52`，**已不在当前分支历史上**
  （`merge-base --is-ancestor` 结果为否）。分支 reflog 显示：2026-10-20 19:27 rebase 到 `f0d0f523`；
  2026-10-21 20:15 合入 `feat/ios-native-message-timeline`，生成 `04fc269b`。该提交对象仍在本地可读。
  四个 iOS 锚点文件里有三个与 `89f0a369` 字节一致；`CCCodeBridgeBackendClient.swift` 已变化。
- **U**：与方案一致。
- 其他只读材料：r6 报告（SHA-256 `e82eeef0…2c0c2691`）与 r2 报告（`ca898845…4d64cf`）全文；
  r1/r2-meta/r3/r4/r5 只读标题、verdict 与意见索引。v1.7 快照取自
  `~/.claude/file-history/db23b46f-9203-4969-ae11-2e561f33bb22/583a522a9ad71e4e@v5`
  （本地 Claude Code 文件历史，非 git），SHA-256 `37ddbaf0…7bd47` 与 r6 的评审对象一致，只用于 A8 的 diff。
- 运行态：本轮按指令没有探测 49374/4096。所有活体结论都是复用 2026-10-19 r1/r2 与附录 A.8 的记录。

## 2. 评审范围与方法

1. 通读方案 v1.8 全文（1269 行）以及 r6、r2 全文，先按 A1~A8 形成自己的风险清单。
2. **机械化消费者扫描（A3）**：
   - M：`rg '"opencode"'`，搜索根为 go-bridge/core/agent/MacBridge；排除 `agent/opencode-web/**`、
     `deprecated/**`、`*_test.go`、testdata 与文档。
   - I：`rg '\.openCode\b(?!Web)'` 与 `rg '"opencode"'`，搜索根为 `OpenCodeiOS`，排除 Tests/UITests。
   - I/remote-web：`rg -i opencode remote-web/src`。
   - 对命中逐处读上下文，分为五类：旧语义分流、同族成员表（会自动纳入新驱动）、仅因旧 proxy 不存在而失效、
     fallback-only、纯展示。
3. **依赖闭包静态检查（A6）**：运行 `GOFLAGS=-mod=readonly GOPROXY=off go list -deps [-test]`
   （只列静态依赖，不构建），并枚举 `agent/opencode-web` 的全部导入者。
4. **锚点抽查**：在 M/I/U 当前提交上亲核；同时用 `git show 1fe9c65d:<path>` 对照方案登记提交，
   区分「原引用就错」和「其后漂移」。
5. **v1.7→v1.8 diff（A8）**。
6. 未做：活体复测、构建/测试/写操作。r6 表 B 的架构可行性锚（core 事件接口等）与 think.md 五处未重核，
   不影响本轮结论。

---

## 3. 分项判定（A1 ~ A8）

### A1 单期交付范围：逻辑自洽，没有隐藏的「必须分期」矛盾；交付前置需写明

- 产品放行只有 S-2b 一道门，其前置为 E-2/E-3/E-4/E-5、S-3~S-6 与 E-11b 全部满足（§3.3、§4 Gate A、
  §5 S-2b 行、§6 阻塞清单四处一致）。因此不存在「只读先放行」这类绕过路径，单期交付在门控上是自洽的。
- 次序约束不构成分期，但需要写清楚：
  - iOS/remote-web 的 marker 识别要先于 Mac 放行（§3.11 末条）；
  - E-3/E-5/E-6/E-9 需要 owner 授权的隔离环境（Gate C）。
  这两项分别见 F-24（remote-web 是否必须作为硬前置）与 F-21（隔离配方）。
- 规模风险：iOS 侧 `.openCode` 有约 59 处代码引用，remote-web 有一组 backend-kind 消费点，方案只锚了其中
  4 处 iOS、0 处 remote-web。单期范围在 iOS/remote-web 面被低估，已并入 F-18。

### A2 E/S 依赖 DAG：无环；pending 证据门确实挡住了对应实现；有两处可加强

- 依赖链：S-0 → E-11a →（S-1 的选择/身份部分、S-4、S-7）；S-7 → E-11b → S-2b；S-2a + E-2 → S-3；
  S-2a + E-4 → S-4；S-2a + E-3/E-5 → S-5；E-7/E-10 → S-6 完成；{S-3~S-6, E-11b} → S-2b → S-8 → E-11c →
  S-9（另加 §3.12 门与 owner 指令）。E-11c 不回指 S-2b/S-7，A.15 修复的循环没有复发。
- §3.3 中「S-3~S-6 新代码/fixture 回放可在组件环境开发」不构成先编码后补证据的漏洞：§6 明确要求
  「真实 wire fixture 必须来自 E-2~E-5 官方样本」，各切片的依赖列也绑定了对应的 E。
- 两处可加强：
  - (a) live 与 cold 同一身份的对账没有写进 E-2/E-4 的断言（F-20）。只能等 S-3/S-4 集成时才暴露。
  - (b) S-1 依赖列把前置条件（E-1、E-11a）和完成门（E-6/E-9，其活体部分要等 S-8 验收窗）写在同一列。
    Gate A 文本「S-1…完成依赖 E-6/E-9」可以读通；S-8 只依赖 S-1 的组件完成，因此不构成循环。
    这一项只作记录，不单列意见。

### A3 backend ID 复用的兼容矩阵：**不穷尽 → F-18（阻塞）**

扫描结果（搜索根与排除项见 §2）：

| 面 | 命中 | §3.11 直接锚定 | 有语义后果的漏项（举例，详见 F-18） |
| --- | --- | --- | --- |
| M go-bridge 生产 Go | `"opencode"` 共 39 行（其中 3 行注释） | 8 行 | `main.go:1275` passive SSE 门、`backend_capabilities.go:158` 能力排除、`handlers.go:1802` 按请求切目录、`server.go:660` SSV2 归属能力、`web_push_*` 推送、`handlers.go:593` 重订阅名单、`handlers_session_pin.go:308`、pathless 同族名单 7 处（含 v1.8 之后新增的 `projection_window_v2_handler.go:119`） |
| M MacBridge Swift | `"opencode"` 共 9 行 | 0 行（§3.11 只锚了 `opencode-web` 相关的 :1157/:1778/:1817） | **`RuntimeManager.swift:1135` Desktop 配置写入门**（F-18a） |
| I iOS 生产 Swift | `.openCode` 共 61 处（其中 2 处注释） | 3 处（加上 `CCCodeBridgeBackendClient` 1 处） | 不受编译器穷尽检查约束的 `==` 特性门：`ChatViewModel+CodexStreaming.swift:2139`、`ChatViewModel+Generation.swift:1580/2533/2547/3207`、`SidebarView.swift:170/736/753/771`、`SessionsView.swift` 8 处等 |
| I remote-web/src | 28 个文件含 opencode 字样 | 0 处 | `core/backend/backend-client.ts:52-53`、`adapter/moreMenuItems.ts:50-51,63`、`adapter/backendLogos.ts:25`、`core/protocol/bridge-v1.ts:73` |

方案列出的条目语义都成立（见 §4），但清单不完整，S-0/E-11a 也没有要求做全量枚举。r6 关于
「§3.11 没有虚列或漏列关键分流点」的结论被反证。

### A4 手动启动主路径的四拍：完整，失败态诚实；两条建议

- 四拍齐全（§3.2.3）：从 OpenCode（v2）行进入；无需输入（安装命令只复制）；发出时只读重查，
  再执行 `serve --service` 单飞；过程依次显示启动中 → 服务已启动 → 正在接入 → 连接正常。
- 状态矩阵（§3.2.2）能区分以下状态：检测中、未接入、未启动、未安装、检测失败、冲突、启动中、正在接入、
  已接入、bridge 已停止、启动/接入失败。没有自动拉起，没有回退 4096；30s 有界预算；退出码 0 不当作 ready；
  detached 语义由 E-9 把关。
- 上游锚点全部亲核命中：contender spawn 的 detached/stdio/8KiB/unref；ensure 的 3 次超时 terminate 与
  版本 replace（见 §4 第 30~31 行）。
- 建议：
  - F-23：官方 contender 会传入 `PtyHandoff.environment`，方案未镜像该环境。
  - F-21：隔离取证缺少配方。
- 另：F-18a 同时破坏了「启动/接入不得改 Desktop 状态」这条不变量。

### A5 目录桶隔离与 SSV2 单 writer/fence：自洽；Mac 侧三态可区分

- catalog partial 不发 `session.deleted`，不动 Kernel/ProjectionStore；scoped 请求不被聚合策略改写；
  generation 过期即丢弃；列表经 discovery fingerprint/cache fence 同源失效（§3.7）。这些与「Kernel 唯一 writer」
  不冲突。
- Mac 侧可以区分：真实空（registry 为空，complete-empty）、partial（`catalogObservation` K/N）、全失败
  （`catalog_unavailable` 错误）。registry 自身失败按整次数据源失败处理。
- iOS 侧：全失败显示为错误，真实空显示为空列表；partial 只显示为一个变短的列表，没有提示。方案已明示
  「告警只在本机行呈现，本期 iOS 不新增跨端告警 UI」，与 R-5 和验收第 6 条（Mac 行）一致，**不构成缺陷**。
  建议在 U6 的「应看到」里补一句「iOS 列表可能变短且无提示」，避免验收时误判。

### A6 公共依赖边界与退役可分离：可以机械执行

- 本轮静态核对：`go list -deps -test ./core/... ./pinstore/... ./transcriptindex/... ./toolinventory/...`
  对 `agent/opencode-web` 与 `deprecated/` 的命中均为 **0**。
- go-bridge 生产代码只在 `main.go:30` 用 blank import 引入旧包；测试代码另有 5 个导入者
  （`agent_descriptor_test.go`、`audit008_capability_restore_test.go`、`audit008_fullpath_test.go`、
  `management_api_test.go`、`projection_webperf_fixture_gen_test.go`）。
- 因此 S-9 的「受控排除旧包 → 移除 blank import → 排除上述测试 → 构建并跑定向测试」可以机械执行。
  建议固化具体命令与测试文件的处置清单（F-25，低）。

### A7 风险表重大遗漏：主线风险已覆盖；有四处补强

- **SSE volatile 恢复**：§3.4 有冷读恢复的方向，但没有锚定官方执行态冷读入口 `GET /api/session/active`
  （U `groups/session.ts:275-285`，原文 "Sessions absent from the result are inactive"），也没有锚定
  `session.inbox.list`（`:607`）。见 F-20。
- **permission/form 生命周期**：E-5 已覆盖 session 作用域 form 的四个路由与完整流，成立。
- **附件**：上游 `PromptInput.FileAttachment` 用 `uri` 表达附件（`schema/src/prompt-input.ts:9-14,29-34`），
  服务端既接受 `data:` URI（直接解码），也接受 file URL（`core/src/session/prompt.ts:97-106`）。
  因此不需要单独的上传端点，E-3 把关形状即可。成立。
- **cursor 分页边界**：上游 `SessionMessagesQuery`（`groups/message.ts:8-40`）规定 limit 为 1..200；
  `order` 只作用于首页，且与 cursor 不可同用（"Do not combine with order"）；cursor 是不透明的
  previous/next。E-4 只写了「cursor.next 至耗尽」，没有覆盖方向语义。见 F-20。
- **Desktop 升级轮换 id/pid/password**：E-6 已覆盖。但方案把支持版本 pin 在 2.0.32，Desktop 自动升级后
  会落到「版本不支持」，升版流程没有写明。见 F-24。
- **安全边界**：
  - 密码经 env 传递、不进 argv；loopback 校验；pid 校验——成立。
  - 绕过路径 1：F-18a 会把 v1 密码写进 Desktop 的文件，密码离开了 CordCode 的边界。
  - 绕过路径 2：web push 向未协商 marker 的旧客户端投递新 backend 的回复预览
    （`web_push_dispatcher.go:236-253` 只按设备授权门控）。这一路已并入 F-18b。

### A8 v1.8 增量诚实性：诚实，只有声明的三类改动

用 v1.7 快照（SHA-256 `37ddbaf0…`）与 v1.8 做 diff，共 70 行增删，全部落在以下位置：

- 头部（版本、状态、版本史、本轮改动范围）；
- §3.11 第 4 行补子锚点（F-16）；
- Gate C 的状态回写；
- §5 S-1 依赖列（F-17）；
- §6 阻塞清单的状态回写；
- 新增附录 A.17。

R、OD、§3 设计命题、E/S 门控与验收矩阵**零改动**，没有越权改动设计命题。

---

## 4. 锚点抽查表（本轮亲核；原引用保持原样）

| # | 命题 / 方案原引用 | 实际位置（本轮） | 源码原文摘录（带行号，节选） | 定位 / 语义 | 依据 |
| --- | --- | --- | --- | --- | --- |
| 1 | §3.11 `agent_descriptor.go:116` 旧轮询 | M 同文件（blob `b1e8d341`，与 `1fe9c65d` 相同） | `:116 return id == "claude" \|\| id == "opencode" \|\| id == "grokbuild"` | 准确 / 成立（fallback-only） | 亲核 |
| 2 | §3.11 `:216`（`case "opencode":`@:216、health@:229） | 同上 | `:212 func detectAgentStatus(…)`；`:214 case "claude":`；`:216 case "opencode":`；`:229 return detectOpenCodeService(cfg.OpenCodeURL, ocUser, ocPass)` | 准确 / 成立 | 亲核（见 §5） |
| 3 | §3.11 `deprecated/opencode/opencode.go:23` | M 同文件 | `:23 func init() {`；`:24 core.RegisterAgent("opencode", New)` | 准确 / 成立 | 亲核 |
| 4 | §3.11 `core/registry.go:12` 同名覆盖 | M 同文件 | `:12 func RegisterAgent(name string, factory AgentFactory) {`；`:13 agentFactories[name] = factory` | 准确 / 成立 | 亲核 |
| 5 | §3.11 `main.go:326-329` 旧 proxy | M：HEAD 与 `1fe9c65d` 同行 | `:326 if id == "opencode" && *ocBaseURL != "" {`；`:327 proxy := NewOpenCodeProxy(…)`；`:328 handlers.RegisterOpenCodeProxy(proxy)` | 准确 / 成立 | 亲核 |
| 6 | §3.11 `handlers.go:1264` 旧 dispatcher | M：`1fe9c65d` 在 :1264；HEAD 在 **:1270** | `:1270 if msg.BackendID == "opencode" && h.isOC() {`；`:1271 h.handleOpenCodeRPC(conn, msg)` | 登记提交上准确；HEAD 漂移 +6 / 成立 | 亲核 |
| 7 | §3.11 `handlers.go:1272` catalog 门 | M：`1fe9c65d` 在 :1272；HEAD 在 **:1278**（case 行 :1280） | `:1278 func catalogCapabilityRequiredFor(agentName string) bool {`；`:1280 case "codex", "grokbuild", "opencode", "claude", "claudecode":` | 同上，+6 / 成立 | 亲核 |
| 8 | §3.11 `handlers.go:2697` create 白名单 | M：`1fe9c65d` 在 :2697；HEAD 在 **:2705** | `:2705 if agent.Name() == "codex" \|\| agent.Name() == "claudecode" \|\| agent.Name() == "opencode-web" {` | 同上，+8 / 成立 | 亲核 |
| 9 | §3.11 `handlers_opencode.go:422,437` | M：两个提交同行 | `:422 func (h *Handlers) buildOpenCodeEnrichedSessions(…)`；`:437 h.overlayPinnedState(mapped, "opencode")` | 准确 / 成立 | 亲核 |
| 10 | §3.11 `handlers_projection.go:664,1386` | M：:664 两提交同行；:1386 → HEAD **:1424** | `:664 if backendID == "opencode-web" \|\| backendID == "codex-remote" {`；`:1424 case "opencode":` / `:1425 return h.streamOpenCodeRichHistoryProjectionEvents(ctx, sessionID, emit)` | :664 准确；:1386 漂移 +38 / 成立 | 亲核 |
| 11 | §3.11 `projection_kernel.go:188-200` checkpoint 文件名 | M：`1fe9c65d` 在 :189；HEAD 在 :195-197 | `:195 func projectionCheckpointFilename(backendID, sessionID string) string {`；`:196 sum := sha256.Sum256([]byte(backendID + "\x00" + sessionID))` | 区间仍覆盖（+7）/ 成立：没有 generation 维度 | 亲核 |
| 12 | §3.11 `pinstore/store.go:110` 与跨 scope API | M（两提交 blob 相同） | `:110 func key(backendID, scope, sessionID string) string {`；`:111 return backendID + "\|" + scope + "\|" + sessionID`；`:163 GetAnyScope`；`:185 List`；`:242 RemoveAll` | 准确（指向函数行）/ 成立 | 亲核 |
| 13 | §3.11 `handlers_session_pin.go:201` | M 同行 | `:201 case "opencode":`；`:202 return h.resolveOpenCodePin(pin)` | 准确 / 成立 | 亲核 |
| 14 | §3.11 `hello_handler.go:15` | M 同行 | `:15 Capabilities []string \`json:"capabilities,omitempty"\`` | 准确 / 成立 | 亲核 |
| 15 | §2.3 `main.go:104-106` OPENCODE_WEB_*；`:110` source 枚举 | M 同行 | `:104 ocwBaseURL := flag.String("opencode-web-url", envOr("OPENCODE_WEB_BASE_URL", ""), …)`；`:110 … (managed_local, external_http, legacy_64667, service_discovery_future, disabled)` | 准确 / 成立 | 亲核 |
| 16 | §2.3 `management_api.go:692-733` live 重读 | M | `:716 if descs[i].ID == "opencode-web" {`；`:724-730` 仅实现结构化接口时走通用分支 | 准确 / 成立（新驱动只实现结构化接口即可走通用分支） | 亲核 |
| 17 | `RuntimeManager.swift:709-723` 3s 监控 | M（文件自 `1fe9c65d` 未变） | `:709 private func startMonitoring() {`；`:722 try? await Task.sleep(nanoseconds: 3_000_000_000)` | 准确 / 成立 | 亲核 |
| 18 | `RuntimeManager.swift:1070-1072` | 同文件 | `:1071 if s == .ready \|\| s == .readyNoAgents {`；`:1072 stuckRestartCount = 0` | 准确 / 成立 | 亲核 |
| 19 | `RuntimeManager.swift:1293` 只比较 URL | 同文件 | `:1293 if endpoint.url != config.opencodeURL {` | 准确 / 成立 | 亲核 |
| 20 | `RuntimeManager.swift:1157` resolve guard | 同文件 | `:1158 guard config.drivers.contains("opencode-web"), config.opencodeSource == .managedLocal else {`；`:1159 openCodeManagedServer?.stop()` | 准确 / 成立（另见 F-22） | 亲核 |
| 21 | `RuntimeManager.swift:1778,1817` | 同文件 | `:1778 if !config.opencodeURL.isEmpty {`；`:1817 if !config.opencodeUser.isEmpty {` | 准确 / 成立 | 亲核 |
| 22 | `ProductAgentCatalog.swift:17` | M | `:17 Product(id: "opencode-web", name: "OpenCode Web", kind: "opencode-web", …)` | 准确 / 成立 | 亲核 |
| 23 | `AgentSelectionStore.swift:12,62` | M | `:12 func validated() throws -> AgentSelection {`；`:62 func setEnabled(…)` / `:63 guard ProductAgentCatalog.ids.contains(id), …` | 语义成立 | 亲核 |
| 24 | §2.3 `OpenCodeEndpointResolver.swift:25-27` | M | `:25 case serviceDiscoveryFuture = "service_discovery_future"` | 准确 / 成立 | 亲核 |
| 25 | I `BackendModels.swift:69-76` | I（与 `89f0a369` 字节一致） | `:69 static func fromWireKind(_ wireKind: String) -> BackendKind? {`；`:75 case "opencode": return .openCode` | 准确 / 成立 | 亲核 |
| 26 | I `BackendModels.swift:309` | 同文件 | `:309 var cacheScopeKey: String {`；`:310 "\(backendKind.rawValue)\|\(endpoint)\|\(username)"` | 准确 / 成立 | 亲核 |
| 27 | I `BridgeProvider.swift:985` | I（字节一致） | `:985 guard let kind = BackendKind.fromWireKind(backend.kind) else {` | 准确 / 成立 | 亲核 |
| 28 | I `ServerViewModel.swift:885` | I（字节一致） | `:885 private func bridgeTargetIndex(`；`:890 BridgeIdentity(server: server) == bridgeIdentity && server.backendKind == backendKind` | 准确 / 成立 | 亲核 |
| 29 | I `CCCodeBridgeBackendClient.swift:155` | I：HEAD 在 **:163**（文件已变） | `:163 var projectionSourceIdentity: String {`；`:164 "\(transport.connectionID):\(projectionConnectionGeneration):\(backendID)"` | 漂移 +8 / 成立 | 亲核 |
| 30 | §2.2/§3.2.3 `service-contender.ts:19` | U | `:12 const stderrLimit = 8 * 1024`；`:19 const child = spawn(command, args, {`；`:20 detached: true,`；`:22 stdio: ["ignore", "ignore", "pipe"],`；`:43 child.unref()` | 准确 / 成立 | 亲核 |
| 31 | §2.2/§3.2.3 `effect/service.ts:75-87,98-104` | U | `:80 if (timeouts.count >= 3) {`；`:84 yield* terminate(info, options, timing)`；`:98 if (decision._tag === "replace") {`；`:102 yield* stop({ file: options.file, pty: decision.pty })…` | 准确 / 成立（另：`:63` 会传入 `PtyHandoff.environment`，见 F-23） | 亲核 |
| 32 | §2.2 `service-config.ts:56` 0xC0DE | U | `:56 if (channel === "latest" \|\| channel === "dev" \|\| channel === "beta" \|\| channel === "next") return 0xc0de`；`:41-43` 注册文件名随 channel 变化 | 准确 / 成立 | 亲核 |
| 33 | §2.2/§3.6 `groups/session.ts:396-407` prompt | U | `:396 HttpApiEndpoint.post("session.prompt", "/api/session/:sessionID/prompt", {`；`:399 id: SessionMessage.ID.pipe(Schema.optional),`；`:406 error: [ConflictError, InvalidRequestError, SessionNotFoundError],`；`:408 .middleware(sessionLocationMiddleware)` | 准确 / 成立 | 亲核 |
| 34 | §2.2/§3.4 `groups/event.ts:44-52` volatile | U | `:44 HttpApiEndpoint.get("event.subscribe", "/api/event", {`；`:51 "…Volatile by contract: a slow consumer overflows and fails the stream, and events during disconnection are missed."` | 准确 / 成立 | 亲核 |
| 35 | §2.2 `session.ts:168-174` SessionsQuery（F-1 承重锚） | U | `:170 directory: AbsolutePath.pipe(Schema.optional),`；`:173 cursor: SessionsCursor.pipe(Schema.optional),` | 准确 / 成立 | 亲核（r2/r6 亦核） |

意见中另外引用了几处方案未引用的上游事实，均为本轮亲核：
- U `groups/session.ts:275-285`（session.active）；`groups/message.ts:8-40`（SessionMessagesQuery）；
- `schema/src/prompt-input.ts:9-34`；`core/src/session/prompt.ts:97-106`；
- `desktop/src/main/storage/index.ts:28-35`、`storage/legacy.ts:18-24,65-93`、`storage/keys.ts:1`；
- `util/src/global-roots.ts:5-8`；`client/src/pty-handoff.ts:68-78`。

未核/低风险未核：r6 表 B 的 `core/interfaces.go:1051/1060` 与 `main.go:320-322,1017`（本轮只读到
`main.go:318-323` passive 订阅分支，与 F-18b 相关）；think.md 五处；`service-lifecycle.test.ts:59`；
desktop 安装旁证三处。以上不影响结论。

## 5. F-16 核验结论（独立核定）

- 文件身份：`go-bridge/agent_descriptor.go` 在 `1fe9c65d` 与 `bd240b80` 上是同一个 blob
  （`b1e8d3414a302c103a4592658913d9c978324dae`），`1fe9c65d..HEAD` 没有提交触及该文件，工作树干净。
- 实测（逐行 awk 输出与 rg 两种方法结果一致）：
  - `:115 func legacyRequiresPolling`；
  - `:116 return id == "claude" || id == "opencode" || …`；
  - `:212 func detectAgentStatus(…)`；`:213 switch id {`；
  - `:214 case "claude":`；`:216 case "opencode":`；`:217-219` 为注释，`:220` 为 URL 空值判定；
  - `:229 return detectOpenCodeService(cfg.OpenCodeURL, ocUser, ocPass)`。
- 判定：
  - r6 称「`:216` 为 `case "claude":`、`case "opencode":` 在 `:218`、函数自 `:214` 起」，三个行号整体偏了 +2，
    **不成立**；`:218` 只是注释行。
  - v1.8 作者的亲核正确。「保留 `:216`，补注子锚点（opencode case@:216、health@:229）」这一处置
    **正确**：既没有制造新的漂移，也实现了 F-16「消除引用歧义」的意图。**F-16 闭合。**
  - 本轮结果与 v1.8 一致，不存在与两方都不同的第三种结果。
- 附带：同文件 `:55`（agentKind）与 `:72`（agentDisplayName）也有 `case "opencode":`。但
  `resolveDescriptorNames`（`:96-101`）会优先使用 WireDescriptor，这两处只在驱动不自描述时生效。
  方案已要求新驱动提供 WireDescriptor，因此它们不构成新问题。

## 6. 历轮声明抽查（r6）

| r6 声明 | 本轮实测 | 判定 |
| --- | --- | --- |
| F-16：`:216` 是 claude、`:218` 是 opencode、函数自 `:214` 起 | `:214` 是 claude、`:216` 是 opencode、函数在 `:212` | r6 错（+2） |
| `main.go :327 if id == "opencode"…`、`:329 RegisterOpenCodeProxy` | `1fe9c65d` 与 HEAD 均为 `:326` / `:328` | r6 摘录行号偏 +1；方案区间正确 |
| `handlers_opencode.go :437 func ocHandleListSessions` | `:437` 是 `h.overlayPinnedState(mapped, "opencode")`；`ocHandleListSessions` 在 `:441` | r6 摘录对象错；方案「:437 overlay」正确 |
| `pinstore/store.go :110 return backendID + …` | `:110` 是 `func key(…)`，`:111` 才是 `return …` | r6 摘录偏 −1；方案指向函数行，可以接受 |
| `AgentSelectionStore.swift :12 static let initial` | `:10` 是 `static let initial`，`:12` 是 `func validated()` | r6 摘录偏 +2；方案语义可以接受 |
| 「§3.11 适配清单没有虚列或漏列关键分流点」 | 见 F-18：漏列 Desktop 配置写入门等多处有语义后果的消费点 | **被反证** |
| E-11a/b/c 无环；S-2b 前置四处一致；退役标准三处一致 | 本轮复核一致 | 成立 |

结论：r6 对已列锚点的语义判断大体成立，但摘录行号有系统性小偏差，「无漏列」的完整性结论不成立。
r6 的「全量亲核」不能当作穷尽枚举的证据。

---

## 7. 意见列表（编号从 F-18 续用）

### F-18 [阻塞] §3.11 身份复用消费者清单不完整：漏项含一条可复现 A.6 类回归的 Desktop 配置写入路径，且 S-0/E-11a 没有机械化全量枚举

- **位置**：
  - §3.11 边界表（尤其 Swift 行与 iOS 行）；
  - §3.2.4 末条（只点名了 `syncDesktopConfig`）；
  - §2.3「Swift 会把 managed URL 同步进 desktop 配置」行；
  - E-11a/E-11b、S-0/S-1/S-7，以及 U4/U10/U12。
- **证据 (a)：Desktop 配置写入（Swift，方案未列）**
  - `RuntimeManager.swift:1134-1153`：`configureOpenCodeDesktopServerIfNeeded()` 的 guard 为
    `config.drivers.contains("opencode"), !config.opencodeURL.isEmpty, !config.opencodeUser.isEmpty, !config.opencodePass.isEmpty`。
    条件满足时调用 `configureOpenCodeDesktopSettings`（`:1345-1399`），向
    `~/Library/Application Support/ai.opencode.desktop/` 写入两处：
    - `opencode.global.dat`：server 列表，内容为 `{url, username, password}`，并设置 `currentSidecarUrl`（`:1351-1389`）；
    - `opencode.settings`：设置 `defaultServerUrl`（`:1391-1393`）。
  - 该函数在**每次** `launchBridgeProcess()` 都会被调用（`:581-582`）。
  - 选择路径：`applyAgentSelection`（`:528-545`）里 `config.drivers = selection.enabledIDs`，产品 ID 与 driver ID
    一一对应。新 v2 产品 ID = `opencode`，因此 guard 第一项必然成立。
  - 旧值来源：
    - `AppDependencies.swift:92-96` 在凭据为空时自动生成 user/pass；
    - `:99-116`：有旧凭据的存量用户默认迁移为 `legacy_64667`；
    - `:117-129` 与 `:413-421`：`legacy_64667`/`external_http` 在启动时就离线解析出 `opencodeURL`；
    - `resolveManagedOpenCodeIfNeeded()` 在 guard 失败分支（`:1158-1162`）只 stop managed，不清空
      `config.opencodeURL`/凭据。
  - 上游：Desktop v2 每次启动都会在 DesktopStorage 构造时执行 `importLegacyStores`
    （U `desktop/src/main/storage/index.ts:28-35`）。它导入所有 `opencode.*.dat`，以 onConflictDoNothing
    写入 state 表后删除源文件（`legacy.ts:18-24,65-93`）。`opencode.settings` 仍是 Desktop v2 正在使用的
    settings store（`storage/keys.ts:1`）。
- **证据 (b)：有语义后果、方案未列的 Go 消费点**（M HEAD）
  - `main.go:1271-1279`：`shouldStartPassiveSubscription` 对 `"opencode"` 要求旧 `-opencode-url` 非空。
    新驱动如果实现的是 `core.EventSubscriber`（而不是 `LiveEventSubscriber`），SSE 永远不会启动（`:318-323`）。
  - `backend_capabilities.go:152-162`：`id != "opencode"` 会屏蔽由 ToolAuthorizer 推导的 `permission_resolve`。
  - `handlers.go:1797-1804`：对名为 `"opencode"` 的驱动，每个带目录的请求都执行 `switchDir(agent, dir)`，
    改写共享驱动的工作目录。这与 v2 的逐请求 location 模型（E-3 三态矩阵）冲突，也存在并发隐患。
  - `server.go:650-671`：按 ID/kind 为 `"opencode"` 附加 `session_sync_v2` 归属能力，不看新驱动是否已放行、
    投影是否就绪。
  - web push：`web_push_dispatcher.go:236-253` 只按设备授权做 fan-out，`web_push_protocol.go:110-125`
    把 `opencode` 映射为显示名。未协商 marker 的旧客户端会收到新 backend 的完成通知与回复预览，
    而方案的旧客户端门控只枚举了「握手、RPC、subscribe/observe/replay、mailbox」。
  - 其余：
    - `handlers.go:588-593` relay 重连重订阅名单；
    - `handlers_session_pin.go:307-315` pin overlay 按原始 `"opencode"` 查 store；
    - pathless 同族名单 7 处：`handlers_projection.go:122-127/477-479/635-636/785-791/886/1368-1372`、
      `projection_kernel.go:1004-1011`、`projection_window_handler.go:109-114`、
      `projection_window_v2_handler.go:117-121`（提交 `a9b09636`，2026-10-19 22:57，晚于 v1.8）、
      `projection_anchor_newer_handler.go:93-98`；
    - `handlers_projection.go:1424→1807`：旧的「OpenCode flat convention」折叠（以 user message id 作为 turnId）
      会按名字解析到新驱动；
    - `handlers_opencode.go:32`：旧 proxy 方法表在 v1.8 之后新增了 `get_session_projection_window_v2`，
      说明这个面还在持续变化。
- **证据 (c)：iOS 与 remote-web**
  - I 生产 Swift 中 `.openCode` 共 61 处，方案只锚了 4 处。不受编译器穷尽检查约束的 `==` 特性门包括：
    - `ChatViewModel+CodexStreaming.swift:2137-2139`（自动批权限只给旧 `.openCode`）；
    - `ChatViewModel+Generation.swift:1580/2533/2547/3207`；`ChatViewModel.swift:2750`；`ServerViewModel.swift:486`；
    - `SidebarView.swift:170/736/753/771` 与 `SessionsView.swift:1035/1946/2317/2330/2334/2986/3008/3039`
      （project roots 分组等功能按 `.openCode || .openCodeWeb` 门控）；
    - `ChatViewModel+SessionManagement.swift:1275/1281`（primary agents）；
    - `BridgeDiscoveryService.swift:62/78`；
    - `Models/Server.swift:113`（缺 `backendKind` 时默认解码为 `.openCode`）。
  - remote-web：`core/backend/backend-client.ts:52-53`、`adapter/moreMenuItems.ts:50-51,63`、
    `adapter/backendLogos.ts:25`、`core/protocol/bridge-v1.ts:73` 等，方案一处也没锚定。
- **影响**：
  - (a) 失败场景：用户正在用 opencode-web（managed 4096、legacy_64667 或 external_http），在 U10 里切到新
    OpenCode（v2）。随后 `applyConfigAndRestart` → `launchBridgeProcess` → managed guard 失败（URL 不清空）→
    `configureOpenCodeDesktopServerIfNeeded` 通过 guard → 把 v1 URL 和密码写进 Desktop 的
    `opencode.global.dat`/`opencode.settings`。legacy_64667/external_http 用户在**每次启动**都会写一次。
    后果：
    - 至少违反方案的非目标（「不改上游配置/数据库」，§5「不改 Desktop 状态」），也违反 R-6 的精神：
      密码落到另一个 App 的目录里；
    - 是否覆盖 Desktop 现有 server 状态，取决于 state 表里是否已有同键（onConflictDoNothing），
      **不能排除 A.6 复发**（A.6 原文写明 4096 加密码被写进 Desktop 状态是崩溃链的一环）；
    - 方案的负例矩阵（E-11b、U4/U10）里没有任何一条能抓到这个问题。
  - (b) 旧客户端隔离出现推送缺口；新驱动 SSE 不启动、permission 能力被屏蔽、目录上下文被共享改写、
    放行前就广告 SSV2 归属——这些都是「按 §3.11 实施却走错」的路径。
  - (c) `.openCodeV2` 会从几十个 `==` 特性门里被**悄无声息地排除**（project roots 分组、agent picker 等），
    方案没有登记相应的产品处置。
- **修订方向（最小充分）**：
  1. §3.11 Swift 行补列 `RuntimeManager.swift:1134-1153` 及调用点 `:582`，写明处置：
     新 v2 永不写 Desktop 配置；以 `"opencode"` 为门的旧写入器要么删除，要么改为只对显式旧来源生效，
     且在新产品被选中时不可达。S-1/E-11b 增加负例：选择、启动、接入新 opencode，或从 v1 切换过来时，
     `~/Library/Application Support/ai.opencode.desktop/` 下的文件不被 CordCode 改写。U4/U10 的「应看到」
     同步补上这一条。
  2. 把 §3.11 明确标为**非穷尽基线**。S-0/E-11a 增加交付物：机械化全量枚举。
     - 搜索根：M 的 go-bridge/core/agent/MacBridge；I 的 OpenCodeiOS；I 的 remote-web/src。
     - 模式：`"opencode"`、kind 比较、`.openCode`（含 `==`）。
     - 每处给出处置：改接 / 作为同族成员保留（附证据）/ 因旧 proxy 不存在而失效 / 需要产品决定。
     - 给出对账计数；把上面 (b)(c) 的已知消费点列为必处理项。
  3. E-11b 的旧客户端负例矩阵补上 web push 及其他不经连接的投递通道。另加组件测试，覆盖新驱动的
     passive 订阅启动、能力推导、目录/location 处理。
- **闭合标准**：v1.9 包含上述 1~3。定向复核确认三点即可，无需全量重审：
  - Desktop 写入器的处置与负例已写入 S-1/E-11b/U 矩阵；
  - S-0 枚举交付物有明确方法与对账要求；
  - E-11b 已含 web push。

### F-19 [建议] 锚点基线已漂移，iOS 登记提交已脱离分支

- **位置**：§2.1、§2.3、§3.11 引用的 M/I 提交；附录 C/D。
- **证据**：
  - I 的 `89f0a369` 不是 `04fc269b` 的祖先（rebase 于 2026-10-20 19:27，合并于 2026-10-21 20:15）；
  - M 的 HEAD 比登记提交多 65 个提交；
  - 漂移明细：`handlers.go` :1264→:1270、:1272→:1278、:2697→:2705；`handlers_projection.go` :1386→:1424；
    `projection_kernel.go` 哈希行 :189→:196（区间仍覆盖）；`CCCodeBridgeBackendClient.swift` :155→:163；
  - 新增消费点：`projection_window_v2_handler.go`。设计命题本身经本轮语义核对仍然成立（§4）。
- **影响**：实施时如果照搬旧行号，会定位错误。方案已声明「未来实施须重新生成来源门」，不构成阻塞。
- **修订方向**：S-0 在实施 HEAD 上重建 §2.3/§3.11 的全部锚点并附漂移表；iOS 来源身份改为当前分支提交，
  并注明 iOS 分支历史在方案落盘后被改写过。
- **闭合标准**：S-0 产物含漂移表，或 v1.9 刷新来源表。

### F-20 [建议] live/cold 同一身份对账与执行态冷读没有写成 E-2/E-4 断言

- **位置**：§3.4（volatile 恢复、去重）、§3.5（分页）、E-2/E-4、S-3/S-4。
- **证据**：
  - U `groups/session.ts:275-285`：`GET /api/session/active`，原文 "Sessions absent from the result are inactive"；
    `:607` 是 `session.inbox.list`。方案都没有引用。
  - `groups/message.ts:8-40`：limit 1..200；`order` 只用于首页，且与 cursor 不可同用；cursor 不透明；有 type 过滤。
  - M 现有 pathless 折叠 `streamOpenCodeRichHistoryProjectionEvents` 以 user message id 作为 turnId
    （`handlers_projection.go:1798-1807`）。codex-web 的注释已记录 live 与 cold 必须合并到同一身份的先例
    （`:1436-1440`）。
- **影响**：S-3/S-4 可能各自通过独立的 E-2/E-4 样本测试，却在 Kernel 里无法收敛（turn 重复或错位），
  直到集成或 S-8 才暴露。
- **修订方向**：
  - E-2 与 E-4 在同一时间窗、同一批 turn 上采集，附身份对照表（event id ↔ message/part id ↔ execution/inbox），
    并据此定出 v2 的 turn 身份约定；
  - §3.4 锚定 session.active 与 inbox 作为恢复入口；
  - E-4 断言 order/cursor 的方向语义与 desc 方向游走；
  - S-3 的验证列包含「断线缺口经 session.active 对账」。
- **闭合标准**：E-2/E-4 断言列已更新。

### F-21 [建议] 写入类取证缺少隔离环境配方

- **位置**：E-3/E-5/E-6/E-9、§3.3、§6「真实路径」、Gate C。
- **证据**：
  - v2 遵循 XDG_* 环境变量（U `util/src/global-roots.ts:5-8`；`client/src/service-probe.ts:40`）；
  - 注册文件名随 channel 变化（`service-config.ts:41-44`）；
  - latest/dev/beta/next 默认端口都是 0xC0DE（`:55-56`），隔离实例如果不在隔离配置里改端口，会与 owner 正在
    运行的 49374 冲突；
  - 方案要求「不扰动用户现有环境」，但没有给出具体做法。
- **修订方向**：S-0 定义隔离取证环境，包括：
  - 临时的 XDG_STATE/DATA/CONFIG/CACHE 目录；
  - 隔离配置里显式指定非默认端口；
  - CordCode resolver 通过测试 seam 指向隔离注册文件；
  - 清理确认步骤；
  - 哪些 E 只读复用 owner 现网（例如 E-2 的 GET 订阅），哪些只能在隔离环境做。
- **闭合标准**：相关 E 行的「获取方式」引用该配方。

### F-22 [建议] 切换时旧 managed 子进程是否停止，方案文本互相冲突

- **位置**：
  - §3.2.1「旧 `resolveManagedOpenCodeIfNeeded()` 继续只对显式旧 opencode-web 来源生效」；
  - §3.11 用户切换第 3 步「也不得把旧 managed child 当迁移清理目标」；
  - U10；
  - 非目标「不…自动停止现有 v1…服务」。
- **证据**：M `RuntimeManager.swift:1157-1162`：opencode-web 被取消选择时 guard 失败，执行
  `openCodeManagedServer?.stop()`。U10 的切换路径为 `:528-545` → launch → `:581`。
- **影响**：实施者要么保留现有代码（切换会停掉 CordCode 托管的 4096，与第 3 步冲突），要么改代码
  （与「继续生效」冲突）。产品语义不明确。
- **修订方向**：二选一写定。推荐沿用现状：CordCode 自己托管的子进程随其席位取消而停止，把第 3 步改成
  「不停止外部/共享服务」。也可以选择保活，那就修改 `:1158-1160`。无论哪种，都同步 U10。
- **闭合标准**：只留一处一致表述，U10 的「应看到」与之对应。

### F-23 [建议] 手动启动没有镜像官方 contender 的 PtyHandoff 环境

- **证据**：U `effect/service.ts:60-69` 的 spawnContender 会传入 `PtyHandoff.environment(...)`；
  `pty-handoff.ts:68-78` 从未过期的 `<service.json>.pty-handoff` 读取，生成 `OPENCODE_PTY_HANDOFF`。
- **影响**：只在一种少见竞态下出现：官方替换流程进行中，CordCode 的 contender 恰好抢到端口，
  持久终端随之丢失。方案自称「镜像官方进程/stdio 不变量」，但这一项缺失。
- **修订方向**：只读镜像该环境变量，或在 §3.2.3 登记为有意差异，并加一个 E-9 用例。
- **闭合标准**：§3.2.3 写明处理方式。

### F-24 [建议] 两项交付运营前置没有写明：版本 pin 的升版流程；remote-web 是否为放行硬前置

- **证据**：
  - S-2a「基线只接受目标 2.0.32」；§3.2.3 的 launcher 优先使用 OpenCode.app 内置 CLI。Desktop 一旦自动升级，
    服务版本就离开支持范围，行显示「版本不支持」。
  - §3.11 要求「新 iOS/remote-web 需先具备识别/拒绝能力，Mac 再开放新 driver」；而服务端 marker 门控本来就会
    把未升级客户端隔离为「需要升级」。
- **修订方向**：
  - 写明升版流程：谁触发、patch 升版的最小证据包、用户可见文案；
  - 写明 remote-web 的 marker 支持只是「在 remote-web 上提供 v2」的前置，不是 Mac 放行的硬前置；
    如果坚持作为硬前置，就写明对排期的影响。
- **闭合标准**：§6 风险表或 §3.12 补上这两项。

### F-25 [建议·低] 依赖闭包与退役可分离的机械命令没有固化

- **证据**：本轮静态核对结果见 A6；go-bridge 测试导入者共 5 个文件。
- **修订方向**：E-11b/S-9 固化 `go list -deps -test` 的根包与匹配模式（对 `agent/opencode-web` 与
  `deprecated/opencode` 的命中必须为空），并列出 S-9 演练时视为「旧包测试资源」的 go-bridge 测试文件清单。
- **闭合标准**：E-11b/S-9 的验证列包含这些命令。

---

## 8. 复审处置、剩余门与实施就绪

- **历轮意见**：
  - r1 F-1~F-9 维持闭合；本轮抽核 F-1 的承重锚 SessionsQuery，命中。
  - r2-meta 1~4、r3 F-14、r4 F-15 维持闭合。
  - r2 F-10~F-13 维持「实施期吸收」，文本仍在 §3.2.1/§3.2.4/§6，实施可行性未重核。
  - **F-16 闭合**：处置正确，见 §5。**F-17 闭合**：diff 证实。
- **新意见**：F-18（阻塞）；F-19~F-25（建议）。F-19/F-21/F-25 可以由 S-0 吸收，但须在 v1.9 写明归属。
- **证据门**：E-1 为 verified（复用 2026-10-19 活体，本轮未复测）；E-2~E-11 维持 pending，状态不因本轮改变。
- **产品决定**：OD-2/5/7 已 decided；OD-1/3/4/6/8 为工程决定或延续。F-22 需要一处明确表述：
  作者可按推荐写定，涉及停止 v1 服务语义时由 owner 确认。
- **外部格式专项（audit-plan）**：
  - 本轮按指令没有触碰 49374/4096，无法新捕获样本。
  - 方案中写成事实的形状（`/api/info`、envelope 三分型、project 裸数组、cursor 分页、目录参数矩阵）依赖
    2026-10-19 r1/r2/A.8 的活体记录，本轮只按命题复用。按 audit-plan「本轮 dump」的严格口径，
    **专项本轮未完成复测**。
  - E-2~E-5 维持 pending，对应实现被门控，符合待捕项裁定。
  - 本轮新发现的上游**源码**事实（SessionMessagesQuery、session.active、FileAttachment.uri）只作为源码证据，
    已进入 F-20/A7，没有当作活体形状。
- **implementation_readiness**：
  - 设计层面方向可接受；**冻结基线资格要等 F-18 闭合**。
  - F-18 经 v1.9 与定向复核闭合，且 owner 另行授权实施后：
    - S-0 可开工，须含 F-18 的全量枚举交付物与 F-19 的锚点重建；
    - S-1、S-2a、S-7 的组件部分依赖 E-11a；
    - S-3、S-4、S-5、S-6 仍分别受 E-2、E-4、E-3/E-5、E-7/E-10 阻塞；
    - S-2b 的放行前置不变；S-8、S-9 的门不变。
  - 授权：**无**。本报告不授予任何实施、取证写操作、服务启停或安装权限。

## 9. 终审结论

**verdict: REVISION_REQUIRED**（blockers=1，advisories=7）。

v1.8 的设计主体经得起终审：
- 单期范围在门控上自洽；
- E/S 依赖 DAG 无环，pending 证据门确实挡住对应实现；
- 手动启动四拍完整，失败态诚实；
- 桶隔离与 Kernel 单 writer 不冲突；
- 公共依赖边界可以机械检查；
- v1.8 相对 v1.7 只改了声明的位置；
- F-16 由 v1.8 作者正确处置，r6 的行号更正本身是错的。

但「`opencode` ID 复用」是本方案风险最高的设计点，承载它的 §3.11 消费者清单不完整：漏项中
`configureOpenCodeDesktopServerIfNeeded()` 会在新产品主路径上改写 OpenCode Desktop 配置并写入 v1 密码，
复现附录 A.6 的同类前提；Go、iOS、remote-web 还有多处有语义后果的未列消费点（推送隔离、SSE 启动门、
能力推导、目录改写、几十个 `.openCode` 特性门），S-0/E-11a 也没有要求机械化枚举。r6「无漏列」的结论被反证。

**闭合路径**：
1. v1.9 收编 F-18（必须）与 F-19~F-25（建议；可由 S-0 吸收的写明归属）；
2. 定向复核，范围为 §3.11、§3.2.4、S-0/S-1/S-7、E-11a/E-11b、U4/U10/U12，以及新增枚举交付物的定义；
3. 复核通过后即可作为实施冻结基线。实施启动仍须 owner 另行授权。
