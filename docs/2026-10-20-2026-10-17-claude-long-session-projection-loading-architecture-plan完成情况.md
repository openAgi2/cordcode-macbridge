# 本轮任务完成情况：Claude 长 session 投影加载架构方案（S-0…S-8 实施 → 2026-10-20 摘要首开退役收口）

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading`
- Plan: `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（v30，R25 APPROVED，commit `6a76d92e`，正文 SHA-256 `88dede7e…a4a388` 已机械校验）
- Canonical State File: `.exec-plan/state/plan-b70583acde4f.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: 29/29 todos done（原队列 26 项 + 2026-10-20 追加 3 项：排序尾部反扫 / v2 预览扩容 / 摘要首开退役）；owner 已终态验收
- Generated At: `2026-10-20T23:45:00+08:00`
- Queue Hash: `all-done-29-owner-accepted`

Source identity at completion（P0 来源门，本报告生成时点重新核对）：

| 仓库 / 产物 | 身份 |
| --- | --- |
| MacBridge worktree | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading`，branch `feat/claude-long-session-projection-loading`，commit `66a6de16cc79dc19c5983f30d6cebeac54dc0c17`，clean |
| 配套 iOS worktree | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading`，branch `feat/claude-long-session-projection-loading`，commit `f65e71784e2ebe06490ac3775cb568f2baea7dcd`，clean；**两仓均未 push（owner 指示暂缓）** |
| 正式安装 runtime | `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，PID 40490，`lstart Tue Oct 20 22:14:33 2026`，8777 监听者，内嵌 commit `5e4d0a97b9c2`；无 `/tmp`、DerivedData、`build/` 违规残留进程 |
| iPhone16 安装 | bundle 构建身份 `f796e89c`（CordCodeSourceCommit；`f65e7178` 为其后的纯文档提交，不改变安装产物） |

## 1. Overall Verdict (总体结论)

方案 v30 全部切片（S-0…S-8、S-3 打开路径加固、v2 范围收口）已实施、测试、部署并经 owner 验收。其中 **S-3 v2 摘要首开在 2026-10-20 owner 验收通过的当日，被 owner 裁决「别搞那些摘要了，即使几十M，那就老老实实的加载吧，现在这个摘要方案太复杂」整体退役**。产品终态＝**冷开全量直拉（几十 MB 转一下属预期）+ S-4 基底缓存暖开秒开**，owner 终态验收原话：「首次打开直接加载完整内容了，重开也秒开✅」。同日交付并验收的还有：大会话列表排序改「仅真实消息活动才前移」（Mac `9eac1a59`）、清除离线缓存按钮补齐投影基底缓存（iOS `8a813c47`）。

Mac 端 v2 摘要生产代码（`projection_window_v2*.go`、capability、协议 pack v2 冻结章节与样本）按裁决保留**休眠**——iOS 已不再发 `get_session_projection_window_v2`，服务端不触发；这不是活跃行为描述，清理属另案。重建摘要/分页首开类路径须 owner 明确要求（记忆 `claude-session-loading-accepted-shape` 已锚定）。

## 2. Phase Completion Matrix (阶段完成矩阵)

