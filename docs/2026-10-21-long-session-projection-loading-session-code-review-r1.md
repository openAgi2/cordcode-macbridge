# Claude 长 session 投影加载分支——整轮代码评审 R1（独立 · 对抗式 · 只读）

> **总结论：`FAIL`**（2 blocker / 2 major / 8 minor / 9 nit）。
>
> 1. **BLOCKER-1（数据损坏，已在生产发生）**：S-2 物化存储对 Claude 组合源"接受 cursor 推进 + 后缀折叠"，而 Claude 解析器的 turn ID 是**相对读取起点的行号**（`user-line-N`），后缀读取又**越过冻结 cut**。结果：后来的 prompt 覆盖会话最早的 turn，新 turn 从尾部消失或变成虚构 turn，并被写回 checkpoint、播种进 iOS 缓存，后续不会自愈。触发不限于重启：**同一 runtime 内 transcript 增长后再次打开也会触发**。生产日志捕获到 18 次折叠，离线重放其中 13 次，**11 次分歧**（另 2 次后缀解析结果为 0 条）。
> 2. **BLOCKER-2（休眠契约不成立）**：S-3 v2 退役后，Claude descriptor 仍声明 `turn_detail_chunks_v1`。iOS 会在 Claude 已完成、turnId 为 UUID 的 turn 上显示「加载详细过程」入口；生产连接全部协商了 chunks，所以每次点击都会进 v2 批量引擎。该引擎要求 `TurnItemsPager`，Claude 没有实现，结果必定是 `failed(upstream_error)`，并把失败态提交进内核投影。
> 3. MAJOR：d78100ee 把 coverage 放宽到所有尾部非终态 turn，结果尾部 `/model` 这类本地命令回显 turn 被判 `aborted`，会话被标成失败；iOS「清除离线缓存」删掉缓存目录后，基底缓存写盘静默失败，直到 App 重启前暖开一直不可用。
>
> **owner 下一步（仅建议）**：① 本分支暂不合并、不 push；② 先排期修 BLOCKER-1：恢复 main 的「组合源 cut 不等即诚实重建」语义，并 bump 物化 schema 以清掉已损坏的 checkpoint，然后核验 iOS 缓存能否经 anchor-newer 自愈；③ 撤掉或门控 Claude 的 `turn_detail_chunks_v1` 声明；④ 两个 major 各补一条带真实 transcript 的 handler 级回归测试后再修。

---

## §0 来源清单（实测时点）

### 0.1 P0 门点记录

| 门点 | 时点（CST） | 结果 |
| --- | --- | --- |
| ① 读源码/评审前 | 2026-10-21 会话开始（首轮），04:03:58 复核，04:25:16 复核 | 两仓路径/分支/HEAD/干净状态一致 |
| ② 首次写文件（本报告）前 | 2026-10-21 04:50:03 | 一致；目标文件不存在；`docs/` 不在 ignore 内 |
| ②′ 写入后、提交前 | 2026-10-21 04:56:23 | 两仓 HEAD 不变；Mac 仅新增本文件（`??`），iOS 干净 |
| ③ 构建/安装/部署 | — | **不适用**：本评审只读，未构建、未安装、未重启任何运行时 |

### 0.2 两仓来源清单（P0 七字段，实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=d3b72f029b116952b293d18d29992a711229fd01
未提交状态=干净（status --porcelain 0 行；本报告写入后仅新增本文件）
任务预期分支=feat/claude-long-session-projection-loading（任务指令指定；merge-base main = f868af18bbbbff6e7f47994c11fb76b49406b33e，main..HEAD = 48 提交）
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / f65e71784e2ebe06490ac3775cb568f2baea7dcd
预期产品特性=Claude 冷开全量直拉 + S-4 暖开；S-2 物化恢复；S-7 anchor-newer；S-8 direct_gzip_v1；S-3 v2 摘要首开退役且 Mac v2 代码休眠

仓库路径=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=f65e71784e2ebe06490ac3775cb568f2baea7dcd
未提交状态=干净（status --porcelain 0 行）
任务预期分支=feat/claude-long-session-projection-loading（任务指令指定；merge-base main = f0d0f5233cde2a522637c3134e07b32c5bae3f3e，main..HEAD = 26 提交）
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / d3b72f029b116952b293d18d29992a711229fd01
预期产品特性=同上（iOS 消费端：S-4 基底缓存 / S-5 单次解析 / S-7 对账 / S-8 gzip 接收 / 摘要首开停用）
```

两仓 `git worktree list` 中与本任务分支对应的工作树各只有一个（上面两条），配对唯一。

### 0.3 运行态（只读观察，未做任何操作）

- 8777 监听者是 `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，PID 24784，PPID 40449（`CordCodeLink`），`lstart Wed Oct 21 02:38:03 2026`；二进制内嵌提交 `5e4d0a97b9c2`。这是本分支最后一个业务代码提交，其后 3 个提交都只改文档。**结论：§2 引用的生产日志全部来自本分支代码。**
- 日志可得性：`go-bridge.log` 从 2026-10-21T02:38:03 runtime 重启开始；`go-bridge.log.1` 覆盖 2026-10-19T23:44 → 10-20T14:10。runtime 在 2026-10-20T22:14:34 启动的那份日志，我在 10-21 约 00:45 读过并记录，**02:38 重启后已被替换、磁盘上不存在**；§2 引用的这部分行均为当时的捕获，标注【生产日志·已轮转】。

### 0.4 证据类别与复现位置（均在仓库外，未提交）

- 【源码】行号均在 0.2 的 HEAD 上取得；报告写入后（04:56–05:05）又逐条按 HEAD 回读核对过。iOS 路径约定：`Models/`、`ViewModels/`、`Services/`、`Views/`、`App/` 均相对于 `OpenCodeiOS/OpenCodeiOS/`；`OpenCodeiOSTests/` 相对于 `OpenCodeiOS/`；`message-web/` 相对于 iOS 仓库根。Mac 路径相对于仓库根。
- 【scratch】评审用临时测试：在 `/tmp/cr-r1-scratch/mac/`（HEAD 的 go module rsync 副本，已核 go-bridge/agent 与 HEAD 一致）和 `/tmp/cr-r1-scratch/mac-main/`（`git archive f868af18`）中运行，**不在仓库内**。文件名：`agent/claudecode/zz_review_suffix_bound_test.go`、`go-bridge/zz_review_s2_suffix_fold_test.go`、`zz_review_s2_restart_e2e_test.go`、`zz_review_s2_catchup_test.go`、`zz_review_s2_realdata_replay_test.go`、`zz_review_anchor_fence_leak_test.go`、`zz_review_claude_turn_detail_v2_test.go`、`zz_review_command_echo_coverage_test.go`。Swift 组件复现：`/tmp/cr-r1-scratch/swift-scanner/`（编译 HEAD 原文件 `BridgeInboundRoutingScanner.swift`）、`/tmp/cr-r1-scratch/swift-cache/`。
- 【真实样本】`/tmp/cr-r1-scratch/replay/*.jsonl`：生产 transcript 按日志 `startCut` 截断的副本（0600）。只输出计数和 ID，不复制正文。
- 【生产日志】`~/Library/Application Support/CordCode Link/logs/`，只读 grep。
- 【推断】根据源码推理、没有直接运行证据的结论，在正文中单独标注。
- 上游锚点：Claude Code 没有开源源码，以真实 transcript 样本为准（见 BLOCKER-1 证据 b）。bbolt 契约以模块源码 `go.etcd.io/bbolt@v1.4.3/bucket.go:580-585` 为准（`go.mod:15` 锁定 v1.4.3）。

验证手段遵守约束：Mac 只跑了定向 `GOTOOLCHAIN=local /tmp/gobin/go test ... -run '<主题>'`，没有 clean、没有全量、没有改 DerivedData；iOS 只做静态阅读，外加两个仓库外的 swiftc 组件复现；没有构建、安装、真机、模拟器、UI automation 或截屏。

---

## §1 commit 清单与范围确认

### 1.1 范围

