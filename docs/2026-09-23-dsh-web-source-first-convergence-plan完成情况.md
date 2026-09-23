# 本轮任务完成情况：dsh-web source-first convergence 专项方案（S2 第一修复项队列）

> **范围声明**：本报告只覆盖方案 v8（docs/2026-09-23-dsh-web-source-first-convergence-plan.md）
> 中 **S2 第一修复项**（commands/execute 现行 bug 修复）的执行队列——owner 指令明确
> 放行的唯一实施内容（证据独立于 OD-5，方案 §2.3/§9.7-1）。方案整体（S2 第二项跨版本
> 防御、S3/S4/S5）**不因本报告判定完成**：S2 第二项受 A2 门控（alpha.2 座位样本未取得），
> S3/S4/S5 受各自 Gate A 样本门与 OD-2b/OD-3/OD-1 未裁决约束，均保持阻塞/future。

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- Plan: `docs/2026-09-23-dsh-web-source-first-convergence-plan.md`（v8，已过审，本轮未修改）
- Canonical State File: `.exec-plan/state/plan-4fa3de3c08b4.json`
- Legacy State File: none（本队列为新建，origin=new）
- Completion Report Verdict: **proved-complete**（仅就 S2 第一修复项队列）
- Queue Summary: 6/6 todos done，6/6 proven，其中 1 项 re-verified（tests 退出审计复跑）
- Related Commits: none（按 owner 指令未做 commit；工作树提交基线
  `b4e42d5c5a9af6c12abe062a32a30bccece3bd5e`，分支 `feat/ios-native-message-timeline`，
  改动以未提交状态交付）
- Generated At: 2026-09-23T14:55:00+08:00（based_on_queue_hash: `e36c1deb732c`）

## 1. Overall Verdict (总体结论)

S2 第一修复项已完整交付并验证：桥的 `commands/execute` payload 第三参从 `images`
改为 `submittedAttachments: []`（官方无附件调用语义），斜杠命令「执行」路径在运行
座位（dsh 0.1.7-alpha.1）与生产 runtime 两级恢复可用。对账数字：修复前 1/1 payload
被座位 descriptor 拒绝（verbatim：`missing "submittedAttachments"; unexpected
"images"`）→ 修复后 0/2 拒绝、2/2 被接受（1 次未知命令官方 no-op + 1 次真实 `/plan`
完整 settle），生产 runtime 客户端面复测 `execute_session_command /plan` 返回官方
settle（`cmd-560ee2f9-2`，success）。

证据分级如实标注：自动化测试为 **re-verified**（退出审计复跑）；活体对账与生产
runtime 探针为 **self-attested**（一次性运行态取证，证据文件含逐项 verbatim 记录，
但无法由第三方原样重放——座位会话已归档、探针设备已撤销）。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| s2-first-fix | proven-done（payload 修复+注释改写+调用点） | proven-done（旧断言纠正+契约测试钉 alpha.1 形状；全包绿 ×2） | proven-done（活体对账 2/2 + 生产 runtime 复测 + 部署运行态四项） | **proven-complete** | tests=re-verified；impl/回归=self-attested |
| 交付收尾（CHANGELOG） | proven-done | n/a | n/a | proven-done | self-attested |

### 2.1 Upstream Anchors (上游锚点) — port/parity 必填

| Fix / todo | Upstream anchor (file:line) | First divergence vs upstream |
| --- | --- | --- |
| commands/execute 第三参形状（s2fix-payload-impl） | `deepseek-harness@dsh-v0.1.7-alpha.2 (00102833)` `packages/interaction/commands/src/index.ts:354-366`（execute docstring「empty for a plain invocation」+ 签名 `execute(agent, line, submittedAttachments, signal)`，亲核） | 无分歧：桥镜像官方无附件调用语义（恒空数组）。有意差异仅两点——①alpha.1 座位首参仍是 `agentId`（活体证据，非 alpha.2 的 `agent`），桥跟随运行座位；②phase 1 无图命令面，元素类型 `commandSubmitAttachment` 镜像官方 union（`types.ts:15-17` + `attachment/attachment/src/types.ts:85-92`）但非空 wire 未活体取证（S4/A4a 所有），恒不序列化 |
| 契约测试钉 alpha.1 形状（s2fix-payload-tests） | 同上 + 活体 verbatim 样本 `scripts/dshweb-phase0/alpha1-commands-goals-wire.json`（四条 execute 探针，脱敏副本 `agent/dsh-web/testdata/commands_execute_alpha1_wire.json`） | 无分歧：正形/反形断言全部来自归档 verbatim，无手写推测形状 |

