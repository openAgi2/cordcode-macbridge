# 第三轮评审：OpenCode 共享本地服务接入方案（修订二版）

> 评审对象：[docs/2026-07-02-opencode-shared-service-discovery-plan.md](../2026-07-02-opencode-shared-service-discovery-plan.md)（已按第二轮评审修订）
> 评审日期：2026-07-02
> 评审人：Claude（Opus 4.8）
> 评审基线：源码核对 + 本机运行态实测（含本次新增的无密码 server 认证行为实测，见 §2）
> 前序评审：[第一轮](2026-07-02-opencode-shared-service-discovery-review.md) · [第二轮](2026-07-02-opencode-shared-service-discovery-review-r2.md)

## 0. 总体结论

**方案接近实现就绪。第二轮的 R2-1～R2-8 全部闭环。第三轮只剩 1 个主要问题（R3-1）：
新增的"拒绝无密码 server"安全规则，其实现意图（§0/§5.2/§8）与 T02 的校验逻辑不一致——
实测确认无密码 server 会放行任意 auth 头，T02 现逻辑会把它误判为已认证而接受。补一个
no-auth 探测步即可闭合。其余为打磨项。**

| 维度 | 第二轮 | 第三轮 |
| --- | --- | --- |
| R2-1 共享 server 持久化/归属 | 主要 | ✅ §5.8 bring-your-own-server + LaunchAgent 模板（不自动安装）+ 失败模式/验收 #13 |
| R2-2 升级默认行为 | 主要 | ✅ §5.5/T01 存量→`legacy_64667`+提示，新装→`not_configured` |
| R2-3 Desktop 已运行不立即切换 | 次要 | ✅ §7/T06/T07 人工确认项 |
| R2-4 T07 测试范围 | 次要 | ✅ T07 列三个 `-only-testing:` target |
| R2-5 `opencode web` 不作 server 来源 | 次要 | ✅ §1/§2 改为 canonical=serve，web 仅手动验证 |
| R2-6 无密码启动行为文档化 | 次要 | ✅ §0/§3/§4/§8 写明 |
| R2-8 localhost→127.0.0.1 | 次要 | ✅ §5.2/T01 规范化 |
| **R3-1 passwordless 检测意图 vs 实现** | — | ❌ **主要**（实测确认，见 §1） |

---

## 1. 主要问题（第三轮）

### R3-1 — "拒绝无密码 server" 的实现逻辑没达到 §0/§5.2/§8 声明的安全意图

**严重度：主要（安全意图未落地，小改可闭合）**

方案多处明确要求：无密码 OpenCode server（`/global/health` 对无 auth 返回 200）不得被
CordCode 默认/自动路径接受（§0、§3 step 7、§4、§5.2 "无密码 endpoint 即使
`/global/health` 返回 200 也应被拒绝"、§8 "无密码 OpenCode server 不作为默认/自动 endpoint"）。

**但 T02 的校验逻辑实现不了这个意图。** T02 现写：

```
1. GET /global/health (2s 超时)
2. 带 Basic Auth（username 默认 opencode）
3. password 为空 → 拒绝 auth_required_for_cordcode
4. 401 → auth_failed
5. connection refused/timeout → unreachable
6. 非 JSON/缺 healthy/version → not_opencode_server
```

步骤 3 的"password 为空"是**客户端配置检查**（用户在 CordCode 里没填密码），
而 §5.2 要的是**服务端密码态检查**（server 本身是否启用了 Basic Auth）。**这是两个不同的检查。**

**本次评审实测确认（loopback，5s 后关闭并清理）：**

```
PASSWORDLESS stable opencode serve（未设 OPENCODE_SERVER_PASSWORD）：
  no-auth  /global/health            → HTTP 200 {"healthy":true,"version":"1.17.13"}
  auth     opencode:somepassword     → HTTP 200   ← 无密码 server 直接忽略 Authorization 头
  auth     randomuser:anypass        → HTTP 200   ← 任意用户名/密码都放行
  server log: "Warning: OPENCODE_SERVER_PASSWORD is not set; server is unsecured."
```

**结论：无密码 server 对任何 auth 头都回 200。** 因此在"用户在 CordCode 里填了密码、但
server 端忘记设 `OPENCODE_SERVER_PASSWORD`"这个**最常见的误配场景**下，T02 的 authed 请求
会拿到 200，CordCode 把一台**裸奔的 OpenCode server 当作已认证而接受**——直接违背方案反复
声明的安全约束。loopback 虽限制了网络可达性，但本机任何进程/用户都能驱动该 server（读
session、以用户身份发 prompt），是真实的本地提权面。

**修复（最小、确定性）：T02 增加一个 no-auth 探测，置于 authed 探测之前。**

```text
validate(url, username, password):
  0. password 为空                       → auth_required_for_cordcode   # 客户端配置守卫
  1. no-auth GET /global/health (2s):
       conn refused/timeout              → unreachable
       200 + OpenCode body               → server_unauthenticated       # 无密码 server，拒绝
       401                               → 进入步骤 2（server 要求认证）
       其他/非 OpenCode body             → not_opencode_server
  2. authed GET /global/health (配置的 user:pass):
       200 + OpenCode body               → 通过
       401                               → auth_failed
```

关键点：**必须先发 no-auth 探测**——只有 no-auth 回 401 才能证明 server 启用了 Basic Auth，
才进一步用配置凭据验证。只有步骤 2 的 authed 请求无法区分"密码正确"与"server 根本不要密码"。

需同步更新：
- T02 实现与测试（新增"no-auth 200 → server_unauthenticated 拒绝"、"无密码 server 即使带
  正确用户名也不接受"两条用例——实测脚本可作 fixture 依据）；
