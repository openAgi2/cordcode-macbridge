# 独立审计报告：MacBridge Catalog 周期性扫描、Codex Discovery 超时、Passive Error 风暴与 Projection 冷打开饥饿 — 完成情况审计

## 0. 审计上下文

- 审计对象 Plan: `docs/2026-09-12-macbridge-catalog-periodic-scan-and-discovery-timeout-plan.md`
- 审计对象完成报告: `docs/2026-09-12-macbridge-catalog-periodic-scan-and-discovery-timeout-plan完成情况.md`（verdict: `proved-complete`）
- 审计对象状态文件: `.exec-plan/state/plan-62a7641d0f97.json`（记录 `based_on_queue_hash: 03271392a4aa`）
- 审计模式: exec-plan `audit` — 复验可重跑项、独立核对现场证据、不实施修复、不降级有据项
- 审计执行: 独立审计会话（非执行方），2026-09-12 23:00–23:20 +08
- 来源清单（P0 来源门）:
  - 仓库路径=`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`
  - 分支=`plan/approval-layer`，提交=`915bc6b682421435748076aca69c68488e2955ba`（完整哈希）
  - 未提交状态=干净（`git status --porcelain` 为空）
  - 本审计仅涉及 Mac 仓只读分析 + 本地测试复跑 + 本机运行时取证；未触碰 iOS 仓

## 1. 总体裁定

**完成裁定成立，无需降级。** 33/33 required todo 全部 `done` 且证明在档；自动化命令全部独立复跑通过（1 项既有偶发失败经单包复跑排除，与本轮改动无关）；完成报告中的关键现场数据（passive 日志行数、日志字节增量、Claude fork 行数、projection 两次时延、runtime 版本、抑制计数器）由本审计从仍在运行的 runtime（PID 60871）与未轮转的日志中**逐项独立复核，全部精确吻合**。

但审计发现 **1 项 P2 元数据完整性缺陷**（记录的队列哈希无法用 canonical 算法复现）与 2 项 P3 记录口径问题，详见 §4。它们不推翻完成语义，但削弱"报告↔队列"绑定的可第三方验证性，应予记录。

## 2. 独立复验结果

### 2.1 自动化命令复跑（本审计重新执行，非采信旧摘要）

| 命令 | 复跑结果 | 备注 |
| --- | --- | --- |
| `go test ./... -count=1` | **通过**（1 项例外，见 F4） | go-bridge 76.834s、agent/codex-remote 3.487s、core 0.407s 及其余包全部 ok；`agent/grokbuild` 的 `TestSendUnknownModelIdSoftensToCurrentModel` 失败一次，单包 `-count=1` 复跑通过——与记忆在案的既有偶发失败（跨测试状态干扰）一致；本轮 6 个 commit 未触碰 `agent/grokbuild`，判定非本轮回归 |
| `go vet ./...` | 通过（exit 0） | |
| `go build ./...` | 通过（exit 0） | |
| `GOOS=linux GOARCH=arm64 go build ./go-bridge` | 通过（exit 0） | |
| `git diff --check` | 通过（exit 0） | 工作区 clean |

### 2.2 现场证据独立复核（runtime 仍在运行）

审计时 `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`（PID 60871，22:50 启动）仍在运行，当前日志 `~/Library/Application Support/CordCode Link/logs/go-bridge.log` 未轮转、覆盖完整最终窗口。逐项复核：

| 完成报告声明 | 独立复核值 | 判定 |
| --- | --- | --- |
| 已安装 runtime `0.1.0 (commit 79315c218624)` | `-version` 输出逐字一致 | ✅ 精确吻合 |
| 最终窗口 `[22:50:34, 23:00:34)` passive log row = 1 行 | 按时间戳精确过滤 = **1 行**（22:59:31.054 aggregated WARN，reason=first） | ✅ 精确吻合 |
| 日志增量 99,987 bytes / 10 分钟 | 窗口内逐行字节数累计 = **99,987** | ✅ 精确吻合 |
| Claude fork 90 行 / 10 分钟 | 窗口内 fork detected = **90**（另有 compact 10 行，声明口径为 fork rows，无误） | ✅ 精确吻合 |
| 第一次 projection 15.9s 返回 hydrating | req_5: mac_receive 22:50:15.451 → response_enqueue 22:50:31.396 = **15.945s**，outcome=projection.hydrating | ✅ 精确吻合 |
| 重试 1.19s 拿到 snapshot | req_10: 22:50:43.890 → 22:50:45.079 = **1.189s**，outcome=snapshot（hydrate_commit elapsedMillis=28682 先行落账） | ✅ 精确吻合 |
| minute 8 抑制 1,842,559 条 identical notification | 日志计数器里程碑 22:57→1,600,000、22:58→1,900,000，1,842,559 落于其中（该精确值应来自管理端点读数，日志仅按 1000/100k 打点）；抑制机制确实在工作 | ✅ 一致（精确值无法从日志逐字复现，量级与进度吻合） |
| bridge CPU 平均 30.74% / 峰值 39.6% | 分钟采样算术复核：10 样本和 307.4 / 10 = **30.74** ✓；进程存活期累计均值 39.9%（23:10 采样，含窗口后持续风暴与一次 rebind），与窗口均值不矛盾 | ⚠️ 算术正确、与进程状态相容；历史分钟采样本身不可重放，采信执行方读数 |

