# “远程访问”界面重构设计方案第三轮评审

> 评审对象：[remote_access_design_proposal.md](../remote_access_design_proposal.md)（第二版）  
> 上一轮评审：[remote_access_design_proposal_review_r2.md](remote_access_design_proposal_review_r2.md)  
> 评审日期：2026-06-19  
> 评审视角：资深 iOS / Apple 平台交互设计 + 当前实现能力复核  
> 核对方法：逐条对照 `GET /internal/remote/status`、`relay_management.go`、`relay_config.go`、`RemoteStatus` Codable、`OfficialRelayProvisioner.ensureRoute()`、`RuntimeManager.restart()`、`remoteIdentityURLs()` 的真实行为

## 一、结论

**通过。R2 的全部 P0（P0-1 ~ P0-3）与 P1（P1-1 ~ P1-5）八项均已在第二版正文中落地，且对当前后端能力的描述诚实。可以进入实现阶段。**

本轮复核未再发现阻塞性的信息架构或“状态造假”问题。下面三条 P2 级别的问题是“实现迁移时必须对齐、但不应再返工设计稿”的细节，建议作为实现验收清单的补充项，而不是再次修订设计方案的理由。

修订日志中的 8 条逐条复核结论见第三节。

## 二、与 R2 实施门槛的对照（共 8 条，全部达成）

| R2 门槛项 | 第二版落实情况 | 代码层佐证 | 结论 |
|---|---|---|---|
| 1. 删除接口不支持的 Relay 连通状态 | Relay 收敛为 `已配置 / 需要配置 / 状态未知` | `handleRemoteStatus` 仅返回 `relay.configured / endpoint / routeId`，无 `connected`/`lastConnect`/`lastError` | ✅ |
| 2. 保存状态机补“应用、重启、应用确认” | Relay 事务含 `Provisioning / 应用配置 / Bridge 重启`；VPS 收敛为 `正在保存 / 已应用` | `OfficialRelayProvisioner.ensureRoute()` POST `v1/activations/routes`；`handleRemoteURLChange()` → `restart()` | ✅ |
| 3. 恢复公网 `ws://` 安全规则 | 引入 `isPublicWS` 持久橙黄警告 + `https://→wss://` 规范化 | `remoteURLAnalysis.isPublicWS`、`BridgeRemoteURLFormatter.normalize` 已实现 | ✅ |
| 4. Tailscale 文案收敛到“地址是否存在” | `已检测到 Tailscale 地址 / 未检测到可用的 Tailscale 地址` | `tailscaleURL` 仅来自 `detectTailscaleIP()`，空值只证明无地址 | ✅ |
| 5. 统一左栏主状态维度 | 四行统一为可用性/配置状态，“加入新配对”降为次级 Badge | — | ✅ |
| 6. 修正局域网地址安全表述 | 改为“局域网连接地址……仅应在可信局域网内使用” | — | ✅ |
| 7. 定义前置条件消失时 Toggle 行为 | 保持开启但置灰禁用、标注“无可发布地址”、空候选不入配对 | `remoteIdentityURLs()` 经 `uniqueNonEmptyStrings` 过滤空 URL | ✅ |
| 8. 窄窗口切换状态保持 | `<680pt` NavigationStack + `< 返回`；承诺焦点/输入/报错 100% 保留 | — | ✅ |

## 三、R2 八条逐项复核

### P0-1：Relay 连通性对齐 — ✅ 完全落实
正文第 84—86 行明确：“接口展示数据：展示当前配置的 `endpoint`，屏蔽内部敏感的 `routeId` 及 `relayCredential`，不进行假延迟检测。”  
左栏状态词（第 62 行）已收敛为 `已配置 / 需要配置 / 状态未知`，去除了 R1 遗留的“可用/连接失败/正在检查”。

**代码佐证**：`GET /internal/remote/status`（`management_api.go:670`）的 `relay` 对象只含 `configured / endpoint / routeId` 三个字段；Mac 端 `RemoteStatus.RelayStatus`（`ManagementAPIClient.swift:65`）也只解码这三者。虽然 `relay_management.go` 内部 `RelayConfigSnapshot` 携带 `connected/lastConnect/lastError`，但这些字段只通过 `get_relay_status` RPC 暴露，本页面使用的管理接口确实拿不到。设计方案选择“不改接口、只显示已配置态”是正确且诚实的。

