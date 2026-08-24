# codex-web 目录取证 v5 §7.3 证据判决书（evidence verdict）

日期：2026-08-24
关联蓝图：docs/2026-08-24-codex-web-topology-split-and-sync-monitor-analysis.md §7.3（交付门）
观察目标：发现拓扑中的权威 catalog（authoritative corpus）变化检测与 generation 语义
产物：受限只读 instrumentation（catalog_forensics.v1）+ 两轮真实变化取证 + 证据包

## 0. 结论摘要（verdict）

- **交付门总体判定：PASS**。实验 B 三门（idle 基线、新会话出现、合法变化可解释）全部以真实样本闭环，附超预期的 storm 归因证据。
- **实验 A 判定：blocked_manual_owner_close（诚实声明）**。private/mixed 目录样本需要 owner 人工干预（拖动目录、关闭/创建会话窗口）才能产生，本轮无 owner 参与，未伪造任何"Ok"；操作指引见 §6。
- **诚实披露**：实验 B 首轮（run1）在 rename/archive/delete 之前因 1 MiB 字节预算耗尽停止（limit_reached），三变更样本全部丢失；已以 run2 补齐，证据链完整（§4.2）。提取器发现一处 runId 校验宽度 bug（64-hex vs 实际 32-hex），已修复并重跑提取（§5）。
- **关键事实修正**：generation 的语义是"消耗了有内容变化的刷新"，**不是**目录成员变化的计数；同一行 updatedAt 单字段变化即推进一代（run1 storm 9 次推进全部为此）。此前会话观察到的"codex 与 codex-web 双后端 catalog 不同（441 vs 438 raw）"在两轮连续 trace 中**未获复现支持**，降级为"未证实"（§4.4 F3）。

## 1. 执行路径与工件

| 项 | run1 | run2 |
|---|---|---|
| runId | 761860654c5e234a782d724ed8d6089e | 087e843eaabd372baab6164b36319940 |
| 窗口（墙钟） | 2026-08-24 16:01:24.158 → 16:17:12.226 | 16:25:31.103 → 证据快照 16:31（进程内继续至预算耗尽） |
| 时长 | 949.8 s | ≥340 s（快照） |
| 事件总数 | 1808 | 397 |
| authoritative 样本 | 34 | 10 |
| head 样本 | 311 | 0（见 §4.4 F7） |
| 停止原因 | 1 MiB 预算（limit_reached，samples 345<512） | 快照截取（预算未耗尽） |
| observerError | none×1807 + limit_reached×1 | none×全量 |

运行形态：`-drivers claude,codex-web,grokbuild,dsh-web,opencode-web`（**无独立 codex driver**），`GO_BRIDGE_CODEX_CATALOG_TRACE=1`、`_MAX_SAMPLES=512`。证据包（git-ignored，禁止入库）：
`scripts/codex-web-phase0/dumps/catalog-forensics/<runId>/catalog-forensics.v1.jsonl + manifest.json`
（提取入口 `scripts/codex-web-phase0/extract_catalog_forensics.sh`，全量 schema 校验 + 递归脱敏自检通过。）

### 同源证据（§3.2）

- 权威路径：`discoveryFingerprint`（session_discovery.go:329）→ `codexVisibleMembershipCounts` **单次** `thread/list`（catalog_native_membership.go:48-52）→ 同一 `wire` 先 `capture(...)`（:360）再 `listSemanticFingerprint(wire)`（:362）。
- head 路径：`codexDiscoveryHintFingerprint`（session_discovery.go:192-202）同一 `wire` 先 capture 再 fingerprint。
- observer 代码零 fetch：`catalog_forensics.go` 无任何 thread/list/client 调用，全部输入由调用方注入。
- 数量对照：run2 无客户端在线，`codexweb: thread/list bounded fetch` 10 次 == authoritative 样本 10（严格 1:1）。run1 客户端在线，fetch 47 次 - 样本 34 次 = 13 次为 presentation（list_sessions）读取路径（该路径无 capture 且不产样本），与观察者无涉。

## 2. 实验 B 门 0：idle 基线（无变化不产噪声）

- run1 权威样本在风暴平息后（@183248–423134，约 240 s 窗口）**连续 5 个 periodic_tick**，gen 恒为 4、fp 恒为 `3d3e389db7`、diff 为 0——变化检测在真实空闲窗口零误报。
- run1 head 311 次探测贯穿全程：head 噪声不转化为权威样本（无客户端的 run2 中 head 探测整体休眠，见 F7）。
- 结论：idle 门达成；目录空闲时观察者无输出、无生成推进。

## 3. 实验 B 场景 1：新会话出现（两次独立复现）

实验协议：daemon 直连（unix socket ~/.codex/app-server-control/）`thread/start`（cwd=活跃 root `/Users/jacklee/Projects/cordcode-ios`，通过 roots 白名单可见）→ `turn/start`。

