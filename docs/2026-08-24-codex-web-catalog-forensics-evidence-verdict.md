# codex-web 目录取证 v5 §7.3 证据判决书（二版：证据补洞修订）

日期：2026-08-24（二版）
关联蓝图：docs/2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md §7.3（交付门）
一版：63ddf57。二版改动（依独立复核）：
1. run1 按 v5 §6.2 判**无效**（导出 1,116,728 B > 1 MiB 且含 limit_reached），不再作为任何断言的证据；
2. 新增 run3 —— 完整有效的门达标 run（idle ≥30 head + ≥5 periodic_tick、active turn 跨 ≥2 interval、四类合法变化、无 drop/error/limit）；
3. 实验 A 定义更正：private/mixed 指 **Desktop transport 拓扑**（§6），非目录过滤；
4. storm 判词修正：**无成员变化风暴，但确认存在 idx0 updatedAt 引发的 fingerprint/generation churn**；旧 3 秒级风暴未复现，run1 的 9 次/9.5 分钟频率不作有效引用。

## 0. 结论摘要（verdict）

- **交付门判定：PASS（依据 run2 + run3，均为 §6.2 意义下的有效 run）**。
- run2（087e…，397 事件 / 254,098 B，无 run_summary/limit/drop/error）产出四类合法变化因果链；
- run3（b83259…，1051 事件 / 656,762 B，无 limit/drop/error）单独完成 §6.2 数量门：**118 head + 6 periodic_tick authoritative**，active turn 跨越 4 个 authoritative interval，并在单轮内复现 add→updatedAt→rename→archive→delete 全链路；
- **实验 A：blocked_manual_owner_close**（诚实声明）——private/mixed 的 Desktop transport 拓扑样本需要隔离 Desktop + 进程级 force-stdio 的人工实验（v5 §2.1/§4.1 判据），本轮未做，未伪造任何 topology 判定，禁止以目录过滤代替；操作指引见 §6；
- **storm 修正判词**：本轮全部有效观测（run2 + run3）中，目录成员从未发生无因果的变化；存在的 churn 是**单行 updatedAt 变化**——run3 有效观测 1 次（新线程自身 +7 秒，gen 1→2），与"外部会话被触碰"的旧 3 秒级风暴**未复现**；旧风暴频率（run1，无效）不作引用。

## 1. 运行与工件

| 项 | run1（INVALID） | run2（VALID） | run3（VALID） |
|---|---|---|---|
| runId | 761860654c5e234a782d724ed8d6089e | 087e843eaabd372baab6164b36319940 | b83259f4fcf6f629b4dff0a6e542caa6 |
| 窗口（墙钟） | 16:01:24→16:17:12 | 16:25:31→16:31 快照 | 17:04:00→17:11:11（实验段） |
| 时长 | 949.8 s | ≥340 s（快照） | ≈431 s |
| 事件总数 | 1808 | 397 | 1051 |
| 导出字节 | **1,116,728（超限）** | 254,098 | 656,762 |
| head 样本 | 311 | **0**（无在线客户端） | **118** |
| authoritative 样本 | 34 | 10 | 13（6 periodic_tick） |
| observerError | none×1807 + **limit_reached×1** | 全部 none（无 run_summary） | **全部 none（无 run_summary）** |
| dropped | 0（run_summary 未含 drop 计数） | 无 | 无 |
| v5 §6.2 判定 | 无效（越过 1 MiB） | 有效（观察受限：无 head） | 有效（门全项） |

运行形态：run1/run3 — `-drivers claude,codex-web,grokbuild,dsh-web,opencode-web`；run3 为独立实例（端口 8799、data-dir /tmp/fx/fxdata、relay-off），codex-web 端点来自 `$CODEX_HOME/app-server-control/app-server-control.sock`（共享同一 daemon）；run2 — 同 run1 但无客户端在线。run3 全程有一位 capability 正确客户端在线（`dev_forensics_34b29615`，`session_sync_v2` + `catalog_cursor_epoch_v2`，配对+hello 完成），head 3 秒探测因此激活（run2 无客户端→head 为 0，解释了 run2 的观察边界）。trace 经 env 显式开启（默认关），maxSamples=512。