## 3. Key File Changes (关键文件变更)

- `agent/dsh-web/commands.go`：`commandsExecuteArgs.Images`（`json:"images"`）→
  `SubmittedAttachments`（`json:"submittedAttachments"`）；元素类型
  `encodedImageAttachment` → `commandSubmitAttachment`（镜像官方
  `CommandSubmitAttachment` union：type 判别 + image 变体 mediaType/data/name +
  file 变体 receiptId）；47-56 行旧注释（rc.2 时点「images:[] 被座位接受」结论）按
  alpha.1 活体证据改写并显式标注被推翻；调用点恒发空数组；文件头版本钉同步去除。
  **禁止事项遵守**：无双发（payload 只有一个第三参字段）、无版本试探。
- `agent/dsh-web/commands_test.go`：`TestExecuteSessionCommandLinePassthrough` 旧断言
  （`rawArgs["images"]=="[]"`）与 2026-09-05 rc.2 注释改为 submittedAttachments（精确
  `[]`、断言无 `images` 键）；新增 `TestCommandsExecuteAlpha1WireContract`（正形
  ok+null no-op、images/agent/缺参三反形 verbatim 拒绝原文、桥序列化键集==正形键集）。
- `agent/dsh-web/testdata/commands_execute_alpha1_wire.json`（新增）：phase0 verbatim
  四条 execute 探针的脱敏副本，契约测试夹具。
- `scripts/dshweb-phase0/s2fix-commands-execute-live-reconcile.json`（新增）：活体对账
  + 生产 runtime 探针证据（对账数字、座位/进程身份、脱敏规则、凭据卫生声明）。
- `CHANGELOG.md`：[Unreleased] Fixed 节补一条（按现有格式）。
- `.exec-plan/state/plan-4fa3de3c08b4.json`：本队列持久化状态。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands: `GOTOOLCHAIN=local go test ./agent/dsh-web/... -count=1`（实施后与退出审计各一次）
- Result: 两次全绿（ok, 20.181s / ok, 22.483s）；定向 commands 测试先绿（3.422s）
- Attestation: **re-verified**（退出审计复跑记录新鲜结果）
- Main test files: `agent/dsh-web/commands_test.go`（含新增契约测试）
- Artifact paths: `agent/dsh-web/testdata/commands_execute_alpha1_wire.json`

### 4.2 Regression evidence

- **活体对账（运行座位 127.0.0.1:3080，PID 38778，alpha.1，探针前后复核进程身份）**：
  一次性隔离会话（`session/create`，cwd=`/tmp/dshweb-phase0-probe`）上——①未知命令名
  no-op 探针 `{agentId, line:/zz-s2fix-probe-noop, submittedAttachments:[]}` → ok:true
  value:null（形状过 descriptor，官方 no-op）；②真实命令 `/plan` 同形状 → ok:true 真实
  settle `{commandId: cmd-560ee2f9-1, result:{kind:success, text:"Plan mode on. Use
  /plan off to leave."}}`；③`workspace/archiveSession {sessionId, stopActivity:true}`
  → ok，membership=true（§3.1 收尾）。对账数字：修复前 1/1 拒绝 → 修复后 0/2 拒绝、
  2/2 接受、1/2 真实 settle。
- **生产 runtime 复测（部署后）**：四项运行态验证全过——①进程代际：runtime PID 30448
  启动 14:14:46 晚于 14:13:18 构建，epoch `db28e1a5`，8777 由新 PID 监听，app/runtime
  均来自 `/Applications`（无临时产物混入）；②本次提交独有特征输出：经管理 API 本地
  配对流创建临时探针设备（用后撤销），以真实客户端面（WS 8777 + device token 鉴权）
  驱动生产 runtime——hello_ack ok（dsh-web available）→ `list_session_commands` 6 条 →
  `execute_session_command /plan` 返回官方 settle `{commandId: cmd-560ee2f9-2,
  resultKind: success, resultText: "Plan mode on. Use /plan off to leave."}`；这是修复后
  runtime 的独有生产行为（修复前同路径返回 execute_failed + descriptor 拒绝原文）；
  生产日志 14:33:23 `pairing websocket claimed` 行佐证，探针窗口无新错误；③dsh-web 行
  available：管理 API `/internal/agents` 确认 status=available（external seat 3080），
  日志 mux open + `$events ready`；④诊断分级：生产路径已验证（生产 runtime 进程/日志/
  客户端面）。