- Mac：`f868af18..d3b72f02` 共 48 提交。其中 16 个改代码（逐行评审），32 个只改文档/状态（只做一致性对账）。
- iOS：`f0d0f523..f65e7178` 共 26 提交。其中 19 个改代码/工程（含 `8770624f` 纯 pbxproj、`f796e89c` 纯格式），7 个只改文档。
- `8770624f`：已核仅改 `OpenCodeiOS/CordCode.xcodeproj/project.pbxproj`（+57/−1），**无业务改动** ✓。
- diff 规模：Mac 104 文件 +15890/−205，其中非文档 42 文件 +5222/−47；iOS 47 文件 +5881/−159，其中 `OpenCodeiOS/OpenCodeiOS` 22 文件 +2616/−143。

### 1.2 Mac（48）

| # | 提交 | 类型 | 标题 |
| --- | --- | --- | --- |
| 1 | `6a76d92e` | 文档/状态（一致性对账） | docs(plan): Claude 长 session 打开架构优化方案 v30（R25 APPROVED 终版）与 R10-R25 评审报告 |
| 2 | `8aca6662` | 文档/状态（一致性对账） | docs(projection): S-0 运行身份取证与 E-3 根因判定（方案 v30 实施队列建档） |
| 3 | `0bae9bea` | 代码（逐行） | feat(transport): direct_gzip_v1 直连逐帧压缩（S-8 第一阶段承重） |
| 4 | `380c6939` | 代码（逐行） | feat(protocol): anchor-newer 对账增量协议冻结批次（S-7 re-freeze） |
| 5 | `455d49e4` | 代码（逐行） | feat(projection): anchor-newer 对账增量 RPC 实现（S-7 主交付） |
| 6 | `1fe9c65d` | 文档/状态（一致性对账） | docs(handoff): 阶段交接——第一阶段+S-7 主交付完成，iOS 设备操作按 owner 调度约束后置 |
| 7 | `cbd0a109` | 文档/状态（一致性对账） | docs(opencode): OpenCode v2 共享服务接入设计方案 v1.8（r1~r6 评审收口）〔与本任务无关的设计文档，分支卫生观察，不计发现〕 |
| 8 | `41523b1f` | 代码（逐行） | feat(projection): bbolt 物化存储与增量重启恢复（S-2） |
| 9 | `edab8660` | 代码（逐行） | test(projection): S-2 realdata 证据测试与收口（E-6） |
| 10 | `60da8dc3` | 文档/状态（一致性对账） | docs(handoff): S-2 完成更新——剩余队列 iOS 门控暂停 |
| 11 | `ccad8a83` | 代码（逐行） | feat(observability): hello_ack 日志携带 echoedCapabilities（§7.3 协商可观测性） |
| 12 | `30546ef8` | 文档/状态（一致性对账） | chore(exec-plan): S-4 impl/tests 完成（真机 6/6），regression 待活体窗口 |
| 13 | `ef115e1d` | 文档/状态（一致性对账） | docs(think): 2026-10-19 长 session 真机打开风暴复盘（WiFi rx 劣化×全量冷拉×超时族乘积） |
| 14 | `966c221e` | 文档/状态（一致性对账） | chore(exec-plan): S-3 扩展范围登记（s3-open-hardening done） |
| 15 | `0c979a9c` | 文档/状态（一致性对账） | docs: record iOS long-session reopen cache root cause analysis |
| 16 | `61de6909` | 文档/状态（一致性对账） | chore(exec-plan): S-4 完成交付登记（P0+P1，f74b96fe，19/19） |
| 17 | `5f8963be` | 文档/状态（一致性对账） | docs: S-4 验收矩阵 + think.md 根因闭环补记（协议遵循缺失） |
| 18 | `ab67989c` | 文档/状态（一致性对账） | docs: S-4 验收通过——四字段实测回填 + 恢复期竞态缺口登记 |
| 19 | `a90b3c08` | 代码（逐行） | feat(observability): anchor stale 服务端分光 + relay hello_ack 能力回显 |
| 20 | `d2c1c85f` | 文档/状态（一致性对账） | docs(exec-plan): S-7/S-6 收口 + fence 竞态 follow-up 登记 |
| 21 | `d8c27e13` | 文档/状态（一致性对账） | docs(changelog): anchor 观测增强条目 |
| 22 | `7b80128a` | 文档/状态（一致性对账） | docs: S-6 owner 自然使用验收通过——10/15 场景实测覆盖收口 |
| 23 | `03393e1c` | 文档/状态（一致性对账） | chore(exec-plan): owner 裁决 A+③——S-3 v2 冻结规范启动 |
| 24 | `d5255ed1` | 文档/状态（一致性对账） | docs: S-3 v2 summary 冻结规范送审稿 v1（owner A+③ 裁决落地） |
| 25 | `ae016134` | 文档/状态（一致性对账） | docs: S-3 v2 冻结规范评审通过（R1 修订→R2 修订→R3 APPROVED + 元审核 OK） |
| 26 | `492099fb` | 文档/状态（一致性对账） | chore(exec-plan): s3 开发阶段启动（v3 过审） |
| 27 | `6595283b` | 代码（逐行） | feat(protocol): S-3 v2 summary 冻结批次（交付物 1/2/3/4/6） |
| 28 | `a9b09636` | 代码（逐行） | feat(v2): S-3 Mac producer 完整实施——window_v2 handler/summary 组装/anchor itemsView/claude 明细 |
| 29 | `237128e9` | 代码（逐行） | fix(v2): 权限例外官方标记 + anchor 摘要行标准字段（Codex F-3 + wire 对齐） |
| 30 | `d78100ee` | 代码（逐行） | fix(kernel): 尾部悬挂 turn 卡死 hydrate——coverage 裁决扩至全部尾部非终态 |
| 31 | `585c75f9` | 文档/状态（一致性对账） | docs: S-3 v2 owner 验收通过——报告/CHANGELOG/exec-plan 收口 |
| 32 | `5e7dcabc` | 文档/状态（一致性对账） | docs(think): 2026-10-20 空白屏事故与悬挂 turn 卡死复盘 |
| 33 | `bf3f9373` | 代码（逐行） | test(kernel): 快照一致性并发复现排除——55ms 竞态假设不成立，followup 关闭 |
| 34 | `b7ca3621` | 文档/状态（一致性对账） | docs(handoff): v30 全部完成交接——25/25、验收档案、环境注意 |
| 35 | `4ad45e8c` | 文档/状态（一致性对账） | docs: review backend applicability and v2 summary scope |
| 36 | `9914cc57` | 代码（逐行） | fix(v2): F-01/F-05 修复——服务端双 summary 入口范围门 + echo 对齐冻结条件 |
| 37 | `dae13ae6` | 文档/状态（一致性对账） | docs: v2 范围收口登记（exec-plan + CHANGELOG） |
| 38 | `1b411da3` | 代码（逐行） | fix(v2): summary 窗口按摘要等价尺寸切——大 turn 会话不再被 full 字节预算截断 |
| 39 | `9eac1a59` | 代码（逐行） | fix(claude): 大会话排序改尾部反扫真实活动——「仅打开」不再顶到列表最前 |
| 40 | `7eba7e33` | 文档/状态（一致性对账） | docs: CHANGELOG 登记 Claude 会话排序修复 |
| 41 | `088e4f53` | 文档/状态（一致性对账） | docs: record scoped Claude preview expansion and cache compatibility plan |
| 42 | `20236aeb` | 文档/状态（一致性对账） | docs: preserve independent preview expansion review R1 |
| 43 | `327b5aba` | 文档/状态（一致性对账） | docs: revise preview plan for actual UTF-8 budget and patch persistence |
| 44 | `cf55f6a4` | 文档/状态（一致性对账） | docs: archive approval for Claude preview expansion v2 |
| 45 | `5e4d0a97` | 代码（逐行） | feat(claude): v2 首屏正文预览扩容 128→1024 / head 64→256（owner 2026-10-20 反馈） |
| 46 | `d4d3fe8a` | 文档/状态（一致性对账） | docs: CHANGELOG 登记 Claude 首屏预览扩容 |
| 47 | `66a6de16` | 文档/状态（一致性对账） | docs: 摘要首开退役收口（CHANGELOG + think.md 复盘） |
| 48 | `d3b72f02` | 文档/状态（一致性对账） | docs: 补齐长 session 投影加载完成情况报告 + exec-plan 终态收口 |