证据包（git-ignored，禁止入库）：`scripts/codex-web-phase0/dumps/catalog-forensics/<runId>/catalog-forensics.v1.jsonl + manifest.json`（提取入口 extract_catalog_forensics.sh，全量 schema 校验 + 递归脱敏自检通过）。

### 同源证据（§3.2）

- 权威路径：`discoveryFingerprint` → `codexVisibleMembershipCounts` 单次 `thread/list` → 同一 `wire` 先 `capture` 再 `listSemanticFingerprint`；
- head 路径：`codexDiscoveryHintFingerprint` 同一 `wire` 先 capture 再 fingerprint；
- observer 代码零 fetch（catalog_forensics.go 无任何 thread/list 调用）；
- run3 运行级对照：`codexweb: thread/list bounded fetch` **13 次 == 13 个 authoritative 样本**（严格 1:1，无额外/重复拉取）；run3 无 presentation 消费方。

## 2. 实验 B 门 0：idle 基线（run3）

- 118 个 head 样本贯穿 6.1 分钟无一次误触发权威刷新中的成员变化；
- 6 个 periodic_tick authoritative 样本；其中 **4 个为纯空闲样本**（@122903、@182994、@242983、@302722、@363231 中的后三个 gen/fp 全程稳定、diff 为空）；
- 唯一一次"空闲期 gen 推进"发生在 @62927 —— 它**不是噪声**，是新线程自身 updatedAt +7 秒（见 §4.2 因果链）；
- 结论：idle 门下观察者在无业务变化时零输出、零 gen 推进。

## 3. 实验 B 场景 1：新会话出现（两次有效复现）

实验协议：daemon 直连 `thread/start`（cwd=活跃 root `/Users/jacklee/Projects/cordcode-ios`）→ `turn/start`。

| 观察 | run2 @63597 | run3 @44653 |
|---|---|---|
| 触发 | periodic_tick | **head_changed** |
| rawCount | 441→444 | 441→444 |
| rows | 192→193 | 192→193 |
| generation | 0→1 | 0→1 |
| row_diff | added(1)×1@idx0 + index(4)×192 | added(1)×1@idx0 + index(4)×192 |
| 新增行 hmac | — | `f8c65579c0…`（后续 rename/updatedAt 同 hmac，同 run 内可关联） |

判定：新会话进入权威 corpus，行数与 generation 均恰好 +1；diff 零字段噪声；两种触发路径（head_changed 与周期轮询）各复现一次，行为一致。（run1 也观察过同型 add @920667，但 run1 无效，不作证据。）

## 4. 实验 B 场景 2/3：合法变化因果裁决（run3 单轮全链 + run2 佐证）

### 4.1 run3 单链（同一线程 01a03304-3d37…，同一 run 内 hmac 全程 = `f8c65579c0…`）

| 变更 | offset | 触发 | gen | rows | row_diff | fingerprint 前→后 |
|---|---|---|---|---|---|---|
| **add** | 44653 | head_changed | 0→1 | 192→193 | added(1)×1@idx0 + index(4)×192 | af0b801b…→d3dc6a6c… |
| **updatedAt churn** | 62927 | periodic_tick | 1→2 | 193 | **8**×1@idx0，delta +7000 ms | d3dc6a6c…→dfc48de2… |
| （空闲） | 122903/182994 | periodic_tick | 2→2 | 193 | 0 diff | 不变 |
| **rename** | 215533 | catalog_signal_coalesced | 2→3 | 193 | **64**×1@idx0（仅 title），updatedAtDelta=None | dfc48de2…→881b2b96… |
| （空闲） | 242983 | periodic_tick | 3→3 | 193 | 0 diff | 不变 |
| **archive** | 256332 | catalog_signal_coalesced | 3→4 | 193→192 | removed(2)×1 + index(4)×192 | 881b2b96…→（回测基线，略） |
| （head 空事件） | 257837 | head_changed | 4→4 | 192 | 0 diff | 不变 |
| **delete** | 296651 | catalog_signal_coalesced | 4→4 | 192 | **0 diff（空信号）** | 不变 |
| （空闲） | 302722/363231 | periodic_tick | 4→4 | 192 | 0 diff | 不变 |

