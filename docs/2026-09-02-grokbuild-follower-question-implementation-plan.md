# Grok Build follower 问题应答实施计划（question-only，裁决 B）

> 事实基线：[docs/2026-09-02-grokbuild-follower-interaction-research.md](2026-09-02-grokbuild-follower-interaction-research.md)
> （评审通过、wire 样本实测、上游语义冻结、§6.1 六点裁决）。本文只写实施分解与验收，
> 不重复取证内容；所有 wire/语义断言以调研文档 §2/§3 为准。

## 0. 来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader
分支=codex/grokbuild-leader-mode
提交=a48e5d9487392fe80d98f02e7a32da4e22ffa258
未提交状态=仅未跟踪 docs/2026-09-02-same-name-ios-device-eviction-fix-design.md（用户另案，不得触碰）
任务预期分支=codex/grokbuild-leader-mode
配套仓库路径/分支/提交=上游只读 /Users/jacklee/Projects/grok-build @ main 72a61251（干净）；
iOS 仓本计划暂不修改（见 §3 红线 5），若 Phase 3 判定需要 iOS 改动，先按 P0 门解析配套
工作树再动
预期产品特性=grokbuild leader 观察模式下，iPhone 能看到 ask_user_question 并回答生效
```

## 1. 目标与范围

**目标**（裁决 §6.1-1，候选 B）：Leader 模式开启时，官方 grok 发起 `x.ai/ask_user_question`
后，iPhone 实时看到问题与选项并可直接回答；应答端到端生效（turn 正常收口）；Mac TUI
抢答时 iPhone 静默收口；iPhone 断线重连后经上游 replay 恢复未决问题。

**本期不做**（§6.1-2/6）：`session/request_permission` / `exit_plan_mode` / `x.ai/mcp/elicit`
的应答；permission 仍维持只读（iPhone 可见 pending 存在即可，不提供应答）。

## 2. 现状根因与既有资产（调研文档 §4 摘要）

- 根因：`agent/grokbuild/leader_subscriber.go:327-338` 方法门把 REQUEST 帧静默丢弃；
  `acp_codec.go:356-362` 对 `pending_interaction`/`interaction_resolved` 落 default 丢弃；
  `session.go:643-650` RespondQuestion 返回 ErrNotSupported（注释已过时）。
- 可复用：自有 turn permission 全链路（handleRequest/handlePermissionRequest/
  RespondPermission/encodeResponse）、bridge-v1 `question_reply` RPC 与
  handleQuestionReply 门（go-bridge/handlers.go:4493）、EventPermissionRequest→wire
  （events.go:181）、reducer requestId 身份核（projection_reducer.go:970）、
  leader rail relay loop（handlers_relay.go:176）。

## 3. 红线（防偏航）

1. **不自建仲裁**：first-answer-wins 由上游保证；本地乐观应答只展示「已提交」；
   `interaction_resolved` 广播是 pending 表清理的唯一真值，turn 收口时强制清理兜底。
2. **wire 契约以实测样本为准**：request 帧形态（半包装 `_x.ai/ask_user_question` +
   params 内联 + 数字 id）、应答帧（原数字 id + accepted 形态）、replay 同 id 同全文——
   实现与测试 fixture 必须逐字取自调研文档 §3；不得从源码推测改写。
3. **迟到应答静默是上游语义**：不得为「已消费 id 再答」造错误帧或本地报错。
4. **半/全包装两形态都要归一化**（调研 §2.1.5 `method_of`/`interaction_inner_params`
   语义），单一形态实现不算完成。
5. **iOS 仓**：先验证复用既有 question wire 后 iOS 是否零改动可用（裁决 §6.1-5 不改
   协议）；确需改动时，先枚举 iOS 工作树、按 P0 配对门确定配套树，iOS 修改单独成
   todo 并双仓构建验证。不得在未解析配对树前动 iOS 源码。
6. **探针纪律**：需要新 wire 证据时零 prompt/零 API 消耗、用完即删；不动
   `~/.grok/config.toml`（裁决 §6.1-2 本期无 permission 采集）。
7. 验证纪律沿用构建成本分级（D2/D3 局部测试 + 交付前一次 Release 集中安装），
   禁止临时产物测试 MacBridge。

## 4. 阶段交付

### Phase 1 订阅侧识别与登记（纯读路径）

- `leader_subscriber.go`：方法门扩展——`_x.ai/ask_user_question`（含全包装形态归一化，
  参照上游 `method_of`/`interaction_inner_params` 容忍集）→ 登记待答请求
  （id + method + params，键 tool_call_id）→ 产出「交互请求到达」事件供上层转发。
- `acp_codec.go`：`pending_interaction`（含 kind）/ `interaction_resolved`（仅
  tool_call_id）两个 sessionUpdate 的映射，作为官方收口与 pending 清理信号；kind !=
  question 的 pending 只投影存在、不进应答链。
- 测试 fixture：调研 §3.1（REQUEST 全文）、§3.2（pending/resolved 广播）、§3.4/§3.6
  （replay 同 id 同全文、post-death 恢复）。

### Phase 2 应答通道与 session 接口（写路径）

- `leader_subscriber.go` 写路径：按登记的原数字 id 编码
  `{"jsonrpc":"2.0","id":<id>,"result":{"outcome":"accepted","answers":{...}}}` 经同一
  socket 回发（复用 encodeResponse 的 Response 编码）；订阅者写路径必须与「observer
  永不回答 agent→client 请求」的旧红线解除方式一并更新架构文档表述——新语义：
  observer 不再对 permission 请求作答，question 请求按本计划应答。
- `session.go`：RespondQuestion/RejectQuestion 实现（accepted / cancelled 形态），
  替换 ErrNotSupported 与过时注释；pending 表以 tool_call_id 为键，收到
  interaction_resolved 或 turn 收口即清。
- 测试：应答帧逐字断言（§3.3 样本）、已消费 id 静默（§3.5）、TUI 抢答后本地清理、
  RejectQuestion → cancelled。

### Phase 3 bridge-v1 投影与 iOS 面

- `go-bridge/events.go` / `handlers.go`：question 事件下发 + handleQuestionReply 接通
  （core.SessionQuestionResponder 门已存在，裁决 §6.1-5 复用既有 wire，无协议包变更）。
- `projection_reducer.go`：requestId = tool_call_id 身份核对；活跃 turn 约束对 leader
  rail 合成 turn 的适配（问题到达必须落在 relay loop 合成的活跃 turn 内，否则排队/
  丢弃策略要显式定义并测试）。
- `handlers_relay.go`：relay loop 内交互事件转发与收口（复用 D-G2 守望模式）。
- iOS 面：真机或模拟器（如需 owner 授权）验证既有 question UI 渲染 grokbuild 问题
  （multiSelect=null 单选、options.description、mode）；字段不适配则按红线 5 走 iOS
  配对门。

### Phase 4 Release 与真机验收

- Release 构建 + 覆盖安装 + 四门核对（pid/lstart/stamp/无残留 + grokbuild available）。
- owner 真机矩阵（交付时提供中文步骤）：①iPhone 实时看到问题并可回答，回答后 turn
  收口；②Mac TUI 先答 → iPhone 静默收口不弹提示；③iPhone 应答后杀 leader/断线重连 →
  replay 恢复未决问题；④Leader 模式关闭 → 行为回退只读（OFF 基线）。
- 文档回写：CHANGELOG、GO_BRIDGE_ARCHITECTURE.md（observer 红线新表述）、
  think.md 总账收口、本计划状态。

## 5. 验收标准

1. 全部 todo 按 exec-plan proof-carrying 三元组完成（impl/tests/regression）；
2. `go test ./agent/grokbuild/... ./go-bridge/... -count=1` 全绿（新增 fixture 全部来自
   实测样本）；
3. Release 四门核对通过 + management API grokbuild available；
4. owner 真机矩阵 4 行全过（或未过行有明确根因与另案登记）；
5. 无协议包变更（若 Phase 3 被迫引入协议字段，视为偏离裁决 §6.1-5，必须先回到 owner）。
