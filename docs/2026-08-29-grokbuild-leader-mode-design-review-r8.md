# Grok Build Leader 模式开关设计 v8 第八轮评审

- 评审日期：2026-08-29
- 评审对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v8（1376 行）
- 文档 SHA-256：`6ebd2c3c93fa87894359f261fc304ac28b8672fe2634a0efff5a792d2ddb417f`
- 评审方式：只读源码与设计文档；未修改设计稿，未构建、未测试
- 评审定位：先读 §0.4。本设计是给既有 grokbuild leader 观察链路增加 Mac 端配置开关及两处 Owner 已批准的受控 Go 修正，不是新 backend adapter；本报告不要求补 API 客户端、事件泵或协议翻译，不提出 follower 可写方向，也不删除或绕过 file tailer。

## 1. 来源核对结果

评审开始与写报告前各执行一次来源门，结果一致：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅下列未跟踪设计/评审文档；与评审范围相符：
  docs/2026-08-28-grokbuild-leader-mode-design.md
  docs/2026-08-28-grokbuild-leader-mode-design-review.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r2.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r3.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r4.md
  docs/2026-08-29-grokbuild-leader-mode-design-review-r5.md
  docs/2026-08-29-grokbuild-leader-mode-design-review-r6.md
  docs/2026-08-29-grokbuild-leader-mode-design-review-r7.md
任务预期分支=main

