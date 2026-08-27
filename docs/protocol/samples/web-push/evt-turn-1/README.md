# EVT-TURN-1 — 真实 `turn_completed` 事件样本（completion 通知 gate 证据）

- **来源**：生产 runtime 的脱敏捕获钩子（`CCCODE_WEB_PUSH_SAMPLE_CAPTURE=1`，运行时自身 redaction：id 记为 `prefix8:len`）。
- **窗口**：2026-08-27T04:25:38Z – 07:34:52Z（6 份样本，同一 session 的 6 个真实 turn，含 owner iPhone PWA 发起的 5 个与本机 web 客户端发起的 1 个）。
- **文件**：`samples.jsonl`（原始脱敏样本逐条）、`analysis.json`（方案 §3.3 双重提取：A=严格 event type+字段路径；B=key-presence+非空候选；6/6 一致，`rawShape.turnId ≡ kernel activeTurn`）。
- **notificationKey 形状**：`opencode-web|<sessionId>|<turnId>|completed`（见 analysis.json；投递期 eventId 集合由 dispatcher ledger 在发送时记录，本批事件 gate 关闭未派发，故为空）。
- **gate 决定**：基于本证据开启 `WebPushKindCompletion` producer（监工指令 3 号 C.3；permission/input/error 维持各自样本门）。