### P0-2：完整保存事务 — ✅ 落实（存在一处与当前代码路径的迁移差异，见 P2-1）
正文第 89—93 行给出完整状态机：
```
未保存 → 正在校验格式 → 正在申请中继(Provisioning) → 正在应用配置 → Bridge 正在重启 → 已应用
```
失败分支区分 `格式错误 / 中继注册失败 / Bridge 重启超时`；VPS/FRP 因不做网络探测，文案收敛为 `正在保存 / 已应用`。

**代码佐证**：
- `OfficialRelayProvisioner.ensureRoute()`（`RuntimeManager.swift:1121`）确实向 `endpoint/v1/activations/routes` 发 POST 并期望 `201`，对应“正在申请中继(Provisioning)”阶段；失败抛 `registrationFailed`。
- `restart()`（`RuntimeManager.swift:199`）+ `applyConfigAndRestart()` 真实存在，对应“Bridge 正在重启”阶段。
- VPS/FRP 保存（`saveRemoteURL()`）确实只做格式校验 + 写 `@AppStorage` + 发 `remoteURLDidChange`，无网络探测，文案收敛为“保存/已应用”符合真实能力。

> ⚠️ 见 P2-1：当前 `applySelectedRelayEndpoint()` 在 Relay endpoint 未实际变更时不会触发 restart，设计稿的“Bridge 正在重启”阶段需要按“endpoint 变更才进入”实现，否则会出现“提示重启但实际未重启”。

### P0-3：公网 ws:// 风险提示 — ✅ 完全落实
正文第 105—106 行：保存时 `BridgeRemoteURLFormatter` 将 `https://` 规整为 `wss://`；当 `remoteAnalysis.isPublicWS` 为真时，在输入框下方持久显示橙黄色警告横幅。

**代码佐证**：`BridgeRemoteURLFormatter.normalize()`（`RemoteAccessView.swift:361`）已实现 `https://→wss://` 规则；`remoteURLAnalysis`（`management_api.go:751`）提供 `isPublicWS / securityLevel / hostCategory`，测试 `TestMgmtRemoteStatus_PublicWS` 验证了公网 `ws://` 下 `isPublicWS=true, securityLevel="insecure", hostCategory="public"`。

### P1-1：Tailscale 去推断化 — ✅ 完全落实
正文第 101 行收敛为 `🟢 100.x.x.x（检测到可用地址）/ ⚪️ 未检测到可用的 Tailscale 地址`，不再写“客户端未运行/未安装”。

**代码佐证**：`tailscaleURL` 仅来自 `detectTailscaleIP()`（`advertise_url.go:96`，扫描 100.64.0.0/10 CGNAT 段），空值只能证明“当前无可发布地址”，无法区分未安装/未运行/未登录。文案与接口能证明的事实严格对齐。

### P1-2：左栏状态维度统一 — ✅ 完全落实
正文第 60—68 行：四行主状态统一为路径本身的可用性/配置态；绿色仅用于已验证可用，灰色用于需要配置/无地址/未知，红色仅用于明确失败；“加入新配对”降为次级弱化 Badge。

### P1-3：局域网安全表述 — ✅ 完全落实
正文第 77 行：“本地监听地址使用 `ws://` 协议，未启用传输层加密。此设计基于在可信本地局域网内运行的安全策略，**仅应在可信局域网内使用**。”本地化表（第 141 行）同步为“局域网连接地址……仅应在可信局域网内使用。”修正了 R2 指出的“公网安全地址”概念错误。

### P1-4：失效过滤与状态继承 — ✅ 落实（存在一处与当前代码的迁移差异，见 P2-2）
正文第 99 行定义：`tailscaleURL` 为空时 Toggle **保持用户偏好但变灰禁用**，旁置红色警告“当前无可用的 Tailscale 虚拟网 IP……”。第 108 行自定义地址同理。设计原则（“尊重用户设置偏好，不擅自修改”）正确。

