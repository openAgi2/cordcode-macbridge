# 方案评审报告 r1：codex-remote 恢复循环探针经济性优化（P1）

- verdict: REVISION_REQUIRED
- plan: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-12-codex-remote-probe-economy-p1-plan.md`
- plan_sha256: `c5690bf39cf6bb53673f7788c5b1f0e01d8f378ed7ca4fcc17641d4a13269048`
- scope: full（fresh 首轮；无历轮报告）
- blockers: 2
- advisories: 5
- contract_version: plan-contract-v1.1
- round_complete: true
- implementation_readiness: **不可实施。** 设计未过审；即使修订通过，写代码 / 构建 / 覆盖安装 / 重启 runtime 仍须 owner 另行授权（任务简报）。S-4 重启时机另须避开 iOS timeline 真机测试窗口，由 owner 拍板。

## 结论

方案把事故档案 §5.2 收成可追踪的 R/E/S，上游三级刷新门控、auth 生命周期缓存、401 恢复优先三条语义均有可核对锚点，范围与非目标与任务简报一致，无 OD 的声明与「工程取舍有上游锚点」相符。

不能通过：D1 把 `NotNeeded` 写成同时跳过 `chatGPTAuth` **和** `refreshControlToken`，与 D2 冷启动必取 auth、`lookupDesktopEnv` 依赖内存 `state.token`、以及「Desktop 打开后 env 查找每轮照跑即可 restored」互相矛盾。持久化 pairing **不含** account JWT，进程重启后按原文实施会在 ctrl token 仍新鲜的最多约 300s 内整轮 `pairing state incomplete`——含覆盖安装后的第一次启动。另：`restoreOnce` 对非撤销 refresh 失败「吞错 + 试用已保存 ctrl token」的既有回退未写入改造后的控制流，存在断线韧性回归。

## 来源与覆盖

本轮亲核（fresh，无复用）。评审时刻 2026-10-12。

| 仓库 | 路径 | 分支 | 提交 | 未提交 |
| --- | --- | --- | --- | --- |
| 本仓 | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `ff990f73be81a55f94e9f799c9b8a9fa7d0e1662` | 仅未跟踪 `docs/2026-10-12-codex-remote-probe-economy-p1-plan.md`（本方案）；与方案 §2.1 一致 |
| 上游 | `/Users/jacklee/Projects/codex` | `main` | `cc7ba33601286a751477832b01de7b6f8de0dd9c` | 干净；与方案 §2.1 一致 |
| iOS | 未读 | — | — | 方案声明纯 Mac 侧、无协议/iOS 改动；本轮同意 N/A |

已核范围：`agent/codex-remote/pairing_persist.go`、`pairing.go`（`jsonRequest` / `refreshControlToken` / `lookupDesktopEnv` / `chatGPTAuth` / persist schema）、`chatgpt_auth.go`、`jwt.go`、`backoff.go`、`diagnostics.go`、`ws.go`（`dialControllerWS` / `bindLive`）、`pairing_ctrl_refresh_test.go`、`pairing_test.go` restore 三态、`chatgpt_auth_test.go`；上游 `enroll.rs` 刷新分级、`server_api.rs` NotNeeded no-op / 失败退避、`auth.rs` capture/load/recover、`mod.rs:460,704`、`websocket.rs` 401 与 `next_reconnect_delay`；事故档案 §3.2/§3.3/§5.2/§5.5 对账数字。

未核 / 不影响通过结论：E-9 原始 JWT 已按方案删除，本轮无法复核 claims 字节（见 F-7）；未跑 audit-plan 专项（本方案不是外部 wire 解析器；E-9 为缓存定界的运行证据，缺样本不推翻内部设计可行性）。交互四拍：无新用户入口，N/A。

### 锚点核验表

| 命题/方案原引用 | 实际来源及位置 | 源码原文摘录 | 定位 / 语义 | 亲核 |
| --- | --- | --- | --- | --- |
| E-1 `enroll.rs:22-28` SKEW=5min；`:241-264` 三级判定 | 上游 `enroll.rs:26` 常量；`:246-263` `server_token_refresh_requirement_at` | `const REMOTE_CONTROL_SERVER_TOKEN_REFRESH_SKEW_SECS: i64 = 5 * 60;`；无 token/`expires_at`→Required；`expires_at <= now`→Required；`expires_at > now+SKEW \|\| next_refresh_at > now`→NotNeeded；否则 Proactive | 定位：`:22-28` 含无关 timeout/header，目标常量在 `:26`。语义：三级判定成立；方案未提 `next_refresh_at` 使 Proactive 变 NotNeeded 的一侧（见意见外说明，不单开阻塞） | 亲核 |
| E-1 调用点 `mod.rs:460`、`:704` | 同文件 `:460`、`:704` | `if enrollment.should_refresh_server_token() {` 才 `refresh_pairing_enrollment` | 定位准 / 语义准 | 亲核 |
| E-2 `server_api.rs:134-151` NotNeeded no-op | `:134-151` | `if refresh_requirement == NotNeeded { return Ok(()); }`；Required 且 `next_refresh_at > now`→WouldBlock | 定位准 / 语义准 | 亲核 |
| E-2 失败退避 `server_api.rs:24-28`、`:366-386` | `:27-28` 24/36s；`:366-386` `refresh_deferral` | `BACKOFF_MIN_SECS = 24` / `MAX = 36`；无 Retry-After 时随机该区间 | 定位准 / 语义准；本方案非目标不改 backoff | 亲核 |
| E-3 `auth.rs:1-2,29-46,89-96` 缓存非逐次 | `:1-2` 模块注释；`:29-46` `capture`；`:89-96` `load_remote_control_auth`；reload 循环在 `:98-123` | 「Credentials and recovery bound to one remote-control login lifetime」；`load` 调 `load_auth_manager`，后者 `reloaded` 至多一次 | 定位：`:89-96` 本身不含 reload；语义「至多一次 reload」在 `:98-123`。缓存生命周期绑定成立 | 亲核 |
| E-4 `auth.rs:177-214` 恢复优先；`websocket.rs:1518` | `auth.rs:177-214` `recover_remote_control_auth`；WS 401/403 在 `websocket.rs:1512-1520`（`:1518` 为文案行） | 恢复 `has_next`/`next` 成功才 true；WS：`401 \| 403` → `clear_..._token` → `refreshing server token before reconnect` | 定位略偏一行 / 语义准。官方是多步迭代器，方案单步 re-probe 是有意收窄 | 亲核 |
| E-5 `pairing_persist.go:266-279` 无条件探针+刷新 | 同 | `token, accountID, err := p.chatGPTAuth(ctx)` 后 `refreshControlToken`；非撤销 refresh 错只 Warn「trying saved token」 | 定位准 / 语义准（含 F-2 所依回退） | 亲核 |
| E-5 循环 `:299-328` | `:299-329` | store 存在则 `for { restoreOnce; backoff.Next() }`，成功才 `watchBinding` | 定位准 / 语义准 | 亲核 |
| E-6 `pairing.go:401-403,426-428` 401/403→撤销 | 同 | start/finish 两处 `status == 401 \|\| 403 { return errPairingRevoked }` | 定位准 / 语义准 | 亲核 |
| E-7 `pairing.go:440-449` env 依赖 token | `:440-449` | `if token == "" \|\| clientID == "" { return fmt.Errorf("pairing state incomplete") }`；`jsonRequest` 的 status 被 `_` 丢弃 | 定位准 / 语义准；D2 接 status 的必要性成立 | 亲核 |
| persist 不含 account token（方案未单列，支撑 D2 冷启动） | `pairing_persist.go:95-106` `persistedPairing` | 字段仅 client/env/ctrl/key，无 `token`/`accountID` | — / 冷启动内存 token 必空，除非本轮取 auth | 亲核 |
| E-8 lead=60s、600s 常量注释 | `pairing_persist.go:19-23`；调度 `:47-52` | `ctrlTokenRefreshLead = 60 * time.Second`；注释 E-9 5 样本 600s | 定位准 / 语义准 | 亲核 |
| JWT 解析 `chatgpt_auth.go:110-135`、`jwt.go:8-14` | `accountIDFromJWT` 在 `chatgpt_auth.go:125-150`；`jwt.go:8-14` `decodeJWTSegment` | 只解 `https://api.openai.com/auth` 下 account id，**无 `exp` 字段**；扩展 exp 确为小改 | 定位不覆盖函数全区间（见 F-3）/ 语义「已解析 claims、可扩 exp」成立 | 亲核 |
| 配对/诊断直连探针 R-5 | `pairing.go:133-137` `chatGPTAuth`→`loadChatGPTAuth`；`diagnostics.go:55` 直接 `loadChatGPTAuth` | 用户显式 `StartRemoteControl` 不经 restore 缓存 | 定位准 / 语义准 | 亲核 |
| 既有 seam `ctrlRefreshExec` / `authTokenFn` | `pairing_persist.go:58-61`；`pairing.go:73,133-136` | `ctrlRefreshExec` 可注入；**已有** `authTokenFn` 拦截 `chatGPTAuth` | 定位准；方案再造 `chatGPTAuthExec` 与现 seam 重叠（F-4） | 亲核 |
| E-10 Desktop 关闭 refresh 仍成功 | 事故档案 §3.2 步骤 2 + §5.5：11.5 min 53 次 `token_refreshed`、57 次 `no desktop environment` | 刷新成功发生在 env 失败之前 | 文档证据，非本轮新采样 / 与源码顺序一致 | 亲核文档 |
| 风暴节奏 ~4.6/min | 事故档案 §3.2/§5.5 与 `backoff.go` 1s→30s 重数 | 11.5 min 53 刷新 | 文档 / 语义准 | 亲核文档 |
| 官方 `next_reconnect_delay` | `websocket.rs:1366-1374` | 指数封顶 + jitter | 方案非目标不改；定位准 | 亲核 |

## 需求与门控

| ID | 方案覆盖 | 本轮判断 |
| --- | --- | --- |
| R-1 缺席风暴探针→缓存窗口内 0 | S-1+S-2；对账 auth_probe≈1 | 目标成立，但 D1 写法会让冷启动连 env 查找都拿不到 token（F-1），R-1 验收「Desktop 打开后无新增卡点」不成立 |
| R-2 在线刷新复用缓存 | `runCtrlTokenRefresh` 改 D2；lead=60s 不改 | 与非目标一致；在线 `token_refreshed` ~9min 与离线 Proactive ~5min 是两条节奏，正文已分写，实施勿混对账 |
| R-3 401 先恢复再撤销 | D3 / S-3 | 方向对准官方；须与 F-2 的 restoreOnce 回退并存；既有 `TestRestoreRevokedInvalidatesEveryIdentityLayer` 应变为 401×2 仍撤销 |
| R-4 特征日志 | D4 `auth_probe` / `ctrlFresh` | 可验收；Info 仅 miss 才能当对账计数 |
| R-5 配对/诊断直连 | D1 点名两处 | 成立 |
| 非目标 backoff / watchBinding lead / 探针机制 / P2 / 可见配对语义 | 正文未改这些切片 | 成立；enrollment 撤销检测延迟未登记（F-5） |
| OD | 无 | 同意无产品取舍需 owner 拍板；F-1/F-2 是设计漏洞不是 OD |
| Gate A | E-1..E-10 标 verified | 源码门 E-1..E-8 亲核成立；E-9 样本不可复核但不阻塞缓存可行性（F-7）；E-10 依事故档案 |
| Gate B | 无 OD | 成立 |

切片依赖 S-1→S-2/S-3→S-4→S-5 有序。F-1 闭合前 S-1/S-2 控制流不能开工。

## 意见

### [F-1][阻塞] NotNeeded 跳过 chatGPTAuth 会在冷启动掏空 env 查找所需 token

- 位置：§3 D1「`NotNeeded` 时跳过 `chatGPTAuth` + `refreshControlToken`，直接 `lookupDesktopEnv`（`state.token` 已在内存——上一轮或冷启动探针取得）」；同节风险表「门控只跳过 auth+刷新，env 查找每轮照跑 → 立即 restored」；S-2「ctrl fresh（>300s）→0 探针 0 刷新且 env 查找仍执行」。
- 证据：
  1. `persistedPairing`（`pairing_persist.go:95-106`）不保存 account JWT / accountID；`restoreOnce` 从磁盘只恢复 ctrl 侧（`:250-256`）。进程重启后 `state.token` **为空**，D2 内存缓存同样为空。
  2. `lookupDesktopEnv`（`pairing.go:446-447`）在 `token == ""` 时报 `pairing state incomplete`，根本不会打 environments API。
  3. `ctrlTokenRequirement` 只看 **ctrl** `expires_at`。ctrl 寿命 600s、SKEW 300s：覆盖安装、120min 自愈重启、crash 拉起时，磁盘 ctrl 经常仍 >300s → NotNeeded → 按 D1 不取 auth。
  4. D2 写「缓存空 → 探针」但 D1 在 NotNeeded 时根本不进入 `chatGPTAuthCached`，冷启动 miss 路径不可达。env-401 失效缓存后同一窗口内也无法重取（D2 自己写的「否则门控窗口内会盲目重试」）。
- 影响：部署 P1 后第一次启动、以及之后每次 runtime 重启，最多约 300s 内即使用户打开 Desktop 也无法 restored（卡在 incomplete，不是 `no desktop environment`）。R-1 验收「无新增卡点」失败；S-4 覆盖安装本身就会踩中。
- 修订方向（最小）：把「取 auth」和「刷 ctrl token」拆开。`restoreOnce` **每轮**走 `chatGPTAuthCached`（冷启动 miss=1 探针并写入 `state.token`；命中 0 spawn）；**仅** `refreshControlTokenWithRecovery` 受 `ctrlTokenRequirement` 门控。env-401 失效后本轮或下一轮经 Cached 必 miss。S-2 验收改为三态：冷启动+ctrl fresh → 1 探针 0 刷新 + env 查找执行；缓存已填+ctrl fresh → 0 探针 0 刷新 + env 查找执行；Proactive/Required → 刷新。
- 闭合标准：正文不再出现「NotNeeded 跳过 chatGPTAuth」；明确 Cached 每轮可达；S-2 三态与风险表「Desktop 打开立即 restored」一致；能指出 persist schema 无 account token 故冷启动不能假设 `state.token` 已在内存。

### [F-2][阻塞] restoreOnce 非撤销 refresh 失败的 saved-token 回退未纳入改造后控制流

- 位置：§3 D1/D3 改调 `refreshControlTokenWithRecovery`；S-3「非认证错误原样透传」「非认证失败行为与现状完全一致」。
- 证据：现状 `pairing_persist.go:274-279`：`refreshControlToken` 非 `errPairingRevoked` 时 **不 return**，Warn「trying saved token」，继续 `lookupDesktopEnv` / `bindEnvironment`。这是断线韧性既有行为（ctrl 仍可用、refresh 端点 5xx/网络失败时仍能绑 Desktop）。S-3「透传」若被写成 `if err := withRecovery(); err != nil { return err }`，会把该回退删掉。D3 逐步伪代码只处理 `errCtrlAuthRejected`，未规定 restoreOnce 调用方对其他 error 的处置。
- 影响：refresh API 瞬时失败 + Desktop 在线 + 磁盘 ctrl 未过期 → 整轮 restore 失败，用户停在「已配对，等待 ChatGPT Desktop」，直到 refresh 成功。与「行为与现状完全一致」矛盾，也与非目标「不改 offline 可见语义」在失败分类上漂移。
- 修订方向：在 D1/D3 写明 restoreOnce 调用契约：`errPairingRevoked` 终止；`errCtrlAuthRejected` 只存在于 with-recovery 内部；其余 refresh 错误仍 Warn + 试用已保存 ctrl token + 继续 env 查找。S-3 单测加一态：refresh 5xx/网络错 → 不撤销、env 查找仍执行。
- 闭合标准：改造后控制流伪代码含该回退；S-3 验收含非认证失败不升级为整轮失败。

### [F-3][建议] 若干锚点行号区间不贴目标符号

- 位置：§2.2/§2.3 E-1/E-3/E-4/JWT 行。
- 证据：见上表。`chatgpt_auth.go:110-135` 大半是 stdio scanner，`accountIDFromJWT` 为 `:125-150`；`enroll.rs:22-28` 含无关常量；`auth.rs` reload 在 `:98-123`；`websocket.rs:1518` 是文案，分支在 `:1512-1520`。
- 影响：不改变语义成立，但实施/复审会对错函数。项目来源门要求行号可归属。
- 修订方向：改成符号 + 覆盖目标行的闭区间，不必扩写。
- 闭合标准：本报告表内「定位不覆盖」项在修订版消失或改为与摘录一致。

### [F-4][建议] 不要平行新增 `chatGPTAuthExec`，复用已有 `authTokenFn`

- 位置：§2.4 / S-1「新增 `chatGPTAuthExec` 仿 `ctrlRefreshExec`」。
- 证据：`PairingController.authTokenFn`（`pairing.go:73,133-136`）已拦截 `chatGPTAuth`；restore 测试（`pairing_test.go:278-280`）已用它。`ctrlRefreshExec` 替换的是整段 auth→refresh→persist，职责不同。
- 影响：双 seam 让 S-1 单测与既有 restore 三态各走一套注入，漏接则 R-5/D3 测不到生产路径。
- 修订方向：`chatGPTAuthCached` 未命中时调用现有 `chatGPTAuth`（从而 `authTokenFn` / `loadChatGPTAuth`）；用调用计数断言 hit/miss。`ctrlRefreshExec` 保持只包 refresh 序列。
- 闭合标准：方案只保留一套 auth 注入点，并写明与 `authTokenFn` 的关系。

### [F-5][建议] enrollment 撤销检测从「每轮 refresh」变为「最多一个 Proactive 窗口」，未写入非目标/OD

- 位置：§3 OD 表只谈 Desktop 登出/换号（account auth 陈旧）；非目标「不改配对用户可见语义」。
- 证据：现状每轮 `refreshControlToken`，401/403 即 `errPairingRevoked`（`pairing.go:401-403`）。门控后 NotNeeded 期间不 refresh；`dialControllerWS`（`ws.go:108-110`）握手失败不解析 401，不会映射撤销。account 仍有效时 environments 也可能 200。用户在 chatgpt.com 撤销控制器后，缺席恢复循环最多 ~300s 仍显示「请打开 ChatGPT Desktop」而非「请重新配对」。
- 影响：这是官方「不无条件 refresh」的代价，可接受，但属于用户可见时延，不是「无产品面变化」。
- 修订方向：在非目标或 OD 显式接受「enrollment 撤销检测延迟 ≤ ctrl Proactive 窗口（300s）」；或不接受则在 env/WS 401 增加撤销映射（会扩大 S-3，需另写验收）。
- 闭合标准：延迟被写成有意差异，或给出同切片的检测补丁；不再写「用户可见行为面不变」而不提撤销时延。

### [F-6][建议] 只在探针成功时写入缓存；失败/未登录不得占坑

- 位置：D2「缓存空/过期 → 调真实探针填缓存」。
- 证据：`loadChatGPTAuth` 失败返回「请先安装…」「ChatGPT 未登录」「读取…超时」（`chatgpt_auth.go:54-55,112-113,122`），无 token。若 Cached 把失败也当成已填或每轮 Info miss，未安装场景会刷 `auth_probe` 计数（R-4 对账失效）；若把空结果当 hit，R-3 恢复会跳过真正探针。
- 影响：实施歧义，不是现行源码已错。
- 修订方向：成功才写 `authCache`；失败不置 hit；Info `auth_probe cache=miss` 仅真实 spawn（或 Stat 成功后的探针），Stat 早退可 Debug。
- 闭合标准：D2/S-1 有「失败不缓存」与日志计数口径。

### [F-7][建议] E-9 JWT exp 样本已删除，96h 不得写成硬 TTL

- 位置：§4 E-9 verified；§1/§6 验收「JWT exp ≈ 96h 内 0 探针」。
- 证据：方案写明 token 未落盘、临时文件已删。本轮无法复核 `exp`/`iat` 键。D2 已有 `hasExp=false` 失败驱动，设计不依赖 96h 才能工作。
- 影响：把 96h 写进生产常量会在短寿命 JWT 上过度缓存；对账「10 分钟 0 探针」仍可用（远小于任何合理 exp）。
- 修订方向：E-9 标「作者一次探针，样本已销毁，本轮未复核」；实现只信当场解析的 `exp`，禁止硬编码 96h；对账窗口用「10 分钟 auth_probe miss ≤1」而非「96h 窗口」。
- 闭合标准：无硬编码 TTL；验收数字不依赖已删样本。

## 复审处置与剩余门

首轮无旧意见。本轮阻塞 F-1、F-2 未闭合。

仍待（**不是**方案阻塞计数，修订通过后仍在）：

1. **实施授权**：评审通过 ≠ 写代码/测试以外的构建安装重启授权。
2. **S-4 重启窗口**：owner 避开 iOS native-message-timeline 真机测试后拍板。
3. **S-4 运行态对账**：实施后 Desktop 关闭 ≥10 min：`auth_probe` miss ≈1、`token_refreshed` ≈ N/5、runtime 僵尸 0；Desktop 打开后既有 backoff 内 `phase=restored`。属切片完成门。
4. **E-9**：无残留样本；不阻塞设计，禁止当硬 TTL。

下一步：作者按 F-1/F-2（及所采建议）只改方案文档为 v2，再送审。本轮不得改产品代码。
