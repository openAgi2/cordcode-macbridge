# 监工审计报告 1 号（对应监工指令 1 号）

> **审计对象**：开发 agent 对监工指令 1 号（codex-web 源码对齐审计修复）的完成报告
> **审计时间**：2026-08-26T00:01+08:00
> **审计严格度**：严格（独立复跑 + grep 源码 + git show + 部署二进制 strings + 进程核对）
> **Verdict**：**verified**（监工轨通过；≠ owner 真机 done，见 §6 Three-Track）

## 0. Audit Context

- 监工指令：`docs/supervisor-logs/2026-08-25-codex-web-source-parity-audit/directive-001-source-parity-audit-fix.md`（2026-08-25 经 owner 会话下发，本周期补录；后续指令从 2 号起走 skill issue 流程）
- 开发报告：`docs/supervisor-logs/2026-08-25-codex-web-source-parity-audit/report-001-source-parity-audit-fix.md`
- 独立核实命令清单：`git log/show` ×10 提交；`go test ./... -count=1`（全绿）；`go vet ./...`（干净）；`git status`；锚点 grep ×9（interactions.go 头声明/codec.go/events.go/context_usage.go/session.go）；codex-rs 源码核对 ×6（app_server_requests.rs、app_server_events.rs、app.rs:643-703、thread_routing.rs:604-618、thread_data.rs:352、protocol.rs:2072-2161、notification.rs:54-55、chatwidget/protocol.rs:127-143）；`strings` 部署二进制查审计标记；`ps` 运行时进程。
- 设计文档 hash 状态：design_doc_changed=true——审计文档被批次 5/6 回写（§3 处置状态、§5 实施状态、§8 iOS 附录），**属指令明确要求的落档行为，非路线漂移**；关键章节已重读确认裁决未变。

## 1. Overall Verdict（总体结论）

**verified。** 九项处置（A1–A3/B3–B5/C1–C2 + 流程项 + iOS 抽查）全部对应真实提交、真实测试与真实上游锚点；提交链 10 笔逐一核对无误；最终门监工独立复跑全绿；部署声明属实（二进制 mtime 23:45、strings 含三处审计标记、PID 1980 运行中）；A2 的事实修正（乐观 publish 源自 630fb8d dsh-web、经共享 handler 波及）经 `git show 630fb8d` 核实为真，条件化处置（`core.OfficialResolutionSource`）符合指令"若官方路径无法覆盖产品缺口则登记豁免"的分支且声明齐全。门事故自报诚实，provenance 白名单核实为仅放行 C1 标记。三项非阻断发现见 §4，无一项构成驳回。

## 2. 逐项核实矩阵

| 检查项 | 开发自述 | 监工核实方式 | 结论 |
|---|---|---|---|
| 提交链 10 笔（202b41c…6b155ae） | 已提交 | `git log --oneline -14` 逐一比对主题 | ✅ |
| `go test ./... -count=1` 0 失败 | 全绿 | 监工复跑：全部 ok | ✅ |
| `go vet ./...` | 干净 | 监工复跑：无输出 | ✅ |
| 工作树干净（仅 handoff 未跟踪） | clean | `git status --short`：仅 2 个 handoff md | ✅ |
| A1 移植声明+差异清单 | 文件头 | interactions.go:1-12 逐行锚点（:74-80/:89/:97/:201/:305） | ✅ |
| A1 history 有界 | 1024 | `interactionHistoryMaxEntries=1024`（interactions.go:100） | ✅ |
| A2 条件化+标记 | OfficialResolutionSource | e319ea5 diff：接口定义+handler 条件豁免+声明注释 | ✅ |
| A2 事实修正 630fb8d | dsh-web 起源 | `git show 630fb8d`：2026-08-17 fix(dsh-web) 审批/问答同步 | ✅ |
| A2 测试 3 用例 | 3 | permission_closure_audit_test.go：3 个 Test 函数 | ✅ |
| A3 双前缀解析+Missing+重试一次 | 已移植 | events.go:516-526（steer 反引号）、:545-547（interrupt 裸）、:522（Missing）、:586-591（attempt==0 重试） | ✅ |
| A3 官方锚点 | app.rs:643-703 + thread_routing.rs | thread_routing.rs:604-618 实测为 interrupt resync-retry 循环 | ✅ |
| B5 锚点 | thread_data.rs:352 非Option | protocol/v2/thread_data.rs:352：`pub struct Turn { pub id: String }` | ✅ |
| B5 行为 | 诊断+丢弃 | codec.go:158 注释+实现 | ✅ |
| C1 三层加固 | fixture/版本门控/可见性 | faa9e92 diff：`persistedUsageVerifiedCLIFamilies=["0.149."]`、testdata fixture、descriptor 标记、不吻合 warn 弃用 | ✅ |
| C1 锚点 | protocol.rs:2094-2164 + rollout_payload.rs:49-51 | protocol/src/protocol.rs：TokenUsageInfo:2094、TokenCountEvent:2161（恰在区间）；rollout_payload.rs 存在于 history/src | ✅ |
| C1 §0.3 草案停等 owner | 停等 | 完成情况 §3b：草案+「待 owner 批准后回写」 | ✅ 纪律正确 |
| B3 类型化 | TransportConnectionError | session.go:285-292：类型断言优先、%w 链、文案兜底+Warn | ✅ |
| B4 卡 | 豁免卡 | codec.go:442-448：卡内容含官方锚点+「漂移只影响显示」边界 | ✅ |
| B4 锚点 | chatwidget/protocol.rs:127-143 + notification.rs:54-56 | 两者实测吻合（will_retry 分支 / "transient…will not interrupt a turn"） | ✅（路径小瑕疵见 F3） |
| 批次5 skill 改动存在 | 判据9+upstream_anchor | ~/.agents/skills 实测：drift-criteria v4 判据9、state-format upstream_anchor+证明规则4 | ✅（版本化缺失见 F2） |
| 审计文档状态回写 | §3 十项 | grep 处置状态行×6 含 git 哈希；§8 iOS 附录存在 | ✅ |
| 部署 | d18c6ec/PID1980/23:45 | 二进制 mtime 2026-08-25 23:45；strings 含 rollout-tail-experimental 等标记；ps 确认 PID 1980 | ✅ |
| owner 真机记录 | A2/A3 PASS | 6b155ae：diff 显示「未代填」→「owner PASS」的诚实演进 | ✅ 已记录（owner 轨确认权在 owner） |