| 阶段 | Verdict | Evidence（attestation 摘要；详见 state file todos） |
| --- | --- | --- |
| S-0 运行身份取证（E-3 闭合） | complete | 生产运行态实测 + 双端协商源码亲核（`docs/2026-10-18-s0-runtime-identity-evidence.md`） |
| S-2 Mac 物化存储 | complete | bbolt 物化 + 增量重启恢复；E-6 realdata 证据测试；Release 重启运行态回归（`41523b1f`/`edab8660`） |
| S-4 iOS 基底缓存 | complete | restore-then-increment 打开路径 + P0 协议遵循修复（生产 metatype 转换回归）；E-7/E-11 真机收口（`b1952252`/`061d8d05`） |
| S-5 单次解析管线 | complete | iOS 长会话解码单次化；E-5 阶段耗时分布取证（iOS `8393f0f1`） |
| S-7 anchor-newer 对账 | complete | re-freeze 协议批次 + 双端实施；E-4/E-10 证据门（`380c6939`/`455d49e4`/iOS `ce464295`） |
| S-8 direct_gzip_v1 | complete | 直连逐帧压缩双端实施 + 传输压缩实测（`0bae9bea`） |
| S-3 打开路径加固 | complete | 2026-10-19 真机风暴复盘 H1-H4 修复（`6c5af574`）+ 尾部悬挂 turn 卡死修复（`d78100ee`） |
| S-3 v2 summary 冻结+实施 | complete **then retired** | 冻结 R3 APPROVED → Mac producer（`a9b09636`）→ iOS 消费端（`fb039716`）→ 空白屏全量修复（`48d47a86`）→ owner 验收通过（`docs/2026-10-20-s3-v2-owner-acceptance.md`）→ **同日 owner 裁决退役（见 §3）** |
| S-6 owner 验收矩阵 | complete | 材料准备不代验；S-7 owner 自然使用验收通过（`7b80128a`）+ S-3 v2 验收通过 → 后被终态验收覆盖 |
| v2 范围收口（Codex F-01/F-02/F-05） | complete | 双端 summary 范围门 + 坏种子弃用 + echo 对齐；真机 30/30 + 模拟器 10/10（`9914cc57`/iOS `18ca7f6b`） |
| 排序尾部反扫（2026-10-20 追加） | complete | 「仅打开」不再前移；定向测试 2/2 + Release 部署；owner ✅（`9eac1a59`） |
| v2 预览扩容（2026-10-20 追加） | complete **→ 休眠** | 128→1024 / head 64→256 + UTF-8 实际字节预算（R1 F-01 四字节反例修复）；R2 APPROVED 后实施部署（Mac `5e4d0a97`/iOS `c2017bbe`/mirror `a42675c6`）；随后被退役裁决覆盖，iOS 不请求即不触发 |
| 摘要首开退役（2026-10-20 追加） | complete | iOS `f4b9caef`：删 `pullWindowV2Summary`（净删约 300 行），冷开一律全量；摘要种子弃用重建（reason=summary-mode-retired）；真机 21/21 + owner 终态验收 ✅ |
| followup：fence 快照竞态 | complete（排除） | 55ms 竞态假设不成立，设计内回退，followup 关闭（`bf3f9373`） |

## 3. 产品终态（owner 已验收形态，2026-10-20 定案）

1. **冷开**：直接全量加载（`get_session_projection` + direct_gzip_v1 压缩 + S-5 单次解析）。几十 MB 转一下属预期；恢复期 `performForegroundRecoveryTransaction` 亦走全量。
2. **暖开**：S-4 基底缓存 restore-then-increment + anchor-newer 对账增量，秒开。基底播种在冷 miss 全量成功后立即执行；`claudePreviewRevision` 门保证旧预览种子一次性 miss 不预删。
3. **摘要形态**：S-3 v2 摘要首开退役。iOS 冷 miss 不发 v2 请求；**任何 backend** 的摘要形态种子（summary 列非空）打开即弃用重建（reason=summary-mode-retired）；push snapshot 应用路径显式清 summaryRows。
4. **排序**：Claude 会话 updatedAt 取尾部 64KB 反扫最后带 timestamp 的对话行；仅真实消息活动才前移，「仅打开」不再顶到列表最前。
5. **清缓存**：清除离线缓存按钮补齐投影基底缓存清理。
6. **休眠契约**：Mac `projection_window_v2*.go`、`projection_window_v2` capability、`docs/protocol/bridge-v1.md` 与 schema 的 v2 冻结章节、冻结样本（SHA `5c39e17a…`/`8959e1a6…`，两仓字节一致）保留在树——iOS 不请求即不触发；**清理属独立任务，需 owner 明确要求**。

## 4. Key File Changes (关键文件变更)

MacBridge（代表提交；完整清单以 `git log main..HEAD` 为真值，共 47 提交）：

- `go-bridge/projection_materialization.go`（+ `_test` / `_realdata_test`）：S-2 bbolt 物化存储与重启恢复。
- `go-bridge/projection_anchor_newer.go` / `_handler.go` / `_test.go`、`handlers.go`：S-7 anchor-newer 对账增量 RPC。
- direct_gzip_v1 直连逐帧压缩（S-8）：接线于 `server.go` / `relay_outbound_writer.go`，契约与等价测试 `direct_gzip_spec_test.go`、`parity_*_transport_test.go`。
- `go-bridge/projection_window_v2.go` / `_handler.go` / `_test.go`：S-3 v2 summary producer（现休眠）+ 2026-10-20 预览扩容 1024/256 与实际 UTF-8 字节预算（`TestSummaryBudgetCountsActualUTF8Bytes` 四字节反例）。
- `go-bridge/claude_session_activity_tail.go` / `_test.go`：排序尾部反扫真实活动。
- `go-bridge/projection_kernel.go` / `projection_snapshot_fence.go`（`d78100ee`）：尾部悬挂 turn 卡死 hydrate，coverage 裁决扩至全部尾部非终态。
- `docs/protocol/`（bridge-v1.md + schema）：S-7 re-freeze、S-3 v2 冻结、预览扩容勘误（schemaRevision 不变——策略阈值非 wire 变更）。

iOS（共 26 提交）：

