# 活文档 vs 代码现状 差距审计

- **审计日期**：2026-07-03
- **审计范围**：MacBridge 仓库（`cordcode-macbridge`）与 iOS 仓库（`cordcode-ios`）的全部活文档（根目录 `.md` + `docs/protocol/`），对照当前代码与 `docs/` 近期过程文档（6/13 起）
- **方法**：4 个并行只读审计 agent，分头覆盖 (A) backend driver、(B) runtime 核心 + 构建、(C) Relay + 远程连接、(D) iOS 活文档；每个 agent 读活文档描述 → 对照代码现状 → 用过程文档「完成情况/审计报告」类确认落地。**全程未修改任何文件**。
- **状态**：差距清单，待 owner 决策是否修复。行号基于审计时快照，可能随代码漂移，使用时以符号名为准。
- **修订**：2026-07-03 owner 抽样核验后修正 3 处口径——(a) `session_pagination` 改为「阻塞理由过期但 capability 仍未宣告」（非「产品已启用」）；(b) OpenCode 服务源过时**收窄到 CLAUDE.md**，`GO_BRIDGE_ARCHITECTURE.md` OpenCode 段已对齐；(c) `question_reply` 单列为「Management API 与 hello_ack capability 来源不一致」。优先级改为下方三批顺序。
- **覆盖的活文档**：`CLAUDE.md`、`BUILD_INSTALL_AND_RUNTIME.md`、`GO_BRIDGE_ARCHITECTURE.md`、`RELAY_SERVER_OPERATIONS.md`、`README.md`、`PRIVACY.md`、`CONTRIBUTING.md`、`SECURITY.md`、`docs/protocol/{README,bridge-v1,relay-v1}.md`、`docs/{install-macos,signing-and-release,unsigned-release-notes,release-checklist}.md`；iOS 侧 `CLAUDE.md`、`AGENTS.md`、`IOS_MAC_INTERACTION_FLOW.md`、`README.md`、`REAL_DEVICE_DEBUGGING.md`、`CONTRIBUTING.md`、`SECURITY.md`。

---

## 核心结论

差距集中在 **4 个集群**：

1. **品牌改名残留（cccode → cordcode）**：跨 6+ 活文档，最广，部分会让 owner/CI 照抄的命令直接失败。
2. **CLAUDE.md 的 OpenCode 默认服务源描述过时**：`CLAUDE.md` 还写「64667 + 复用 LaunchAgent」，实际新装已是 `managed_local`（CordCode Link 自起 `opencode serve`）；`GO_BRIDGE_ARCHITECTURE.md` OpenCode 段（L154-181）已对齐，不在本条范围。
3. **一批已落地的新行为没进根活文档**：Codex transcript relay、Claude streaming partial、双路径 Phase A `locals`、Web QR Flow C、transcriptindex 索引层、iOS 双路径自动切换、message-web process group / runtime status、精品化功能集。
4. **运维真值 `RELAY_SERVER_OPERATIONS.md` 明显落后**（停在 6/24）：env 变量名、二进制名、Web QR 的 nginx 需求都没跟上。

共约 **40 条**差距。下表标 ★ 为**高严重**（会误导排查 / 让命令直接失败）。

---

## 一、品牌改名残留（cccode → cordcode）

根因：commit `2b9c155 跨仓品牌 CCCode→CordCode`、`af17200 密码学契约字面量改名`、`a4b21e4 内部标识改名`、`87c1526 CordCodeLink.xcodeproj 改名`。代码已全面改名，活文档未跟进。

