# 第四轮评审：OpenCode 共享本地服务接入方案（修订三版）

> 评审对象：[docs/2026-07-02-opencode-shared-service-discovery-plan.md](../2026-07-02-opencode-shared-service-discovery-plan.md)（已按第三轮评审修订）
> 评审日期：2026-07-02
> 评审人：Claude（Opus 4.8）
> 前序评审：[第一轮](2026-07-02-opencode-shared-service-discovery-review.md) · [第二轮](2026-07-02-opencode-shared-service-discovery-review-r2.md) · [第三轮](2026-07-02-opencode-shared-service-discovery-review-r3.md)

## 0. 结论：批准进入实现

第三轮的 R3-1（passwordless 检测意图 vs 实现）、R3-2（legacy_64667 例外）以及 R3-3～R3-8
**全部稳健闭合**，未引入新问题。方案已具备实现就绪度，可进入 T01。本轮仅余 2 项**可选打磨**
（非阻断，实现时可顺手处理或忽略）。

| 维度 | 第三轮 | 第四轮 |
| --- | --- | --- |
| R3-1 passwordless no-auth 探测 | 主要 | ✅ 闭合（边界情况逐条验证，见 §1） |
| R3-2 legacy_64667 例外 + 警告送达 | 主要 | ✅ 闭合（见 §2） |
| R3-3 LaunchAgent `chmod 600` | 次要 | ✅ §5.8 已加 |
| R3-4 LaunchAgent 日志路径 | 次要 | ✅ §5.8 已加（占位用户名） |
| R3-5 T02 测试归属 | 次要 | ✅ T02 标注 `OpenCodeEndpointResolverTests` |
| R3-6 localhost 措辞一致 | 次要 | ✅ §3 step 2 改为"输入可填/规范化" |
| R3-7 reason 命名拆分 | 次要 | ✅ `password_required` / `server_unauthenticated` |
| R3-8 CHANGELOG 补 passwordless | 次要 | ✅ line 14 已补 |

---

## 1. R3-1 闭合确认（逐边界验证 T02 探测逻辑）

T02 step 1-6 的 no-auth-first 逻辑，按所有有意义的输入组合推演：

| 场景 | no-auth 探测 | 走到 | 最终 reason | 安全意图 |
| --- | --- | --- | --- | --- |
| 无密码 server + 任意 auth 头（最常见误配） | 200 + OpenCode body | step 2 短路 | `server_unauthenticated` 拒绝 | ✅ 关键漏洞堵住 |
| 无密码 server + CordCode 配了密码 | 同上（no-auth 先于 authed） | step 2 短路 | `server_unauthenticated` | ✅ 不被 authed 200 骗过 |
| 非 OpenCode server 返 200 | 200 + 非 OpenCode body | step 2 末分支 | `not_opencode_server` | ✅ 与 passwordless 区分 |
| 密码 server + 正确凭据 | 401 → authed 200 + OC body | step 4 | 通过 | ✅ |
| 密码 server + 错误凭据 | 401 → authed 401 | step 5 | `auth_failed` | ✅ |
| 密码 server + authed 200 但 body 非 OpenCode | 401 → authed 200 非 OC | step 6 | `not_opencode_server` | ✅ |
| 不可达 / 超时 | 连接失败 | step 2 首分支 | `unreachable` | ✅ |

关键点验证通过：**body 校验在 no-auth 与 authed 两个探测点都做**，因此能区分"无密码
OpenCode（200 + OC body）"与"随机 200（非 OC body）"；且 no-auth 200 短路在 authed 探测之前，
保证"用户配了密码但 server 无密码"的误配必被 `server_unauthenticated` 拦下。§5.2/§8/验收 #14
与 T02 完全对齐。**R3-1 稳健闭合。**

---

## 2. R3-2 闭合确认（legacy_64667 例外）

legacy_64667 作为"升级连续性兼容例外"在五处一致落地，且警告确实能送达用户：

- §5.2（282-285）：例外仅限 legacy_64667；no-auth 200 时可继续但必须显示
  `legacy_insecure_unverified`，引导清理旧 bridge + 改配 external_http；新装与 external_http 不享此例外。
- §5.5（359-361）：迁移提示明确写清"仅为不中断旧用户、不能证明 Basic Auth、不承诺与 Desktop vlocal
  共享、本机 64667 可能是无密码/`0.0.0.0` 的 unified bridge"——这是唯一允许带警告运行的 source。
- §7 失败模式（663）：legacy 显式选择 → 标记 legacy；no-auth 200 → `legacy_insecure_unverified`。
- §8（678-679）+ 验收 #15：例外必须带警告，不得当安全共享或新装默认。

警告通过 **Settings 迁移提示 + backend reason 字段（iOS 经 `/internal/agents` 可见）** 双通道送达。
**R3-2 闭合。**

---

## 3. 可选打磨项（非阻断）

### R4-1 — legacy_64667 + 密码 server 的路径未显式写明

§5.2/T02 详细规定了 `external_http` 的 no-auth→authed 流程，以及 legacy 在 no-auth 200 时的
`legacy_insecure_unverified` 处理；但**当 legacy_64667 的 no-auth 探测返回 401（即 64667 确实
启用了 Basic Auth）时**，是否继续用迁移过来的 credentials.json 凭据做 authed 校验、通过后以
legacy（不带 insecure 警告）可用——这一支没有显式写出来。实现时大概率会自然这么做，但规格
上留个 1 行说明更稳妥（例如 §5.2 legacy 段补一句"no-auth 401 时按 authed 校验，通过即 legacy
可用，无需 insecure 警告"）。

### R4-2 — health body schema 锁定 1.x，升级 opencode 时需复测

T02 的 body 校验以 `healthy` + `version` 字段为准（实测自 1.17.13）。若未来 opencode 改了
`/global/health` 的响应 schema，`server_unauthenticated` 与 authed 通过判定都会受影响。
Phase B 条件 4 已要求"端点兼容性复测"，但 Phase A 本身在 opencode 升级时也应触发该复测。
建议 T02 注一句"health body schema 以 1.17.13 实测为准，opencode 升级时必须重核"——把隐含
前提显式化，避免后人改 schema 时漏更新校验。

---

## 4. 实现时的小提醒（非评审发现）

- **手动验证前先清旧 unified bridge**（T07 已列 `pgrep` 命令）：本机 64667 现仍被
  `opencodeIosNew/start-unified.mjs` 的 `opencode serve --hostname 0.0.0.0` 占用，不清掉会污染
  legacy_64667 与 external_http 的验收观察。
- **reason 文案国际化**：`password_required` / `server_unauthenticated` / `auth_failed` /
  `legacy_insecure_unverified` 等会经 backend reason 到达 iOS，T06 改 Localization.swift 时记得
  给这些 reason 配中英文案，避免 iOS 直接显示英文 reason code。
- **T02 测试 fixture**：no-auth 200、authed 200、401 等用例需要一个可控的假 OpenCode server
  （或直接拉起真实 `opencode serve` 带/不带 `OPENCODE_SERVER_PASSWORD`），实现时一并搭好。

---

## 5. 一句话总结

**第三轮全部问题稳健闭合，方案实现就绪、批准进入 T01。** 唯一可选项是 R4-1（补 legacy + 密码
server 那一支的 1 行说明）和 R4-2（health schema 锁版本提示），均非阻断，实现时顺手即可。
