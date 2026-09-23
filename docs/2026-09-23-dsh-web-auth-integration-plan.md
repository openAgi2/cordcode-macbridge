# DeepSeek Harness：MacBridge 对接 dsh web 浏览器认证层（cookie 持久化 + token 自动交换 + 密钥铸 cookie 兜底）

Date: 2026-09-23
Status: 方案 v1，未实施，待评审。评审通过前不改业务代码。
Branch: `feat/ios-native-message-timeline`
HEAD: `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e`（工作树另有未提交的 2026-09-22 代装方案实现，见 §0）

## owner 裁决与约束（2026-09-23 本会话）

- owner 确认方向：「cookie 持久化 + 启动输出抓 token 自动换 cookie + 密钥铸 cookie 兜底」。
- **owner 约束（红线）：所有操作只在 MacBridge 侧完成，不得修改 DeepSeek harness 自己的源码或安装**。dsh 作为黑盒对待：读它的启动输出、只读它的凭据文件、按它既有的 cookie 格式办事；不给 dsh 加 flag、不改它的配置、不碰它的进程内部。
- 本方案不改 iOS，不新增 bridge wire 协议字段；认证只存在于 MacBridge↔dsh 的本地 HTTP/WS 之间，对 iPhone 完全透明。

## 0. 来源

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=b4e42d5c5a9af6c12abe062a32a30bccece3bd5e
未提交状态=2026-09-22 代装方案的全部实现（agent/dsh-web 六文件 + go-bridge 三文件 +
  Swift 四文件 + CHANGELOG + 测试），属上一变更集，等 owner 提交指令；另有
  doc-only 注记两处与方案/评审文档五份。本方案在其上叠加，不回退它们。
任务预期分支=feat/ios-native-message-timeline ✓
配套 iOS=本任务零改动（不触碰）
上游 checkout=/Users/jacklee/Projects/deepseek-harness @ 0d1f50007f9bca3f52b06e1c3074fa14d5fb0720
  （dsh-v0.1.6-alpha.1-5；认证层锚点 packages/client/connection/src/browser-auth.ts、
  api-request-trust.ts，2026-08-25 "authenticate the browser Host API" 引入）
本机安装包（运行真值）=/opt/homebrew/lib/node_modules/@deepseek-ai/dsh @ 0.1.7-alpha.1
  （dsh-client-connection/lib/index.js 打包形态；与上游源码逐段对照过）
活体实例=127.0.0.1:3080（2026-09-23 00:34 由 agent 以 nohup 拉起，0.1.7-alpha.1）
真实样本=token 行（/tmp/dsh-web.log）、Set-Cookie 响应（303 交换实测）、
  ~/.dsh/.credentials.yaml 结构（只验证结构，密钥值未打印）
