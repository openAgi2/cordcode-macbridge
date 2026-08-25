# 监工指令 1 号：codex-web 源码对齐审计修复（六批次）

> **下发时间**：2026-08-25（经 owner 会话转发给开发 agent；本文件为 skill 流程补录，全文与下发原文一致）
> **依据文档**：`docs/2026-08-25-codex-web-source-parity-audit.md`（下称"审计文档"）
> **kind**: implementation

---

第一动作：通读审计文档，所有行号、官方锚点、处置裁决以它为准，本指令只定顺序和验收。

背景：owner 审计确认 codex-web 存在三类对齐问题——A=官方有算法但实现漂移；B=架构必需的发明但无豁免卡；C=红线违规。逐项裁决见审计文档 §3。

总纪律（违反任一条 = 该批次退回重做）：
1. 设计文档 §3.4：每个修复先在完成记录写明「官方实现位置 + 我方实现的第一处分歧」，再动代码。禁止在旧假设上叠第二个补丁；一次修复未消除现象必须停线回到官方调用链。
2. 每处改动的代码注释必须记录上游锚点（codex-rs file:line）或豁免卡编号（审计文档 §3.2 的 B1–B5）。
3. 工作区当前未提交的 interactions per-epoch 重构是 A1/B1 的基础：先确认它与审计文档 B1 不变量一致、补齐测试，作为批次 1 的第一笔提交。
4. 真机相关验收保留 owner，禁止代填 PASS。
5. 每批完成：定向测试 + go test ./... + go vet 通过，完成情况落档（沿用「完成情况」文档惯例），再进下一批。

━━━ 批次 1：收口对称性与小修 ━━━
1a. A2 permission 乐观收口清算（go-bridge/handlers.go handleResolvePermission）
    - 先复现：注释掉乐观 publish 后走允许/拒绝审批，确认是否复现"卡片不消失"（原注释声称的 SSV2 重映射卡 UI）；
    - 若复现：在 per-epoch resolvedEvents 对 permission kind 的双泵投递链上找第一处分歧修掉（参照 user_input 修复范式：agent/codex-web/interactions.go resolvedEvents）；
    - 删除乐观 publish 与任何派生 status。若最终证明官方路径无法覆盖产品缺口，按 B 类登记豁免卡，不允许无声明保留；
    - 验收：允许/拒绝后卡片立即收口；新测试断言收口事件源 = serverRequest/resolved 驱动。
1b. A1 InteractionRegistry（agent/codex-web/interactions.go）
    - 文件头补移植母本声明（tui/src/app/app_server_requests.rs:74-360）+ 差异清单（官方单视角 ↔ 本仓双泵两视角）；
    - history map 有界化（对齐 resolvedByRequest 的 1024 全清策略）；
    - 对照官方 clear() 语义审查 epoch/会话重置路径是否等价覆盖；
    - 补不变量测试：每 epoch 恰发一次收口事件；第二泵晚到仍可经 resolvedByRequest 归属；DropEpoch 后旧 epoch 不再产出。
1c. C2 plan 合成终态（agent/codex-web/history.go mapHistoryItem）
    - plan 卡 status 改为从官方 turn.status 推导（turn completed → completed，否则 unknown），删除无条件合成 "completed"；
    - system note 合成 ID 补碰撞不可能性注释；加 plan 状态映射单测。
1d. B5 turn/completed 缺 turn.id 归属（agent/codex-web/codec.go:157-163）
    - 核对 pin 源码 protocol/v2/turn.rs 的 CompletedParams：turn id 若必有 → 改为诊断日志 + 丢弃（不静默归属）；若存在合法缺失场景 → 登记豁免卡。

━━━ 批次 2：官方算法回迁 ━━━
2a. A3 steer/interrupt 失配 resync-retry（agent/codex-web/events.go:481-526）
    - 移植 tui/src/app.rs:643-703：解析两种失配消息（steer 前缀 "expected active turn id `X` but found `Y`"；interrupt 前缀 "expected active turn id X but found Y"）+ "no active turn to steer" Missing 分支；重同步 turn 身份后重试一次；
    - 三源观测（liveCodec > 本端 start 返回 > 冷基线）保留为首选身份，失配 resync 作为权威纠正；冷基线扫描降为最后手段；
    - B2 豁免卡（不变量/失败模式）补进审计文档 §3.2 并在代码注释引用；
    - 验收：两种失配解析 + 重试单测；正常路径不再因过期 local id 报 -32600。

━━━ 批次 3：C1 冷用量加固（owner 已裁决"保留并加固+设计修订"）━━━
    - contract fixture：按 pin 源码 rollout 记录结构 + 真实脱敏样本冻结 token_count 形状；不吻合 → 弃用文件路径并打诊断（不静默回退）；
    - 版本门控：initialize 记录的 server/CLI 版本与已验证版本族不匹配 → 不走文件路径；
    - 可见性：诊断/descriptor 标注 usage-source: rollout-tail-experimental；解析失败记 warn；
    - 起草设计文档 §0.3 修订案（登记为记录在案的豁免，待官方 RPC 后退役）：修订段落写好后停下等 owner 批准，代码加固可先行提交。

━━━ 批次 4：B3/B4 ━━━
    - B3：isConnectionLoss（agent/codex-web/session.go:284-296）从错误文案匹配改为 transport 层结构化分类（WS close code / 类型化错误），文案匹配仅兜底并打诊断标记；豁免卡登记退避参数（2s→60s）与 §8.3 冷校准顺序；补分类单测。
    - B4：核对官方 TUI 对 willRetry error 帧的呈现方式，对齐之，或登记豁免卡说明 iOS 需要什么官方不提供的信号。

━━━ 批次 5：流程项（改约定，不改产品代码）━━━
    - exec-plan 任务模板与完成报告模板加必填字段「上游锚点」（codex-rs file:line 或豁免卡编号），缺字段 = 任务不可标 done；
    - supervisor 固定问句清单加入 §3.4 问句："这个修复与官方调用链的第一处分歧在哪里？"；
    - 审计文档即豁免卡登记簿：此后新增架构性发明必须先登记（含不变量与失败模式）再实施。

━━━ 批次 6：iOS 抽查（轻量，可并行）━━━
    - iOS 仓（codex/codex-web-backend-ios 分支）按审计文档 §6 抽查两处：模型目录归一化（70ce93f）、会话控制面取值（27d9b56）；确认 iOS 只消费 bridge 事实不自行推导；结论回写审计文档附录，不单开文档。

完成定义：全部批次完成情况落档；审计文档 §3 每项状态更新（处置完成 / 豁免卡登记）；工作树干净可交付；按 handoff 规范写交接。
遇到与审计文档事实冲突的现场证据：停线、记录证据、按 §3.4 回官方源码定位、回写审计文档后再继续——不允许就地打补丁绕过。
