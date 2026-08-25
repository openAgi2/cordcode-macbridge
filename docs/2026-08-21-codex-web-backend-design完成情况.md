# codex-web Backend v2.0 完成情况报告

- 日期：2026-08-25
- 计划：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)（v2.0 topology-first 施工合同）
- 队列：`.exec-plan/state/plan-c48486da6336.json` — **111/111 todo done（全部 proven）**
- 结论：**codex-web backend 全阶段完成；老 codex backend 已退役并经 owner 真机复测确认。**

## 阶段总览

| 阶段 | 内容 | 关键证据 |
|---|---|---|
| Phase 0 | 证据冻结：目标二进制 0.149.0-alpha.4 schema 生成与交叉核对、官方 call-site 索引、九组脱敏样本、Terminal 三场景 Gate、宿主实测、ownership/重连行为、PASS 裁决 | scripts/codex-web-phase0/（136 断言四套 validator 全 PASS）；docs/2026-08-21-codex-web-phase0-gate-verdict.md |
| Phase 1 | agent/codex-web 纯新增骨架 + 官方生命周期（daemon 复用/拉起/托管 WS）+ JSON-RPC 客户端（epoch/有序事件/服务端请求） | lifecycle/rpc 单测 + 真实 daemon e2e（CODEXWEB_E2E=1） |
| Phase 2 | thread/list catalog、rich history pathless hydrate、SSV2 十一处接线 | 真实 daemon round-trip e2e、重建一致 e2e |
| Phase 3 | turn 生命周期（start/resume/steer/interrupt）、codec 全映射、断线重连冷校准、A/B 帧级对照 | docs/2026-08-21-codex-web-ab-frame-metrics.md |
| Phase 4 | 审批（command/file/permission）+ requestUserInput/elicitation 门控 | 官方 fixture contract tests + 真实服务审批 e2e |
| Phase 5 | 部署、共享 daemon 拓扑 hardening、模型目录 parity、owner 真机矩阵 | 安装态拓扑 snapshot healthy/shared；owner 矩阵全 PASS（2026-08-25） |
| Phase 6 | 并行观察（owner 明示放弃，2026-08-25）→ 退役执行 → owner 退役后复测 | 本报告「退役」节 |

## 交互链验收（owner 真机矩阵，2026-08-25）

command/file/permission 审批允许/拒绝、requestUserInput 作答/跳过/多题、继续/中断、卡片唯一收口，双拓扑（LocalDaemon + 共享 daemon）全 PASS。验收缺陷修复链：Mac `0f524d7..202b41c` / iOS `aeb13d5`。

期间的关键收敛：交互收口从本地乐观 + 多层 fallback 改为**官方 per-pump 模式**——各泵独立消费 `serverRequest/resolved` 广播、kernel 按 interactionId 幂等，删除全部 fallback（202b41c）；审批理由全文经 `ev.Content` 直达 iOS 卡片。

## 退役（Phase 6，2026-08-25）

owner 决策：跳过并行观察窗，直接退役；代码全部保留。

- **Mac** `980d358`：RuntimeManager drivers 列表移除 `codex`（`agent/codex` 与 `-codex-backend` argv 保留无害），行为测试断言翻转（翻转即回滚）。
- **iOS** `b700932`：`BackendKind.codex` isDeprecated + 退出 serverCreationCases（7 处入口过滤生效，既有会话数据不动）。
- **部署验证**：Mac Release（runtime 980d358ea138）冷启后 PID 76570（lstart 22:40:10）持有 8777，`runtime_ready` drivers 无 codex；iOS 真机安装启动。
- **测试**：Mac 行为套件全 PASS；iOS 定向 PASS；全量 CCCodeTests 的 5 个失败经 stash 基线对照证实为分支既有问题（4 个 SessionsViewModelServerSwitch 重试时序 + 1 个 TodoRoute 去重），退役改动零新增失败。
- **owner 退役后复测（2026-08-25）**：backend 列表 codex 消失 ✅、codex-web 会话/交互正常 ✅。
- **回滚通道**：drivers 加回 `"codex"` + iOS 两处枚举还原 + 断言翻转。

## 遗留（非阻塞）

- iOS 既有 5 个测试失败（上述基线对照集合），独立于本计划，待另行修复。
- iOS 仓 `opencode/web` 分支合并（17 文件冲突）由后续 agent 处理；取证背景：main 曾于 08-24 20:50 合并（bdcc3e7）但 45 秒后被 revert（84ccf4b）+ 两次 reset 抹掉，当前分支不含。
- 设计 §15「确认无回滚后另案删除源码」按合同保持独立后续。
