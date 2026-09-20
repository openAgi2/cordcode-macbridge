# Desktop persist 脱敏 fixtures（1.18.31）

OpenCode Desktop（1.18.31）home/window persist 的可执行脱敏样本，用于
off-home membership（方案 M，阶段 2）的 parser 测试。与
`testdata/official-1.18.18/` 同规：真实样本派生、脱敏后保留全部结构不变量。

## 来源与采集

- 采集时间：2026-09-20 23:30（本地）；来源机器为事故同机。
- 原始文件（只读采集，未修改生产 persist）：
  - `opencode.settings`（216 B）——`windowIds` + `defaultServerUrl`；
  - `opencode.window.<uuid>.dat`（1,260 B）——注册窗口，3 个 active
    SessionTab（全部 `server:"sidecar"`、root、未归档）；
  - 同目录 orphan 窗口文件（17 B，`{"tabs":"[]"}`，UUID 不在 `windowIds`）；
  - `opencode.global.dat`（105,660 B）——`server` 行含 4096 的 8 个 home
    worktree 与 `tabs.closed` 里的 full-URL server 形状。
- 上游版本锚点：Desktop 1.18.31（`highlights.v1`）；tabKey 算法源
  `packages/app/src/utils/session-route.ts:5-7` + `packages/app/src/context/tabs.tsx:47`
  （`server + "\n" + /server/<base64(server)>/session/<id>`，base64 为
  **URL-safe 无 padding** 变体：`+`→`-`、`/`→`_`、去 `=`，
  `packages/core/src/util/encode.ts base64Encode`）。sidecar 的编码值固定为
  `c2lkZWNhcg`（无 `==`）。

## 文件清单

| 文件 | 对应真实文件 | 内容 |
| --- | --- | --- |
| `samples/settings.json` | `opencode.settings` | 1 个注册窗口 ID + defaultServerUrl |
| `samples/window-registered.json` | `opencode.window.<uuid>.dat`（23:30 采集） | 3 active sidecar tab + tabs.info（tabKey 重算）+ tabs.recent + tabs.closed（full-URL server 形状） |
| `samples/window-fullurl-archived-tab.json` | 同上（23:58 采集） | 4 active tab：3 sidecar + **1 个 full-URL server tab，其 session 已归档**（非 Desktop 客户端按产品同款 `PATCH /session/:id {time:{archived}}` 归档后，Desktop 不删 tab——实测保留） |
| `samples/window-orphan.json` | orphan 窗口文件 | `{"tabs":"[]"}`（不在 windowIds） |
| `samples/global-home.json` | `opencode.global.dat`（23:30 采集） | server 行：list（凭据 REDACTED）+ projects（4096 → 8 worktree）+ recentlyClosed |
| `samples/global-home-offhome.json` | 同上（23:58 采集） | **M 状态活体**：home 行 7 worktree（无 ios-worktree），而 window fixture 的 2 个 sidecar tab 指向该目录——原始事故场景自然重现（discovery sessionCount 174→172 实证） |

## 脱敏策略（确定性映射，形状全保留）

- 目录：`/Users/jacklee/Projects/<name>` → `/Users/samples/<name>`（8 个 home
  目录与 tab info 目录全部映射）。
- session ID：`ses_` + 22 字符 → `ses_sample…`（同长度同前缀形状）。
- 窗口 UUID → 固定样本 UUID；标题 → 「样本会话：…」。
- `server.list[].http.password` → `REDACTED-CREDENTIAL-PLACEHOLDER`（真实
  Basic 凭据永不入库；`http://127.0.0.1:4096` 为本机回环地址，非机密）。
- **不变量**：`tabs` / `tabs.info` / `tabs.closed` / `tabs.recent` /
  `server` 的值仍是二次 JSON 字符串（外层 JSON 的值是内层 JSON 的字符串化）；
  `tabs.info` 的 key 从脱敏后 ID 用官方 tabKey 算法**重算**，保证 exact join
  可验证。

## 验证（双方法一致）

- 方法 A（jq）：`jq '.tabs | fromjson'` 等，得 tabs=3、info=3、closed=2、
  home=8、closed server 全为 full URL。
- 方法 B（独立 Python）：重算 tabKey（URL-safe 无 padding base64）与
  `tabs.info` keys exact join 相等；断言双重编码保留、脱敏无泄漏、orphan
  语义保留。两方法结果一致（2026-09-20 执行记录见阶段 1 方案文档 §样本）。

## 样本状态（2026-09-20 23:58 更新）

四类 active-tab 样本中三类已有活体采集（本目录）：

1. happy path（3 sidecar root/unarchived tab）——`window-registered.json`；
2. **active full-URL server tab**——owner 在默认 server（4096）下自然新建
   session 产生，`window-fullurl-archived-tab.json` 第 4 个 tab；
3. **active archived tab**——同一 tab：非 Desktop 客户端按产品同款
   `PATCH /session/:id {"time":{"archived":ms}}`（与 CordCode iOS 左滑归档
   同一 wire，`agent/opencode-web/sessions.go ArchiveSession`）归档后，
   Desktop **不删 tab**（实测保留；源码依据：tab 移除仅由 Desktop 自身 UI
   归档动作触发）。注：1.18.20 全客户端（Desktop/TUI/HTTP API）无 unarchive
   路径，归档不可逆——样本目标为 owner 当日创建的一次性测试 session；
4. child tab（session 有 parentID）——源码证明官方 UI 全路径不可达（方案
   文档 §2.2），不设 fixture；算法按 by-ID proof 自然覆盖。

**未采集**：超限 old tab 的活体 persist（需在 Desktop 命令面板搜索打开一个
窗口外老 session；owner 已明确不愿执行手动测试步骤）。其技术声明已用活体
证据验证：目标老 session（Chat rank 105）by-ID 取回 200、窗口实测见方案
文档 §1.3、palette 可达性源码锚点见 §2.3。若未来自然产生该状态，按本
README 同规补 `window-overlimit-old-tab.json`。