| # | 文档 / 位置 | 现状 | 严重度 |
|---|---|---|---|
| ★1 | `CLAUDE.md`、`BUILD_INSTALL_AND_RUNTIME.md`、`GO_BRIDGE_ARCHITECTURE.md`、`README.md`、`RELAY_SERVER_OPERATIONS.md` 多处 | 嵌入二进制名仍写 `cccode-bridge-runtime`，实际是 `cordcode-bridge-runtime`（证据：`MacBridge/project.yml:33,47`、`go-bridge/runtime_version.go:5`、`scripts/build-unsigned-release.sh:42,49,56`、`RuntimeManager.swift:638,644`）。照抄 `pgrep -fl "cccode-bridge-runtime"` 找不到进程 | 高 |
| ★2 | `docs/install-macos.md` 整篇（:14,17,43,48,49,52） | 用旧名 `CCCodeBridge.app`、旧 bundle id `org.openagi.cccode.macbridge`、旧数据目录 `~/Library/Application Support/CCCode Bridge`、Keychain service `org.openagi.cccode.macbridge.relay`。实际是 `CordCodeLink.app` / `org.openagi.cordcode.link`（`project.yml:51`）/ `CordCode Link`，relay 凭据已迁出 Keychain 改用文件 `relay-secrets/` 0600（`RuntimeManager.swift:1104-1203`） | 高 |
| ★3 | `docs/release-checklist.md:33` | Bundle Gate 校验 `$BUILT_PRODUCTS_DIR/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime`，按此执行校验必失败 | 高 |
| ★4 | `CLAUDE.md:22,66` | relay-server module 名仍写 `cccode-relay`，实际 `relay-server/go.mod` 已是 `cordcode-relay`。**注意**：wire 协议名 `cccode-relay`（`relay_identity.go:25`）是冻结契约，保留三 c 正确，不要改 | 高 |
| ★5 | `CLAUDE.md:174` | 备份路径写成 `/opt/cccode-relay/bin/...`（三 c 的 typo），实际是 `/opt/cordcode-relay/`（`scripts/deploy-relay-vps.sh:15`），照抄回滚命令找不到备份 | 高 |
| ★6 | `RELAY_SERVER_OPERATIONS.md:60-64` | env 变量仍写 `CCCODE_RELAY_VPS_HOST/USER/PASS`，部署脚本（`scripts/deploy-relay-vps.sh:6,20-22`）和 `CLAUDE.md:131-133` 已改 `CORDCODE_RELAY_VPS_*`。按 ops 文档配新机器会部署失败 | 高 |
| 7 | `docs/unsigned-release-notes.md:3` | 仍写 "CCCode MacBridge" | 低 |
| 8 | `CLAUDE.md:139` + `scripts/deploy-relay-vps.sh:7,14` | ssh alias `cccode-relay-prod`（两处自洽，非 bug，但若要彻底统一需同步三处） | 低 |

> **附带发现（代码侧，非文档）**：`go-bridge/pagination.go:31` 的 transcriptindex 目录仍是 `~/.cccode/transcript-index`，是改名遗漏点——活文档本身也没记录这个路径。修复时建议一并改为 `.cordcode`。

---

## 二、OpenCode 默认服务源（CLAUDE.md 与现实脱节最严重）

| # | 文档 / 位置 | 现状 | 严重度 |
|---|---|---|---|
| ★9 | **（范围收窄）仅** `CLAUDE.md`「Backend runtime model」表 OpenCode 行（:210）+「OpenCode server」段（:235）+ `:64667/health` 排查命令（:241） | 仍写「默认 `http://127.0.0.1:64667`，复用 `com.opencode.server` LaunchAgent 凭据」。实际新装默认 = `managed_local`：CordCode Link 作为 supervisor 自起 loopback-only `opencode serve`（端口 `4096…4196`，`opencode-managed-server.json` 0600 存托管凭据），64667 仅作 legacy 升级连续性路径。证据：`docs/2026-07-03-opencode-managed-local-server-seamless-plan完成情况.md`、`BUILD_INSTALL_AND_RUNTIME.md:211`、`docs/backends-and-config.md:52`、commit `f2f2cf6`。**`GO_BRIDGE_ARCHITECTURE.md` OpenCode 段（L154-181）已相当新**（明确写了 managed_local / legacy_64667 / 无 URL 不回落 64667 / SSE 空 URL fail-closed），**不需改**；#12 同理 | 高 |
| ★10 | `docs/protocol/bridge-v1.md:218` | 写「Array-only OpenCode server tracks return `hasMore=false`; product pagination is then limited to the first page for that project」。实际 go-bridge 已对 OpenCode 单次拉上游 100 条（`openCodeSessionFetchLimit=100`），再由 `paginateSessionList` 内存 cursor+limit 分页，`hasMore` 来自真实剩余量（`handlers.go:930,946-966`，commit `41b7148`）。这句话会让 iOS 误判 OpenCode 永远无法翻页 | 高 |
| 11 | `BUILD_INSTALL_AND_RUNTIME.md:27` 端口表 | 把 `4096…4196`（managed）与 go-bridge 自身 8777/8778/管理端口并列，归属误导——该范围只在 Swift 端 `OpenCodeManagedServer.selectPort` 实现，go-bridge 无引用 | 低 |
| 12 | `CLAUDE.md` OpenCode 行 liveEvent/polling 列 | 未说明「无 SSE URL 时 `shouldStartPassiveSubscription` fail-closed、不再退避重连 64667」。**注**：此点 `GO_BRIDGE_ARCHITECTURE.md` L154-181 已覆盖，仅 CLAUDE.md 高层表缺；与 #9 同属「CLAUDE.md 落后、GO_BRIDGE_ARCH 已对齐」，可并入 #9 一起修（`agent/opencode/sse_subscriber.go:64-66,790-792`、`opencode.go:78-81`） | 中 |

