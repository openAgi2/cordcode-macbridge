# 方案评审报告 r3：codex-remote 恢复循环探针经济性优化（P1）

- verdict: APPROVED
- plan: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-12-codex-remote-probe-economy-p1-plan.md`
- plan_sha256: `28bd60b1904f753c896d9b9ac77b79a4aac46cce90bfa6465d8cf56e85a9c880`
- plan_version: v3（对照 r2 所审 v2 sha `81b94d991141a47c464201e29868a3daa3a6f1334f85037c5fcd0a07830ccb23`；v1 sha `c5690bf39cf6bb53673f7788c5b1f0e01d8f378ed7ca4fcc17641d4a13269048`）
- scope: full（评审员模式 **fresh**：未见历轮结论前独立读方案与源码形成风险清单，再以 r1/r2/r2-meta 作回归清单）
- blockers: 0
- advisories: 0
- contract_version: plan-contract-v1.1
- round_complete: true
- implementation_readiness: **设计可接受，切片尚未授权开工。** 评审通过 ≠ 写代码 / 定向测试以外的构建 / 覆盖安装 / 重启 runtime。S-1～S-3 在 owner 实施授权后可按序开工；S-4 另须 owner 拍板重启窗口（避开配套 iOS 工作树 `cordcode-ios-native-message-timeline` 上的 timeline 真机测试）；S-5 依赖 S-4 对账。E-9 无残留样本，不阻塞设计，禁止当硬 TTL。

## 结论

v3 是 r2 APPROVED 之后的通过后纠错修订：设计语义（判定式、切片、测试与对账口径）与 r2 已审控制流一致；本轮补录的配套 iOS 工作树身份与 `ctrlRefreshExec` 声明锚点均经亲核成立。需求 R-1～R-5、非目标、无 OD、上游三级刷新 / 生命周期缓存 / 401 恢复优先三条语义，与本仓现状及既有 seam 对齐，覆盖足够。

本轮无设计级阻塞，无新建议。r1 F-1～F-7、r2 F-8、r2-meta issues#1 全部闭合。

## 来源与覆盖

本轮来源门（读取源码前生成；方案 §2.1 的本仓/上游身份未漂移；配套 iOS 身份为本轮独立亲核，与方案补录一致）：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=ff990f73be81a55f94e9f799c9b8a9fa7d0e1662
未提交状态=产品代码干净；未跟踪 4 个 docs 文件：本方案 + r1/r2/r2-meta 评审报告（本轮报告写入前计数；写入后将再增本文件）
任务预期分支=纯 Mac 侧 runtime 行为优化（feat/ios-native-message-timeline worktree，无 iOS 代码改动）
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
配套分支=feat/ios-native-message-timeline
配套提交=7e32a62163ef66ab855eb71d571b38b1823574da
配套未提交状态=脏（status --porcelain 精确 275 行：273 M + 2 ??；M：docs/ 266、.exec-plan/ 6、OpenCodeiOS 1；??：.exec-plan/native-timeline-status-index.json、.exec-plan/state/plan-4f2d48b2dd54.json）。本轮未读 iOS 源码、不修改 iOS 仓。
预期产品特性=cordcode-bridge-runtime（drivers 不变）

上游仓库路径=/Users/jacklee/Projects/codex
上游分支=main
上游提交=cc7ba33601286a751477832b01de7b6f8de0dd9c
上游未提交状态=干净
```

方案正文 SHA-256 与派发时一致：`28bd60b1904f753c896d9b9ac77b79a4aac46cce90bfa6465d8cf56e85a9c880`（36950 bytes）。

Mac 工作树枚举：`cordcode-macbridge` main、`cordcode-macbridge-codex-timeline-parity`、本树、`cordcode-macbridge-plan-approval`（detached）、`cordcode-macbridge-widget-support`。iOS 同名功能分支族唯一对应为 `cordcode-ios-native-message-timeline`（其余为 main checkout、`codex/chatgpt-codex-timeline-parity`、`codex/ios-widget-support` 与 3 个 detached，其中 2 个位于该树 `output/` 下的实现暂存树）。

已核范围（本轮亲核；不以 r1/r2「已独立核实」代替。高风险/影响通过结论的锚点全部亲核；低风险说明抽样并入表）：

