# projection-window-v1 samples

PERF-S4A 冻结协议的 wire-shape 样本（`window-response-spec.json`）。

- **Provenance**: `synthetic-spec-fixture` —— 生产 producer 未实现（freeze-only 单元），
  无真实捕获；本样本冻结字段集/错误码形状。PERF-S4B 首个真实 capture 落地时在同一
  README 记录 raw hash 与 sanitization 边界并替换（对齐 `session-projection-v2` 纪律）。
- **真实 capture 已落地（2026-09-15）**：见 `real-capture/`——S4B producer 上线后首批
  真实 wire 捕获（owner 真实 codex 会话 kernel checkpoint 经生产
  `handleGetSessionProjectionWindow` 服务；window_0/older/locate/错误形状）。本
  synthetic spec 保留为字段集冻结参考，不再作为唯一证据。
- **Canonical rules**: `docs/protocol/bridge-v1.md` §Projection Window（R1–R10）。
- **Schema**: `docs/protocol/schema/bridge-v1.types.ts`
  （`BridgeGetSessionProjectionWindowParams` / `BridgeProjectionWindow` /
  `BridgeGetSessionProjectionWindowResult`）。
- **Decode 验证**: Go `projection_window_spec_test.go` 与 iOS mirror 侧 Swift parity test
  共同消费本样本（双端字段集一致；`turns` 内部形状复用 `BridgeTurnProjection`，由
  session-projection-v2 fixtures 单独覆盖）。

## anchor-newer-response-spec.json（2026-10-18，S-7）

- `projection_window_anchor_newer_v1` 对账增量页的独立冻结样本（canonical =
  `docs/protocol/bridge-v1.md` §Anchor-Newer Incremental Reconciliation；schema =
  `BridgeGetSessionProjectionAnchorNewerParams` / `BridgeAnchorNewerScalarState` /
  `BridgeGetSessionProjectionAnchorNewerResult`）。
- **Provenance**: `synthetic-spec-fixture` —— 生产 handler 未实现（freeze-only）；
  首个真实 capture 落地时按上方纪律替换并记 raw hash。SHA-256：
  `191cc8710262b807d560faa15ae11ee8b4961190068cb3865e1885c0d59c7b71`。
- 冻结内容：首包/续页两种请求形态（`correctionCursor` 续页身份标记；`baseDigests`
  含 `null` 失效语义）；首包响应（`turns` 严格后继 + `anchorRefresh` 仅首包 +
  `changedTurns`/`removedTurnIds`/`scalarState`/`correctionsPending`）与续页响应
  （无 anchorRefresh、可含 turns/changedTurns 剩余片、末页 correctionsPending 缺席）；
  四个 typed error 形状；`turnTokens`（turnId → token）覆盖本页交付全集（full 种子同帧配对纪律）。
- 本文件为**两仓字节一致副本**（iOS 仓同路径）；spec 测试经各自仓内副本的独立
  读取点消费，**不动** `window-response-spec.json` 及其断言。