> 注：`GO_BRIDGE_ARCHITECTURE.md` 的 OpenCode 段（L154-181）本身较新、已提到 managed_local / `not_configured` / no-auth `200` 拒绝 / SSE URL 空时 false，**不需改**。

---

## 三、分页 / transcriptindex 叙事（GO_BRIDGE_ARCHITECTURE.md 滞后）

| # | 文档 / 位置 | 现状 | 严重度 |
|---|---|---|---|
| 13 | `GO_BRIDGE_ARCHITECTURE.md:221-223,252` | 写「`session_pagination` 刻意关闭，重新启用必须先解决稳定游标 + 大内容分片」。**实现层确实已就位**：`go-bridge/pagination.go` 有稳定游标（`MessageCursor{SessionID,Ordinal,Generation}`）+ 256KB byte-budget（`maxPageResponseBytes`），`handlers.go:3661` 在 `Paginate=true` 时调用——所以文档的**阻塞理由（「需先解决稳定游标」）已过期**。**但** `session_pagination` capability 在 `agent_descriptor.go:115-123` 仍被注释关闭：真实阻塞原因是 backward paging 在长 session 上的 newest↔backward UI 振荡 + relay 帧上限，而非缺稳定游标；运行态对客户端仍不可见、iOS 走全量 fallback。**定性**：不是「产品已启用而文档错」，而是「文档的阻塞诊断过期，但『capability 仍未宣告』这一结论本身正确」 | 中 |
| ★14 | `GO_BRIDGE_ARCHITECTURE.md` 全文 | `transcriptindex/`（13 源文件 + 测试的边界安全索引包）**完全没出现**（grep 0 命中），但它已被 `go-bridge/pagination.go:18`、`handlers.go`、`agent/claudecode/claudecode.go`、`agent/codex/rich_history.go` 实际引用，`core/interfaces.go` 的 `TranscriptLocator` 接口注释明确指向它 | 高 |

---

## 四、已落地新行为未进根活文档

