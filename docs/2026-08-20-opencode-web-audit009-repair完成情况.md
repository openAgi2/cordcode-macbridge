# OpenCode Web audit-008 集中产品路径补洞 — 指令 9 号完成报告

- 日期：2026-08-20（UTC）
- 指令：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/directive-009-audit008-concentrated-product-path-repair.md`
- 依据：audit-008 rejected + owner 方案 A；canonical 未修改
- **诚实声明：exec-plan proven ≠ supervisor verified ≠ owner 真机 done。全部证据 self-attested；owner UI 矩阵未执行。**

## 1. Commits（按类别）

| commit | 类别 | scope |
|---|---|---|
| `851c2c6` | product+tests（W1+W2） | 单一 ingest owner（adapter passive 过滤 + bridge passive 门控）；question source identity；todo/provider/config/mutation 全面 fail-closed；全链路红灯复现器 |
| `cb1d8e6` | tests/activation（W4 门） | 能力门改为生产派生（deriveBackendCapabilities）+ negative-before-positive |
| （本报告 commit） | closeout | exec-plan、本报告 |
| iOS `71007a4` | product+tests（W3） | ModelVariantSelection 共享 builder + UIKit submenu + SwiftUI 行 + 7 项测试 |

## 2. Audit-008 缺口 before → root cause → after

| 缺口 | before（红灯复现） | root cause | after（同测试转绿） |
|---|---|---|---|
| C4 双 Kernel ingest | 投影文本 `"ONCEONCE"`（同一事实 2 次） | emit() 把 routed 会话事件同时复制到 route 与 passive tap，两条消费者都调 deltaBatcher.Send | passive tap 不再接收 routed 会话事件（route relay = 唯一 ingest owner）；bridge passive 只为客户端实际订阅的会话发布（unopened+未订阅 = 仅目录） |
| C6 Question 不可达 | 投影 0 个 user_input part | asked 帧 未解码 tool.messageID/callID，Event 无 TurnID → reducer identityless drop | asked 携带 armed-turn 相关性（TurnID）+ tool.callID（ItemID）；无身份/无 armed turn → fail-closed 无 phantom turn；投影恰 1 个 pending part |
| unopened 隐藏 timeline | 未打开会话获得 reducer state | passive 无条件 PublishLogical | 无订阅者 → 零 ingest（仅 registry bookkeeping） |
| Todo 宽松 | `text` 别名/默认值/坏行跳过/partial emit | decode 逻辑含 fallback | endpoint/event 只接受精确 {content,status,priority}；任一坏行整次失败、lastTodos 不动、零 partial EventPlan |
| Provider 宽松 | map-key 回退/坏行 continue | decode fallback | connected 行缺 id → 错误；顶层 shape 先行判定；未连接行过滤但不修补 |
| Config null/absent 混同 | `model:null` 当 absent | `string` 解码归零 | 仅 evidence-proven 缺键 = 无配置；null/非 string/空串 → shape error |
| Mutation 宽松 | 任意 2xx + 缺 ID 回填 | 未验证响应事实 | rename/archive 校验回显 id/title/time.archived；delete 只认裸 `true` + by-ID 404 + scoped list absence（非收敛不 signal catalog） |
| iOS variant 无 UI | selector 不存在 | 只接了状态层 | UIKit `makeModelConfigurationMenu` 变体子菜单 + SwiftUI popover 模型页变体行，共用同一 builder |

## 3. C4 production owner/route 简表

| 场景 | ingest owner | 证明 |
|---|---|---|
| opened session（active route） | 该 session route 的 relay（唯一） | SingleIngest 全链路：一次事实 → 投影恰一次 |
| 多连接观察同一 session | 同一 route → 一次 publish → broadcaster 扇出 | 既有 broadcaster 语义 + 单 relay per sessionID |
| unopened + 客户端订阅（observation scope） | passive 单次发布（E3 订阅路径） | passive 门控 = HasSessionSubscriber |
| unopened + 无订阅 | 无（仅目录/control 刷新） | UnopenedSessionCatalogOnlyNoTimeline |

## 4. Question identity 字段映射

`question.asked.tool.messageID` → （subscriber armed-turn 相关性）→ `Event.TurnID` → wire `turnId` → reducer 挂到 owning turn；`tool.callID` → `Event.ItemID` → `itemId`。replied/rejected 按 interactionId 原位 resolve（不回灌 legacy、不写 messages[]）。GET /question 冷恢复保持同规则（无 tool identity 的 pending 行不投 phantom）。

## 5. 命令与真实输出（要点）

```text
go build ./... ; go vet ./...                     => PASS（零告警输出）
go test -race ./agent/opencode-web/               => ok 5.241s
go test ./... -count=1 -timeout 10m               => 全 ok（0 失败）
go test ./go-bridge ./agent/opencode-web -run TestAudit008 => 全 PASS
OCW_SANDBOX=4398 TestSandboxC2/C3C5/C4/C6C7/E2E/ArchiveDelete => 全 PASS（真实 serve）
xcodebuild test ModelVariantSelectionTests(7) + OpencodeWebVariantAgentWireTests(4) => 11/11 PASS
```

进程收尾：4398/4399 回收（LISTEN=0）；沙盒 ROOT 留证。**4096**：批次内 PID 9258→67389，唯一原因是 CLAUDE.md 强制的 killall+Release 覆盖安装（新 serve 父进程 = 新装 `/Applications/CordCodeLink.app/Contents/MacOS/CordCodeLink`，PPID 67358）；harness 未触碰 4096。

## 6. Capability 恢复（生产派生，非手写镜像）

- negative-before-positive：bare agent（零可选接口）经 `deriveBackendCapabilities` 不产出任一 gated capability（todos/structured_user_input_v1/session_mutation/session_delete/permission_resolve）。
- 真实 agent 精确集：gated 全恢复 + image/file + 基础集；`question_reply`/reasoning/compression/checkpoint/rollback 继续 absent。
- 未被 audit 驳回的能力（permission、attachments 等）未受误伤；无 backend-id 例外，无需新增 wire 语义（未触发停止线 3）。

## 7. 安装与运行态

- **Mac**：Release 重建于 `cb1d8e6da767`（2026-08-20T20:03:56Z），覆盖安装 `/Applications/CordCodeLink.app`；8777 = PID 67457，binary 路径来自安装目录，drivers 含 opencode-web。
- **iOS**：Release（generic/platform=iOS）`** BUILD SUCCEEDED **`；定向测试 11/11。**iPhone 16 Pro 在安装时点转为 unavailable（锁屏/断连）**——按指令"检测到可用 iPhone 时安装"，本轮真机 install/launch 顺延至设备可用；未做任何 UI 自动化。
- 双仓 `git status --short`：本报告 commit 后均 clean。

## 8. 边界

- E2 reasoning、OD-3 十四项、legacy question_reply：仍未实现、未广告。
- iOS 真机安装为待设备可用项（构建已就绪）；owner UI 矩阵待 audit-009 通过后一次性下发。
- 无停止线触发（A7 冷恢复字段齐备；未新增 writer/协议字段；未用 dedupe 掩盖双写；首轮修复即改变 full-path 复现结果）。

提交本报告后停止，等待 audit-009。
