# 评审报告：`docs/2026-09-22-dsh-web-install-and-start-plan.md`（round 2，v2）

- 日期：2026-09-22
- 结论：**修改后通过**。round 1 两项阻断（R1-1/R1-2）已按路径 1 解决并核实；round 1
  全部 P2/P3（R2-1…R2-4）已采纳落地。本轮新增两项一行级修订（R2′-1/R2′-2），不触
  设计方向；v3 落地后即视为通过，进入开发阶段。v3 送审只做针对 diff 的快速复核。
- 评审性质：只读评审，未修改任何业务代码；本报告是本轮唯一交付物。

## 0. 评审来源清单（round 2）

```text
Mac 仓=/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline
分支=feat/ios-native-message-timeline
提交=b4e42d5c5a9af6c12abe062a32a30bccece3bd5e
未提交状态=2 M + 2 ??：
  M  GO_BRIDGE_ARCHITECTURE.md（本变更集 doc-only 注记，已按 R1-2 改写）
  M  docs/2026-08-19-dsh-web-canonical-3080-instance-design.md（同上）
  ?? docs/2026-09-22-dsh-web-install-and-start-plan.md（v2，本对象）
  ?? docs/2026-09-22-dsh-web-install-and-start-plan-review.md（round 1 报告，未改）
业务代码=未修改 ✓（与 v2 声明一致）
round 1 → round 2 之间的新提交=86452bf（回合级官方净 diff，10 个文件，与 round 1
  记录的无关脏文件逐一吻合）、b4e42d5（其 CHANGELOG 条目）；均与本方案无关 ✓
上游=/Users/jacklee/Projects/deepseek-harness
上游提交=0d1f50007f9bca3f52b06e1c3074fa14d5fb0720（干净，与 round 1 相同）
iOS 配套=/Users/jacklee/Projects/cordcode-ios-native-message-timeline
iOS 提交=1870d0590fc28a434d4bc1ee63c59c478ce8f774 [feat/ios-native-message-timeline]
iOS 未提交=2 M：NativeTimeline/TimelineCardCells.swift、
  NativeTimeline/NativeTimelineViewController.swift（均 timeline 工作，与 dsh-web
  status 无重叠；BackendModels.swift 干净）
```

## 1. round 1 发现的解决核实

### R1-1 来源清单不完整 —— 已解决 ✓

- v2 §0 与当前 `git status` 完全一致（2 M + 2 ??，逐项注明归属与本变更集关系）。
- round 1 的 10 个无关脏文件已进入 `86452bf`（文件名逐一吻合）、其 CHANGELOG 进入
  `b4e42d5`，不再留在工作树；round 1 HEAD（8091d7b）与差异在 §0 如实记录。

### R1-2（路径 1：记出处、改口吻、不回退）—— 已解决 ✓

- 裁决出处落笔：时间（2026-09-22 本会话）、载体（owner 消息原文「修改『不代装』
  裁决，改为可以代装。然后写个详细的实施计划文档」）、内容（只改代装），并如实
  说明 round 1 无法从仓内核实的原因。
- 两处注记已改写（git diff 逐字核实）：不再出现「已被取代」的既成事实口吻；
  GO_BRIDGE_ARCHITECTURE.md 注记明确区分 owner 改令与「未启动 + 启动按钮」推导；
  两处均声明「评审通过前仍是运行中的代码行为」「doc-only 交付，不是已实施记录」。
- 正文第 2 点把「冷启动不再自动补拉」标为方案推导并给出推导链（owner 要求重启后
  显示未启动+启动按钮 → 自动补拉会让按钮没有机会出现），不冒充裁决原文。

### R2-1 port_conflict 只读判定 —— 已解决 ✓

§5 补全判定序列：`host.describe` 只读探测 → 无监听 `service_not_running` → 在听但
不应答则 lsof → 命令行含 `dsh` = `service_not_running`（进程在、接口未就绪）、不含
= `port_conflict`、lsof 失败不猜占用；点「启动」后 spawn 错误可覆盖字幕（更具体）。
§7 补 fake-lsof 测试，含「starter 未被调用」「lsof 失败不猜占用」。fail-closed
方向正确。