| # | 缺失内容 | 证据 | 严重度 |
|---|---|---|---|
| ★15 | Codex 的 `startCodexSessionFileRelay`（transcript relay 与 session relay 并行）未在 `GO_BRIDGE_ARCHITECTURE.md` Codex 段（:124-150） | `go-bridge/handlers.go:3641`、commit `69697c3`/`7d66cc7` | 高 |
| ★16 | Claude streaming partial（`--include-partial-messages` flag）+ `historyDraining` 抑制 resume 重放，未在 Claude 段（:107-121） | `agent/claudecode/session.go:48,100,261-274,556,596`、commit `b7ca1cf`/`7ab3207`/`eff4d89` | 高 |
| ★17 | 双路径 Phase A：relay-first 配对携带 LAN 候选、`hello_ack.locals` 字段——`bridge-v1.md` 正文、`CLAUDE.md` 均无（sample json 有 `locals`，但语义/排除规则/兼容结论未文档化） | `go-bridge/relay_first_pairing.go`、`advertise_url.go`、`bridge_v1_schema.go:29,35`、`hello_handler.go:61,67`、commit `6bf7b81` | 高 |
| 18 | Web QR 配对（Flow C / `webQrPayload`）只在 `relay-v1.md §12.2`，`RELAY_SERVER_OPERATIONS.md` 的 nginx 要点（:98-104）未提 `/web/` 静态路径与 `relay-https-host` scheme 改写的部署需求 | `relay-server` 需同时 serve `wss://` 与 `/web/`、commit `72dcfa9` | 中 |
| 19 | `GO_BRIDGE_ARCHITECTURE.md` capability 表（:204-219）漏 `content_chunking`（Claude 专属，对应 `fetch_content_chunk`）、`permission_resolve`（Claude 专属）——这两项 `deriveCapabilities()` 会发、iOS 可见，但活文档表未列 | `agent_descriptor.go:141-151` | 中 |
| 19b | **（定性：capability 来源不一致，非「漏项」）** `question_reply` 在 `BackendList()`（`handlers.go:400`，Management API `/internal/agents`）对 codex app_server 发，但 `deriveCapabilities()`（`agent_descriptor.go:103-160`，`hello_ack.backends[]` 路径）**不发**——而 `agent_descriptor.go:102` 注释自述「逻辑与 handlers.go BackendList() 保持一致」，实际 `question_reply` 漏在 deriveCapabilities。这是**代码侧来源分叉**（若要让 iOS 收到需补 deriveCapabilities）；活文档应明确「iOS 可见能力以 `hello_ack.backends[]`（deriveCapabilities）为准，`/internal/agents` 的 `question_reply` 不下发」 | `handlers.go:398-401` vs `agent_descriptor.go:102,155-157` | 中 |
| 20 | Claude 模型/effort 真值源 = `~/.claude/settings.json`（`ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL` + `*_MODEL_NAME`，mtime 懒重载），活文档未说明 | `agent/claudecode/settings_models.go`、commit `5c4e8c5`/`20389d5` | 中 |

---

## 五、CI / 构建工具链（CLAUDE.md 过期）

| # | 文档 / 位置 | 现状 | 严重度 |
|---|---|---|---|
| 21 | `CLAUDE.md:88-89`「CI (`.github/workflows/ci.yml`) runs gitleaks, `go test` on macos-latest, and the Xcode build」 | 实际三 job：`secret-scan`(ubuntu, gitleaks `v8.21.2`)、`go`(macos-latest, `go-version-file: go.mod`→toolchain go1.26.4, `npm install -g @openai/codex`, 跑 `govulncheck ./go-bridge/...` 与 relay-server)、`macbridge`(`runs-on: macos-26`, `sudo xcode-select -s /Applications/Xcode_26.5.app`, 跑 xcodebuild + build-unsigned-release + upload-artifact)。commits `273d5b3`/`3e1cd3e`/`c5ab578` | 中 |
| 22 | `CLAUDE.md:272-273` runtime.json 字段 | 漏列 `bridgeEpoch`（实际四字段：`port`/`pid`/`managementUrl`/`bridgeEpoch`，防同 PID 残留文件误判，`runtime_startup.go:17-24`、`RuntimeManager.swift:163-164,466-473,757`）。`BUILD_INSTALL_AND_RUNTIME.md:112` 已提，仅 CLAUDE.md 这段漏 | 低 |

---

## 六、运维真值 RELAY_SERVER_OPERATIONS.md 落后（6/24 后未更新）

除上面 #6（env 名）、#18（Web QR nginx）外：

| # | 位置 | 现状 | 严重度 |
|---|---|---|---|
| 23 | ops 文档多处（:76-77,88,108,160）+ 脚本 :15-16 | 二进制名混乱：文档写 `/opt/cordcode-relay/bin/cordcode-relay-server`，但脚本远程路径实际是 `/opt/cordcode-relay/bin/relay-server`（无前缀，`REMOTE_BIN`），本地构建产物才带 `cordcode-` 前缀（`LOCAL_BIN=/tmp/cordcode-relay-server`）。ops 文档:160 的回滚 `sha256sum` 命令指向不存在文件 | 中 |
| 24 | ops 文档:3 + 脚本:9-10,54 | 引用 `relay-server-install.md`（迁移自原一体仓库 `../opencode-cc-connect/`）做首次部署，但**本仓库内不存在此文件**，首次安装的完整命令无真值落地（ops 文档:82-104 只给推荐布局与 nginx 要点，未含完整首次安装命令） | 中 |
| 25 | ops 文档:87-90 | data 目录写 `/opt/cordcode-relay/data/`，代码默认 `RELAY_DB_PATH=/var/lib/cordcode-relay/relay.db`（`relay-server/cmd/relay-server/main.go:21`） | 低 |

