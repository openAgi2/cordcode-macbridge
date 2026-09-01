# Codex Remote Phase 0：按原门禁此路不通

- 日期：2026-08-28
- 裁决：`FAIL-BLOCKED`
- Owner：确认按当前方案门禁停止产品 backend / iOS 接线；未改门禁、未合入 `main`
- 工作树：`codex/codex-remote-backend`（Mac）+ 冻结配套 `codex/codex-remote-backend-ios`（iOS 不动）

> **状态注记（2026-09-02）**：本裁决是 2026-08-28 时点记录。当日 Owner 改写 Gate P0 进入
> Phase 1，产品路径随后全线落地并经 owner 授权合入 `main`（见 2026-08-26 根方案完成报告）。
> 本文以下 CAUTION 仅对当时生效，保留作历史取证记录。

> [!CAUTION]
> 后续 agent **禁止**把 `codex-remote` 当可实施产品 backend 继续写。不得注册 backend、
> 不得复制 `agent/codex-web`、不得改 `agent/codex`、不得进入 iOS Phase 3。
> 再跑同一条 ping/pong 或再猜 `x-codex-subscribe-cursor` 不会让 Gate 变成 PASS。

## 一句话

官方 Remote Control **会接纳**独立 MacBridge controller。Owner 授权的
attempt-008 证明：对 Desktop 内存中的 thread 发 `thread/resume` 之后，当前打开线程的
Desktop turn 会把 `turn/started`、`item/agentMessage/delta`、`turn/completed` 推到
controller。按**原** Gate P0 **仍不能出货**，因为官方 **不把 reconnect cursor 交给
controller**。产品 backend / iOS 接线仍须 owner 明确开始 Phase 1。

## 已证明（不得再当未知重查）

冻结目标：ChatGPT Desktop `26.825.32147` / bundle `7303` / 内嵌 Codex
`0.150.0-alpha.12.2` / controller protocol v3。

Live fixtures：`agent/codex-remote/testdata/phase0/live/attempt-001` … `attempt-008`。

| 路径 | 结果 |
| --- | --- |
| enroll / refresh / Computer-tab pair | 真链路上成功 |
| 独立非导出 P-256 probe key + WSS challenge/proof | 成功 |
| 选中当前 Mac ChatGPT Desktop environment | 成功 |
| app-server `initialize` + `initialized` | 成功 |
| `thread/list` | 成功（5 items；分页字段 `nextCursor`/`backwardsCursor` **不是** reconnect cursor） |
| Desktop 在探针仍连接时发消息 | 成功打到探针：多次 `thread/status/changed`，env/stream 匹配 |
| 探针只撤销自己 | DELETE 204，随后 refresh/start 403 |
| `x-codex-subscribe-cursor` | **从未出现**在 initialize、thread/list、active pong、Desktop-turn live envelope 上 |
| `thread/loaded/list` + `thread/resume(excludeTurns)` | attempt-008：4 条内存 thread 全部 resume 成功 |
| `turn/started` / item delta / `turn/completed` | attempt-008 **已观察到**：`turn/started` ×1、`item/agentMessage/delta` ×36、`item/completed` ×2、`turn/completed` ×1 |

Host 开源 `codex-rs/.../remote_control` 是 **Desktop 出站 host**，不是 controller 客户端。
Host `ServerEnvelope` 没有 cursor 字段；host 自己的 `x-codex-subscribe-cursor` 不能抄到
MacBridge controller 上。

## 禁止事项

1. 注册 `codex-remote` 产品 backend 或接 iOS。
2. 合成 / 推断 / 静态 schema 提升 reconnect cursor。
3. 修改或重签 ChatGPT App、代理、DNS/TLS MITM、持久化 Desktop/Codex 原始 token。
4. 把 REST 分页 cursor 或 `thread/status/changed` 当成 reconnect cursor 或完整 live turn。
5. 用 standalone app-server、假 relay、JSONL、SQLite、轮询冒充 Remote 接力已实现。
6. 把功能分支合入 `main`（本停工未授权分支集成）。

## 以后只能在这两种条件下再动产品代码

1. Owner **明确开始 Phase 1**（接受无 cursor 的首连 live 流，并把断线续传列为已知缺口或另开证明）；或
2. 官方 target 在新的 owner 授权 live run 中，真的在 WSS envelope 上给出 controller reconnect cursor。

attempt-008 已经把「resume 后有没有 turn/item 流」从未知改成 proven。它**不是** Phase 1
开工令，也不是 cursor 断线续传已证明。

恢复路径：owner 明确开始 Phase 1 后，先
`/exec-plan docs/2026-08-26-codex-remote-backend-implementation-plan.md audit`。
当前产品队列 resume 仍是 `none`。