**代码佐证（后端诚实）**：`remoteIdentityURLs()`（`main.go:522`）经 `uniqueNonEmptyStrings` 过滤，即使 `includeTailscale=true`，空地址也不进入配对候选——与正文“空候选不入配对”一致。

> ⚠️ 见 P2-2：当前 `saveRemoteURL()`（`RemoteAccessView.swift:280,284`）在 URL 为空或非法时会**主动写 `includeRemote = false`**，这与设计稿“不擅自修改用户偏好”的原则相悖。这是实现迁移项，不是设计缺陷。

### P1-5：窄屏折叠与状态保持 — ✅ 完全落实
正文第 117—120 行：`<680pt` 转 NavigationStack，左上 `< 返回`；窗口跨阈值变化时，未保存文本/焦点/错误 100% 保留。第 130—132 行补充了“刷新状态”保留旧值 + 微型 ProgressView + 失败时“上次更新时间 · 刷新未成功”的友好降级。

## 四、P2：实现迁移对齐项（不阻塞设计定稿）

> 这三项是“当前代码与第二版设计在行为上仍有差异”的点。设计稿本身是对的，下列条目应进入**实现验收清单**，而非再次返工设计稿。

### P2-1：Relay“Bridge 正在重启”阶段需按“endpoint 变更才进入”实现
设计稿把 `[正在应用配置] → [Bridge 正在重启] → [已应用]` 作为 Relay 保存的固定尾部。但当前 `applySelectedRelayEndpoint()`（`RemoteAccessView.swift:308`）只调用 `ensureRoute()` + `notifyPairingConfigChanged()`；真正触发 `restart()` 的 `handleRemoteURLChange()`（`AppDependencies.swift:163`）仅在 relay endpoint/routeID/credential **实际发生变化**时才 `restart()`（见 `AppDependencies.swift:147-155` 的 guard）。

**实现要求**：UI 上的“Bridge 正在重启”状态灯应与“配置是否实际变更”绑定——endpoint 未变（如重复保存、或新 endpoint 与现值相同）时，事务应直接从 `[正在应用配置]` 跳到 `[已应用]`，不要展示“正在重启”造成虚假进度。

### P2-2：`saveRemoteURL()` 不得静默关闭 `includeRemote`
`RemoteAccessView.swift:280、284` 当前在 URL 为空或非法时执行 `includeRemote = false`。这违反 P1-4“保留用户偏好、不擅自关闭”的设计原则。实现时应改为：保持 `includeRemote` 原值，仅置灰 Toggle 并展示“无可发布地址”提示，由后端 `uniqueNonEmptyStrings` 保证空候选不入配对。Relay/Tailscale 的对称路径同理。

### P2-3：左栏 Relay 主状态不应继承“绿色=已配置”
正文第 65 行声明“绿色仅用于已验证可用”。但 Relay 在本接口下永远只能证明“已配置”、无法证明“已验证可用”（P0-1 的核心结论）。因此 Relay 行即便 `configured=true` 也应使用**灰色**（“已配置”是配置态，不是可用态），与正文第 62 行 “🟢 已配置 / ⚪️ 需要配置” 的写法存在自相矛盾——62 行用了绿点标注“已配置”，与 65 行“绿色仅用于已验证可用”冲突。

**修订建议（仅文档措辞，1 处）**：将第 62 行 Relay 行的 `🟢 已配置` 改为 `⚪️ 已配置`（或新增一个“配置完成但未验证连通”的中间色/文案），使左栏颜色规则在第 65 行的约束下自洽。这是本轮唯一建议在正文动笔的修订，且属 P2，不阻塞实现启动。

## 五、最终意见

**第三轮评审结论：通过，可进入实现。**

- R2 全部 P0/P1（8 项）已落实，且对后端能力的描述经代码核对诚实准确；
- 不再存在阻塞性问题；
- P2-1、P2-2 列为实现迁移验收项（设计稿无需改动）；
- P2-3 是唯一的文档措辞修订建议（1 处颜色自洽），可在实现并行期顺手修正，不影响动工。

建议下一步：将本评审第四节的三条 P2 项并入实现任务清单，按第二版设计方案直接开工。