补充观察（执行方未声明但审计取证到）：抑制计数器在最终窗口内即达 **2,100,000**（22:59），且 23:00–23:01 发生一次 rebind（计数器清零）后又累计 **1,500,000**+（23:11）——上游 terminal transport error 风暴在窗口内外均未停止，codec 边界抑制是唯一防线。这印证完成报告"剩余风险 1"的定性：上游根因仍未修，值得单独反馈。

### 2.3 代码抽查（完成报告 §6 建议审核重点 1–4，逐项核对）

1. **codec 只抑制 byte-identical terminal error、rebind 清零、参数变化仍交付** — ✅ `agent/codex-remote/codec.go:690-728`：`bytes.Equal(c.lastErrorParams, n.Params)` 才计数丢弃；`ResetNativeSessionState()`（codec.go:112-117）清空 `lastErrorParams` 与计数器；`willRetry` 路径不抑制且清空 lastErrorParams；非 identical 参数正常解码交付。
2. **background_tasks.list 不把未知无 workflow 伪装成空成功、错误走 30s 同 revision 退避** — ✅ `go-bridge/background_tasks.go:312-333`：projection 未 Ready 返回可重试 `background_tasks.projection_not_ready`（带 RetryAfterMillis），summary-only 不推断空（代码注释明示）；`go-bridge/background_task_cache.go:68-105`：错误 flight 的 `lookup` 永远返回 false（不缓存为成功），`retryAt = now + 30s` 内不重扫，revision 变化立即重扫。
3. **Grok/workspace 缓存 TTL 低于 60s safety scan** — ✅ `go-bridge/catalog_native_membership.go:56-59`：`grokMembershipCacheTTL = 50 * time.Second`，注释明示"stays below the 60s discovery safety scan"；`handlers_grok_catalog.go:132` 出站再滤覆盖 TTL 窗口内的磁盘删除。
4. **discovery 不引入秒级 head 轮询、不削弱官方信号** — ✅ `go-bridge/session_discovery.go:56`：`codexRemoteDiscoveryRequestTimeout = 12 * time.Second`（与 `catalogRequestTimeout = 8s` 解耦）；timeout 退避 `10s–30s`（:57-58），hard error 保留既有指数退避；`codexRemoteDiscoveryHintInterval`（:45）为包级 var，生产代码无任何赋值（仅测试 seam），保持零值=禁用，官方 lifecycle signal 仍是快路径。
5. **管理端点认证** — ✅ `go-bridge/management_api.go:390-405`：`Authorization: Bearer` 校验，缺失/不匹配返回 401；runtime 以 `-management-host 127.0.0.1 -management-port 0` 运行。

### 2.4 文档与提交核对

- 6 个 commit（`6b3ad9b`/`038c504`/`5ddfef1`/`71d4d30`/`79315c2`/`915bc6b`）全部存在，`--stat` 范围与完成报告 §3 关键文件变更一致；`915bc6b` 同一提交内写入最终 todo 状态、报告与哈希（无事后篡改痕迹）。
- `CHANGELOG.md`、`GO_BRIDGE_ARCHITECTURE.md`、`docs/protocol/bridge-v1.md`、plan Status 头（`implemented`）均已同步，内容与实现相符。

## 3. 结构审计