> **经核实仍准确、无需改动**：三路径/TLS pin 论断（`CLAUDE.md:284-294` vs `tls_cert_store.go` + `resolveTailscaleRemote`）；relay-v1 全部协议字段/schema revision `2026-05-24-r1`/HPKE/endpoint `wss://relay.byteseek.uk:8443`；Ed25519 activation identity + 0600 权限；CONTRIBUTING/SECURITY 的 module 边界。HKDF info `cordcode-relay/...` 改名已落地且与 `relay-v1.md` 一致。

---

## 七、Mac app runtime 行为描述不全（BUILD_INSTALL_AND_RUNTIME.md + CLAUDE.md）

`BUILD_INSTALL_AND_RUNTIME.md` 和 CLAUDE.md 只提了 120min 兜底重启 + 8MiB×3 日志滚动，但 `RuntimeManager.swift` 实际还有：

| # | 缺失行为 | 证据 | 严重度 |
|---|---|---|---|
| 26 | 三类自愈：卡 `.starting` 60s 自动重启（`stuckRestartThreshold`，连续 `maxStuckRestarts=5` 后停止并判 `.crashed`）、连续意外退出 `maxCrashRetries=3` 后停止——日志「从某时刻重新开始」也可能是 60s 卡住触发，非仅 120min 兜底 | `RuntimeManager.swift:166,178,180,377-381,419-447` | 中 |
| 27 | stale-port-takeover 实际规则：仅当占用者是自家二进制（`cordcode-bridge-runtime` / `/go-bridge/go-bridge`）才 SIGTERM→SIGKILL，不可接管直接判 `.crashed`；且每次启动自动 `disableLegacyGoBridgeLaunchAgents`（bootout 含 `/go-bridge/go-bridge`+8777 的旧 plist 并改名 `.disabled-by-cccodebridge`） | `RuntimeManager.swift:607-686` | 中 |
| 28 | `-tls-port 8778` 实为 go-bridge flag 默认值承担（`main.go:47`），RuntimeManager `processArguments` **不传**——文档说成「产品态传 8778」 | `RuntimeManager.swift:953-982` vs `go-bridge/main.go:47` | 中 |
| 29 | flag 表（BUILD:41-55）漏 `-relay-service-addr` / `-pairing-include-tailscale` / `-pairing-include-remote`（`RuntimeManager.swift:975-979`）；环境变量表（:64-66）漏 PATH 合并注入（`~/.bun/bin`、`~/.local/bin`、`/opt/homebrew/bin`、`/Applications/Codex.app/...`，`:1025-1036`）；自动重启（:182）漏「5 分钟下限 + `autoRestartEnabled` 开关 + 实时可调」；sleep/wake（:189）漏「等 2s + 唤醒必重启（重置 crashCount）」 | `RuntimeManager.swift` 多处 | 低 |

---

## 八、iOS 活文档（IOS_MAC_INTERACTION_FLOW 维护好；CLAUDE.md/README/AGENTS 落后）

跨仓库权威 `IOS_MAC_INTERACTION_FLOW.md` 和 `CHANGELOG.md` 维护得很好（RecoveryCoordinator 退让机制、mailbox 恢复、snapshot 真实性规则、`BackendUnavailable` 恢复顺序、飞行模式重连风暴修复 single-flight + wakeBackoff 均准确）。差距主要在更高层：