- §5.2 校验要求把"无密码 endpoint 即使返回 200 也拒绝"与 T02 对齐为显式 no-auth 探测；
- 验收标准补一条"server 对 no-auth 返回 200 时，CordCode 必须拒绝该 endpoint"；
- reason 名建议从 `auth_required_for_cordcode` 拆为：客户端未配密码用 `password_required`，
  server 无密码用 `server_unauthenticated`（见 R3-5）。

> 顺带：OpenCode 自身会在 stderr 打 `Warning: OPENCODE_SERVER_PASSWORD is not set; server
> is unsecured.`。建议 T06 文案告诉用户：若在 OpenCode 日志看到这条，CordCode 会拒绝该
> endpoint，需给 server 设密码后重启。降低用户排障成本。

---

## 2. 第二轮问题的闭环确认（简要）

- **R2-1 ✅**：§5.8 明确 Phase A = bring-your-own-server，CordCode 不启动/不 keepalive；
  提供 LaunchAgent 模板（占位符、不含真实路径/密码）、声明不自动安装；§7 加"server 退出→
  三端同时失联，Phase A 不 keepalive"行；验收 #13。
- **R2-2 ✅**：§5.5 定义升级迁移分支（存量 credentials.json + 无显式 source → `legacy_64667`
  +一次性提示；新装 → `not_configured`；已有显式 source → 尊重）；T01 step 6 + 测试覆盖；
  §7 失败模式表对应行。
- **R2-3 ✅** / **R2-4 ✅** / **R2-5 ✅** / **R2-6 ✅** / **R2-8 ✅**：见 §0 表。

---

## 3. 次要问题（第三轮）

| ID | 位置 | 问题 | 建议 |
| --- | --- | --- | --- |
| R3-2 | §5.1/§5.2 + 升级迁移 | `legacy_64667` 是否套用 passwordless-guard 不明确。§5.2 校验要求泛指"Phase A endpoint"，但 `legacy_64667` 是显式 source。升级自动迁移（R2-2）会把存量用户**自动**落到 `legacy_64667`，而本机 64667 实为旧 unified bridge 的无密码/`0.0.0.0` server——用户被静默迁到无密码 server。 | 明确 `legacy_64667` 是否豁免 no-auth 探测；若豁免（保连续性），迁移提示必须警告"该 64667 可能无密码且监听 0.0.0.0"，并引导清理旧 bridge 后改用 `external_http`。 |
| R3-3 | §5.8 LaunchAgent 模板 | plist 把 `OPENCODE_SERVER_PASSWORD` 放进 `EnvironmentVariables`，文件含密，但模板未要求 `chmod 600`。 | 模板后补一行：`chmod 600 ~/Library/LaunchAgents/local.opencode.shared-server.plist`（CONTRIBUTING 对凭据严格）。 |
| R3-4 | §5.8 LaunchAgent 模板 | 缺 `StandardOutPath`/`StandardErrorPath`，server 日志无处可查，排障困难。 | 模板加这两个 key 指向用户可写路径（如 `~/Library/Logs/opencode-shared.log`），不提交具体路径。 |
| R3-5 | T07 测试归属 | T07 跑 `SettingsViewModelTests`，但 T01 只提到 `SettingsViewModel.swift`，该测试类是否叫这名未确认；T02 的 health/auth 测试属于哪个测试类未写明。 | T02 注明其测试（含 R3-1 新增的 no-auth 用例）归入 `OpenCodeEndpointResolverTests`；T07 的 target 名与 T01/T02 实际测试类对齐。 |
| R3-6 | §3 step 2 vs §5.2 | §3 列 `http://localhost:<port>` 为可接受，§5.2 又说规范化 localhost→127.0.0.1。措辞轻微打架。 | §3 改为"输入可填 `localhost`，保存时规范化为 `127.0.0.1`"。 |
| R3-7 | reason 命名 | `auth_required_for_cordcode` 一个名既表"客户端未配密码"又表"server 无密码"，UI 文案难懂。 | 拆成 `password_required`（客户端未配）与 `server_unauthenticated`（server 无密码）。 |
| R3-8 | CHANGELOG line 14 | 已补二轮内容，但未提 passwordless-rejection 这条用户可见的安全行为。 | line 14 末尾补一句：默认拒绝无密码 OpenCode server（no-auth `/global/health` 返回 200 即拒绝）。 |

---

## 4. 进入实现前的最小修订清单（按优先级）

1. **【R3-1，必做】** T02 增加 no-auth 探测步（见 §1 伪代码）；§5.2/验收标准/reason 名同步；
   T02 测试补 no-auth-200-拒绝、无密码 server 带正确用户名仍拒绝 两条用例。
2. **【R3-2，必做】** 明确 `legacy_64667` 是否豁免 passwordless-guard；不豁免则一并探测，
   豁免则在迁移提示中警告无密码/`0.0.0.0` 风险。
3. **【R3-3/R3-4】** LaunchAgent 模板补 `chmod 600` 与日志输出路径。
4. **【R3-5/R3-6/R3-7/R3-8】** 文档与命名打磨，择机处理。

完成 R3-1 与 R3-2 后，Phase A 可进入 T01 实现，且其"拒绝无密码 server"安全承诺才算真正
可兑现。

---

## 5. 一句话总结

**第二轮问题全部闭环，方案接近实现就绪；第三轮只剩一个主要问题——"拒绝无密码 server"
的安全意图没落到 T02 的探测逻辑上（实测确认无密码 server 对任意 auth 头都回 200），补一个
no-auth 探测步即可闭合；另需明确 legacy_64667 与该规则的交互。**