### 1.3 iOS（26）

| # | 提交 | 类型 | 标题 |
| --- | --- | --- | --- |
| 1 | `8393f0f1` | 代码（逐行） | feat(bridge): 长会话投影解码单次化（S-5）+ direct_gzip_v1 压缩帧接收（S-8） |
| 2 | `427152dd` | 代码（逐行） | feat(protocol): anchor-newer 冻结协议 mirror 与 spec 断言（S-7） |
| 3 | `ce464295` | 代码（逐行） | feat(projection): anchor-newer wire 模型与 fetch seam（S-7 iOS 侧） |
| 4 | `b1952252` | 代码（逐行） | feat(projection): iOS 基底缓存与 restore-then-increment 打开路径（S-4） |
| 5 | `0f3b3f73` | 代码（逐行） | fix(projection): 冷 miss 全量成功后立即播种基底缓存（S-4） |
| 6 | `6c5af574` | 代码（逐行） | fix(projection): S-3 打开路径加固 H1-H4（2026-10-19 真机风暴复盘） |
| 7 | `6645f480` | 文档/状态（一致性对账） | docs(changelog): S-3 打开路径加固条目 |
| 8 | `21e0dc10` | 代码（逐行） | fix(projection): S-4 播种收口——迟到的全量应用成功统一播种基底缓存 |
| 9 | `ff66831b` | 代码（逐行） | fix(projection): 基底缓存字节预算按实际编码字节数执行 + open S4 miss 可观测 |
| 10 | `061d8d05` | 代码（逐行） | fix(projection): S-4 完成交付——P0 协议遵循 + P1 对账加固（Codex 分析全项落地） |
| 11 | `7320bef5` | 文档/状态（一致性对账） | docs(changelog): S-4 暖开修复条目 |
| 12 | `e38b9c77` | 代码（逐行） | test(projection): E-10 定向——running 会话经 anchor 重开执行态保持 |
| 13 | `605e6447` | 代码（逐行） | feat(protocol): S-3 v2 summary 冻结批次 iOS 镜像（交付物 3/5） |
| 14 | `fb039716` | 代码（逐行） | feat(v2): S-3 iOS 消费端——模型/decoder/fetcher/summary 首开集成 |
| 15 | `48d47a86` | 代码（逐行） | fix(v2): S-3 空白屏全量修复——判别解码/摘要缓存/权限例外/契约测试 |
| 16 | `ba0d985e` | 文档/状态（一致性对账） | docs: S-3 v2 owner 验收通过——报告/CHANGELOG/exec-plan 收口 |
| 17 | `8770624f` | 工程文件（仅核无业务改动 ✓） | chore(project): xcodegen 重生成（rebase 至 timeline f0d0f523 后；注册双方新增源文件） |
| 18 | `18ca7f6b` | 代码（逐行） | fix(v2): F-01/F-02 修复——summary 范围门/坏种子弃用/超时家族补全 |
| 19 | `fdfd98c1` | 文档/状态（一致性对账） | docs(changelog): v2 范围收口条目 |
| 20 | `8a813c47` | 代码（逐行） | feat(settings): 清除离线缓存按钮补齐投影基底缓存 |
| 21 | `a42675c6` | 文档/状态（一致性对账） | docs(protocol): mirror Claude v2 preview expansion 1024/256 + UTF-8 budget errata |
| 22 | `c2017bbe` | 代码（逐行） | feat(claude): 首屏预览扩容消费端——前缀去重、摘要形态保真、Claude 缓存 revision |
| 23 | `09bcbd6e` | 文档/状态（一致性对账） | docs: CHANGELOG 登记 Claude 首屏预览扩容 |
| 24 | `f4b9caef` | 代码（逐行） | feat(claude): 摘要首开停用——冷开一律全量加载（owner 2026-10-20 裁决） |
| 25 | `f796e89c` | 代码（逐行） | style: 去掉文件尾多余空行 |
| 26 | `f65e7178` | 文档/状态（一致性对账） | docs: CHANGELOG 登记摘要首开停用 |

### 1.4 范围外与不重复报告项

- 按指令排除：timeline 任务代码；main 独有内容；已在方案评审中出现过的条目（R1–R25、S-3 冻结 R1–R3、预览扩容 R1–R2 含 UTF-8 F-01、Codex v2 范围评审）；55ms 快照竞态（没有新证据）。
- 按基线不计为发现：冷开几十 MB 全量直拉 + S-4 暖开；S-3 v2 摘要退役；Mac v2 代码按休眠契约保留；摘要种子弃用、尾部反扫排序、清缓存同时清基底缓存——这些是 owner 已接受的**行为**。（MAJOR-2 针对的是清缓存**实现**的缺陷，不是"该不该清"。）
- 「应删除休眠代码」类意见不计。例：iOS `ProjectionRestoreThenIncrement.swift:140-144` / `:179-205` / `:433-453` 的 summary 分支，在摘要种子弃用后已不可达，本报告不列为发现。

---

## §2 发现（按分级）

### 2.0 汇总

| ID | 级别 | 仓库 | 一句话 | 置信度 |
| --- | --- | --- | --- | --- |
| BLOCKER-1 | blocker | Mac | S-2 对 Claude 组合源的后缀折叠损坏投影（重启和运行期重开都会触发），已在生产发生并被持久化 | CONFIRMED |
| BLOCKER-2 | blocker | Mac+iOS | 休眠契约不成立：Claude 仍声明 `turn_detail_chunks_v1`，iOS 入口可见，点击必失败并污染投影 | CONFIRMED |
| MAJOR-1 | major | Mac | d78100ee 放宽 coverage 后，尾部本地命令回显 turn 被判 `aborted`，会话被标失败（回归 2026-10-11 修复） | CONFIRMED（徽标部分为静态推断） |
| MAJOR-2 | major | iOS | 清除离线缓存后基底缓存写盘静默失败，直到 App 重启前暖开不可用 | CONFIRMED（组件级） |
| MINOR-1 | minor | Mac | 两个新 RPC 没登记 §6.3 scope 表，CI 守卫靠手工清单，测不出来 | CONFIRMED |
| MINOR-2 | minor | Mac | anchor-newer 的 itemsView 校验在 fence 安装之后才做，失败直接返回导致 fence 泄漏 | CONFIRMED（生产触发 PLAUSIBLE） |
| MINOR-3 | minor | iOS | S-5 信封解码用 `try?` 吞掉真实 data 解码错误，再按整帧回退解码（掩盖真实失败） | CONFIRMED |
| MINOR-4 | minor | iOS | 路由扫描器遇到 data 不含 headRev 的帧整体返回 nil，anchor/window 帧走慢路径；对应测试是空断言 | CONFIRMED |
| MINOR-5 | minor | iOS | anchor-newer「≤2 页」上限差一，实际允许 3 个 turns 页 | CONFIRMED |
| MINOR-6 | minor | 双仓 | 协议包两仓漂移：`unified-bridge-protocol.md` 与 read-file-v2 样本 | CONFIRMED |
| MINOR-7 | minor | Mac | S-2 冻结格式（turns key = ordinal‖turnId）与实现（只有 ordinal）不一致 | CONFIRMED |
| MINOR-8 | minor | 双仓 | 测试有效性问题汇总（空断言 / 前置失败 / 缺 metatype 守卫 / 测试锁死缺陷） | CONFIRMED |
| NIT-1…9 | nit | 见下 | bbolt ForEach 内删除；store 从不 Close；冗余校验；双重读盘；粗估尺寸；占位身份；胶囊 UI 小问题；个人路径；pbxproj 抖动 | — |

---

