# 评审报告：`docs/2026-09-22-dsh-web-install-and-start-plan.md`（round 3，v3）

- 日期：2026-09-22
- 结论：**通过（APPROVE，收口）**。round 2 的两项条件（R2′-1/R2′-2）均已按建议
  落地并核实。方案评审循环关闭，可进入开发阶段。
- 评审性质：只读，针对 v2→v3 diff 的快速复核（round 2 承诺的复核方式）；本报告是
  本轮唯一交付物。

## 0. 评审来源清单（round 3）

```text
Mac 仓=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=b4e42d5c5a9af6c12abe062a32a30bccece3bd5e（与 round 2 相同，无新提交）
未提交状态=2 M + 3 ??：
  M  GO_BRIDGE_ARCHITECTURE.md（doc-only 注记，与 round 2 核实过的文案一致，无漂移）
  M  docs/2026-08-19-dsh-web-canonical-3080-instance-design.md（同上）
  ?? docs/2026-09-22-dsh-web-install-and-start-plan.md（v3，本对象）
  ?? docs/2026-09-22-dsh-web-install-and-start-plan-review.md（round 1，未改）
  ?? docs/2026-09-22-dsh-web-install-and-start-plan-review-r2.md（round 2，未改）
业务代码=未修改 ✓（与 v3「未实施」声明一致）
上游=/Users/jacklee/Projects/deepseek-harness @ 0d1f50007f9bca3f52b06e1c3074fa14d5fb0720（未变）
iOS 配套=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
iOS 提交=544a8de705bd91db6c03e66092d4122507a2074b [feat/ios-native-message-timeline]
iOS 未提交=干净（与 v3 §0 声明一致）
```

## 1. round 2 两项条件的落地核实

### R2′-1 组合态优先级 + 判别顺序 —— 已解决 ✓

- §3 row 1 显式声明优先于「端口占用」：无二进制 → `not_detected`（未安装 + 安装），
  3080 同时被非 dsh 占用仍走本行；row 5 条件改为「已有 `dsh`，TCP 能连上座位，
  `host.describe` 失败，且 lsof 命令行不含 `dsh`。没有二进制时不走本行」。
- §5 判别顺序写成一句无歧义序列：先找二进制（没有就停在 `not_detected`，**不看
  端口**）→ 只读 `host.describe`（成功即 `available`）→ 失败后 TCP 连座位（连不上 =
  无监听 `service_not_running`；连上才 `lsof`）→ 命令行含 `dsh` =
  `service_not_running`、不含 = `port_conflict`、`lsof` 失败不猜占用。点「启动」后
  spawn 错误可覆盖字幕。
- 「本轮修订」节按评审纪律记录了采纳（建议方案）与不采纳（`port_conflict` 优先
  备选）及理由。§3 与 §5 的条件表述一致。

### R2′-2 iOS 快照 —— 已解决 ✓

- §0 配套 iOS 更新为 `544a8de`（实测 HEAD 一致，工作树干净），写明 round 2 的
  2 个 M 已进入 `544a8de`（实测：该提交只含 NativeTimelineViewController.swift 与
  TimelineCardCells.swift，与 dsh-web 无重叠），并加注「iOS 快照会漂移，status
  结论以实施门复核为准」。
- `544a8de` 未触碰 `BackendModels.swift`（`git diff 1870d059..544a8de -- …
  BackendModels.swift` 为空），§3 引用的 `:25/:61/:81` 行号在当前配套提交上仍有效。

## 2. 附带核实

- Mac HEAD 与工作树（2 M + 3 ??）与 v3 §0 完全一致；两处活文档注记与 round 2
  核实过的文案逐字一致，无漂移；业务代码零修改。
- v3 未改动 §1–§2、§4、§6–§8 的已审内容（对照 v2 通读确认）。

## 3. 非条件性备注（不阻断，实施时自行取舍）

§7 可选加一条组合态断言：无二进制 + 3080 被 fake 非 dsh 监听 → 仍 `not_detected`
（starter 未被调用、不查 lsof）。§5 的控制流顺序（「没有就停在 not_detected，
不看端口」）已使该行为结构上成立，此断言只是把它锁进回归。

## 4. 结论

**通过。** 三轮评审收口：round 1 程序门（来源清单、裁决出处与注记口吻）→ round 2
设计补全（port_conflict 只读判定、诊断只读回归、CHANGELOG、日期锚点）→ round 3
组合态优先级与快照漂移。方案文档即为实施依据；进入开发阶段时按 §8 接线顺序执行、
按 §7 收口测试，实施门按 §0/§3 要求对当时的配套 iOS 提交复核一次。