- 33/33 todo `status=done`，全部 `verification.required=true`、`status=present`、summary 非空、artifacts 非空；无结构性缺证明项。
- attestation 计数：**22 re-verified / 11 self-attested**，与完成报告声明一致；11 项 self-attested 全部为 `impl` kind，符合技能默认（impl 自证可接受）。
- 依赖图完整（tests→impl、regression→tests、跨 phase 串联），无悬空依赖。
- 3 组 review-fix 三胞胎（passive-error-suppression、background-task-error-backoff、claude-lineage-log）均带 `review-fix-for:` 交叉引用，且对应父 todo（storm/starvation/cadence 的 regression）保持 `done` 而非 `blocked`——因 review-fix 是对已验证行为的增强修复且其自身 regression 已证明，符合修复环规则的精神；此点记录为口径备注而非缺陷。
- 工件路径：所有仓库内文件工件存在；2 项"缺失"项（`go-bridge.log projection_rpc req_5/req_10`、`go-bridge.log Claude lineage counts`）为对现场日志的描述性引用，本审计已用同一日志独立复现其内容（§2.2），实质成立。
- 状态文件历史（6 个版本）显示队列随执行单调推进，最终提交将 3 个 in_progress review-fix regression 与 phase4 regression 收口为 done 并附证明，时序自洽。

## 4. 审计发现

### F1（P2，元数据完整性）队列哈希不可复现

状态文件与完成报告均记录 `based_on_queue_hash: 03271392a4aa`，但按 `references/state-format.md` 钉死的 canonical 算法（sha1 over sorted `[id, status, verification{required,status,attestation,summary,artifacts}]`，`separators=(",",":")`）重算当前队列得 **`59dd1852a7ff`**。审计尝试了 20+ 种算法变体（ensure_ascii 两态、全 verification 字段、整 todo 对象、id+status、sha256/md5、未排序、报告文件哈希等）并对全部 6 个历史状态版本重算，**无一命中 `03271392a4aa`**。结论：执行方使用了未记录的非 canonical 计算方式（或对未知数据计算），"报告↔队列"绑定无法被第三方按钉死算法验证。该缺陷不影响 33 项 todo 各自的证明（本审计已逐项复验），但 `based_on_queue_hash` 的完整性校验功能失效。**建议**：后续会话将状态文件 `reports.based_on_queue_hash` 修正为 canonical 值 `59dd1852a7ff`（或在状态中显式记录实际使用的算法），本审计不代改以保留缺陷现场。

### F2（P3，覆盖面记录）plan 交付物 0d 无队列 todo、无处置记录

Plan Phase 0 定义了 **0d `daemon-churn-ab-test`**（`ensure-codex-shared-daemon.sh` churn 开/关只读 A/B，§1.7 与 §6-Q3 均依赖它定论 thread/list 变慢归因），但队列 33 项中没有任何对应 todo，plan 文本与完成报告也未记录该交付物被放弃的原因。实质上该 A/B 已结构性失效——daemon 于同日（2026-09-12）被三层清零退役（commit `07dc9b3` 及退役记录），最终真机回归本就在无 daemon 环境下运行——但按 exec-plan 漂移规则，分解时静默丢弃 plan 交付物应显式记录。**影响**：§6-Q3（7.2s 是固有成本还是 app-server 变慢）失去定论路径，完成报告也未将其列为未决项。

### F3（P3，标注口径）live 回归证据被标为 re-verified

4 项携带真机/运行时证据的 regression todo（passive-error-suppression-review-fix、background-task-error-backoff-review-fix、claude-lineage-log-review-fix、phase4-doc-sync 的 regression）被执行方标为 `attestation: re-verified`，完成报告 §4.2 注明"re-verified for runtime metrics directly sampled … by executing agent"。按技能的诚实框架，**同一执行方的现场采样仍属 self-attested**——"re-verified"保留给审计/复跑的独立核验。本审计现已对其中全部可持久复核的声明（日志行数、字节增量、时延、版本、计数器）完成真正的独立复核且全部吻合，实质成立、无需降级；但执行方的原始标注口径越界，应在此记录，避免未来报告效仿。

### F4（观察项）grokbuild 全量偶发失败复现

本审计全量 `go test ./...` 中 `agent/grokbuild` 的 `TestSendUnknownModelIdSoftensToCurrentModel` 失败一次（session_model_switch_test.go:524），单包复跑通过。与记忆在案的既有偶发失败（跨测试状态干扰，`-count=1` 单包与整包复跑均过）一致；本轮 6 个 commit 的 `--stat` 不含 `agent/grokbuild` 任何文件。判定：**非本轮回归，不构成降级依据**；该 flake 值得独立修复（另案）。