### BLOCKER-1　S-2 后缀折叠损坏 Claude 投影（重启 + 运行期重开均触发；已在生产发生并持久化）

- **仓库 / 引入提交**：Mac，`41523b1f`（S-2）。相关测试：`edab8660`。
- **核心位置**：
  - `agent/claudecode/continuation.go:371-377`：先 `io.LimitReader(file, segment.Cursor)`，后 `file.Seek(from, …)`。LimitReader 的计数从 seek 后的位置开始，所以实际读到的是 `[from, from+Cursor)`，不是注释和接口契约声明的 `[from, Cursor)`（`core/interfaces.go:445-457`）。
  - `agent/claudecode/claudecode.go:1465-1467,1523,1586`：`lineNo` 从**本次 reader 的起点**开始计数；user 行没有 `message.id` 时 ID 是 `user-line-<lineNo>`，assistant 行没有 `message.id` 时是 `assistant-line-<lineNo>`。
  - `go-bridge/handlers_projection.go:1311-1318`：S-2 的后缀分发；`:1967-1970`：user entry 的 ID 直接用作 `turnId`；`:1992-1995`：后缀起点落在 turn 中间时，assistant 以自身 ID 开新 turn，产生虚构 turn。
  - `go-bridge/projection_kernel.go:1165-1173`：组合源**接受 cursor 推进**，以 stored cursors 为折叠基线（S-2 新增）。`:1084`：`wasReadyCatchUp` 要求 `source.Path != ""`，而 Claude 组合源的 Path 恒为空，所以 Ready 会话重新 hydrate 时**一律走 store 恢复 + 后缀折叠**，不走 catch-up。
  - 对照 main：`f868af18:go-bridge/projection_kernel.go:320-327` 的 JSON checkpoint 在组合源 `stored.Cursor != segment.Cursor` 时返回 `composite source cut mismatch → Invalid`，即诚实全量重建。**S-2 改变了这条安全语义。**
- **证据**：
  - (a)【真实样本】生产 transcript `3fd0515b` 的截断副本：user 行 33/33 带行级 `uuid`，0/33 带 `message.id`。也就是说，冷解析出的每个 user turn 的 ID 都是行号派生的，后缀从 1 重新编号属于**确定性碰撞**，不是偶发。（本仓 live relay 一直以 `uuid` 作为 identity：`go-bridge/handlers_relay.go:1710-1720`；行号不是官方身份。）
  - (b)【scratch】`TestReviewSuffixReadRespectsCursorWhenFileGrew`：from=2540、cursor=3820 时返回 20 条，应为 10 条。cut 之后的行被读进了 hydrate。
  - (c)【scratch】`TestReviewS2SuffixFoldDivergesFromFullParse`：cut 落在 turn 边界时，第 3 轮被折进 `user-line-1`（user=second prompt）；cut 落在 turn 中间时出现虚构 turn `msg_C`。两种情况都与全前缀解析不一致。
  - (d)【scratch·生产 handler 路径】`TestReviewS2RestartSuffixFoldE2E`：真实 `claudecode.Agent` + bbolt + `handleGetSessionProjection`。重启后 transcript 增长：
    ```text
    run2（重启+折叠）: user-line-1 user=third prompt asst=first answerthird answer / user-line-3 second…
    诚实重建        : user-line-1 first… / user-line-3 second… / user-line-5 third prompt…
    ```
  - (e)【scratch·不重启】`TestReviewS2InRuntimeRepullFolds`：同一个 Handlers，checkpoint 落盘后 transcript 增长（相当于外部终端 turn），再次拉取，日志为 `checkpointHit=true startCursor=620 startCut=929`，第 3 轮同样被折进 `user-line-1`。`TestReviewS2CorruptionPersistsAcrossCatchUpAndRestart`：损坏后再增长一轮再拉取，第 4 轮折进 `user-line-3`；再次重启（文件不再变化）原样恢复损坏结果（`catch-up repaired=false`、`second-restart matches honest=false`）。**损坏会持久化并持续叠加，后续 hydrate 不会修复。**
  - (f)【生产日志 + 真实样本离线重放】`projection_shadow stage=hydrate_commit backendID=claude checkpointHit=true` 且 `0<startCursor<startCut` 的提交即 S-2 折叠。共捕获 **18 次**：22:14 runtime 的已轮转日志 12 次，`go-bridge.log.1` 5 次，当前日志 1 次。用 `TestReviewS2RealdataReplay` 在按 `startCut` 截断的副本上忠实重放了 13 次（同一 From API、同一 `streamRichHistoryProjectionEntries`、同一 reducer）：

    | 时间（CST） | 会话 | startCursor→startCut | 性质 | 诚实重建/基线/折叠后 turn 数 | 缺失 / 虚构 / 覆盖旧 turn |
    | --- | --- | --- | --- | --- | --- |
    | 10-20 02:23:19 | 3fd0515b | 691528→692791 | 启动后首个 | 17/17/17 | 0/0/0（后缀 0 条） |
    | 10-20 02:23:21 | 608e03af | 27774495→28297173 | 运行期 | 55/55/56 | 0/1/1 |
    | 10-20 03:41:28 | 608e03af | 28297173→29589081 | 运行期 | 57/55/58 | 0/1/1 |
    | 10-20 03:41:39 | 608e03af | 29589081→29598995 | 运行期 | 57/57/58 | 0/1/0 |
    | 10-20 03:41:54 | 608e03af | 29598995→29607900 | 运行期 | 57/57/58 | 0/1/1 |
    | 10-20 22:28:17 | 3b95f2e6 | 1084836→3640487 | 启动后首个【已轮转】 | 22/15/21 | **7/6/2** |
    | 10-20 22:28:45 | ce5ca3c8 | 6896143→11430405 | 启动后首个【已轮转】 | 44/26/44 | **16/16/0** |
    | 10-20 22:32:22 | ce5ca3c8 | 11430405→11522183 | 运行期【已轮转】 | 44/44/45 | 0/1/1 |
    | 10-20 22:32:28 | ce5ca3c8 | 11522183→11527230 | 运行期【已轮转】 | 44/44/45 | 0/1/1 |
    | 10-20 22:47:55 | ce5ca3c8 | 11527230→11954395 | 运行期【已轮转】 | 45/44/46 | 1/2/1 |
    | 10-20 22:48:02 | ce5ca3c8 | 11954395→11957703 | 运行期【已轮转】 | 45/45/46 | 0/1/0 |
    | 10-20 22:52:02 | 608e03af | 46834800→47373871 | 启动后首个【已轮转】 | 113/110/114 | **3/4/1** |
    | 10-21 03:00:11 | 608e03af | 47373871→47398673 | 启动后首个 | 113/113/113 | 0/0/0（后缀 0 条） |

    合计 **11/13 分歧**；没有分歧的 2 次后缀解析结果为 0 条 entry。重放的基线是对 `[0,startCursor)` 的诚实解析，而生产基线本身可能已被前几次折叠损坏，所以表中数字是**下界**。【推断】10-21 03:00 那次的真实基线就是 22:52 损坏折叠的结果（其 `startCursor` 正好等于 22:52 的 `startCut`），因此 608e03af 当前持久化的投影是损坏的。运行中的 runtime 持有 bbolt 锁，没有直接读取以免干扰。
  - (g)【测试有效性】现有测试全部通过（HEAD 上定向运行 27 个 go-bridge 用例 + `TestLoadClaudeContinuationHistoryAtSegmentsFromSuffixEquivalence`，全绿），但对两个缺陷都是盲的：
    - `agent/claudecode/continuation_offset_test.go:30,44`：`Cursor` 取文件末尾，LimitReader 越界在 EOF 前不可见。`:52-57` 只比较 `claudeHistoryDedupKey`，而该函数**刻意排除** `user-line-`/`assistant-line-` ID（`continuation.go:441-450`），所以重编号不可见。
    - `go-bridge/projection_materialization_realdata_test.go:47-96`：投影是 119 个合成 turn，从未解析 transcript，也从未执行折叠，只断言 `LoadValidated` 返回了 stored cursor。
    - exec-plan `s2-materialization-regression` 第 ④ 条自认「新 runtime 上首个真实 Claude 会话打开/重启周期尚未发生……单元/合成/realdata 三层已覆盖」。这三层都不覆盖「折叠结果 == 全量重建」。