配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
```

结论：两仓 HEAD 均与评审指令指定的 pin 完全一致，无来源漂移，可以继续评审。写入本报告后，Mac 仓会额外出现本文件这一项未跟踪文档，属于本次用户明确要求的评审产物。

补充核查：冻结依赖 `mattt/swift-toml` 2.0.0 的 tag commit、`Package.swift`（Swift tools 6.0、macOS 10.15、包内 `CTomlPlusPlus`、无外部 target 依赖）、README 所列 `TOMLDecoder` Codable API 与 MIT license 均与 §3.6 主张一致。此项已核实，无新增发现。

## 2. 分级发现清单

### B（阻断）

#### R8-B1：registry 的 unknown/删除不会自动穿透 Grok catalog 的 10 分钟富化快照，G7 与验收第 12 行当前不可成立

**核查结论：不符，阻断开发交接。**

设计稿 §3.5.2-5/6（约 713–721 行）断言 release 后“catalog 下一次 list 输出 unknown”，G7（约 1000 行）要求 catalog list 立即走无记录/unknown，§7.3 第 12 行（约 1027 行）要求等待取消后侧栏不再显示运行中；同时 §3.5.2 范围限定明确“不改 catalog/enrich 代码”（约 726–732 行）。但实际链路是：

- `go-bridge/handlers_grok_catalog.go:75-81` 在 builder 内先执行 `enrichSessionStatesForList`，把当时的 registry 状态写进 wire map；
- `go-bridge/catalog_wire_snapshot.go:44-50,232-305` 缓存的正是已经富化的 wire maps，后续 page-0 会 `FetchOrReuse`，不是每次重新 overlay registry；
- `go-bridge/catalog_cursor_v2.go:24-31` 的快照 TTL 是 **10 分钟**；
- `go-bridge/catalog_native_membership.go:78-99` 还明确把 running/pin 这类 presentation overlay 排除在 semantic fingerprint 外；
- `go-bridge/handlers.go:264-273` 的 registry `onStateChange` 只失效 Claude running-map，并不会失效 Grok catalog wire cache。

因此，只改 registry 会出现双向陈旧：取消前若快照已缓存 `running`，release 后列表最多继续显示约 10 分钟 `running`；反过来，缓存了 `unknown` 后新 relay 重新 claim running，也可能继续显示无徽标。冷拉 history 与 registry/cache 是不同真值层，不能消除此窗口。

**必须闭合：**在正文中明确选择并设计一种可执行方案，不能留给开发 agent 猜。最小范围方案可以仍只改 `handlers_relay.go`：在 Grok claim/release/terminal 状态变化成功后调用现成的 backend-wide catalog fence，使下一次 page-0 重建；但必须披露它会让存量 Grok cursor `cursor_stale`，并补“预热 running 快照 → release → page-0 unknown”“预热 unknown → reclaim → page-0 running”以及旧 cursor stale 的测试。若不接受 fence 的分页代价，则应把 runtime overlay 移出富化快照、在出站时重新覆盖，这会如实扩展 catalog 代码范围。二者必须选一。单纯放宽为“10 分钟内最终一致”与当前第 12 行产品承诺冲突，不建议。

#### R8-B2：当前 registry 不是文档声称的 idle/running 二态；六处 `!isIdle`→`isRunning` 并非二态等价，会静默改变既有 `closing` 语义

**核查结论：不符，阻断开发交接。**

设计稿 §3.5.2-1（约 673–678 行）称 registry “现只有 idle/running 两态”，第 4 条（约 703–712 行）据此声称六处替换在既有状态下“完全等价”。实际 `go-bridge/types.go:226-230` 已声明三种状态：`idle`、`running`、`closing`。当前 `isIdle`（`:373-377`）只把 idle/不存在视为 idle，所以 `closing` 在六个 `!isIdle` 消费点会被视为 active；改成精确 `isRunning` 后，`closing` 会变为 inactive。

当前 grep 未发现 production 写入 `sessionStateClosing` 的位置，但“目前没有写点”不能把已声明状态从语义域中删除，更不能支撑“等价替换”断言。开发 agent 按文档直接实现会造成未声明的通用 registry 行为变更，且六处跨 Codex/Claude/通用 relay，并非 Grok 私有路径。

**必须闭合：**优先新增一个语义明确的谓词，例如 `isKnownActive = state == running || state == closing`，只让 unknown/不存在退出 running-only 自动收口分支；或另案证明并删除 dead `closing` 状态。G8 必须增加 closing 回归断言，证明六处在 idle/running/closing 既有域上行为不变、只有 unknown 改变。正文、§10 和 §19 的“两态/等价”表述同步修正。

### M（必改）

#### R8-M1：两处 relayEvents 终态事实写错，且 cleanupIdleSessions 行号漂移

**核查结论：不符。**

- §3.5.2 第 4 条约 710 行把 `handlers_relay.go:2728` 描述为 channel-close 合成 aborted；§10 约 1157–1159 行又称 `:2728/:2864` 合成 `turn_aborted/turn_completed`。实际 `handlers_relay.go:2717-2737` 的 channel close 合成的是 `turn_completed(reason=events_channel_closed)`；`:2859-2872` 的 idle timer 也合成 `turn_completed`。两处都不是 aborted。
- 文档多处引用 `handlers.go:856-877` 作为 `cleanupIdleSessions` 全段，实际函数为 `go-bridge/handlers.go:857-883`。

这不改变“unknown 不应进入自动终态”的结论，但会让开发 agent 写错 G8 的事件断言，必须修正。

#### R8-M2：改动范围与功能映射内部不一致

**核查结论：不符。**

- §3.2 表 D-G2 行（约 440 行）仍只列 `go-bridge/handlers_relay.go`，遗漏已授权的 `types.go`；
- §4 总原则约 776 行称“D-G1/D-G2 均不触碰该层”，但 D-G2 明确修改 `grokLeaderSessionRelayLoop`、其 defer 与通用 active 谓词；
- §4 又称变化只集中在 §4.3/§4.9，然而 D-G2 的 registry release 与 R8-B1 的 cache 决策直接改变 §4.1 侧栏运行徽标；§4.1 仍写“结论不变”；
- §15 签署效力约 1298–1299 行仍写 `G5–G7`，v8 已扩成 G5–G8。

应统一 §0.3、§3.2、§4.1/§4.3/§4.9、§7、§8、§11、§15 的最终 diff 与行为面，避免开发 agent 按不同章节得到不同范围。

#### R8-M3：G5 要求假时钟精确证明 ticker 边界，但正文没有给出可注入的时间/采样接缝

**核查结论：设计缺口。**

G5（约 998 行）要求 59.9s、首负样本、转正清零和 `[60s,70s)` 的确定性假时钟断言；正文只说事件循环使用 10s ticker（§3.5.2-2/3），没有说明如何避免真实等待一分钟或 flaky scheduler 测试。开发 agent 需要猜是注入 clock/ticker factory，还是把采样判定抽成纯状态机。

建议正文冻结最小 seam：把 `firstNegativeAt` 判定抽成接收 `(now, hasSubscriber)` 的纯 helper，ticker 只负责生产样本；单测直接驱动 helper，另有一个短测试证明 cancel/relay cleanup 接线。这样不需要引入通用 fake-clock 基础设施，也不会跑 60 秒真实时间。

#### R8-M4：canonical locator 缺少“节边界”关键用例

**核查结论：用例不足。**

§3.3 要求在 `[cli]` 节“末尾”追加，但 T27–T30 主要覆盖 multiline 诱饵；没有显式冻结 `[cli]` 后紧跟另一个顶层表、`[cli.subtable]`、`[[array_of_tables]]` 时的节结束位置。若 locator 把子表后的行仍当作 `[cli]`，可能把 `use_leader` 追加到错误语义路径，最后才靠写后校验失败回滚。

至少增加：`[cli]`→`[other]`、`[cli]`→`[cli.child]`、`[cli]`→`[[other.items]]` 三类边界；断言追加发生在下一节头之前，且其他表完全不动。再覆盖无尾随换行文件，确保追加时只补必要换行且写后仍是合法 TOML。

#### R8-M5：symlink“身份钉扎”记录了 inode/device，却未要求 rename 前复核目标 inode/device

**核查结论：设计表述不闭合。**

§3.3-4 约 486–488 行要求记录 canonical path、inode/device、mode，但 rename 前只明确复核“链接链仍解析到同一 canonical 路径”；§3.3-7 的逐字节比较也不能识别“同路径、同内容、不同 inode”的目标替换。既然文档把 inode/device 称为身份钉扎的一部分，就应在 rename 前同时复核最终目标 inode/device 与初始值；目标不存在/已替换均冲突失败。T23 应覆盖“链接文本不变但目标 inode 被替换”，而不仅是 link swap。

#### R8-M6：备份文件名只规定 UTC 时间戳，未冻结碰撞与禁止覆盖语义

**核查结论：设计缺口。**

§3.3-10 以“UTC 时间戳”命名备份，但快速 ON/OFF、低分辨率格式或多窗口竞态可能生成同名路径。若普通写入覆盖旧备份，会破坏“写前字节级备份”和轮转硬不变量。

应规定高分辨率时间戳 + 随机/UUID 后缀，并以 exclusive create（已存在即重试新名，绝不 truncate）创建；T10/T26 增加同一时刻两次写入的碰撞测试。

#### R8-M7：合法 TOML 中 `cli.use_leader` 类型错误没有裁决与测试

**核查结论：状态机缺口。**

交叉矩阵只分“语义值有/无”，T19/T30 只覆盖非法 TOML。`use_leader = "true"`、整数、数组等是合法 TOML，但不是合法 Bool；`TOMLDecoder` 解码 Bool 会产生 type mismatch。正文应明确它归 F1（配置不可安全读取/类型非法）还是独立失败态，并增加测试，禁止误判 absent 后追加第二键。

#### R8-M8：Phase 0 开工门的最终口径仍有冲突

**核查结论：内部不一致。**

§0.2 约 98–100 行要求 v8 的 R7-B1 定向复核通过后才可执行 Phase 0；§15 约 1298 行仍写“四项均已决定，Phase 0 可开工”。本轮发现 R8-B1/B2 后，两句话都不再是当前事实。修订时应把唯一权威门写在 §0.2：R8 必改闭合后即可执行；§15 只说明 Owner 产品裁决已完成，不单独授权开工。

### S（建议）

#### R8-S1：把历史采纳记录明确降级为非规范性附录

§12–§19 保留了大量已被后续轮废弃的方案，虽多数加了 vN 注，但开发 agent 容易从历史表格拾取旧实现。建议在 §12 前加一句：“实施只以 §0–§11 当前正文为规范；§12–§19 仅供审计追溯，不得作为实现要求。”完成 R8 后把“下一轮只需……”类状态句改成最终交接状态。

#### R8-S2：D-G2 的成功日志应冻结结构化字段，而不只冻结自然语言原因

G5/G7 与验收第 12 行依赖 INFO 日志判定 self-cancel。建议冻结至少 `backendID`、`sessionID`、`reason=no_subscribers`、`firstNegativeAt/elapsed`、`claimReleased`、`registryOutcome=deleted|unknown|noop`，但不得记录 cwd/config 内容。这样开发代理和 owner 不必靠模糊文本判断 release/CAS 是否真正发生。

## 3. 五个维度结论

### 事实核查

官方配置分层、leader 生命周期、多客户端/interaction、chat 互斥、日志 effective 证据，以及 macbridge 的路径选择、只读纪律、三行日志链等既有基线未发现新的阻断性漂移；冻结 TOML 依赖的上游事实也核实通过。v8 新增的 registry 论证仍有两处关键事实错误：忽略已声明的 `closing` 状态，且把 catalog 当成每次 list 都重新读取 registry；另有 relayEvents 终态名称和 cleanup 行号错误。结论：R7-B1 的 generation CAS 主体方向成立，但其“第三态消费者等价”和“列表立即可见”两条证明尚未成立。

### 设计闭合性

generation token、gen 失配 no-op、synthetic 删除、real-session 保句柄、主动取消与真断开分流，单看 registry 临界区是闭合的；首负样本数学也正确。系统级闭合仍缺 catalog cache invalidation/overlay 决策、closing 兼容、可测试时钟 seam，以及少量 TOML/备份边界。补完 R8-B1/B2 与 M3–M7 后，开发 agent 才不需要自行做产品或架构取舍。

### “受控 Go 改动”独立验证

D-G1 分界与 D-G2 ctx 互锁仍成立；不需要新增协议客户端、codec 或 follower 写方向。D-G2 可继续维持在 `handlers_relay.go + types.go`，但若选择现成 catalog fence，必须把 cursor stale 代价和缓存命中测试写清；若选择出站 overlay，则如实扩展 catalog 文件范围。无论哪种选择，都不允许以 10 分钟陈旧快照冒充 registry 已收口。

### 纪律一致性与样本背书

§0.4 正确声明 wire 样本冻结、新旧 backend 并存/退役等纪律不适用；source-first、宿主拓扑先证、fail-visible、可逆、保留 tailer、防偏航均已覆盖。按 audit-plan 的内容形状盘点：真实本机 config 样本、官方 canonical fixture、synthetic CRLF/等价形态/multiline/symlink 的来源等级已显式区分；synthetic 只证明保守编辑器行为，没有冒充外部现场证据。新增需要的样本只是 locator 节边界和类型错误测试，不要求新增 wire/API 样本。

### 内部一致性与可交接性

主流程编号和大部分交叉引用可追踪，但改动范围表、§4 功能面、§15 的 G5–G7、Phase 0 开工门仍有旧口径。当前文档**不应直接交开发 agent**：其最可能的错误实现是按 `isRunning` 改坏 closing 语义，并通过 registry 单测却在生产 catalog 缓存中继续显示 running。完成本报告两项 B 与八项 M 后，不需要再做一轮全量架构评审；只需一次机械闭合复核：源码事实、缓存命中测试、closing 回归、章节范围与测试编号逐项对照即可。

## 4. 总结论

**修改后通过。当前不可开 Phase 0，也不可直接交开发 agent。**

必改清单：

1. 裁决并写实 catalog runtimeState 的缓存穿透方案，补预热缓存双向测试与 cursor 代价；
2. 保留 closing 既有语义，改用 unknown-safe active 谓词并补 closing 回归；
3. 修正 relayEvents 终态事实和源码行号；
4. 统一 D-G2 diff/功能面/G5–G8/Phase 0 门；
5. 冻结假时钟测试 seam；
6. 补 locator 节边界、symlink inode 复核、备份名 exclusive create、非法 Bool 类型用例。

上述项全部是有限、可机械验证的闭合项。修订版不需要再接受开放式“继续找新架构问题”的无限评审；下一次只核对这份清单及相应源码行即可决定 **APPROVE**。