| 观察 | run1 @920667 | run2 @63597 |
|---|---|---|
| 触发 | head_changed | periodic_tick |
| rawCount | 441 → 444 | 441 → 444 |
| rows | 192 → **193** | 192 → **193** |
| generation | 9 → **10** | 0 → **1** |
| fingerprint | af0b801be2… → ef4e493f27… | af0b801be2… → 8f89cb1672… |
| row_diff | **added(1)×1 @ idx0** + index(4)×192 | **added(1)×1 @ idx0** + index(4)×192 |

判定：
1. 新线程在 head 信号（run1）/权威周期刷新（run2）下进入权威 corpus，**行数与 generation 均恰好 +1**；
2. diff 完美归因：新增行落在 idx 0（recency 序最顶），其余 192 行全部只有 index 位（整体后移），**无任何 updatedAt/title 等字段噪声**；
3. 两次独立运行（不同 runId、不同触发路径）复现同一形态，结论稳定；
4. run2 的触发者为 periodic_tick——**无客户端、无 3s head 探测时，新会话变化仍被权威周期刷新捕获**（变化检测不依赖 head probe）。

## 4. 实验 B 场景 2/3：合法变化因果裁决

### 4.1 场景 2：rename / archive / delete（run2，同一线程生命周期）

| 变更 | offset | 触发 | gen | rows | row_diff | fingerprint |
|---|---|---|---|---|---|---|
| rename（thread/name/set） | 201453 | catalog_signal_coalesced | 1→2 | 193 | **title(64)×1 @ idx0**，updatedAtDelta=None | 8f89cb1672… → 47e1c07e85… |
| archive（thread/archive） | 241936 | catalog_signal_coalesced | 2→3 | 192 | **removed(2)×1** + index(4)×192 | 47e1c07e85… → **af0b801be2…（= add 前值）** |
| delete（thread/delete） | 282292 | catalog_signal_coalesced | 3→3 | 192 | **0 diff（空信号）** | 不变 |

裁决：
1. **rename**：仅被改名线程本身 1 行 `title=64`，行序、updatedAt、其余 191 行全部不变，gen 恰好 +1——合法、最小、可解释；且证明 thread/name/set **不修改 updatedAt**。
2. **archive**：归档线程从默认目录移除（removed×1），后续 192 行 index 位平移，gen 恰好 +1；观察到的"raw 444→441、rows 193→192"与"一个可见成员离场"完全一致。
3. **delete**：线程序已在 archive 时离开权威列表，delete 触发了 coalesced 信号但刷新结果与上一份完全相同——**0 diff、gen 不推进**。这证明空变化信号不会伪造 generation 推进。
4. **指纹恒等性**：archive 后的 fp 与 add 前的 fp **逐字节一致**（`af0b801be2…`）——fp 是该列表可见行（id/updatedAt/directory/projectId/title）的纯函数，线程生命周期结束后目录无任何残留状态。

### 4.2 场景 3：风暴归因（run1，gen 0→9 的 9 次推进）

| 样本 offset | 触发 | gen 变化 | row_diff 内容 |
|---|---|---|---|
| 110491 | catalog_signal_coalesced | 0→1 | idx0 updatedAt(8)，delta +2212000 ms |
| 111582 | head_changed | 1→2 | idx0 updatedAt(8)，delta +3000 ms |
| 123116 | periodic_tick | 2→3 | idx0 updatedAt(8)，delta +8000 ms |
| 150982 | catalog_signal_coalesced | 3→4 | idx0 updatedAt(8)，delta +27000 ms |
| 482996 | periodic_tick | 4→5 | idx0 updatedAt(8)，delta +333000 ms |
| 496782 | catalog_signal_coalesced | 5→6 | idx0 updatedAt(8)，delta +16000 ms |
| 537017 | catalog_signal_coalesced | 6→7 | idx0 updatedAt(8)，delta +40000 ms |
| 542735 | periodic_tick | 7→8 | idx0 updatedAt(8)，delta +8000 ms |
| 577345 | catalog_signal_coalesced | 8→9 | idx0 updatedAt(8)，delta +25000 ms |

并行的零变化观察：@322364（catalog_signal，gen 4→4，0 diff）、@538085（head_changed，gen 7→7，0 diff）、@659668/869139/870254（head_changed/catalog_signal，rawCount 441↔444 摆动，gen/fp 均不变）。

裁决：**不存在"风暴"**——9.5 分钟窗口内目录成员从未变化（rows 恒 192），波动的是**同一最活跃会话（idx 0）的 updatedAt 被外部重复触碰**（daemon 侧其它客户端或会话刷新），每次触碰各触发一次基于信号/轮询的权威刷新。观察者如实归因：单行、单字段、正 delta；且 raw-only 噪声（非项目 raw 行在 canonical 列表增删）与空信号均不推进 gen、不改变 fp。**generation=刷新过的变化次数，而非成员变更数**。