- **触发场景**：任何 Claude 会话，只要 transcript 在上次 checkpoint 之后增长，下一次 hydrate 就会触发。常见情形：Mac 终端里的外部 turn 在 iOS 没在看时发生；runtime 重启（含 120 分钟定时兜底重启）；relay 尚未追上就重开。
- **影响**：会话最早的 turn（`user-line-1`、`user-line-3`…）的 user 文本被后来的 prompt 覆盖，assistant 文本被拼接；新 turn 不出现在尾部，或以虚构 ID 出现。损坏结果写回 bbolt（成为下次折叠的基线），并经全量/对账页播种进 iOS S-4 缓存。冷开全量直拉拿到的也是损坏投影。
- **对抗式自证**：
  - "是不是 scratch 构造的人工场景？"——生产日志加真实 transcript 重放复现了 11 次。
  - "后续 catch-up 能不能修复？"——组合源 `wasReadyCatchUp` 恒为 false，复现表明损坏会叠加。
  - "main 是否本来就有？"——main 遇到 cut 不等直接诚实重建。
  - "是不是重放 harness 的偏差？"——重放用的就是生产的 From API、事件转换函数和 reducer；端到端测试走的是生产 handler 路径。
- **建议（仅建议）**：
  1. 立即止血：组合源恢复 main 语义（cut 不等 → `ErrProjectionCheckpointInvalid`，诚实重建），或对 Claude 组合源关闭物化折叠；同时 bump `materializationSchemaVersion`，让已损坏的 checkpoint 失效。
  2. 若仍要后缀折叠：读取边界改用 `io.NewSectionReader(file, from, cursor-from)`；builder ID 改为与偏移无关（行级 `uuid`，与 live relay 一致，需评估 turnId 迁移），或者传入绝对行号基数。
  3. 增加 handler 级「重启/运行期增长 → 与全量重建逐 turn 等价」测试，以及基于 realdata 的折叠重放测试。
  4. 修复后核验 iOS 缓存能否经 anchor-newer token 对账自愈（虚构 turn 需要 `removedTurnIds`）；不能自愈就 bump `claudePreviewRevision`。
- **置信度**：CONFIRMED。

---

### BLOCKER-2　S-3 v2 休眠契约不成立：Claude「加载详细过程」入口仍然有效，且必定失败

- **仓库 / 引入提交**：Mac `a9b09636`（descriptor 声明 + Claude v1 producer），iOS 消费端为 main 既有逻辑。退役提交 `f4b9caef` 没有覆盖这里。
- **证据链**：
  1. 【源码·Mac】`agent/claudecode/wire_descriptor.go:33-37` 声明 `turn_detail_chunks_v1`（注释写明是 S-3 v2 第 4 层资格门）。协议包 `docs/protocol/unified-bridge-protocol.md:270`、§11.7 把它记为「v2 producer 扩展 = claude（随 projection_window_v2 冻结）」。
  2. 【源码·iOS】`Services/Bridge/CCCodeBridgeBackendClient.swift:252-253`：`supportsTurnDetailLazy` 在 descriptor 含 `lazy` 或 `chunks` 时为真。`ViewModels/ChatViewModel.swift:906` 用它驱动入口。`Models/SessionProjectionMapping.swift:274-282`：所有 completed assistant turn 都带 detail 戳。`App/MessageWeb/MessageWebSnapshotBuilder.swift:468-471`：只要 turnId 是 UUID 就显示入口。
  3. 【生产日志】保留日志（`go-bridge.log` + `.log.1`，04:22 统计、04:56 复核未变）中，Claude 日志行里的 `turnId` 有 73 次是 UUID 形态（21 个不同 UUID），3 次为空。当前日志 6 次 `hello_ack` 全部回显 `turn_detail_chunks_v1`（两台设备 `dev_9ea0…` 4 次、`dev_c5ad…` 2 次）。
  4. 【源码·iOS】`ViewModels/ChatViewModel+TurnDetail.swift:113-126`：连接回显 chunks 时走 `loadTurnDetailV2`。`:232-247`：`.failed` 结果不会记入 unsupported 备忘，入口保留，重试会再次失败。Claude turn 没有 `detailInline`（Mac 只在 codex-remote 路径置 `detailPreloaded`：`go-bridge/handlers.go:530-549`、`handlers_projection.go:1771-1773`），所以请求一定会发出。
  5. 【源码·Mac】`go-bridge/session_turn_items_handler.go:143-147`：chunks 连接 v2 优先。`go-bridge/turn_detail_batch_engine.go:434-437`：要求 `core.TurnItemsPager`，否则 `failTerminal("upstream_error")`。全仓只有 `agent/codex-remote/history_paginated.go` 实现了 `ReadTurnItemsPage`。a9b09636 为 Claude 写的 v1 producer（同文件 `runTurnDetailFetch` 内 `:301-320`，从 kernel parts 直接合并）在生产连接上**不可达**。
  6. 【scratch】`TestReviewClaudeTurnItemsOnChunksConnection`（claude / claudecode 两种 backendId）：ack `detailLoadState=failed reasonCode=upstream_error`，内核 turn 被提交为 `detailLoadState="failed"`，会经 patch 广播并随 checkpoint 持久化。
  7. 【测试】`go-bridge/projection_window_v2_test.go:279-291` `TestClaudeDescriptorDeclaresTurnDetailChunks` 只断言「声明存在」，等于把缺陷锁死；没有任何 chunks 连接上的 Claude `session_turn_items` handler 级测试。完成报告 §5 的 Mac 复核命令正好包含这个测试。
  8. 【生产日志】保留日志中 `session_turn_items` 出现 0 次。路径还没有人走过，但**点一下就会触发**。
- **触发场景**：在 iOS 打开任意含 UUID turnId 的 Claude 会话，点已完成 turn 上的「加载详细过程」。
- **影响**：多出一个必定失败的入口；失败态写进共享投影与 checkpoint，所有设备都能看到。按评审指令，「休眠不成立 / 存在意外触发路径」属于**最高优先级**，所以定为 blocker。即使把它当成"未退役的现役功能"，它也至少是 major 级回归。
- **对抗式自证**：
  - "iOS 会不会隐藏入口？"——不会，chunks 声明即可解锁，UUID turn 在生产中存在。
  - "Mac 的 v1 路径能不能接住？"——不能，生产连接全部回显 chunks，v2 优先。
  - "真实 agent 有没有 pager？"——没有，只有 codex-remote 实现。
- **建议（仅建议）**：撤掉或门控 Claude descriptor 中的 `turn_detail_chunks_v1`（例如以"Claude 实现 TurnItemsPager"为前提），并把它列入退役清单；增加 chunks 连接上的 handler 级测试；评估是否清理已写入的 failed 态。
- **置信度**：CONFIRMED（Mac 侧由 scratch 测试证明，测试用 fake agent，真实 agent 同样没有 pager 已静态核实；iOS 侧为静态证据链）。

---

### MAJOR-1　d78100ee 放宽 coverage：尾部本地命令回显 turn 被判 `aborted`，会话被标失败

- **仓库 / 提交**：Mac `d78100ee`。
- **位置**：`go-bridge/claude_relay_coverage.go:277-286`：删掉了 `Assistant == nil` 跳过和「pending user_input」过滤，任何非终态 turn 都进 coverage。`go-bridge/handlers_projection.go:1157-1167`：coverage 在 commit **之前**执行。`go-bridge/projection_kernel.go:1424,1626-1658`：commit 时的「命令回显 → completed」结算会跳过 `aborted`（`:1636-1638`）。`claude_relay_coverage.go:338-348`：dead 分支合成 `turn_aborted(process_death)`，并经 `go-bridge/handlers.go:3398-3413` 记成 `markSettled(failed)`。
- **证据**：【scratch】`TestReviewTrailingCommandEchoUnderCoverage`（真实 agent，transcript 尾部为 `/model haiku` 回显）：
  ```text
  HEAD d3b72f02 : claude hydrate coverage dead; synthesized turn_aborted … turns=1 → turn user-line-4 status="aborted"
  main f868af18 : turn user-line-4 status="completed"
  ```
  【测试】新测试 `claude_coverage_trailing_test.go:11-48` 只测纯函数；2026-10-11 的回显测试 `projection_hydrate_transaction_test.go:895-937` 直接驱动内核，绕开了 handler 的 coverage，所以这条回归测不出来。