### R2-2 诊断只读回归 —— 已解决 ✓

§7 独立断言「RunDiagnostics 与描述符刷新不调用 starter」，并明确「单独测，避免只锁
冷启动、诊断仍能补拉」。

### R2-3 CHANGELOG —— 已解决 ✓（§8 第 6 步，含条目文案）

### R2-4 日期锚点 —— 已解决 ✓

正文同时锚 2026-08-13 设计 + 2026-08-15 owner 产品指令 v2（`discovery.go:3-8`）；
`README.zh.md` 行号 23/26/29 本轮逐行 sed 复核，精确无误。

## 2. round 2 新发现（「修改后通过」的两项条件）

### R2′-1 §3 状态表 row 1 与 row 5 的组合态未定义优先级（P2）

「无 dsh 二进制」（row 1 → `not_detected`，未安装 + 安装按钮）与「座位被非 dsh
占用且不应答」（row 5 → `port_conflict`，无按钮）可以同时成立：用户从未安装 dsh，
3080 又被别的服务占了。表没有写哪个赢，§7 也没有覆盖该组合——这是四拍走查的一个
未定义人路径。两种解法都诚实，但必须选一个写明：

- 建议：**二进制缺失优先 → `not_detected`**。安装是唯一前进动作；装完点「启动」时
  端口冲突由 spawn 错误如实浮出（§5 已有覆盖机制，row 6 接住）。
- 备选：`port_conflict` 优先（用户先清端口再谈安装）。

同时把 §5「无监听 vs 端口在听但探测失败」的判别方式（TCP 层判别还是 describe 失败
即 lsof）并成一句无歧义的顺序描述；§7 的 fake-lsof 测试已隐含「describe 失败 →
lsof」流程，补一句文字即可，不需要改测试设计。

### R2′-2 §0 配套 iOS 未提交快照漂移（P2，一行修订）

v2 写 1 个 M（TimelineCardCells.swift）；本轮复核实际 2 个 M（另有
NativeTimelineViewController.swift，系配套工作树上的并行 timeline 工作）。两文件
均与 dsh-web status 无重叠，BackendModels.swift 干净、v2 引用行号（:25/:61/:81）在
1870d059 逐行核实精确，不影响任何声明。修订：更正该行，或注明「iOS 快照会漂移，
以实施门复核为准」（§3 已要求实施门复核，加注即可一劳永逸）。

## 3. 本轮逐项核实记录

| v2 声明 | 证据 |
| --- | --- |
| HEAD `b4e42d5`、工作树 2 M + 2 ?? | `git rev-parse` / `git status` ✓ |
| 无关脏文件已进 `86452bf`/`b4e42d5` | `git show --name-only` 逐文件吻合 ✓ |
| 业务代码未改 | 工作树仅 2 个 doc M + 2 个 doc ?? ✓ |
| 两处注记改写（无「已被取代」、区分改令/推导、doc-only） | 两份 `git diff` 逐字核实 ✓ |
| `README.zh.md:23/26/29` | 逐行 sed：23=「安装 Node.js，然后运行」、26=npx 命令、29=3080/--no-open ✓ |
| iOS @1870d059；`BackendModels.swift:25/:61/:81` | 逐行 sed：25=guard status=="available"、61=case deepSeekWeb、81="deepseek-web" wire 匹配 ✓ |
| iOS 不按 status 藏入口在新提交上仍成立 | resolve() 仅禁用+透传 reason；deepSeekWeb 常驻枚举 ✓ |
| 上游未变 | 0d1f500，干净 ✓ |

## 4. 结论与下一步

**修改后通过。** 开发者出 v3，只做两处一行级修订：

1. R2′-1：§3 表补 row 1/row 5 组合态的优先级（建议二进制缺失优先），§5 判别顺序
   并成一句无歧义描述；
2. R2′-2：§0 iOS 未提交行更正或加「以实施门复核为准」注。

不改设计方向、不改其余章节。v3 送审后本评审只对 diff 快速复核，通过即进入开发阶段
（§8 接线顺序、§7 验证收口、§6 不做清单继续有效）。
