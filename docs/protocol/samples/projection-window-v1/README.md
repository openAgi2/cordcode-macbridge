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