- **触发场景**：会话的最后一个 turn 是 CLI 本地斜杠命令回显（`/model`、`/cost` 等，与 2026-10-11 事故同形），Claude 进程已退出（历史会话的常态），之后发生冷 hydrate。
- **影响**：本应结算为 completed 的回显 turn 变成 aborted，会话列表出现错误的失败徽标（徽标链路为静态推断）。这是 2026-10-11 修复的回归。
- **PLAUSIBLE 附带风险**：coverage 覆盖面变大后，更多 hydrate 会碰到 `errClaudeCoverageNoRelay`（`claude_relay_coverage.go:174-178`，fail-closed → `projection.coverage_failed`），前提是 relay 注册与 hydrate 存在竞态。保留日志中出现 0 次，未证实。
- **建议（仅建议）**：`claudeTrailingCoverageNeededTurnIDs` 恢复对命令回显 turn 的排除（复用 `claudeUserTextIsCommandEcho`，或 `Assistant==nil` 加 user 为 `/cmd`）；增加 handler 级回归：真实 transcript 尾部 `/model`，断言 completed 且没有 failed 徽标。
- **置信度**：CONFIRMED（状态）；徽标为静态推断。

---

### MAJOR-2　iOS「清除离线缓存」后，基底缓存写盘静默失败直至 App 重启

- **仓库 / 提交**：iOS `8a813c47`。
- **位置**：
  - `Models/ProjectionBaseCacheStore.swift:207-212`：`clearAllBaseCachesOnDisk()` 删除整个 `CordCodeBaseCache` 目录。
  - `:94`：目录只在 actor `init` 里创建一次。
  - `:188-199`：`save` 把 temp 写进已被删除的目录，失败后 `catch → return`，没有任何日志或 fgTrace。
  - `ViewModels/ChatViewModel.swift:362`：`let projectionStore = ProjectionStore()` 生命周期很长；`Models/ProjectionStore.swift:162-164` 只构造一次缓存 store。
  - `Views/Settings/ServerSettingsView.swift:622`：设置页调用清除。
- **证据**：【scratch·组件】按 `save()` 的同序操作复现：`save FAILED after clear: NSCocoaErrorDomain code=4 …`，且目录在尝试写入后仍不存在。【测试】没有任何测试调用 `clearAllBaseCachesOnDisk`。
- **触发场景**：owner 点「清除离线缓存」后继续使用 App，不重启。
- **影响**：之后每次重开都是冷 miss，全量拉几十 MB，已验收的「重开秒开」失效直至 App 重启，而且没有任何可观测信号。提交信息承诺的「下次打开重新播种」不成立。
- **对抗式自证**："store 会不会被重建？"——ChatViewModel 以 `let` 持有，`init` 之外没有任何 `createDirectory`。
- **建议（仅建议）**：`save` 前确保目录存在（`createDirectory(withIntermediateDirectories:)`），或把清除改成共享 actor 上的方法；失败时打 fgTrace；补一条「清除 → 播种 → 命中」单测。
- **置信度**：CONFIRMED（组件级；按约束没有做真机验证）。

---

### MINOR-1　新 RPC 未登记 §6.3 scope 表（CI 守卫测不出）

- Mac `455d49e4`、`a9b09636`。`go-bridge/handlers.go:1862-1865` 新增分发了 `get_session_projection_window_v2`、`get_session_projection_anchor_newer`，但 `go-bridge/rpc_scopes.go:38-56` 没有这两项。`scopeForMethod` 遇到表外方法返回空串，即无条件放行（`:149-151`，`deviceHasScope` 遇空 scope 返回 true，`:159-162`）。守卫 `rpc_scopes_test.go:14-39` 依赖手工维护的 `dispatchedRPCMethods` 清单，同样漏了这两项，所以 CI 是绿的。
- 影响：目前还没有受限配对，属于潜伏问题；一旦启用受限 scope，这两个方法会绕过按方法鉴权。
- 建议：两表补登记 `session.read`；守卫改为从 `dispatchRPC` 的 case 自动收集，或至少加反射或源码扫描。CONFIRMED。

### MINOR-2　anchor-newer 校验顺序导致 fence 泄漏

- Mac `a9b09636`/`9914cc57`。`go-bridge/projection_anchor_newer_handler.go:137` 先 `BeginProjectionSnapshot`，`:153-168` 的 itemsView 校验失败后直接 `SendResult` 返回，没有 `CompleteProjectionSnapshotError`（对比同文件 `:202` 的正确释放）。
- 【scratch】`TestReviewAnchorNewerInvalidItemsViewLeaksFence`：同一连接、同一会话的下一次合法请求返回 `projection snapshot already pending`，`fenceStillInstalled=true`。机制见 `projection_snapshot_fence.go:109-111`：后续快照全部被拒，live patch 排在泄漏的 fence 后面，直到断连。
- 触发：当前 iOS 不会发 summary 或非法 itemsView（PLAUSIBLE：带非 Claude 摘要种子的旧构建，或畸形客户端）。
- 建议：把参数校验移到 fence 安装之前，或者失败统一走 `CompleteProjectionSnapshotError`。CONFIRMED（机制）。

### MINOR-3　S-5 信封解码吞掉真实错误后回退整帧解码（fallback 掩盖失败）

- iOS `8393f0f1`。`Services/Bridge/CCCodeBridgeClient.swift:268-275` 与 `Services/Bridge/ProjectionWireDecoder.swift:23-27` 用 `try? decode(BridgeRPCResultEnvelope<T>)` 吞掉 `data` 的类型错误，再 `decode(T, 整帧)`。这个回退是有意设计的（`CCCodeBridgeClient.swift:267` 的注释写明「信封解码失败或 `data` 缺失时回退」），但「信封解码失败」与「`data` 缺失」被混在了一起。main（`f0d0f523` 同文件 `:239-252`）在 `data` 存在时会直接抛出真实错误。
- 影响：真实解码错误（例如某个 turn 的字段漂移）被替换成误导性的 keyNotFound；MB 级载荷在失败时要解码两遍；对字段全可选的 T 有静默"成功"的风险（PLAUSIBLE，未逐一枚举）。违反「禁止 fallback 掩盖真实失败」。
- 建议：只在信封缺少 `data` 键时回退；`data` 存在但解码失败时原样抛出。CONFIRMED（错误丢失）。

### MINOR-4　路由扫描器整体返回 nil，anchor/window 帧走慢路径；对应测试是空断言

- iOS `8393f0f1`。`Services/Bridge/BridgeInboundRoutingScanner.swift:153-157`：`extractDataHeadRev` 时如果 data 对象里找不到 `headRev` 或 `projection.syncRev`（`:176-178` 返回 nil），**整个 header 返回 nil**。唯一调用点 `Services/Bridge/CCCodeBridgeTransport.swift:1759` 总是传 true，于是落入 `:1800` 的 `CCCodeBridgeInboundEnvelope` 解码，其中 `data: BridgeAnyCodable?`（`:53`）会构建完整对象图，调用侧再 typed 解码一次。
- 【scratch·组件，编译 HEAD 原文件】full projection → ok；`window page` / `anchor-newer page` / `list_sessions` → `nil → slow envelope path`。也就是说，S-4 暖开主路径的 anchor-newer 页并没有享受到 S-5 单次解析。
- 测试 `OpenCodeiOSTests/SinglePassDecodePipelineTests.swift:236-254` 的 frame "c"（window 形态）断言 `nil == nil`，扫描整体失败也会通过。
- 建议：headRev 缺失时只把 `dataHeadRev` 置 nil，不要让 header 失败；测试分别断言「header 非 nil」与「headRev 值」。CONFIRMED。

