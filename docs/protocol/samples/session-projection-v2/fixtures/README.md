# Session projection 三档真实类别 fixture（P0 性能基线）

## 来源（provenance）

三份 fixture 都是**真实 owner session** 经**生产 Kernel hydrate → reducer →
`handleGetSessionProjection` 完整管线**的冷全量投影，由 `go-bridge/projection_fixture_capture_real_test.go`
（`-tags realdata`，与 `handlers_projection_real_test.go` 同一取证模式：只有 agent locator 指向真实
transcript 文件，hydrate/reduce/serve 全为生产代码）于 2026-08-14 采集。session 由 owner 日常真实使用
产生，未新跑任何场景。

| 类别 | 源 session | 类别特征 | 冷全量 | at-head |
|---|---|---|---|---|
| `tool-dense` | 019fef03…（编码长任务，本计划全天测试对象） | 1651 parts、tool 内容 5.4MB（97%）、36 turns | 6,316,346 B / 397ms | 85 B / 0.38ms |
| `oversized-output` | 019ffb5e…（重工具输出任务） | 328 parts、tool 内容 2.9MB、15 turns、历史单 patch delta 达 1.1MB | 3,407,914 B / 183ms | 82 B / 0.10ms |
| `long-text` | 019de83f…（纯文本长对话） | 1049 parts、文本 603KB、tool 0、27 turns | 691,177 B / 37ms | 85 B / 0.57ms |

原始（未入库）采集文件 sha256：

- tool-dense: `4bc00dc817845aa83f16d646fc2f9a2f333f830afd19ef71e623f64348992e48`
- oversized-output: `94113aa7fb67cbf24bac2f0b60b312102df42b6cf6455425a4ca35f8f2c436b2`
- long-text: `31484b96c7a00f62c212d9513df48e8360a40778fb283ffc99b88d02748e5f3c`

## 脱敏规则（只改内容标量，保结构/类型/基数）

1. UUID 形态 id（含 `msg_` 前缀）→ 一致性重映射为 `01900000-0000-7000-8000-<12位序号>`；
   同一原始 id 全文映射一致。
2. 长内容字符串（text/toolInput/toolResult 内的叶子值、标题等）→ 等长中性填充（保留换行/空白结构）；
   ≤40 字符的 ASCII 枚举/短 token（toolName、status、type 等）原样保留。
3. 结构、键集合、类型、数组基数、turn 数、part 数、syncRev 逐字段保留；时间戳归一为固定值。

## 基线（2026-08-14 采集 + 当日真机证据）

冷全量（realdata harness，本机 Kernel 计时）与 at-head 见上表。真机侧（iPhone 16 Pro，Relay）：

- 全量投影端到端（#14 复测窗口）：6.19MB 经 Relay 2.53s 送达、恢复事务 4.4s 完成。
- 稳态增量（P5 后）：operation diff p95 1.6ms、快照构建 p50 0.03ms、16 帧零操作。
- 小 delta（live）：2–5KB patch 为常态；单 turn 大输出 delta 可达 1.1MB（oversized 类）。
- LAN 直连计时未在本轮采集（当日真机全程 Relay）；realdata 计时为无网络的本机管线基线。

## 使用

- Go：`go-bridge/projection_fixture_class_test.go`（canonical 侧解码 + 类别特征断言）。
- Swift：`OpenCodeiOSTests/ProjectionFixtureClassTests.swift`（mirror 侧解码 + 同一组断言）。
- iOS mirror：`cordcode-ios/docs/protocol/samples/session-projection-v2/fixtures/`（byte-identical）。

禁止用合成/截断/手写数据替代或再生成本目录；更新必须重走 realdata 采集并更新本 README 的哈希与基线。