### F5（观察项）plan §6 开放问题未全部闭环

已由实现/证据回答：Q1（上游重复发送同一 terminal error——抑制计数器在 codec 收包侧持续增长即为直接证据）、Q2（CPU 归因——诊断端点+最终窗口采样）、Q5（head trigger——按 plan 约束未采纳）、Q6（summary-only 不能权威推断无 workflow——projection_not_ready 语义）、Q9（第二 filter 调用者——declared Grok list_sessions 出站再滤）。**未回答**：Q3（7.2s 归因，依赖已失效的 0d，见 F2）、Q7（iOS 单分钟 8 次 background_tasks.list 的动机；服务端已可承受重复，但动机未证）、Q8（grokbuild 19 会话 406ms 是否正常）。

### F6（观察项）上游风暴仍在持续

抑制计数器显示最终窗口内约 210 万条、窗口后（含一次 rebind）又 150 万条 byte-identical terminal error 被丢弃——上游根因未修，与完成报告"剩余风险 1"一致。MacBridge 侧防线（codec 抑制 + 30s 日志聚合）在持续负载下工作正常，但该防线是唯一的，建议尽快向 upstream 反馈。

## 5. 覆盖面映射（plan 交付物 → 队列 todo）

| Plan 交付物 | 队列承接 | 状态 |
| --- | --- | --- |
| 0a transport-error-storm-root-cause | 并入 phase0-observability + suppression review-fix（抑制计数器即收包侧证据） | ✅ 实质覆盖 |
| 0a+ identical-error-notification-suppression | phase2-passive-error-suppression-review-fix ×3 | ✅ |
| 0b passive-event-log-aggregation | phase2-passive-error-storm ×3 | ✅ |
| 0c cpu-and-throughput-attribution | phase0-observability ×3（诊断端点含 CPU/goroutine/p50/p95） | ✅ |
| **0d daemon-churn-ab-test** | **无** | ❌ 见 F2 |
| 0e background-task-starvation-attribution | 并入 phase0-observability（item request/scan counters） | ✅ |
| 1a/1b/1c discovery 量化 | 并入 phase0-observability + phase2-discovery-push-resilience（outcome 分类、p50/p95、进程 CPU） | ✅ |
| 2a fingerprint-bounded-fetch | 评估后按 plan 约束不采纳 head 轮询（hint interval 保持 0） | ✅ 决策性关闭 |
| 2b timeout-policy / 2c push-liveness | phase2-discovery-push-resilience ×3 | ✅ |
| 2d no-workflow slow-path（含 22:33 补充） | phase2-background-task-starvation ×3 + error-backoff review-fix ×3 | ✅ |
| 2e pairing-restore-race | phase2-pairing-restore-race ×3 | ✅ |
| 3a/3b/3c catalog 扫描降本 | phase3-catalog-scan-cost ×3 | ✅ |
| 3d second-caller / 3e claude-lineage（含 22:39 补充） | phase3-secondary-scan-cadence ×3 + claude-lineage-log review-fix ×3 | ✅ |
| 4a go-tests | 分散于各单元 -tests（技能允许） | ✅ |
| 4b real-device-regression | 各 review-fix-regression + phase4-doc-sync-regression 的 live 窗口 | ✅ |
| 4c doc-sync | phase4-doc-sync ×3 | ✅ |

## 6. 结论与建议

**结论：完成报告 `proved-complete` 裁定经独立审计成立。** 33/33 required todo 证明在档且经复验；自动化验证全过（F4 除外且已排除）；现场数据独立复核精确吻合；关键约束（fail-closed、last-good seen、60s safety scan、TTL<60s、无 head 轮询、无隐私泄漏字段）逐项核实无违反。无需降级任何 todo，完成报告状态维持 `current`。

建议（均非阻断）：

1. 修正 `reports.based_on_queue_hash` 为 canonical 值 `59dd1852a7ff`，或显式记录执行方实际算法（F1）。
2. 在 plan 或状态中补记 0d 的处置（daemon 同日退役、A/B 失效），并将 §6-Q3/Q7/Q8 移交后续观察（F2/F5）。
3. 未来完成报告对执行方自采的 live 证据统一标 `self-attested`，把 `re-verified` 留给独立审计（F3）。
4. 向 upstream 单独反馈 terminal transport error 高频重发根因（F6）。
5. grokbuild 测试偶发失败另案修复（F4）。