```

## 1. 问题

dsh 自 2026-08-25 起给 `dsh web` 的 `/api/*`（含 events.mux/host WS 升级）加了浏览器认证：

- 无 cookie 的 `host.describe` → HTTP 401 `unauthorized`（实测）；
- 无 cookie 的 `/api/events.mux` → 401（实测）；
- 首页 `/` 无 cookie → 401 `dsh web authentication required`（实测）。

MacBridge 的 dsh-web backend 锚定 v1 无认证契约（2026-08-19 设计 S11），对当前所有
registry 版本（latest 0.1.5-rc.2 / next 0.1.5-rc.3 / alpha 0.1.7-alpha.2）与已装
0.1.7-alpha.1 全部 401。症状：工作站行永远「未启动」，iPhone 看不到 dsh 会话。
这不是上一方案的缺陷——上一方案通过评审时（round 3，2026-09-22）尚未在真机暴露
该层；2026-09-23 owner 点击「启动」后实测确认。

## 2. 认证协议（全部有真实样本/源码锚点，非推测）

### 2.1 token 与交换（官方浏览器流程）

1. `dsh web` 启动时生成进程内 launch token（`PROCESS_LAUNCH_TOKENS` WeakMap，
   `randomBytes(32)`，进程重启即换新），并把入口 URL 打印到 **stdout**
   （实测样本，/tmp/dsh-web.log）：
   `dsh web: http://127.0.0.1:3080/?token=0-E8BXPXYRqPNL-HhBlPmAT4BBrlMvkvnYBQQNDwVTQ`
2. `GET /?token=<launch-token>` → `303 See Other` + `Set-Cookie`（实测样本）：
   - 名：`dsh-auth-<base64url(sha256(authority))>`，authority = `host:port`
     （实测推导与下发值逐字一致：`dsh-auth-VPhEEcLKeqRDBoBalzN2Nm7CnfxKhLE00pKIDWxt1sw`）
   - 值：`v1.<base64url(JSON 载荷)>.<base64url(HMAC-SHA256)>`
   - 载荷：`{"version":1,"authority":"127.0.0.1:3080","issuedAt":…,"expiresAt":…}`
   - `Max-Age=2592000`（30 天）、`HttpOnly`、`SameSite=Strict`、`Path=/`
3. 之后所有 `/api` 请求（含 WS 升级）带该 cookie 即通过；有效期校验
   （browser-auth.ts `isAuthenticated`）：`issuedAt ≤ now < expiresAt` 且
   `expiresAt - issuedAt ≤ maxAgeMilliseconds`。

### 2.2 持久密钥（兜底路径的依据）

- 签名密钥不是进程内的：`initializeSecret` 经 dsh-credentials 持久化
  （`credentialKey('client-connection','browser-session')`），存于
  `~/.dsh/.credentials.yaml`（实测结构验证）：
  `records/client-connection/browser-session: {kind: "grant", payload: {version: 1, secret: <43 字符 base64url(32 字节)>}}`
- 因此 **cookie 跨 dsh 重启有效**：token 每次启动换新，但已换到的 cookie 在密钥
  不变期间一直可用。MacBridge 不需要追着 token 跑。
- 该文件同时存有用户 API keys（`refs/DEEPSEEK_API_KEY` 等）——本方案**只读**
  `records/client-connection/browser-session` 一条记录，其余内容不得读取进内存、
  不得进日志（红线，§6）。

### 2.3 Host/Origin fence（不需要对接）

loopback 请求天然过 fence（实测收到 401 而非 403）；`--trusted-host` 只影响
fence（403），与认证（401）无关，本方案不使用该 flag（座位红线本来也禁用）。

## 3. 人怎么走完（三个普通用户场景，全程无 token/URL 输入）

上一方案（2026-09-22 代装/启动）的交互原样保留；本方案让它的「成功」真正发生。
普通用户不看源码、不进终端，只看工作站行：

### 场景 A：已装 dsh，自己正在 3080 用（最常见的存量用户）

1. 装好 MacBridge，打开工作站——**什么都不用点，DeepSeek 行直接绿「就绪」**。
2. 原理：App 探测 3080 时收到「需要登录」→ 自动从 dsh 自己的凭据文件铸一个
   登录 cookie（只读一条记录，§4.4）→ 探测成功。用户的浏览器会话与 App 的
   cookie 互不干扰。
3. iPhone 立即可见 dsh 会话。

### 场景 B：没装 dsh

1. 行显示橙色「未安装」+「安装」。点「安装」→ npm 官方源安装 → 自动启动 →
   **从 dsh 启动输出自动换 cookie（§4.3）** → 行变绿「就绪」。
2. 全程一个按钮，装完即用。

### 场景 C：装了 dsh 但没在跑

1. 行显示「未启动」+「启动」。点「启动」→ App 拉起 dsh → 同场景 B 的自动换
   cookie → 行变绿「就绪」。

### 失败形态（诚实收口）

401 且两条 cookie 获取路径都失败 → 行停「未启动」，字幕写「dsh web 需要登录态
且自动获取失败（详情见日志）」+ 日志里的真实原因；不伪造就绪。场景 A 下点「启动」
不会救活（端口被用户实例占用，spawn 必然失败）——所以兜底铸 cookie 是场景 A 的
唯一正路，也是本方案把它做成一等公民的原因。

## 4. 设计

### 4.1 cookie 持久化（bridge 数据目录）

- 文件：`<dataDir>/dsh-web-auth-cookie.json`，`0600`，内容
  `{version:1, authority, cookieName, cookieValue, expiresAt}`。
- authority = 座位 URL 的 `host:port`（端口即身份，与 dsh 的 cookie 绑定一致）。
- 过期即删，不残留。

### 4.2 HTTP/WS 客户端统一带 cookie

- `agent/dsh-web` 的 unary Client（wire.go）与 mux/host WS 拨号（streams.go）统一
  注入 `Cookie: <name>=<value>` 头；无 cookie 时照发（首次探测/交换自身不需要）。
- 任何 `/api` 调用收到 401 → 触发刷新流程（§4.5）后**原地重试一次**；再 401 才
  作为错误上浮。刷新期间并发调用不风暴（single-flight）。

### 4.3 启动输出抓 token（主路径，官方流程的自动化）

- `execManagedStarter` 增加子进程 stdout 捕获（管道，非文件）：逐行匹配
  `^dsh web: (https?://<authority>)/\?token=([A-Za-z0-9_-]+)$`。
- 命中后：`GET <URL>`（不跟随重定向）→ 解析 `Set-Cookie` → 校验 cookieName 与
  座位 authority 推导一致 → 存 §4.1。交换在 spawn 引导等待（30s）内完成，不新增
  等待；token 行未出现（旧版 dsh 无认证）→ 无操作，行为与今天一致。
- 外部实例（用户终端拉起）没有 stdout 可抓——这正是兜底存在的理由。

### 4.4 密钥铸 cookie（兜底）

- 触发条件：需要 cookie（401 或冷启动已知有认证）且 §4.3 不可用（座位是外部实例
  或 token 行缺失），且 §4.1 无有效 cookie。
- 步骤：只读解析 `~/.dsh/.credentials.yaml` 的
  `records/client-connection/browser-session.payload.secret`（base64url → 32 字节）
  → 按实测格式铸 cookie：载荷 `{version:1, authority, issuedAt:now,
  expiresAt:now+30d}`，`v1.<b64url(载荷 JSON)>.<b64url(HMAC-SHA256(secret, body))>`，
  名 `dsh-auth-<b64url(sha256(authority))>`。
- **fail-closed**：记录 `version != 1`、secret 长度 ≠32、或铸出的 cookie 被 dsh
  拒绝（401）→ 停在「未启动」+ 真实错误，不猜格式、不试变体。
- 密钥只进内存签名用，不写日志、不进安装记录、不进任何诊断输出。

### 4.5 刷新流程（single-flight）

```
/api 401
  → 有本进程 spawn 的 token（§4.3 缓存）→ 交换 → 重试
  → 否则有持久密钥 → 铸（§4.4）→ 重试
  → 都没有 → 诚实错误
```

### 4.6 readiness / 判别顺序

§5 判别顺序（2026-09-22 方案）不变，唯一变化：`host.describe` 探测带 cookie，
且**探测收到 401 时同样走 §4.5 刷新流程**（single-flight）——这是场景 A
（外部实例、无 token 可抓）「打开 App 即绿」的关键：探测 401 → 铸 cookie →
重试应答 → `available`。刷新后仍 401 → 走「TCP 连上 → lsof 含 dsh →
service_not_running（接口未就绪）」既有分支，字幕补「认证失败（自动获取登录态
未成功）」。不新增 wire 状态。

## 5. 红线

1. **不改 DeepSeek harness 源码/安装/配置/进程**（owner 2026-09-23）；不使用
   `--trusted-host`；不给 dsh 传任何新参数。
2. `~/.dsh/.credentials.yaml` **只读**且只取 `client-connection/browser-session`
   一条；文件其余内容（API keys）不读取、不缓存、不打印。
3. cookie 只实现 v1 格式；格式漂移（version≠1 / 校验失败 / 401）一律 fail-closed
   报真实错误，禁止试错式变体猜测。
4. 座位红线不变：默认 127.0.0.1:3080、端口即身份、只绑 loopback、Link 退出不杀
   座位、冷启动不补拉（2026-09-22 方案语义全部保留）。
5. 不新增 wire 枚举/协议字段；iOS 零改动。
6. token 行匹配只认 dsh 的既定输出格式；匹配不到不阻塞启动流程。

## 6. 不做

- 不做 dsh 侧任何修改（owner 约束）。
- 不做「让用户粘贴 token/URL」的输入框——两条自动路径覆盖所有场景，兜底失败才
  诚实报错。
- 不把 cookie/密钥写进 hello_ack、诊断报告或日志。
- 不支持多 authority cookie 池（座位唯一；用户显式配置的 `dsh_web_url` 换 authority
  时旧 cookie 自然失效并走刷新）。
- 不改 legacy `agent/dsh`（SDK 路线不碰认证）。

## 7. 过审后怎么验

- **协议形状测试（fake server）**：`fakedsh_test.go` 增加认证中间件形态——无
  cookie → 401；带正确 cookie → 200/WS 升级。断言 Client/WS 拨号都带 Cookie 头。
- **token 抓取**：fake starter 向 stdout 打印 token 行 → 断言交换 GET 发生、
  Set-Cookie 被解析、cookie 落盘（0600）、cookieName 与 authority 推导一致。
- **铸 cookie**：用测试密钥按格式铸 → fake server（同密钥）验证通过；version≠1、
  secret 长度异常 → fail-closed 错误，不产出 cookie。
- **401 刷新**：fake server 先 401 后 200 → 断言 single-flight 刷新 + 原地重试
  成功；刷新再 401 → 错误上浮，行不伪造就绪。
- **credentials 只读边界**：fixture 里放一条无关记录 → 断言它从未被读取/输出。
- **真机（owner）**：点「启动」→ 行变绿「就绪」→ iPhone 可见 dsh 会话列表与
  历史；重启 dsh（token 换新）→ 行保持绿（cookie 跨重启）；30 天过期场景不实测，
  以单测覆盖。
- 定向 `go test ./agent/dsh-web/`，一次增量编译 MacBridge；不跑全量 UI Test。

## 8. 实施顺序

1. cookie store + Client/WS 统一带 cookie + 401 刷新骨架（single-flight）。
2. stdout token 抓取 + 交换 + 落盘（spawn 路径）。
3. 密钥铸 cookie 兜底（只读一条记录 + v1 fail-closed）。
4. readiness 探测带 cookie + 字幕补认证失败文案（中英）。
5. `CHANGELOG.md` 记一条。
6. 按 §7 收口测试；Release 构建 → 覆盖安装 → owner 真机验收。

## 9. 完成记录（2026-09-23 部署验证）

- **认证层全部落地并验证**：cookie 持久化（0600、30 天）、启动输出抓 token 换
  cookie、密钥铸 cookie 兜底、401 单飞刷新全部按方案实现；定向测试全绿。
- **部署后暴露第二层根因（非认证）**：认证通过后所有 `/api` 方法仍 404。定位为
  上游 2026-08-27 移除旧 ApiProxy 点分隔 API——本桥 wire 面是对 2026-08-16 时点
  的旧代写的，而所有可安装版本（latest/next/alpha）都只讲 typert 网关斜杠代。
  当轮把 dsh-web 整个 wire 面迁到官方新代（斜杠端点 + 单 `args` 信封 +
  `/api/remote.mux` 逻辑流 + `$events/result` waterfall 应答 + `session/follow`
  按需跟随 + `workspace/follow` 归组基线），契约夹具按新代活体重新脱敏捕获。
  详见 CHANGELOG [Unreleased]「dsh-web 整体迁移到 typert 网关 API 代」条目。
- **部署验证（0.1.7-alpha.1 活体）**：工作站行 `available`；34 个会话进目录；
  remote.mux 连接、`$events` ready、workspace 基线、session/follow 快照全通。
- **owner 真机验收项**：iPhone 上 dsh 会话列表/历史可见；Mac web 端发起外部
  turn 时 iPhone 直播跟随；重启 dsh 后行保持绿（cookie 跨重启）。
- **owner 首轮复测暴露两个残留 bug，已修复并重新部署（2026-09-23 03:04）**：
  症状——iPhone 发消息后 Mac 3080 网页端正常同步并流式输出，iPhone 自己反倒
  历史空白、无流式输出。根因①：新代官方事件类型（`session/end-seed`、
  `system/message`、`model/selection`、`assistant/attempt`、`subagent/*` 等
  官方 `known-event-types.ts` 注册表成员）不在桥的已知控制面清单里，解码器按
  「未知必需事件」反复整段复位，历史快照、直播流、回合收口全被打断；根因②：
  分页历史沿用旧语义把 `throughSeq:-1` 当「最新」，新代 `-1` 是空日志游标
  （官方客户端从 follow 快照游标起分页），历史页恒空。修复：①已知控制面清单
  对齐官方注册表全量补齐（未映射注册表成员跳过、真未知仍复位 fail-visible）；
  ②分页改为先 `session/projections` 取 `asOfSeq` 真实日志头再分页（历史与会话
  标题读取同修）。活体验证：owner 会话 38 条（18 user / 18 assistant /
  2 system）全量恢复；回归测试扩展（新代类型 mid-turn 不复位）。
- **owner 二轮复测：历史已恢复，流式仍缺——根因为官方流式通道未接，已修复
  并部署（2026-09-23 10:34，S1 切片）**：typert 代逐块输出不进 journal
  （chunk 帧进程内瞬态，`assistant/message` 完成时才落一条），官方网页端流式
  来自 `session/follow` 的 `assistant-stream` 帧通道且必须请求
  `assistantStream: true` 才订阅（history.ts:165-176；官方客户端
  transport.ts:181 硬编码 true）。桥此前不带开关且显式忽略该帧类型
  （streams.go 旧 `case "assistant-stream"` 注释「ignore defensively」）。
  修复：follow 请求带开关；start/chunk/end 三种帧按官方折叠语义映射
  （text-delta→正文、reasoning-delta→思考、usage→用量；revision/index
  连续性校验，断档告警暂停瞬态发射不动 journal 路径；快照基线
  activeAttempt 前缀补发支持 mid-turn 重连；settlement 走 journal 只簿记
  不双发）。验证：单测 ×4（开关 wire 契约、全帧折叠、基线补发、连续性
  丢弃）+ 全包回归绿；活体真实 turn 7.1s 收 45 正文增量 + 94 思考增量、
  正文完整；部署后 runtime 日志实证帧折叠为 text_delta 进直播通道、零复位。
  已知边界：abandoned 尝试的已发增量无法撤回（bridge 协议无撤回语义），
  登记进专项方案待收敛。