### 4.3 场景 2 门判词

三类合法变化（新增/改名/归档/删除）在权威 corpus 内全部可解释：diff 行数、mask 位、generation 推进、fp 变化四者一一对应，无放大、无伪造、无跨后端污染（prev 按 backend+corpus 键控，见 §5 修复记录）。

## 5. 工具链诚实披露与修复

1. **run1 预算耗尽**：maxSamples=512 未达（345），耗尽的是 1 MiB 字节预算（head 的 1260 条 row_diff 占主导）。run1 因此在 16:17:12 停止，晚于 16:16:43 的 add、早于 16:19:33 的 rename——三变更样本确实丢失，已由 run2 完整补齐。验证方式：daemon 脚本墙钟（16:16:36–16:21:03）与 run_summary 时间戳对照。
2. **提取器 runId 校验 bug**：`HEX64.match(runId)` 与身份白名单循环与实现（randomHex(16)=32-hex）不符，真实日志提取首次即报 `bad run/sample identity`。已修复为 runId=HEX32 / sampleId·correlationId=HEX24 / rowKeyHmac=HEX64，两轮提取重跑通过（1808 + 397 事件，脱敏 clean）。
3. **跨后端 diff 污染修复**（本轮之前）：prev 由 corpus 键控改为 `backend+"\x00"+corpus` 键控，首样本免 row_diff 输出（"相对上一份为空"）；row_diff 事件补齐 fingerprint/catalogGenerationBefore/After。均已有定向单测（TestForensicsSeedHasNoDiff、TestForensicsTwoBackendsNoCrossDiff 等）。

## 6. 实验 A：blocked_manual_owner_close（操作指引）

private/mixed 目录拓扑对照需要 owner 在 Mac 侧人工建立/操作窗口（拖动 private 目录进会话、关闭特定窗口），该干预未发生；按规定不作虚假声明。owner 补做步骤：
1. 保持 trace 开启的 runtime 运行（或下次正式实验）；
2. 在 Codex 桌面端新建/打开一个指向 private 目录（如 /tmp、非 roots 白名单路径）的会话；
3. 观察者预期：head_changed/catalog_signal → 权威刷新 → 出现行级 removed/added 与 kept/raw 差集（filter 路径日志 `dropped_count/kept_count/raw_count/codex_roots_enforced` 已就位）；
4. 复现后按 §1 提取证据包并回填本节判定。

## 7. 附：交付门 checklist 对应

| §7.3 门要求 | 证据 |
|---|---|
| 受限只读、默认关 | env 显式开启；observer 无 fetch；不写 kernel/writer；panic 边界→dropped 语义 |
| 同源 | §1 同源证据（代码级共享 wire + fetch==样本 1:1 对照） |
| schema/上限/HMAC/错误 | 提取器全量校验通过；1 MiB 先到停止（run1 run_summary=limit_reached）；HMAC 64-hex 内存键；1807×none + 1×limit_reached，dropped=0 |
| 实验 B 三门 | §2/§3/§4 |
| 实验 A | §6 blocked，附指引 |
| 证据包脱敏 | 提取器递归扫描 clean；dumps/ 目录 git-ignored，未 add、未入库 |

## 8. 未决观察（留给后续迭代）

- F1：turn 运行期间（run2 @63597→183401，约 2 分钟 token 输出）权威 fp 完全不变——**thread/list 的 updatedAt 不随 turn 进度推进**（与 run1 storm 中同一会话 updatedAt 被反复触碰的行为并存，二者均真实）。
- F2：rawCount 441↔444 的周期性摆动（run1 多次）来自非项目 raw 行的增删（filter 日志 `(empty-or-root)=N` 对应），不影响 kept/fp/gen——**raw 计数不是目录真值**，审计时以 kept 为准。
- F3：本 run 的 fetch 全部为 `codexweb:` 前缀（drivers 无 codex），上会话记录的 441/438 双后端差异在此窗口未复现。恢复现场后（应用同时注册 codex+codex-web 两 driver，16:38 观测）：`codex catalog: thread/list count=438`、codex-web `count=441`，kept 均为 192——**双后端 raw 数差异真实存在（差 3 行，filter 后 kept 相同）**；但与早前会话记录的归属方向相反（彼时记 codex=441/codex-web=438），两次观测间目录有时变且归属漂移，确切映射留待双后端并存拓扑下双写对照实验。两后端的 discovery fingerprint 各自独立（prev 按 backend+corpus 键控，避免跨后端伪 diff），不受此漂移影响。
- F4：rename 不改 updatedAt、archive 后用 delete 只会产生空信号——若产品想以 generation 作为"变化通知"信号源，需注意 gen 也会被 idx0 updatedAt 空转推进（F 值语义告警）。