- 本仓：`agent/codex-remote/pairing_persist.go`（persist schema、restoreOnce、恢复循环、watchBinding、maybeRefreshCtrlToken、runCtrlTokenRefresh、offline 映射、restoreMu）、`pairing.go`（struct seam、jsonRequest、refreshControlToken 401 位点、lookupDesktopEnv、Start、SubmitCode、invalidateRevokedPairing、applyCtrlToken）、`chatgpt_auth.go`、`jwt.go`、`backoff.go`、`diagnostics.go`（StartRemoteControl / loadChatGPTAuth）、`ws.go`（dialControllerWS / parseExpiresUnix）、`pairing_ctrl_refresh_test.go`、`pairing_test.go` restore / revoked / offline 横幅。
- 上游：`enroll.rs` SKEW 与三级判定、`server_api.rs` NotNeeded no-op / 24–36s 退避、`auth.rs` capture / load / recover、`mod.rs:460,704`、`websocket.rs` 401/403 与 `next_reconnect_delay`。
- 事故档案 §3.2/§3.3/§5.2/§5.5（E-10 与风暴基线）；CHANGELOG `[Unreleased]` 与 commit `7d147cdc` / `ff990f73` 往来（P1 默认不记条目的依据）。
- 配套 iOS 工作树身份：`git worktree list` + `rev-parse` + `status --porcelain` 精确计数（本轮亲核，不转引 r2-meta）。

未核 / 不影响通过结论：

- E-9 原始 JWT 字节（方案自述已销毁）；本轮未重捕。设计以当场 `exp` + `hasExp=false` 防御，不依赖 96h。
- 未跑 audit-plan 专项：本方案不是外部记录族解析器；JWT 顶层 `exp` 为标准 NumericDate，缺省走失败驱动，不把已销毁样本当协议真值。
- 交互四拍：无新用户入口，N/A。
- iOS 仓源码内容：任务范围纯 Mac，本轮未引用；身份已记录。
- 配套 iOS 工作树未提交文件的内容：只核身份与计数，未读那些脏文件。