run2 佐证（同型）：rename @201453（title(64)、gen 1→2）、archive @241936（removed+index 平移、gen 2→3、fp 回到 add 前值）、delete @282292（空信号、gen 3→3）。

裁决：
1. **add/rename/archive/delete 四类合法变化在权威 corpus 内全部可解释**：diff 行数=mask 语义=gen 推进=fp 变化一一对应；
2. **同 run 行身份可关联**：run3 中 add→updatedAt→rename 命中同一 hmac 行，证明 causal chain 是"同一行对象的生命周期"，无隐藏重排；
3. **rename 不修改 updatedAt**（title 单独变化，delta=None）；turn 运行输出 token 不驱动 catalog fp；
4. **archive 把线程移出默认列表**（removed + 全表 index 迁移）；**delete 在已归档后仅产生空信号**（gen/fp 不变）——空变化信号绝不伪造 generation 推进；
5. **head_changed 触发 ≠ gen 推进**：@257837 head_changed 零 diff、gen 4→4（head 侧变化但权威 kept 未变）。

### 4.2 风暴判词（修正版）

- **无成员变化风暴**：run2 + run3 全部有效样本中，任何一次 gen 推进都能定位到具体行与 mask，不存在"整表或多数行变化的无因果风暴"。
- **存在 updatedAt 引起的 fingerprint/generation churn**：run3 @62927 单行 idx0 updatedAt +7 s → gen 1→2、fp 变化——该行是实验线程自身（非外部系统），说明**新线程创建后 shortly updatedAt 会被触碰一次**，属合法、可解释的语义变化，但若产品将此 gen 推进当作"目录变化"告警，将产生一次假阳性。
- **旧 3 秒级风暴未复现**：本次在 run3 约 6 分钟窗口内仅 1 次单行 updatedAt 变化；run1（无效）曾观测 9 次/9.5 分钟的 idx0 updatedAt churn（外部客户端行为），因 run1 无效**其频率数字不作引用**，仅作为"此现象可能存在"的既往线索。结论：churn 现象存在但频率与成因因时相而异，现有证据不支持"持续高频戳动"的定论。

## 5. 工具链诚实披露与修复

1. **run1 无效（复核确认）**：导出 1,116,728 B 越过 1 MiB 且含 limit_reached。旧预算实现是"一次 commit 全部事件写完后再检查字节"，单个大 diff 样本可顶穿上限（run1 的 @920667 样本 193 条 diff 即此形态）。按 v5 §6.2 判无效，本版起所有由它单独支撑的断言（311 head、idle 5 连 tick、9 次 updatedAt 归因）一律作废，改由 run3 承载（run3 已重新给出 idle 与 updatedAt 单行归因样本）。
2. **预算修复（已提交）**：改为**逐事件原子检查**——任一事件写入前验证 `bytes + sz ≤ maxBytes - reserve`，越界事件本身不写入并立即停止；为 run_summary 预留字节空间（正常 4 KiB，极小预算退化为四分之一），证据导出总量（含终报）**恒 ≤ maxBytes**；samples 上限只限制新的 sample_summary，当前样本的 row_diff 序列不因此被截断；新增 TestForensicsByteCapAtomic（断言导出 ≤ maxBytes、唯一 run_summary、limit_reached）。run3 基于修复后二进制，导出 656,762 B（62.6%）。
3. **提取器 runId 校验 bug**（一版已修，本版沿用）：runId=32-hex（randomHex(16)），提取器原按 64-hex；已修（HEX32/HEX24/HEX64 按字段宽度），两轮提取重跑通过。
4. **跨后端 diff 污染修复**（早前）：prev 按 `backend+"\x00"+corpus` 键控；首样本免 row_diff；row_diff 携带 fingerprint/gen。单测：TestForensicsSeedHasNoDiff、TestForensicsTwoBackendsNoCrossDiff 等。

## 6. 实验 A：blocked_manual_owner_close（定义更正 + 操作指引）