| # | 文档 / 位置（iOS 仓库 `../cordcode-ios/`） | 现状 | 严重度 |
|---|---|---|---|
| ★30 | iOS `CLAUDE.md:3` | 自述「本文件通过 `AGENTS.md` 软链接同时提供给 Claude Code、Codex 及其他 coding agent」，但 `AGENTS.md`(283 行) 与 `CLAUDE.md`(384 行) **非软链且内容不一致**——`AGENTS.md` 缺整节「部署 remote-web 静态包到 VPS」（约 80 行）。Codex 等读 `AGENTS.md` 的 agent 拿不到完整 runbook。**注**：MacBridge 侧 `AGENTS.md` 是真软链（`AGENTS.md -> CLAUDE.md`），此问题仅 iOS 侧 | 高 |
| 31 | iOS `IOS_MAC_INTERACTION_FLOW.md §2:92-94` | 连接策略写「受控竞速」，实际是 **LAN-first + Relay fallback**（direct 多候选内部才竞速）。`BridgeProvider.selectConnectionStrategy` 显式三分支：`directPhase(allowRelayFallback:)` / `relayOnly`（蜂窝 only 或无 direct 候选）/ `deferredToRecovery`。过程文档标题本身就是 `lan-relay-**fallback**` | 中 |
| 32 | iOS `CLAUDE.md` / `README` / `IOS_MAC_INTERACTION_FLOW` 全文 | 双路径自动切换（WiFi↔蜂窝 在 LAN/Relay 间迁移，`BridgeProvider.shouldSwitchPath`/`evaluateAndExecutePathSwitch`）+ 标题栏连接标识（`ConnectionIndicatorIcon`）——已 owner 真机验收，活文档无记录 | 中 |
| 33 | iOS `CLAUDE.md`「message-web → WKWebView 管线」段 | 漏 message-web 侧的 `ProcessGroup`（过程组聚合，`isExecuting` 时自动展开）和 `RunningStatusBar`（运行时长/token/思考/工具/等待授权/等待回答/压缩上下文/即将完成的状态横条）两大新渲染概念 | 中 |
| ★34 | iOS `README.md:36-41` Release Status | 仍写「clean split candidate, public release still requires owner approval」，与已 owner 真机验收通过的精品化升级（图片/文件附件、Share Extension、听写、Prompt 模板、Task Dock、测试解析、连接健康、项目档案）现实脱节。**注意**：「premium」是精品化命名，代码里无 StoreKit/IAP/订阅逻辑 | 高 |
| 35 | iOS `CLAUDE.md` ChatViewModel extension 清单（:344-348） | 漏 `+AgentRuntimeStatus`（运行时状态横条的 iOS 侧驱动） | 低 |
| 36 | iOS `CLAUDE.md` | 漏 onboarding 重设计（单主操作扫码页 + 四类状态页 + `PairingFailure` 错误模型，commit `05c30d92`）；漏用户可主动关闭 Relay 的 UI（`ServerSettingsView.relayManagementRow` + `SavedBridgeStore.disableRelayCredentials`，commit `7cb00c22`） | 低 |

> iOS 侧 codemote → CordCode 改名**落地彻底、无残留**（grep 全仓库仅 handoff 历史文档保留）；`REAL_DEVICE_DEBUGGING.md` 已更新（commit `86e393df`），无差距。

---

## 优先级建议（按 owner 2026-07-03 核验后定的顺序）

> 修活文档属可逆工程细节，按下面三批推进；每批改完跑一次构建/链接校验再继续。

**第一批 — 照抄会失败的 MacBridge 活文档**（先消除「按文档执行的命令直接失败」）
- runtime 二进制名 `cccode-bridge-runtime` → `cordcode-bridge-runtime`（#1，跨 CLAUDE.md / BUILD / GO_BRIDGE_ARCH / README / RELAY_OPS）
- relay env `CCCODE_RELAY_VPS_*` → `CORDCODE_RELAY_VPS_*`（#6，RELAY_SERVER_OPERATIONS.md）
- relay 备份路径 `/opt/cccode-relay/` typo + module 名 `cccode-relay` → `cordcode-relay`（#4、#5，CLAUDE.md；注意 wire 名 `cccode-relay` 是契约、不动）
- relay 二进制名订正为 `/opt/cordcode-relay/bin/relay-server`（#23，RELAY_SERVER_OPERATIONS.md）
- `docs/install-macos.md` 整篇旧名 + 旧 bundle id + 旧数据目录 + Keychain service（#2）
- `docs/release-checklist.md` Bundle Gate 旧 app/二进制名（#3）