### MINOR-5　anchor-newer「≤2 页」上限差一

- iOS `b1952252`/`061d8d05`。`Models/ProjectionRestoreThenIncrement.swift:340-346`：在应用第 n 页之后、请求下一页之前判断 `turnPages <= 2`。第 2 页之后判定通过，继续拉第 3 个 turns 页；如果第 3 页是最后一页（没有 `correctionsPending`），就会以 3 个 turns 页（约 3×4MiB）完成对账。方案 OD-7（owner 定案）为「≤2 页 / >2 页 FallbackFull」（方案 `:698`、`:890`）。没有测试覆盖这个上限。
- 建议：把超限判定移到 `guard let pending … break` **之前**，即每应用一个含 turns 的页后都检查 `turnPages > 2 → .fallbackFull`。这样第 3 个含 turns 的页即使是末页也会回退，而只含 changedTurns 的修正页不计数，与方案「事务内 turns 计入超 2 页判定」一致。注意：如果改成"发起下一页前判 `turnPages < 2`"，会把合法的纯修正续页也拒掉。补边界测试：2 个 turns 页通过；第 3 个 turns 页为末页时回退；2 个 turns 页加修正页通过。CONFIRMED（静态）。

### MINOR-6　协议包两仓漂移

- `docs/protocol/unified-bridge-protocol.md`：Mac 在 `:270` 和 §11.7（约 `:990-1007`）写入了 Claude producer 扩展；iOS 镜像仍是 merge-base 版本（两仓 merge-base 哈希同为 `19ae7630…`，现在 Mac 是 `15c8dc28…`）。
- `docs/protocol/samples/read-file-v2/hello-ack-read-file-v2.json`：Mac `schemaRevision` 为 `2026-10-19`（与 `go-bridge/bridge_v1_schema.go:109` 一致），iOS 镜像仍为 `2026-10-18`。
- `bridge-v1.md`、schema、各 v1/v2 冻结样本、gzip 样本两仓字节一致 ✓。
- 违反「Mac 权威 pack + iOS mirror 同步」。建议同步镜像。CONFIRMED。

### MINOR-7　S-2 冻结格式与实现不一致

- `docs/2026-10-19-s2-materialization-format-freeze.md:33` 冻结的是「turns key = 8B 大端 ordinal ‖ turnId UTF-8」；`go-bridge/projection_materialization.go:117-121` 只用 ordinal。
- 影响：冻结契约失真（Phase B 依赖这个 key 布局）；中间插入 turn 时，后续行全部按 ordinal 错位重写。
- 建议：改实现，或者走修订流程改冻结文档。CONFIRMED。

### MINOR-8　测试有效性汇总（BLOCKER/MAJOR 内已述的除外）

1. iOS `OpenCodeiOSTests/ProjectionWindowV2ConsumptionTests.swift:281-326`：摘要首开停用后，冷开不会设置 `summaryRows`，所以 push snapshot 之前它就是 nil，`XCTAssertNil(summaryRows)` 不可能失败，属于空断言。
2. iOS 只给 anchor 协议做了生产类型 metatype 守卫（`OpenCodeiOSTests/ProjectionOpenHardeningTests.swift:110-115`）。同样被动态转换的 `CCCodeBridgeEpochProviding`（`Models/ProjectionRestoreThenIncrement.swift:153,287`，遵循声明在 `CCCodeBridgeBackendClient.swift:2391`）没有守卫；一旦缺失，epoch 变化检测会静默关闭，与 2026-10-19 P0 同类。
3. Mac `go-bridge/projection_materialization_test.go:257-265`：失败是在事务**之前**触发的（注释自述「事务前失败」），但 exec-plan `s2-materialization-tests` 声称覆盖了「失败 Save 事务回滚」。好在实现是单个 `db.Update`（`:183-258`），所以只是证据夸大。
4. d78100ee 的测试只测纯函数（见 MAJOR-1）；`TestClaudeDescriptorDeclaresTurnDetailChunks` 锁死了 BLOCKER-2。
- CONFIRMED。

### NIT

- **NIT-1** Mac `go-bridge/projection_materialization.go:199`：在 `segments.ForEach` 回调里 `Delete`。bbolt v1.4.3 `bucket.go:580-585` 明确写着「The provided function must not modify the bucket; this will result in undefined behavior」。目前没有影响，因为 `segments` bucket 只写不读（`LoadValidated` 只读 `head.Sources`）。
- **NIT-2** Mac：`ProjectionMaterializationStore.Close`（`projection_materialization.go:110-115`）在生产代码里没有调用者，`Handlers.Shutdown` 不会释放 bbolt flock。`SetDataDir`（`handlers.go:795-808`）重复调用会再开一次库（5s flock 超时后静默禁用）。
- **NIT-3** Mac `projection_materialization.go:354`：在 turns 填充之前对空切片调用 `validateTurnDetailCheckpointFields`，是冗余校验（`:364` 才是有效的那次）。
- **NIT-4** iOS `Models/ProjectionRestoreThenIncrement.swift:45,105`：每次暖开都把同一份最大 4MiB 的缓存从磁盘读两遍、解码两遍（退役编辑遗留）。
- **NIT-5** iOS `Models/ProjectionRestoreThenIncrement.swift:698-723`：`roughTurnSize` 只计 text UTF-8 字节加常数，对工具调用失明。这正是 `ProjectionBaseCacheStore.swift:147-152` 注释里说已修掉的同一个盲区；只影响内存窗口大小，落盘时仍按实际字节裁剪。
- **NIT-6** iOS `ViewModels/ChatViewModel+SessionManagement.swift:874-880`：缓存身份三元组缺失时回退成 `"unknown-bridge"` / `"unknown-backend"` 占位符，与「诚实身份」纪律不符；Claude revision 门也会因此失效。
- **NIT-7** iOS `App/ChatUIKitContainerView.swift:348`：`projectionSyncStatusTrailingConstraint` 未使用。`:5753` 早退 guard 的第二个条件写反了（状态已一致时反而执行动画；无功能影响）。`:2215` 有花括号格式回归 `}            }`。
- **NIT-8** Mac `go-bridge/projection_materialization_realdata_test.go:20`：硬编码个人绝对路径（受 `realdata` tag 保护，不影响 CI）。
- **NIT-9** iOS `8a813c47`：功能提交里夹带 xcodegen `TEMP_…` group UUID 抖动（`project.pbxproj` 2713 行附近）。

### 2.x 检查清单 A–J 覆盖结论