- `Models/ProjectionRestoreThenIncrement.swift`：S-4 打开路径 + **2026-10-20 摘要退役核心编辑**（删 `pullWindowV2Summary`、摘要种子弃用重建）。
- `Models/ProjectionBaseCacheStore.swift`：S-4 基底缓存 + `claudePreviewRevision` 失效门。
- `Models/SessionProjectionWindowV2.swift`：v2 判别解码（现随退役休眠）+ displayTurn 前缀去重。
- `OpenCodeiOSTests/ProjectionWindowV2ConsumptionTests.swift`：重写为退役行为断言（冷 miss 一律全量 / 摘要种子弃用重建 / push 清 summaryRows）。
- `SettingsUI`（`8a813c47`）：清除离线缓存按钮补齐投影基底缓存。

## 5. Verification Evidence (验证证据)

- **Mac 定向 Go 测试（本报告生成时点复核）**：`GOTOOLCHAIN=local go test ./go-bridge -count=1 -run 'TestSummarize|TestSummary|TestAnchorItemsView|TestV2Hello|TestClaudeDescriptor|TestTailActivity'` → `ok … 1.028s`（覆盖 v2 summary/预算四字节反例/anchor 转换/echo 协商/排序尾部反扫）。
- **iOS 真机定向测试（退役轮）**：21/21 通过（`ProjectionWindowV2ConsumptionTests` 重写后全集；构建身份 `f796e89c`，DerivedData `/tmp/s3v2-dd`）。
- **部署闭环**：Mac Release 覆盖安装 `/Applications` → `killall CordCodeLink` + `pkill -f cordcode-bridge-runtime` → 新 PID 40490（22:14:33）监听 8777；无临时产物残留进程。iPhone16（UDID `00008140-001E69503453001C`）`scripts/run.sh device` 安装，bundle `CordCodeSourceCommit` 身份核验。
- **owner 验收**：排序/清缓存/其他 backend 暖开秒开 ✅；摘要退役终态「首次打开直接加载完整内容了，重开也秒开✅」。
- **secret scan**：gitleaks（pinned，`--no-git --source` 含双仓 diff 包）两轮全绿，no leaks。
- 各切片证据门（E-3/E-4/E-5/E-6/E-7/E-10/E-11）完成时已分别收口，详见 state file 各 todo 的 verification 块。

## 6. 裁决与演化链（2026-10-17 → 2026-10-20）

| 日期 | 事件 |
| --- | --- |
| 10-17 | 架构方案 v30（R25 APPROVED，评审报告 r1-r25 归档）；exec-plan 队列建档 |
| 10-18 | S-0 取证（E-3 闭合）→ S-5 / S-8 / S-7 / S-2 实施；owner 放行 iPhone16 真机窗口 |
| 10-19 | 真机打开风暴复盘（WiFi rx 劣化 × 全量冷拉 × 超时族乘积）→ S-4 协议遵循 P0 修复 → S-3 打开路径加固 H1-H4（owner 批准 A 路线） |
| 10-20 上午 | S-3 v2 冻结规范（R1→R2→R3 APPROVED）→ Mac producer → iOS 消费端 → 空白屏全量修复 → **owner 验收通过**（`docs/2026-10-20-s3-v2-owner-acceptance.md`） |
| 10-20 下午 | owner 反馈首屏过简（截图归因：summary ~1s 就绪，「十几秒后完整」实为 foreground recovery 全量 36.4MB，非摘要补全）→ 预览扩容方案（R1 REVISION_REQUIRED：UTF-8 预算四字节低估 F-01 → v2 修订 → R2 APPROVED）→ 双端实施部署 |
| 10-20 晚 | owner 裁决「别搞摘要了，太复杂」→ **摘要首开整体退役**（iOS `f4b9caef`）→ 排序修复同轮验收 → **owner 终态验收 ✅** → 收口归档（CHANGELOG×2、think.md 补记二、记忆、本报告 + exec-plan 终态） |

## 7. 未尽事项（登记另案，不阻塞本 plan 收口）

1. **两仓未 push**（owner 指示暂缓；发布前需 owner 明确要求）。
2. **Mac 端休眠 v2 摘要代码与两端 protocol pack v2 冻结章节清理**——独立任务，需 owner 明确要求；在此之前按休眠契约保留。
3. **foreground recovery 全量（36.4MB）路径优化**——已向 owner 提及可单独做，未获要求。
4. **跨设备缓存形态优先级**——他机播种的旧缓存使本机首开走全量（低频；`docs/2026-10-20-s3-v2-owner-acceptance.md` 未尽事项节）。

（`remote-web` 12 门浏览器验收矩阵属另一并行任务，不归本 plan 收口。）