**第二批 — 架构认知**（让活文档反映真实运行态与 backend 行为）
- OpenCode source：收窄到 CLAUDE.md，改为 managed_local（#9，含 #12 的 SSE fail-closed）
- transcriptindex 索引层补进 GO_BRIDGE_ARCHITECTURE.md（#14）
- session_pagination 阻塞理由订正（#13）
- Codex transcript relay + Claude streaming partial / history drain 补进 GO_BRIDGE_ARCHITECTURE.md（#15、#16）
- runtime 自愈规则（60s 卡住 / maxStuckRestarts / maxCrashRetries / stale-port-takeover / 自动禁用旧 LaunchAgent）补进 BUILD + CLAUDE.md（#26、#27）
- 双路径 Phase A `locals` 进 bridge-v1.md 正文 + CLAUDE.md（#17）
- Web QR Flow C 的 nginx `/web/` 部署需求进 RELAY_SERVER_OPERATIONS.md（#18）
- capability 来源不一致（question_reply）定性（#19b）
- CI / 工具链描述更新（#21）

**第三批 — iOS 活文档 + README 状态**
- iOS AGENTS.md ≠ CLAUDE.md（自述软链失实）（#30）
- iOS README release 状态 + 精品化功能集（#34）
- IOS_MAC_INTERACTION_FLOW 连接策略措辞改为 LAN-first + Relay fallback（#31）
- 双路径自动切换 + 标题栏标识、message-web process group / runtime status（#32、#33）
- onboarding 重设计 / disable-relay UI / ChatViewModel extension 清单（#35、#36）

**余项（低优先，措辞/数值/清单，随手机会清）**：#7、#8、#11、#20、#22、#25、#28、#29（#29 覆盖 flag/env/自动重启/sleep-wake 表）。

---

## 附录：审计方法与证据来源

- **四个审计 agent 的领域划分**：
  - (A) backend driver：`agent/{opencode,codex,claudecode}/` + `core/interfaces.go` vs `GO_BRIDGE_ARCHITECTURE.md` / `CLAUDE.md` backend 表 / `docs/protocol/{README,bridge-v1}.md`
  - (B) runtime 核心 + 构建：`go-bridge/` 根、`transcriptindex/`、`MacBridge/.../RuntimeManager.swift`、`project.yml`、`ci.yml`、`scripts/` vs `BUILD_INSTALL_AND_RUNTIME.md` / `GO_BRIDGE_ARCHITECTURE.md` runtime 部分 / `docs/install-macos.md` 等
  - (C) Relay + 远程连接：`relay-server/`、`go-bridge/` relay 相关、`tls_cert_store.go`、`scripts/deploy-relay-vps.sh` vs `RELAY_SERVER_OPERATIONS.md` / `CLAUDE.md` 远程路径 / `docs/protocol/relay-v1.md`
  - (D) iOS 活文档：iOS 仓库源码 vs iOS `CLAUDE.md`/`IOS_MAC_INTERACTION_FLOW.md`/`README.md` 等
- **过程文档证据**：6/13 起的 `docs/2026-06-*` / `docs/2026-07-*` 系列，优先采用「完成情况」「审计报告」类文件确认改动落地。
- **git 提交证据**：通过 `git log --oneline --since=2026-06-13 -- <path>` 拉取主题相关提交，对照活文档描述。
- **正向发现（已核实仍准确，无需改动）**：`GO_BRIDGE_ARCHITECTURE.md` 事件管线映射表（:228-239）与 `events.go` 一致；polling 边界（Claude/Codex stdio/OpenCode 均 `requiresPollingForExternalTurns=true`）与 `agentRequiresPolling` 一致；三路径/TLS pin 论断；relay-v1 协议字段/schema/HPKE/endpoint；CONTRIBUTING/SECURITY module 边界；iOS `IOS_MAC_INTERACTION_FLOW.md` 的 RecoveryCoordinator/mailbox/snapshot 描述；iOS codemote 改名落地干净。
- **本审计的 memory 索引位置**：差距清单的 memory 指针写在 Claude Code 的外部项目记忆 `~/.claude/projects/-Users-jacklee-Projects-cordcode-macbridge/memory/MEMORY.md`（**不在仓库内**，由 harness 自动加载到 session context）。仓库内不会出现 `MEMORY.md`；下一轮 agent 若在仓库根目录找它会扑空——**仓库内的执行清单真值以本文件（`docs/2026-07-03-living-docs-gap-audit.md`）为准**。`docs/upstream-memory/` 与代码里的 agent memory 文件是不同东西，不要混淆。