- Attestation: **self-attested**（一次性运行态取证，不可原样重放；证据文件逐项 verbatim）
- Artifact paths: `scripts/dshweb-phase0/s2fix-commands-execute-live-reconcile.json`
  （含 `production_runtime_probe` 节）

### 4.3 Audit downgrade summary

- Downgraded todos: none（6/6 一次通过，无降级、无 review-fix 三元组）
- 退出审计越界检查：方案文档 v8 未修改（保持原未跟踪态）；上游 dsh checkout
  （`dsh-v0.1.7-alpha.2` @ `00102833`）零改动；无 commit/merge/push；改动清单恰为
  预期文件；新文件凭据扫描干净（cookie/token/secret 零出现——管理 token、探针设备
  token、座位 cookie 均运行时读取、未打印未落盘）。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

- **版本漂移边界（已知，方案内登记）**：本修复只钉 alpha.1 形状（运行座位活体证据）。
  alpha.2 源码已把首参改为 `agent`——下一次 dsh 升级会再触发 descriptor 拒绝；届时按
  方案 S2 第二项（A2 门控：先取 alpha.2 座位七面样本）实施跨版本防御，本轮契约测试
  会先红（这是设计意图：fail-visible，不做静默兼容）。descriptor 校验错误继续如实
  上报，无版本试探。
- **非空附件元素形状未活体取证**：`commandSubmitAttachment` 的非空 wire 镜像自
  alpha.2 源码（union），phase 1 恒发空数组、该形状从不序列化；首个真实附件属
  S4（A4a/OD-3 门控）。
- **owner 真机验收待做**：iPhone 斜杠命令面板执行一条命令（如 `/plan`）应成功并显示
  官方反馈文案——生产 runtime 已由探针设备在同一路径预验，真机验收为最终用户面确认。
- 工作树含大量他人在途未提交修改（dsh-web 收敛专项），本队列只做定向编辑、未回退/
  覆盖任何他人改动；`gofmt -l` 列出的三个未格式化文件（apitypes.go、rpcmap_test.go、
  streams_test.go）为在途他人修改，本队列未触碰。

## 6. Audit Focus (建议审核重点)

1. 契约测试夹具是否忠实于 phase0 verbatim（比对
   `scripts/dshweb-phase0/alpha1-commands-goals-wire.json` 四条 execute 探针与
   `agent/dsh-web/testdata/commands_execute_alpha1_wire.json`）。
2. `commands.go` 的 payload 是否无双发/无版本试探（`json:"submittedAttachments"`
   唯一第三参字段；无 images 键残留于 execute args）。
3. 生产 runtime 探针的凭据卫生（探针设备已撤销：管理 API `/internal/devices` 列表
   应无 `s2fix-probe-device-*` 残留；证据文件无 token/cookie）。
4. 对账数字的可复核性（证据文件 `reconciliation` 节 vs 修复前 verbatim 拒绝原文）。

## 7. Constraints (关键约束)

- 方案 v8 冻结未改；S2 第二项（跨版本防御）与 S3/S4/S5 未实施（A2/Gate A/OD 门控，
  缺样本或未裁决即阻塞——方案通过不替代这些门）。
- dsh 自身源码一行未改（owner 2026-09-22 红线）；无 commit/merge/push（owner 未要求）。
- 凭据红线：cookie/管理 token/设备 token 不进日志、诊断、hello_ack 或任何新文件。
- 写入隔离（方案 §3.1）：一切写入只在一次性隔离会话（探针目录），结束归档
  `stopActivity:true`；未触碰 owner 既有会话/工作区；探针设备用后即撤销。
- 构建纪律：Release 构建覆盖安装 `/Applications` 后验证，未用任何临时构建产物测试。
