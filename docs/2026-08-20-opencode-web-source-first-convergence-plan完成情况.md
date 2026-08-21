# OpenCode Web source-first convergence 完成情况

> **产品状态（2026-08-21）**：已完成并收口。exec-plan 75/75 proven、supervisor audit-014 verified、owner 真机集中验收 accepted，三条完成轨道全部闭合。

## 1. 权威与范围

- canonical：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`
- 同版本事实源：OpenCode 1.18.18 源码、A1–A10/WP/E1–E7/E1b/E4b/E5b 真实样本及 bridge mapping。
- 已实施并验收：C1–C7、SSV2 单写者/单 ingest、严格 shape、协议 variant/agent 镜像、iOS variant UI、能力真实性，以及 E2b persisted reasoning hydrate 修复。
- direct-SSE reasoning、v2 generation 与 OD-3 future/unsupported 项仍按设计保持未实施。

## 2. Review-fix 收敛链

| 审计 | 发现 | 最终状态 |
|---|---|---|
| audit-008 | C4 双 ingest、question identity、strict shape、variant UI | directive-009/010 修复 |
| audit-009 | question source identity/cold recovery 与 strict tail | directive-010 修复 |
| audit-010 | SSE gap terminal reconciliation 与真实并发缺口 | directive-011 修复 |
| audit-011 | terminal 后 late asked/stale recovery 重开 reply mapping | directive-012 修复 |
| audit-012 | lifecycle 拒绝 GET row 后 lookup 仍无条件返回 | directive-013 修复 |
| audit-013 | 无新增技术缺口 | verified |
| owner UI / audit-014 | persisted reasoning 导致任意 OpenCode session hydrate 失败 | directive-014 修复并 verified；owner 重测通过 |

## 3. 最终三轨状态

| 轨道 | 状态 | 证据 |
|---|---|---|
| exec-plan | ✅ 75/75 proven | `.exec-plan/state/plan-aac740e7f4ae.json` |
| supervisor | ✅ verified | audit-014 |
| owner 真机产品验收 | ✅ accepted for closeout | `owner-acceptance-2026-08-21.md` |

owner 在 iPhone + MacBridge 的真实路径上覆盖了 12 类场景：首轮/多轮消息、历史重进、模型/agent/variant、provider error、Abort、官方 Web 外部 turn、permission、question、Todo Dock、rename/archive/delete、断网重连。第 1–4、6–12 项有逐项 ✅；第 5 项没有单独勾选，本报告仅依据 owner 明确的整体判断“测试结果基本符合预期，可以收尾”将产品轨道关闭，不把它伪记为逐项显式通过。

## 4. 最终技术边界

- OpenCode 1.18.18 是唯一 verified generation；v2/unknown fail closed。
- timeline 只有一个 EventPublisher/Kernel ingest owner；iOS ProjectionStore 是唯一 messages writer。
- question pending、terminal、recovery 与 resolve 由同一 lifecycle admission 控制，terminal 不会回 pending 或被 stale GET 再提交。
- provider/config/agent/todo/mutation 均严格按同版本 shape 处理，无 legacy fallback、递归 shape 搜索或假成功。
- persisted HTTP history reasoning 已支持 hydrate；direct-SSE live reasoning 因缺同版本真实样本继续不实现、不广告。
- directive-014 广域 race 中发现的 Claude 测试 fixture 全局状态竞争可在基线复现，是独立技术债，不阻塞本计划关闭。

## 5. 收口结论

OpenCode Web source-first convergence 计划已完成，监工模式退出，不再下发后续 directive。其他 agent 当前进行中的独立产品改动不属于本计划的完成证据，也未被本次收口提交吸收。