| 项 | 结论 |
| --- | --- |
| A 退役完整性 | iOS 冷 miss 一律全量 ✓（`ProjectionRestoreThenIncrement.swift:60-68`）；任何 backend 的摘要种子都弃用 ✓（`:49-59`）；push 清 `summaryRows` ✓（`ProjectionStore.swift:577,1217`）。**Claude 明细入口未退役 → BLOCKER-2** |
| B 休眠 | window_v2 handler 只在请求时触发（`projection_window_v2_handler.go:34`），iOS 不再发请求 ✓；anchor `itemsView=summary` 只在 summary 种子时发送，已不可达 ✓。**descriptor 声明破坏休眠 → BLOCKER-2**；休眠代码中有一处经参数可达的泄漏 → MINOR-2 |
| C 并发/锁 | 55ms 竞态没有新证据，按指令排除。fence 泄漏 → MINOR-2；actor 与静态清除的交错 → MAJOR-2；bbolt → NIT-1/2 |
| D anchor-newer 双端对账 | 页上限差一 → MINOR-5；fence → MINOR-2；慢路径 → MINOR-4；epoch 守卫缺失 → MINOR-8(2) |
| E direct_gzip / 超时 / 看门狗 | 无发现：Mac `server.go` 在 ≥32KiB 且压缩后更小时才压缩（BestSpeed）；iOS 50MiB 解压上限 = Mac `projectionMaxBytes`；projection 家族 75s 超时 > Mac 写 deadline 60s，且 v2 已纳入看门狗名单（`CCCodeBridgeTransport.swift` diff） |
| F 排序与预算 | 尾部 64KiB 反扫 ✓（`claude_session_activity_tail.go:14`）；「字符数×常数」类：v2 UTF-8 已在前序评审修复（不重复）；iOS 粗估 → NIT-5 |
| G d78100ee 终态反例 | reducer 实际产生的 turn 终态只有 {completed, aborted, error}，集合完整；**反例是命令回显 turn → MAJOR-1** |
| H 测试有效性 | BLOCKER-1(g)、BLOCKER-2(7)、MAJOR-1、MAJOR-2、MINOR-4、MINOR-8 |
| I 完成报告一致性 | 见 §3 |
| J 红线扫描 | 两仓新增行未命中凭据、token、route id、Team ID、私钥模式；fallback 掩盖 → MINOR-3；吞错 → MAJOR-2（`save` 静默）；协议双仓 → MINOR-6；个人路径在代码中只有 NIT-8 一处（文档中的绝对路径是 P0 来源门要求的，设备 UDID 也是 main 上的既有惯例，均不计） |

---

## §3 完成情况报告一致性对账表

对象：`docs/2026-10-20-2026-10-17-claude-long-session-projection-loading-architecture-plan完成情况.md`（下称「报告」）与 `.exec-plan/state/plan-b70583acde4f.json`。

| # | 报告声明（位置） | 实测 | 结论 |
| --- | --- | --- | --- |
| 1 | 方案正文 SHA-256 `88dede7e…a4a388`（L6） | 按方案自带切分规则重算 = `88dede7eeae6…455a4a388`（整文件 SHA `7dbc754a…`，规则排除了交接块） | ✓ |
| 2 | 29/29 todos done（L10） | state 文件 29 项全 done（20 self-attested / 9 re-verified） | ✓ |
| 3 | Mac 共 47 提交（L59） | 生成时 HEAD=66a6de16 → 47；加上报告自身提交 d3b72f02 后为 48 | ✓（自指差 1） |
| 4 | iOS 共 26 提交（L69） | 26 | ✓ |
| 5 | 正式 runtime 内嵌 `5e4d0a97b9c2`（L20） | 现 runtime（02:38 重启，PID 24784）内嵌仍为 `5e4d0a97b9c2` | ✓ |
| 6 | S-2「complete……Release 重启运行态回归」（L34） | 折叠路径损坏投影，已在生产发生；state ④ 自认没有真实重启周期；三层测试都不验证折叠等价 | ✗ → BLOCKER-1 |
| 7 | S-3 打开路径加固「complete」含 `d78100ee`（L39） | d78100ee 让命令回显 turn 回归 | ✗ → MAJOR-1 |
| 8 | 休眠契约：「iOS 不请求即不触发」（L27、L55） | Claude descriptor 仍激活 v2 明细入口，生产可达 | ✗ → BLOCKER-2 |
| 9 | 「清除离线缓存按钮补齐投影基底缓存清理」（L54）；提交信息「下次打开重新播种」 | 能清除，但之后播种静默失败直到重启 | ⚠ → MAJOR-2 |
| 10 | 冷开「S-5 单次解析」（L50） | full projection 帧成立；anchor/window 帧不成立 | ⚠ → MINOR-4 |
| 11 | S-8「接线于 `server.go` / `relay_outbound_writer.go`，契约与等价测试 `direct_gzip_spec_test.go`、`parity_*_transport_test.go`」（L63） | `0bae9bea` 只改了 `server.go`；`relay_outbound_writer.go` 来自 455d49e4/a9b09636；`parity_*_transport_test.go` 本分支没有改过，且与 gzip 无关（Claude fileChanges parity） | ✗（文件归属错误） |
| 12 | `projection_kernel.go` / `projection_snapshot_fence.go`（`d78100ee`）（L66） | d78100ee 实际只改 `claude_relay_coverage.go` + `claude_coverage_trailing_test.go`；kernel 只有 41523b1f 改过，fence 只有 455d49e4 改过；真正的改动文件在报告中没有出现 | ✗ |
| 13 | iOS「`SettingsUI`（`8a813c47`）」（L75） | 实际是 `Views/Settings/ServerSettingsView.swift`（+ `ProjectionBaseCacheStore.swift`） | ⚠（路径不准确） |
| 14 | `docs/protocol/`（bridge-v1.md + schema）……（L67） | 漏列 `unified-bridge-protocol.md` 的改动，且该文件 iOS 未镜像；read-file-v2 样本两仓不一致 | ✗ → MINOR-6 |
| 15 | 「iOS 真机定向 21/21（`ProjectionWindowV2ConsumptionTests` 重写后全集）」（L45、L80；state `s3-v2-retirement` 同） | 该类在 HEAD 上只有 **10** 个 `func test`。21 不可能是"该类全集"（可能是加上 `ProjectionBaseCacheTests` 或 `ProjectionOpenHardeningTests` 各 11 个，无法核实）。另外 DerivedData `/tmp/s3v2-dd` 与 CLAUDE.md「不得……`/tmp` DerivedData」的纪律冲突 | ✗（计数归属不实） |
| 16 | Mac 定向命令 `-run 'TestSummarize\|…\|TestClaudeDescriptor\|TestTailActivity'` → ok（L79） | HEAD 上复跑通过 ✓；但其中 `TestClaudeDescriptorDeclaresTurnDetailChunks` 锁死了 BLOCKER-2 | ✓（证据真实，但证明对象有缺陷） |
| 17 | 排序取尾部 64KB（L53） | `claude_session_activity_tail.go:14` = 64*1024 | ✓ |
| 18 | `claudePreviewRevision` 门（L51） | `ProjectionBaseCacheStore.swift:37,139-142` | ✓ |
| 19 | 摘要种子任何 backend 一律弃用；push 清 `summaryRows`（L52） | `ProjectionRestoreThenIncrement.swift:49-59`；`ProjectionStore.swift:577,1217`（但对应测试是空断言，见 MINOR-8(1)） | ✓（实现）/ ⚠（测试） |
| 20 | 冻结样本 SHA `5c39e17a…`/`8959e1a6…` 两仓字节一致（L55） | 一致 | ✓ |
| 21 | secret scan 两轮全绿（L83） | 没有复跑 gitleaks；本评审的模式扫描未命中 | ✓（部分核实） |
| 22 | 「关键文件变更」（§4）vs diff stat | 报告没有给出规模；实测 Mac 104 文件 +15890/−205（非文档 42 文件 +5222/−47），iOS 47 文件 +5881/−159（App 源码 22 文件 +2616/−143）。清单作为代表性列举可以接受，但 #11/#12/#13 有归属错误 | ⚠ |

---

## §4 总结论

**`FAIL`**

- 阻断理由：
  1. **BLOCKER-1**：生产主路径（Claude 冷开/重开的内核投影）存在已发生、会持久化、不会自愈的数据损坏，完成报告却把 S-2 记为 complete。
  2. **BLOCKER-2**：退役后的休眠契约不成立，存在点一下就触发的失效路径，而且会污染共享投影状态。
- 另有 2 个 major（命令回显 turn 回归；清缓存后暖开失效），以及 8 个 minor、9 个 nit。
- 完成报告有 6 处不一致（✗），集中在 S-2 / d78100ee / 休眠 / 21/21 计数 / 文件归属。
- 不阻断、已核实成立的部分：方案哈希；29/29 队列；runtime 身份；S-8 direct_gzip 与超时链路（E 项无发现）；排序修复；预览 revision 门；摘要种子弃用；冻结样本两仓一致。
- 本评审没有修改任何业务代码，没有构建、安装、部署或 push；仓库内唯一新增的是本报告。所有 scratch 测试与样本副本都在 `/tmp/cr-r1-scratch/`，可按 §0.4 复现。
