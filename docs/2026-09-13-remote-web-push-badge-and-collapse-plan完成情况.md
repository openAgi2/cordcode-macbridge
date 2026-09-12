# 2026-09-12 Remote Web 推送：通知折叠治理 + 应用图标角标 — 完成情况

日期：2026-09-13（计划创建 2026-09-12）  
队列：`.exec-plan/state/plan-034334e9176f.json`（42/42 done，队列哈希 `f0d4fa25489a`）  
计划：`docs/2026-09-12-remote-web-push-badge-and-collapse-plan.md`  
生产部署：`cordcode-bridge-runtime 0.1.0 (commit: c7d120a103da)`；iOS 仓 `aef93097`

## 结论

计划全部批次完成。核心目标「同一会话的连续完成不再堆成几十条」已真机验证通过；
角标链路完整交付但按 owner 决策休眠（激活缺口已查明，非缺陷）。

## 批次与证据（全部 self-attested，标注可复验项）

### P1 通知覆盖（前序会话 2026-09-12 完成，本次收口）

未打开 session 投递、replay-free eventId、五个 backend 的 replay-free live 通道、
backend 化通知标题。三个曾 blocked 的真机回归按 owner 复测证据正式收口
（`.exec-plan/evidence/034334e9176f/p1-blocked-regressions-owner-verification.md`）。

### P2 通知折叠（`a6ee5ec`，真机回归 PASS）

- `tag` 与 RFC 8030 `Topic` 共用 `(backendId, sessionId)` 聚合键（`ccs_` 前缀）；
  notification key 保持 per-turn 唯一（ledger 幂等不受影响）。
- 单元测试：同会话跨 turn 稳定、跨会话/跨 backend 不碰撞、26 字符合法（可复验：
  `go test ./go-bridge/ -run TestDispatcherPayloadTagStablePerSession`）。
- 真机回归（owner 2026-09-13 01:40，截图归档）：4 subagent 会话各留一条带真实
  回复预览的通知，无堆叠、无异常通知。同会话替换路径由 2026-09-13 离线 Topic
  合并测试（owner PASS）覆盖。

### P3 服务端角标状态（`24fb458` + iOS `bd91f1fa`）

per-device badge state 独立 0600 文件（损坏只关角标不伤订阅）、bindingId 生命周期
（幂等/换绑 `web_push.binding_mismatch`/4096 饱和边界/999 封顶）、payload 全有或全无
badge 元组、协议四处同步。单元测试 12 项（可复验：
`go test ./go-bridge/ -run 'Badge|Binding'`）+ 全仓 20 包。

### P4 badge RPC + 客户端生命周期（`c7d120a` + iOS `aef93097`）

`get_push_badge_state`/`acknowledge_push_badge`（binding_mismatch 稳定非重试错误、
saturated 水位稳定才清空）；SW 原子水位认领（乱序不回退、旧 binding 不复活）、
`setAppBadge` 失败不破坏通知、`CORDCODE_PUSH_BADGE_DIRTY_V1` 前台触发、单飞
get→ack、disable 清角标。Go RPC 测试 + contract 夹具断言 + iOS 15 项新测试
（可复验：`go test ./go-bridge/ -run 'TestBadgeRPC|TestWebPushContract'`；
remote-web `bun x vitest run src/core/push`）。

### P5 设置页角标文案 — owner 决策暂缓（`required:false`）

Owner 2026-09-13：「角标可能不是那么重要，太复杂就先不做」。角标链路已部署休眠
（无 bindingId 的设备不下发角标字段、零开销），P5 文案在角标启用前无用户可见面。
激活方式（已记录 CHANGELOG）：打开一次 Web App 重新注册 bindingId + iOS 徽标开关。

### 角标真机验证 — 激活缺口，非缺陷

01:40 测试时客户端未在 01:19 部署后重新打开 Web App，旧注册无 bindingId，服务端
按设计不下发角标。链路本身由自动化测试覆盖（SW 水位/生命周期/disable 语义）。

## 交付物清单

| 项 | 位置 |
|---|---|
| MacBridge 提交 | `a6ee5ec` `5811fa7` `24fb458` `901f587` `c7d120a` `f27a8db` `f7a22f8`（+ 本报告提交） |
| iOS 提交 | `bd91f1fa` `aef93097`（+ 本轮文档提交） |
| 协议 canonical | `docs/protocol/bridge-v1.md`（badge 元组、两个 RPC、binding_mismatch） |
| schema/夹具 | `docs/protocol/schema/bridge-v1.types.ts`、`docs/protocol/samples/web-push/`（4 个新夹具） |
| iOS 镜像 | iOS 仓 `docs/protocol/`（mirror checker 仅余既有 turn-presentation-v1 漂移） |
| 证据 | `.exec-plan/evidence/034334e9176f/`（测试日志、部署记录、owner 截图） |
| 经验复盘 | 两仓 `think.md` 2026-09-13 条目 |

## 遗留与边界（诚实声明）

- 角标真机端到端（图标出现数字→打开清除）**未验证**——链路休眠，激活后可复验；
  SW/页面行为由自动化测试证明，`setAppBadge` 的 iOS 系统展示行为无法离线复验。
- iOS 全量测试 88/89 文件：`relay frame-connection-rpc`（23 失败）为预存 WASM 模块
  缺失环境问题（文件零本地修改，已核实），非本计划引入。
- §9 另案（幽灵订阅清理、账本按「通知×订阅」记账、原生 App 角标）不在本计划内，未动。
