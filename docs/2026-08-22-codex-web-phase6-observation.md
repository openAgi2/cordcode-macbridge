# Codex Web Phase 6 并行观察与退役证据包

- 设计基线：`docs/2026-08-21-codex-web-backend-design.md` §13–§15
- 状态：`PREPARED`——自动证据和 T1 已入包；完整 owner 矩阵与发布观察窗尚未完成
- 对照对象：`codex-web` 与旧 `codex`，必须使用各自独立的测试 session
- 当前裁决：**禁止退役旧入口**。任何人工项未通过、provider/model/memory 差距未裁决或回滚门未通过时均保持并存。

## 1. 观察规则

1. A/B 使用相同网络、effective provider、model 与同型 prompt，但不得让两个 backend 同时写同一 thread。
2. Thread、Turn、Item、provider/model 和状态只认各自官方 runtime 的返回；不从文件、缓存或界面文字反推。
3. `PASS` 必须附自动命令/文件或 owner 明确结果；`NOT RUN`、`BLOCKED`、历史 `FAIL` 不得折算通过。
4. 先完成自动门，再将所有视觉、交互、网络切换和观察期项目集中交给 owner。
5. 退役只允许先移除旧产品入口；旧源码和回滚通道至少保留一个发布观察窗口。

## 2. 当前证据快照

| 维度 | 当前结果 | 证据 | 仍需 owner 验证 |
|---|---|---|---|
| 单 daemon 拓扑 | PASS | `verify_shared_daemon_topology.sh`；Desktop/CordCode shared peers=1/2；managed-loopback=0 | 已完成 T1，无需重复拓扑取证 |
| 双向 Desktop 接力 | PASS（owner self-attested） | `p0-topology-v2-hardening-regression`；owner 2026-08-22“测试符合预期” | 完整 T3 中仅需观察重连和回滚 |
| 帧级流式 | AUTOMATED PASS | `docs/2026-08-21-codex-web-ab-frame-metrics.md`，同 mock provider 各 5 轮 | LAN/Relay 的真实长回答体验（矩阵 1–2） |
| catalog/history/SSV2 | AUTOMATED PASS | Phase 2 contract/replay 与 pathless hydrate 队列证据 | 长 session、断网恢复后的视觉一致性（矩阵 13） |
| session mutation | PASS（owner self-attested） | rename/archive/delete 修复链与 owner 回报 | 无 |
| model catalog | AUTOMATED PASS | Go model/list；物理 iPhone `CordCodeUnitTests` 三项定向测试 | 官方模型列表视觉等价与 custom provider（矩阵 9） |
| approvals/questions | AUTOMATED PASS | daemon requestUserInput Gate、Go/iOS/web projection tests | allow/reject 与多题提交真实卡片（矩阵 10–11） |
| ownership | T1 PASS | 双端同 daemon 同时打开无 active writer | 旧 backend 独立 session 的冲突诊断（矩阵 12） |
| reconnect | AUTOMATED CONTRACT PASS | reconnect fixtures、epoch/read 校准测试 | live 断网恢复（矩阵 13） |
| rollback | 未执行 | 旧 backend 源码与入口仍保留 | 独立旧 Codex session 回滚（矩阵 14） |

## 3. §15 退役门槛账本

| # | 门槛 | 状态 | 关闭条件 |
|---:|---|---|---|
| 1 | 首帧、连续性、终态不低于旧 backend | PENDING_OWNER | 矩阵 1–2 + Phase 6 观察期 |
| 2 | T0/T1 PASS | PASS | 已闭环 |
| 3 | list/history/mutation/分页无倒退 | PENDING_OWNER | 矩阵 13 与长 session 样本 |
| 4 | iOS ↔ Desktop 双向接力 | PASS | 已闭环 |
| 5 | custom provider | PENDING_OWNER | 矩阵 9 |
| 6 | approvals/questions/interrupt/error/usage | PENDING_OWNER | 矩阵 10–11，并核对 interrupt/error/usage |
| 7 | ownership 冲突可诊断且不抢占 | PENDING_OWNER | 矩阵 12 |
| 8 | daemon/Bridge/LAN/Relay 重连 | PENDING_OWNER | 矩阵 2、13 |
| 9 | stable/experimental 兼容策略 | AUTOMATED PASS | schema/fixture/version fail-closed 证据保持有效 |
| 10 | 旧 backend 回滚 | PENDING_OWNER | 矩阵 14 |
| 11 | provider/model 能力对照 | PENDING_OWNER | 矩阵 9；无 provider 切换 API 的差距需 owner 明确接受 |
| 12 | memory 能力对照 | PENDING_OWNER_DECISION | deliberately unsupported 差距需 owner 明确接受或另案实现 |

## 4. 最终集中人工批次

人工阶段统一使用 `docs/2026-08-22-codex-web-owner-device-acceptance.md` 回报。T1 的 6–8 已通过；集中执行：

- 流式/宿主边界：1–5；
- custom provider/model：9；
- approval 与 requestUserInput：10–11；
- ownership、断网恢复、旧入口回滚：12–14；
- 完成上述功能矩阵后，继续一个发布观察窗口，记录日常使用中的资源、重连和回滚情况。

任一行失败时保持旧入口，不进入退役实现；按 exec-plan 新建对应 review-fix triplet。

## 5. 证据索引

- `docs/2026-08-22-codex-web-owner-device-acceptance.md`
- `docs/2026-08-21-codex-web-ab-frame-metrics.md`
- `scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md`
- `docs/2026-08-22-codex-web-userinput-daemon-gate.md`
- `docs/evidence/2026-08-22-codex-web-phase5-deploy-check.md`
- `.exec-plan/state/plan-c48486da6336.json`