v5 的 private/mixed 指 **Desktop transport 拓扑**（§4.1–§4.2），不是 workspace/private
目录过滤。按 Desktop 实例收集"shared daemon 还是 private stdio"的正证据，聚合为
`shared_only / private_only / dual`，再按 §4.2 真值表得 `all_shared / mixed /
split_present`。判据（v5 §4.1）：private 正证据 = Desktop 日志明确 `transport=stdio`；
递归父进程链证明该 Desktop 拥有 private `codex app-server` 且 stdio pipe/FD 形态
吻合；或当前 build 静态分支与该隔离实例的进程级环境共同证明 force-stdio 命中
（`CODEX_APP_SERVER_FORCE_CLI=1`，v5 §2.1 注明是隔离取证入口）。无 shared FD 不是
private 正证据；启动中、权限失败、PID 竞态、FD 读取失败一律 `unresolved`。

一版 §6 把 private/mixed 误写为"private 目录 / 非 roots 白名单路径"——与 v5 定义
不符，已按上文更正。目录过滤只决定 catalog 可见成员（实验 B 的 kept/raw 差异），
与 Desktop transport 拓扑无涉，**不得以目录过滤代替 transport 证据**。本轮未做
实验 A 活体样本（需 owner 隔离 Desktop + 进程级取证），诚实标
`blocked_manual_owner_close`，未伪造任何 topology 判定。

owner 补做步骤（v5 §2.1/§4.1）：
1. 建立隔离 Desktop 实例（进程级 force-stdio：`CODEX_APP_SERVER_FORCE_CLI=1`，或构造不满足 shared-daemon 分支的条件）；
2. 采集 private 正证据（transport=stdio 日志 / 父进程链 stdio FD 形态 / 静态分支与进程级环境三者之一）；shared FD 证据复用已有只读 FD 现场；
3. 聚合实例集合 → §4.2 真值表 → `all_shared/mixed/split_present` 判定；
4. 复现后按 §1 提取证据包并回填本节判定。

## 7. 交付门 checklist 对应

| §7.3 门要求 | 证据（二版） |
|---|---|
| 受限只读、默认关 | env 显式开启；observer 无 fetch；不写 kernel/writer；panic 边界→dropped 语义 |
| 同源 | §1 同源证据（代码级共享 wire + run3 fetch==样本 1:1） |
| schema/上限/HMAC/错误 | 提取器全量校验通过；原子预算（run3 656,762 B ≤ 1 MiB）；HMAC 64-hex 内存键；run2/run3 全部 none、无 limit/drop |
| 实验 B 三门 | §2（idle：118 head + 6 periodic_tick，含 3 个零 diff 空闲 tick）/§3（新会话双复现）/§4（单轮全链 + run2 佐证） |
| 实验 A | §6 blocked，附合格操作指引（Desktop transport 拓扑定义） |
| 证据包脱敏 | 提取器递归扫描 clean（3 个 run）；dumps/ git-ignored，未 add、未入库 |

## 8. 未决观察（留给后续迭代）

- F1：thread/list 的 updatedAt 不随 turn 进度推进（run3 turn 期间 122903–242983 三个 interval fp 稳定）；但新线程创建后**有一次 +7 s 的 updatedAt 触碰**（run3 @62927）——两者并存且均真实。
- F2：rawCount 441↔444 摆动来自非项目 raw 行的增删（filter 日志 `(empty-or-root)=N`），不影响 kept/fp/gen——raw 计数不是目录真值，审计以 kept 为准。
- F3：双后端差异真实存在（恢复后的应用同时注册 codex+codex-web：`codex catalog count=438` vs codex-web `441`，kept 均 192）；归属方向与早前记录相反（彼时 441↔438 相反），疑似目录时变 + 归档线程差 3 行的合成效应；确切映射待双后端并存拓扑下双写对照。两后端 discovery fingerprint 各自独立（prev 按 backend 键控），不受漂移影响。
- F4：gen 会被合法单行 updatedAt 变化推进（run3 @62927）——若产品以 gen 作"目录变化通知"，应预期这类低幅 churn（每次 =1 单行变化），或改用 semantic fingerprint 变化为门。
- F5：实验 A 的 transport 拓扑证据仍是空窗（blocked），产品 topology monitor 的设计不得把 workspace 过滤信息当作 transport 证据。