## 3. 路线图偏离检查（九条判据 v4）

| # | 判据 | 命中? | 证据 |
|---|---|---|---|
| 1 | 删除性测试 | ❌ | 新增 OfficialResolutionSource 是能力标记（多 backend 差异的架构表达），非裁判补丁；A3/B3 均为"删除分歧"而非"叠加补丁" |
| 2 | 三环归属 | ❌ | 全部改动对应审计文档 §3 逐项裁决 |
| 3 | 待删清单特征 | ❌ | B3 恰好移除启发式（文案匹配降为兜底）；无调阈值/加锁/特例 |
| 4 | 半接管最差状态 | ❌ | 无 |
| 5 | 覆盖面诚实 | ❌ | 门事故与 A2 事实修正均自报 |
| 6 | 叙事真实性 | ❌ | 真机验收归 owner 轨记录，无代填；测试监工复跑属实 |
| 7 | 控制变量污染 | ❌ | 每批独立提交、工作树干净 |
| 8 | 根因锁死门 | ❌ | A2 复现固化为 permission_closure_audit_test 三用例后修复 |
| 9 | 上游分歧锚点 | ❌ | 全部修复带锚点/豁免卡，锚点抽检 6/6 命中真实源码 |

## 4. 非阻断发现（follow-up，不影响 verified）

- **F1 用例计数不精确**：`control_race_test` 实为 6 个 Test 函数（解析×2、cancel 重试/再失败、steer 重试、Missing 兜底），报告与审计文档 §3.1-A3 写"7 用例"。覆盖充分，仅计数口径不严。→ 建议下次报告以 `grep -c "func Test"` 口径或注明"含子断言"。
- **F2 批次 5 交付物无版本管理**：supervise 判据 9 与 exec-plan `upstream_anchor` 字段改动落在 `~/.agents/skills/`——该目录**不是 git 仓**（`git rev-parse` fatal），改动真实存在但不可追溯、无审计历史。→ 建议 owner 将 `~/.agents/skills` git 化（或把每次 skill 变更镜像提交到本仓 docs）。本次不作驳回：改动内容已核实且正确。
- **F3 锚点路径不完整**：codec.go:442 引用 `notification.rs:54-55` 未写 crate 前缀，实际位于 `app-server-protocol/src/protocol/v2/notification.rs`（全仓唯一，可定位，内容吻合）。→ 建议 anchor 统一写全路径（可纳入 upstream_anchor 字段格式规范）。

## 5. Partial 处理

不适用（verdict = verified）。

## 6. 给 owner 的下一步（产品语言）

1. **确认两项真机 PASS 依记录成立**：A2（审批允许/拒绝后卡片立即消失）、A3（iOS 停止 Mac 发起的 turn 直接生效、-32600 不再出现）——6b155ae 已记录为 owner 2026-08-25 晚 PASS，若与你的实际体验一致即闭环。
2. **裁决 §0.3 修订案**（唯一 pending 的 owner 决定）：草案见 `docs/2026-08-25-codex-web-source-parity-audit-fix完成情况.md` §3b（rollout 冷用量豁免登记，五条约束 + 官方 RPC 后退役）。批准 → 让开发 agent 回写设计文档；不批准 → C1 路径重新裁决。
3. （可选）F2：决定 `~/.agents/skills` 是否 git 化。
4. F1/F3 可并入下一条监工指令的口径要求，无需单独开工。

## 7. Three-Track done 提醒

- exec-plan proven: n/a（本指令为监工直发任务；批次 5 已为未来 exec-plan 任务接入 upstream_anchor 门）
- 监工 verified: **verified**（本报告）
- owner 真机矩阵 ✅: A2/A3 已记录 PASS（6b155ae），待 owner 本线程最终确认；C1 修订案待批
- 结论：监工轨通过。done 的最终确认权在 owner——三项轨道中 owner 轨仅剩"确认 + 修订案裁决"。
