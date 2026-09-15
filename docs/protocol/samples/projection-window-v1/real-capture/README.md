# projection-window-v1 真实捕获（canonical windows）

PERF-S4B producer 上线后的**首批真实 wire 捕获**（2026-09-15）。`../window-response-spec.json`
仍为冻结字段集参考（synthetic-spec-fixture）；本目录是它等待的真实证据，采集与替换纪律
对齐 `session-projection-v2` fixtures。

## 来源（provenance）

- **数据**：owner 日常真实 codex 会话的 runtime kernel checkpoint
  （`~/Library/Application Support/CordCode Link/session-projection/checkpoints`，
  只读复制到 /tmp 后消费，未触碰线上目录）。owner 2026-09-15 授权采集
  （"我已经触发了，无需你跑"——真实会话由 owner 使用产生）。
- **backend 谱系（重要）**：盘上 97 个 `backendId="codex"` checkpoint 全部写于
  ≤ 2026-08-25——那是**已退役 legacy file-based `agent/codex` driver**（2026-08-25
  退役；退役影响清单见 macbridge plan-approval 仓
  `docs/2026-09-04-retired-backends-deprecated-migration-impact.md`）时代的 kernel
  遗留。**现役 codex 家族 backendId = `codex-remote`**（API-backed），其 kernel
  状态按设计**不落盘**：checkpoint staging 只覆盖 `source.Path != ""` 的
  file-backed/composite 源（handlers_projection.go），codex-remote 的持久化是
  `.producer.json` 的 R11d producer state。因此离线可得的唯一真实 kernel 源就是
  legacy-era checkpoint。**窗口切片路径 backend 无关**（对 kernel committed
  turns 切片，不触碰 driver），turns/revs 是真实会话投影；样本内游标
  `"b":"codex"` 如实反映采集数据的时代谱系。codex-remote 现役路径的真实 wire
  需活连接捕获，属后续项。
- **采集**：`go-bridge/projection_window_realdata_capture_test.go`（`-tags realdata`，
  `CC_WINDOW_CAPTURE_SPEC` / `CC_WINDOW_CAPTURE_OUT`，与
  `projection_fixture_capture_real_test.go` 同一取证模式）。turns/revs/切片全部来自
  生产 `handleGetSessionProjectionWindow` 对真实 kernel 状态的服务；唯一 stub 是
  kernel 种子复刻 `RestoreCheckpoint` 的验证后提交步（API-backed 源无法跑文件前缀摘要）。
- **捕获时 runtime**：MacBridge `feat/ios-native-message-timeline` @ `0849c52`
  （producer 启用 = `SetProjectionWindowEnabled(true)`，main.go "client first, server
  flip last" 发布门）。

| 类别 | 源 session | turns / syncRev | 捕获面 |
|---|---|---|---|
| `codex-long` | 01a01e96… | 51 / 1980 | window_0（18 turns，**4MB 字节预算真实截断**，limit=20 未满）→ older×2 走到 kernel 底 → locate 中位锚 + 2 错误形状 |
| `codex-mid` | 019f23f6… | 15 / 292 | window_0（整窗 15 turns，hasOlder=false）→ locate + 2 错误形状 |

## 原始（未入库）采集文件 sha256

- codex-long-window0: `af74dbe3b8f5c300357b3be21090019b2be1dfc8f9be5e26dc36069e20897ebf`
- codex-long-older-1: `386f22bc9b15d820de94d1b1e9ec44acebb0953b8f51234fb2be02e7445fd553`
- codex-long-older-2: `09a1b87bb88bf40e130cab29ae74695c7d1a3f916c46ffb770749866763f4d56`
- codex-long-locate-mid: `90fa81ea7cf27504c2a051f6b4e91521b613558f3c69416241109b8373f69083`
- codex-mid-window0: `ce910721d3d672b7183926d5933829284b5cb345b510b649b29d5feed62ad736`
- codex-mid-locate-mid: `072754097e25b0abae02b99299ab0cc3984e327063e079e902a4d06314c4d006`
- 错误形状文件见捕获目录（固定错误码结构；scope 失配样本要求 cursor 形状合法
  ——anchor/side/epoch 齐全——否则 decode 验证先落 cursor_stale（re-freeze：无效
  cursor 共享 stale 恢复契约），捕获不到 scope_mismatch 形状）。

注：捕获含时间戳字段，非逐字节确定性；README 哈希对应用本目录落盘样本的那一次
采集运行（复跑 realdata 采集会得到新哈希，属预期——更新时同步本表）。

## 复核

132 个真实 id（双 session）全量反查零泄漏、无本地路径残留、`gitleaks dir
--redact`（2026-09-15，no leaks）。

## 脱敏边界（sanitization）

iOS 仓 `cordcode-ios/.exec-plan/evidence/native-timeline-pr0/v9/sanitize_window_capture.py`
（规则 = session-projection-v2 fixtures README 冻结三条）：

1. UUID 形态 id（含 `msg_` 前缀）→ 一致性重映射 `01900000-0000-7000-8000-<12位序号>`；
   `call_` 等 opaque token 前缀保留、尾段等长中性化。
2. 长内容 → 等长中性填充（保留换行/空白）；≤40 字符纯 ASCII 枚举/短 token
   （type/status/presentation/toolName 等）原样保留。
3. 结构/键集/类型/基数/syncRev 逐字段保留；时间戳归一固定值。
4. 游标（base64 JSON）：解码 → 内部 id 同表重映射 → 重编码；跨文件锚点保持一致
   （window_0 游标 anchor == 其 headTurnId，older 步间无重叠 turn）。

## 使用

- Go：`go-bridge/projection_window_real_sample_test.go`（解码 + 尾锚/游标一致性/
  无重叠/错误码断言）。
- Swift mirror：`cordcode-ios/docs/protocol/samples/projection-window-v1/real-capture/`
  （byte-identical）+ `ProjectionWindowRealSampleTests.swift` 同组断言。

禁止用合成数据再生成本目录；更新必须重走 realdata 采集并更新本 README 哈希。
