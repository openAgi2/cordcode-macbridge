# 开发 agent 完成报告 1 号（对应监工指令 1 号）

> **接收时间**：2026-08-26T00:00+08:00（owner 经会话粘贴，按双通道契约转存）
> **Verdict（监工）**：见 audit-001

---

**Verdict：全部六批次完成，最终门全绿，owner 真机验收双 PASS；一项 owner 决定待批（§0.3 修订案）。**

## 总纪律执行情况

1. **§3.4 先分歧后代码**：每项修复的「官方实现位置 + 我方实现的第一处分歧」均先写入 `docs/2026-08-25-codex-web-source-parity-audit-fix完成情况.md` 对应批次节，再动代码（可按批次顺序核对文档节与提交时序）。
2. **锚点纪律**：全部改动注释带 codex-rs file:line 锚点或豁免卡编号（§3.2-B1–B5），逐条可 grep。
3. **per-epoch 重构基线**：核实为审计快照时未提交、指令到达前已作为 `202b41c` 入库；批次 1 首笔 `3d84b28` 完成 B1 不变量一致性确认 + 补齐测试（每 epoch 恰一次 / 第二泵经 resolvedByRequest 归属 / DropEpoch 死泵不产出 / history 有界）。
4. **真机验收未代填**：A2/A3 由 owner 2026-08-25 晚真机 PASS（落档 `6b155ae`）。
5. **批次门**：每批定向测试 + `go test ./...` + `go vet`；最终门 0 失败。

## 逐项处置矩阵（上游锚点按新模板 §2.1 格式）

| 项 | 上游锚点 / 豁免卡 | 第一处分歧与处置 | 提交 | 证据 |
|---|---|---|---|---|
| A1 registry | `app_server_requests.rs:74-360` | 官方单视角↔本仓双泵两视角；文件头移植声明+差异清单；history 1024 有界化；clear()↔DropEpoch 对照落注释 | `3d84b28` | 不变量测试 4 用例 |
| A2 permission 乐观收口 | `app_server_events.rs:118-142`（收口唯一路径） | 乐观 publish 溯源 **630fb8d（dsh-web 起源）**波及共享 handler；引入 `core.OfficialResolutionSource` 标记——codex-web 收口唯一真相=官方 `serverRequest/resolved` 双泵投递，dsh-web 保留并补声明。收口源断言测试：resolve 后卡片保持 pending 至官方事件到达 | `e319ea5` | `permission_closure_audit_test` 3 用例 + owner 真机 PASS |
| A3 resync-retry | `app.rs:643-692` + `thread_routing.rs:604-627/683-727` | 原「不重试伪装」与官方算法相反且未声明；移植两失配解析（steer 反引号/interrupt 裸变体）+Missing 分支+重试一次；三源顺序不变（B2 卡） | `cde3cb0` | `control_race_test` 7 用例 + owner 真机 PASS（-32600 不再出现） |
| B5 空 turn.id | `thread_data.rs:352`（Turn.id 必有，非 Option） | 静默归属 ActiveTurn 掩盖契约异常 → 诊断+丢弃 | `8addcb1` | 有观测也零产出用例 |
| C2 plan 终态 | 官方 plan item 无 status 字段；turn.status 唯一终态源 | 无条件合成 "completed" → 从 turn.status 推导（四态单测）；合成 ID 补碰撞注释 | `8addcb1` | 四态映射单测 |
| C1 冷用量 | owner 裁决豁免；pin 结构 `protocol.rs:2094-2164` + `rollout_payload.rs:49-51` | 三层加固：fixture 落盘+形状不符弃用打 warn、版本门控（0.149.x 族）、descriptor 标注 `usage-source: rollout-tail-experimental` | `faa9e92` | 定向 4 用例；**§0.3 修订案草案停等 owner 批准**（完成情况 §3b） |
| B3 连接判定 | 豁免卡（官方无重连） | 文案匹配 → `TransportConnectionError` 类型化（WS close code）>syscall/net>文案兜底+Warn 标记；退避参数（2s×2→60s）与 §8.3 顺序登记入卡 | `27e5f50` | 分类单测（含 %w 链） |
| B4 willRetry | `chatwidget/protocol.rs:127-143` + `notification.rs:54-56` | 官方无计数（on_stream_error 瞬态行）；我方呈现已对齐，`RetryAttempt` 计数登记豁免卡（漂移只影响显示） | `27e5f50` | 卡登记+既有重置用例 |
| 流程项（批次 5） | 审计 §5.1–5.2 | exec-plan `upstream_anchor` 必填字段+证明规则 4；supervise 判据 9（上游分歧锚点问句）；登记簿状态回写 | `ffcbb81` | 模板文件 diff 可核 |
| iOS 抽查（批次 6） | 审计 §6 | 70ce93f=显示级匹配不产 provider 事实（同 bare id 多 provider 错行高亮列为已知边界）；27d9b56=纯应用 get_session 真值零推导。结论回写审计附录 §8，无需整改 | `d18c6ec` | 附录原文 |

## 完成定义核对

- ✅ 全部批次完成情况落档（完成情况文档 + 审计文档 §3 处置状态回写）
- ✅ 审计 §3 十项（A1–A3/B1–B5/C1–C2）状态全部更新（C1=加固完成+修订案待批）
- ✅ 工作树干净（仅 handoff 工件未跟踪）；handoff：`handoffs/handoff-20260825-2348.md`（已更新至验收后状态）
- ✅ 部署：Mac runtime `d18c6eccc29b`（PID 1980, 23:45:21, port 8777，二进制含三处审计标记）+ iOS `b700932` 真机

## 如实报告的事项

1. **门事故**：批次 3 引入的豁免标记撞上 provenance 禁区扫描，批次门 grep head 截断漏看一行 FAIL，final gate 抓获后修复（`provenance_test.go` 白名单化 C1 标记，其余 rollout/store 词汇仍禁区，`d18c6ec`）。教训已记录：门命令应统计 `^--- FAIL` 计数而非 head 截断。
2. **A2 的一个事实修正**：乐观 publish 非本仓 codex-web 发明，系 630fb8d（dsh-web 修复）经共享 handler 波及——处置按共享 handler 条件化而非整体删除，dsh-web 行为保留且已声明（豁免性质，非无声明保留）。
3. **待 owner 决定**：§0.3 修订案（rollout 冷用量豁免登记，五条约束+官方 RPC 后退役）。批准则回写设计文档；不批准则 C1 路径重新裁决。

**证据自查提示（供独立复核）**：提交链 `3d84b28→e319ea5→8addcb1→cde3cb0→faa9e92→27e5f50→ffcbb81→d18c6ec→6b155ae`；`go test ./... -count=1` 0 失败 / `go vet ./...` 干净可复跑；全部锚点可在 pin `536f86e5` 源码 grep 验证。