### 锚点核验表

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录（带行号） | 定位结果 / 语义结果 | 亲核或复用依据 |
| --- | --- | --- | --- | --- |
| E-1 `enroll.rs:26` SKEW；`:246-263` 三级判定 | 上游 `codex-rs/app-server-transport/src/transport/remote_control/enroll.rs:26`；`:246-263` `server_token_refresh_requirement_at` | `:26` `const REMOTE_CONTROL_SERVER_TOKEN_REFRESH_SKEW_SECS: i64 = 5 * 60;`；`:250-263` 无 token/`expires_at`→Required；`expires_at <= now`→Required；`expires_at > now+SKEW \|\| next_refresh_at > now`→NotNeeded；否则 Proactive | 定位准 / 语义准。方案写明不镜像 `next_refresh_at` 一侧（D1），与源码差异登记清楚 | 本轮亲核 |
| 调用点 `mod.rs:460`、`:704` | 同目录 `mod.rs:460`、`:704` | `:460`/`:704` `if enrollment.should_refresh_server_token() {` 才 `refresh_pairing_enrollment` | 定位准 / 语义准 | 本轮亲核 |
| E-2 `server_api.rs:134-151` | `:134-151` | `NotNeeded`→`Ok(())`；Required 且 `next_refresh_at > now`→`retry_deferred` | 定位准 / 语义准 | 本轮亲核 |
| 失败退避 `server_api.rs:27-28`、`:366-387` | `:27-28` 24/36s；`:366-386` `refresh_deferral` + 随机 backoff | `BACKOFF_MIN_SECS = 24` / `MAX = 36` | 定位：`:387` 为函数结束空行，目标符号止于 `:386`。语义准；非目标不改 backoff | 本轮亲核 |
| E-3 `auth.rs:1-2,29-46,89-96,98-123` | 同 | `:1-2` 「Credentials and recovery bound to one remote-control login lifetime」；`:29-46` `capture`；`:89-96` `load_remote_control_auth`；`:98-123` reload 至多一次 | 定位准 / 语义准 | 本轮亲核 |
| E-4 `auth.rs:177-214`；`websocket.rs:1512-1520`（`:1518` 文案） | 同 | 恢复 `has_next`/`next`；WS `401\|403`→清 server token→「refreshing server token before reconnect」 | 定位准 / 语义准。官方多步迭代器 vs 方案单步 re-probe 为有意收窄 | 本轮亲核 |
| E-5 `pairing_persist.go:266-279` 无条件探针+刷新 | 同 | `:266` `p.chatGPTAuth`；`:274-279` refresh；非撤销只 Warn「trying saved token」 | 定位准 / 语义准（F-2 回退仍在） | 本轮亲核 |
| 恢复循环 `:299-330` | `:299-329` 循环，`:329` `watchBinding` | store 存在则无限 `restoreOnce` + backoff | 定位准 / 语义准 | 本轮亲核 |
| E-6 `pairing.go:401-403,426-428` | `:401-403` start；`:426-428` finish | `status == 401 \|\| 403 { return errPairingRevoked }` | 定位准 / 语义准 | 本轮亲核 |
| E-7 `pairing.go:440-449` | 同 | `:446-447` token/clientID 空→`pairing state incomplete`；`:449` `raw, _, err := p.jsonRequest(...)` 丢弃 status | 定位准 / 语义准；D2 接 status 的必要性成立 | 本轮亲核 |
| E-11 persist 无 account JWT | `pairing_persist.go:95-106`；restore `:249-260` | 字段仅 client/env/ctrl/key；restore 写 ctrl 侧，不写 `state.token` | 定位准 / 语义准（F-1 依据） | 本轮亲核 |
| E-8 `pairing_persist.go:19-23` lead=60s / 600s 注释 | `:23` `ctrlTokenRefreshLead = 60 * time.Second`；调度 `:47-52` | 注释 E-9 五样本 600s | 定位准 / 语义准 | 本轮亲核 |
| JWT `chatgpt_auth.go:125-150`、`jwt.go:8-14` | 同 | `accountIDFromJWT` 只解 `https://api.openai.com/auth` 下 account id，无顶层 `exp`；`decodeJWTSegment` `:8-14` | 定位准 / 「扩展顶层 exp 是小改」语义准 | 本轮亲核 |
| R-5 显式路径 | `diagnostics.go:55` `loadChatGPTAuth`；`pairing.go:228-233` `Start` 收显式 token；`SubmitCode` `:489-493` 用 state 内 token | 诊断直连探针；`StartRemoteControl` 无 persist 时走 `loadChatGPTAuth`；配对不经 Cached | 定位准 / 语义准。`StartRemoteControl` 在已有 persist 时走 `reconnectFromStore`→`restoreOnce`（实施后走 Cached）属恢复路径，不是用户显式取新鲜 auth | 本轮亲核 |
| 既有 seam `authTokenFn` / `ctrlRefreshExec` | `pairing.go:73,133-138` 声明+拦截；`pairing.go:83-84` **声明** `ctrlRefreshExec`；调用点 `pairing_persist.go:58-61`；restore 测试 `pairing_test.go:278-280` | `chatGPTAuth` 优先 `authTokenFn` 否则 `loadChatGPTAuth`；`:83-84` `lastCtrlRefreshAttempt` / `ctrlRefreshExec` | v3 声明锚点已改对；`pairing_persist.go:83-84` 仍是 `p.mu.Lock()` / `p.state.token = token`。语义「两套 seam 已存在、应复用」成立 | 本轮亲核 |
| `ws.go:108-110` 握手失败不映射撤销 | 同 | `if err != nil { ... return ..., fmt.Errorf("controller websocket handshake failed") }` | 定位准 / F-5 故意差异依据成立 | 本轮亲核 |
| E-10 Desktop 关闭 refresh 仍成功 | 事故档案 §3.2 步骤 2 + §3.3 一对一表 + §5.5：11.5 min 53 次 `token_refreshed`、57 次 `no desktop environment` | 刷新成功发生在 env 失败之前 | 文档证据，非本轮新采样 / 与源码顺序一致 | 本轮亲核文档 |
| 风暴 ~4.6/min | 事故档案 §3.2/§5.5；`backoff.go:16-17` 1s→30s 封顶 | 11.5 min 53 刷新 | 文档 / 语义准 | 本轮亲核文档 |
| `next_reconnect_delay` | `websocket.rs:1366-1374` | 指数封顶 + jitter | 非目标不改；定位准 | 本轮亲核 |
| CHANGELOG 默认不记 P1 | commit `7d147cdc` 删除、`ff990f73` 恢复 P0 条目（owner 往来） | 当前 `[Unreleased]` 仍含 2026-10-12 僵尸收尸节 | 方案「P0 同类条目曾被移除」与 git 往来相符；P1 默认不记是工程约定，非产品 OD | 本轮亲核 |
| 配套 iOS 身份 §2.1 / §8.1 | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` `feat/ios-native-message-timeline` `7e32a62163ef66ab855eb71d571b38b1823574da` | `git worktree list` 7 项中同名功能分支唯一对应该树；porcelain 精确 275 = 273 M + 2 ??（docs 266 / .exec-plan 6 M + 2 ?? / OpenCodeiOS 1） | 路径/分支/提交吻合；未提交计数与方案一致（`wc -l` 会因带空格路径拆行虚高，精确计数须按 porcelain 记录数） | 本轮亲核 |
| D4 Info 落点 `loadChatGPTAuth` Stat 之后 | `chatgpt_auth.go:52-56` Stat 早退在 spawn 前；`:65` `exec.CommandContext` 为真实 spawn | 现状无 `auth_probe` 行（拟议新增）；Stat 早退路径确实无 token、发生在 spawn 前 | 定位准 / 「Info 仅真实 spawn」可实施；本轮不把尚未存在的日志当事实 | 本轮亲核 |

## 需求与门控

| ID | 方案覆盖 | 本轮判断 |
| --- | --- | --- |
| R-1 缺席风暴探针→缓存窗口内 0 | S-1+S-2；对账 `auth_probe`（真实 spawn）≈1 / 10 min | 成立。D1 每轮 Cached、只门控刷新；冷启动 ctrl fresh 不再卡 incomplete |
| R-2 在线刷新复用缓存 | `runCtrlTokenRefresh` 改 Cached+WithRecovery；lead=60s 不改 | 成立。在线 ~9 min（600−60）与缺席 Proactive ~5 min 已分开对账 |
| R-3 401 先恢复再撤销 | D3 / S-3；既有 revoked 测试改为 401×2 仍撤销 | 成立，且与 F-2 回退并存 |
| R-4 特征日志 | D4 Info 仅真实 spawn；`ctrlFresh=true` 挂在既有 Warn | 成立 |
| R-5 配对/诊断直连 | D1 点名 `loadChatGPTAuth` / `Start` 显式 token / `SubmitCode` 沿用 | 成立 |
| 非目标 backoff / watchBinding lead / 探针机制 / P2 / offline 可见语义 | 正文未改这些切片；F-5 撤销时延已登记为故意差异 | 成立 |
| OD | 无 | 同意：SKEW=300s、exp 定界、恢复优先均有上游锚点；F-5 为接受的故意差异，不升 OD |
| Gate A | E-1..E-11 标 verified | 源码门 E-1..E-8/E-11 本轮亲核成立；E-9 样本不可复核但不阻塞（当场 exp + 负例分支）；E-10 依事故档案 |
| Gate B | 无 pending OD | 成立 |

切片依赖 S-1→S-2/S-3→S-4→S-5 有序。回退边界（不改 persist schema / 协议，revert 即可）成立。v3 设计语义零改动，不改变 Gate A/B 与切片前置。

独立风险清单中已排除、不升意见的项：

- **env 401 只失效缓存、不升级为撤销**：D2 把 `lookupDesktopEnv` 的 401/403 接到 `invalidateAuthCache`，使陈旧 account JWT 能在 NotNeeded 窗口内重探。真 enrollment 撤销仍只由 refresh 401/403（经 D3 恢复一次后）变成 `errPairingRevoked`。这与 F-5「不加 env/WS 401 撤销映射」一致；env 401 在缓存窗口内最多导致下轮 miss 再探，不会假撤销。S-2 已列「env 401 → 缓存失效、下轮 miss 重探」。不升意见。
- **`StartRemoteControl` 在已有 persist 时走 `reconnectFromStore`**：显式配对无 persist 时直连 `loadChatGPTAuth`（R-5）；有 persist 时走恢复路径是现状，实施后经 Cached 符合 R-1/R-2，不是用户「点配对取新鲜 auth」被缓存。
- **D4 Info 落在 `loadChatGPTAuth`**：Cached hit 不进该函数，对账计数不会被 restore 风暴刷屏。`authTokenFn` 测试注入绕过 `loadChatGPTAuth`，单测用调用计数而非 Info 行，方案已写明。
- **缺席 Proactive 窗口内 refresh 失败按 restore 节奏重试 HTTP**：现状本就每轮刷新；成功后 `applyCtrlToken` 把 exp 拉回 600s，对账 `token_refreshed ≈ N/5` 在 E-10 成立的路径上成立。不镜像 `next_refresh_at` 已在 D1 登记。
- **`restoreMu` 跨探针持锁**：现状 `restoreOnce` 已如此，非本方案引入。
- **JWT `exp` 为 NumericDate**：扩展 `accountIDFromJWT` 同源 payload 是小改，`hasExp=false` 覆盖解析失败；不要求活体样本。
- **配套脏计数瞬时虚高**：本轮第一次 `status --porcelain \| wc -l` 得到 278，是带空格路径被 `wc -l` 拆行；按记录精确计数仍为 275，与方案 §2.1 一致。不升意见。
- **audit-plan**：外部形状声明不足以启动专项（见上）。

## 意见

本轮无新阻塞、无新建议。r2 F-8 与 r2-meta issues#1 已随 v3 闭合，不重开。

## 复审处置与剩余门

评审员模式：fresh。先独立读 v3 方案与源码形成风险清单，再对 r1/r2/r2-meta 意见做闭合核对。v3 声明「设计语义零改动」，本轮全局核对了 R/E/OD/S 关系与纠错范围，未发现夹带新需求或验收变化。

| 意见 | 闭合 | 依据 |
| --- | --- | --- |
| F-1 [阻塞] NotNeeded 跳过 chatGPTAuth 掏空冷启动 env token | **闭合** | D1 每轮 `chatGPTAuthCached`、仅刷新受门控；E-11 persist 无 account JWT；S-2 四态含「冷启动+ctrl fresh → 1 探针 0 刷新且 env 查找仍执行」。本轮亲核 persist schema 与 `lookupDesktopEnv:446-447` 仍支持该修正；v3 未改该控制流 |
| F-2 [阻塞] 非撤销 refresh 失败的 saved-token 回退 | **闭合** | D3 调用方契约：restoreOnce 非 `errPairingRevoked` 仍 Warn + 试用已保存 ctrl + 继续 env 查找；在线路径仍交 `maybeRefreshCtrlToken`。S-3 含 5xx/网络错态。本轮亲核 `pairing_persist.go:274-279` 与 `:62-70` 仍为该契约的现状真值 |
| F-3 [建议] 锚点行号不贴目标符号 | **闭合** | r1 点名项在 v2 已收紧；本轮亲核表内定位均覆盖目标符号 |
| F-4 [建议] 复用 `authTokenFn` | **闭合** | D2/S-1 miss 走既有 `chatGPTAuth`；hit/miss 用 `authTokenFn` 调用计数；不新增平行 seam。本轮亲核 `pairing.go:73,133-138` 与 `pairing_test.go:278-280` |
| F-5 [建议] 撤销检测延迟未登记 | **闭合** | §1 非目标末条 + OD 段 + §6 风险表登记为故意差异（≤ Proactive ~300s + backoff，官方同款）；明确不加 env/WS 401 撤销映射。本轮亲核 `ws.go:108-110` 与 `enroll.rs:246-263` |
| F-6 [建议] 失败不写缓存；Info 只计真实 spawn | **闭合** | D2 成功才写缓存；D4 Info 在 `loadChatGPTAuth` 通过 Stat 之后；S-1 含「探针失败不写缓存」。本轮亲核失败路径 `:54-55,112-113,122` 均无 token |
| F-7 [建议] E-9 样本已删，96h 不得当硬 TTL | **闭合** | E-9 自述不可复核；D2 禁止硬编码 TTL；对账改 10 分钟窗口。本轮同意不重捕 |
| F-8 [建议] `ctrlRefreshExec` 声明锚点误指 persist.go | **闭合** | v3 §2.3/§2.4/§7 声明改为 `pairing.go:83-84`，调用点保持 `pairing_persist.go:58-61`。本轮亲核 `pairing.go:80-84` 与 `pairing_persist.go:83-84`（锁行） |
| r2-meta issues#1 配套 iOS 工作树身份缺项 | **闭合** | v3 §2.1/§8.1 写入路径/分支/完整提交/未提交状态；S-4 协调对象点名该树。本轮独立 `worktree list` + `rev-parse` + porcelain 精确 275 行，与方案一致。影响范围仅来源清单/协调对象，不改变设计 |

仍待（**不是**方案阻塞计数，与意见列表分开）：

1. **实施授权**：写代码 / `go test ./agent/codex-remote/...` 以外的 Release 构建、覆盖安装、杀进程、重启 runtime，须 owner 另行授权。
2. **S-4 重启窗口**：owner 避开配套工作树 `cordcode-ios-native-message-timeline`（`feat/ios-native-message-timeline` @ `7e32a621`）上的 iOS timeline 真机测试后拍板。实施阶段若需读 iOS 源码或构建 iOS，必须按方案 §8.1 重新生成当时来源清单，不得沿用本条快照。
3. **S-4 运行态对账（切片完成门）**：实施后 Desktop 关闭 ≥10 min：`auth_probe`（真实 spawn）≈1、`token_refreshed` ≈ N/5（缺席 Proactive）、runtime 名下僵尸 0；Desktop 打开后既有 backoff 内 `phase=restored`，含冷启动磁盘 ctrl 仍新鲜。在线路径另按 ~9 min 对账，勿与缺席混用。
4. **E-9**：无残留样本；运行时只信当场解析的 `exp`。

下一步：交 owner 决定是否授权实施。授权后按 S-1→S-5 进入开发阶段（exec-plan）；本轮不得改产品代码。
