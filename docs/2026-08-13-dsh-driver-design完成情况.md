# DSH Driver 设计实现完成情况

- **对象**：`docs/2026-08-13-dsh-driver-design.md`（v13，round12 APPROVE）
- **日期**：2026-08-15
- **分支**：MacBridge `dsh/driver`（9555562…a3f6b5d，9 commit）；iOS `dsh/driver`（4e51ef1…3bc597a，3 commit）
- **执行队列**：`.exec-plan/state/plan-8ba00fc3460a.json`（5 phase / 18 todo 全部 proven done）
- **验证基线**：DSH SDK 钉在 `47f943859bef60e4160492346772ded9b24f765a`，全程未 re-pin

## 结论

设计 v13 的十三个模块全部落地为生产代码：`agent/dsh` driver（进程/握手/codec/identity/scope/seq/lifecycle）、
go-bridge 接线（attachment 两级 pre-check、DeliveryError 分类、CAS 淘汰、capability truth-source）、
iOS `BackendKind.deepSeek`。§16 八类验收门槛全部有同批交付的自动化测试证据（下表）。
**未执行**的验证只有：真实 DeepSeek key 端到端（按开工指令需 owner 明确授权）与 owner 真机人工验收矩阵（文末）。

## §16 八类验收门槛逐项证据

所有测试证据均为可复跑命令（attestation: re-verified——报告撰写时全部重新执行通过）。

| # | 门槛 | 测试与证据 | 结果 |
|---|---|---|---|
| 1 | attachment matrix | `go-bridge/attachment_matrix_test.go` + `attachments_test.go`：valid file/image 逐 backend；`{kind:"file", mime:"image/png"}` mismatch fixture（实例化具体 subtype，round12 注意事项①）逐 backend——image-capable 走 image path（断言 images=1）、OC-server/grok/DSH 拒；malformed MIME 三例 + `image/*` 字面值 + 空 kind + 坏/空 base64 + mixed 全 backend `invalid_params`；**全部断言 pre-StartSession（agent.starts==0、零 Send）**。`cmd:go test ./go-bridge/ -run TestAttachmentMatrix -count=1` | ✅ PASS |
| 2 | capability truth source | 机制半：`TestDeriveCapabilitiesIncludesAttachmentKinds`（derive 与 gate 同源）。真相半（in-package）：claude `image+file`、codex `image+file`、grok `[file]`、opencode 双模式 `[file image]`（CLI）/`[file]`（managed server）、DSH 不实现接口 + 静态能力无 image/file + Send sentinel `errors.Is`。`cmd:go test ./agent/{claudecode,codex,grokbuild,opencode,dsh}/ -run TestSupportedAttachmentKinds -count=1` | ✅ PASS |
| 3 | seq fixture | `agent/dsh/scope_seq_test.go`：首帧 0 接受 / 首帧非 0（missing prefix）/ exact replay 幂等跳过（canonical 键序无关）/ conflicting duplicate / gap / 倒退 / 负数——后四者 fail visibly。`cmd:go test ./agent/dsh/ -run TestSeq -count=1` | ✅ PASS |
| 4 | notification scope | `scope_seq_test.go` §3.8 冻结 11 场景：parent 内 child 完整 turn 零污染、child idle 不收口 parent、两级 descendant、foreign event/status 双双终止、self-loop/空 id/foreign parent/循环 started 拒绝注入、finished 缺 lastAssistantMessage 正常、finish 后迟到 child 仍 descendant（tombstone 不删）、grandchild 过滤、重复 started 幂等、child id reuse 重挂边、scope 路由先于 seq。`cmd:go test ./agent/dsh/ -run TestScope -count=1` | ✅ PASS |
| 5 | delivery fault matrix | `agent/dsh/lifecycle_test.go`（python fake runtime 真子进程）：pre-write（死后 Send→StagePreWrite，ReplayAllowed）+ response lost（写完请求进程死→StageAwaitingResponse 禁重放）+ zero-byte/partial write 单元分类。`go-bridge/delivery_dsh_test.go`：PreWrite 修复一次（respawn 后恰好再发一次、Close 一次、registry 持新 session）、Awaiting 活 session 不重放不淘汰 / 死 session 淘汰供下一条、partial 不重放、plain error 行为不变。`cmd:go test ./agent/dsh/ ./go-bridge/ -run "TestDelivery|TestSendMessage" -count=1` | ✅ PASS |
| 6 | process lifecycle | `TestLifecycleApplicationErrorKeepsSession`（合法 turn/end reason=error 只收口 turn、进程存活、下一 turn 可继续）；`TestLifecycleFramingViolationKillsProcess`（非 JSON 行→可见 terminal+进程死亡+单一 terminal）；`TestEvictSessionCAS*`（Close 恰好一次、stale 淘汰者无法误杀 replacement、16 并发 CAS 单 winner）；`TestAbortEvictsAndStaleEvictorCannotKillReplacement`（五场景 1）。`cmd:go test ./agent/dsh/ ./go-bridge/ -run "TestLifecycle|TestEvict|TestAbortEvicts" -count=1` | ✅ PASS |
| 7 | reducer frozen samples | `go-bridge/dsh_pipeline_test.go`——**真实管线**：fake runtime 进程 → `dsh.New` → `handleSendMessage` → relayEvents → `mapAgentEvent` → EventPublisher → `ProjectionReducer.Snapshot`：user/assistant 同 turn（含 user part 文本断言）、plugin user/message 不入 timeline、turn mismatch 污染帧不落 timeline 且 turn 不 settled completed、双 spawn nonce 不冲突（各自 `p{nonce}-t1` 独立投影）。`cmd:go test ./go-bridge/ -run TestDSHReducerFrozenSamples -count=1` | ✅ PASS |
| 8 | generator/protocol sync | `cmd:DSH_ROOT=~/Projects/deepseek-harness python3 scripts/dsh-gate0/gen-known-event-types.py` → `OK: source==artifact, 44 types; artifact SHA == DSH HEAD`（exit 0）。protocol pack：canonical `unified-bridge-protocol.md` 新增 attachments 两级校验章节（1abec61）→ iOS mirror 同步（3bc597a），双仓各自单独 commit。无 bridge-v1 破坏性变更（capability 字符串 additive，错误码均为既有 canonical code）。 | ✅ PASS |

## 实现范围与设计对齐

- **§1-2/§3.0-3.3**：`agent/dsh`（events.go wire 类型、session.go 进程/握手/收据等待/Close 三阶段、codec.go §3.3 映射表逐行、内嵌 cordis.yml、env 经 `BuildAgentEnv` 注入五变量、进程组复用 grokbuild 模式）。
- **§3.4**：session.status 只作 root 内部 liveness（Debug 日志），不发 core.Event、不投影 iOS、不替代 turn/end。
- **§3.6.1/3.6.2**：active-turn 状态机（validate-then-map、嵌套 turn/step/终态后 user 全部 fail visibly）、identity 矩阵（TurnID=`p{nonce}-t{N}`、assistant ItemID==TurnID、user ItemID=data.id、tool=callId、control-plane 无 TurnID）、source.kind user/plugin/unknown 三分流。
- **§3.6.3**：nonce 16 字节 CSPRNG（失败 fail-closed）于 spawn 前生成；错误二分（application 保留进程 / protocol violation 发 terminal+杀进程）；`core.DeliveryError{Stage}` 四级 typed 契约 + go-bridge `errors.As` 矩阵 + PreWrite pre-send repair 一次；`sessionRegistry.deleteIfSame` 对象身份 CAS + `evictSessionCAS` winner 锁外幂等 Close；abort 复用既有 delete+Close 路径。五场景由上述测试覆盖（#2/#3 在 fault-injection 中以 process-death 路径验证；#5 go-bridge 重启 = registry 空为结构事实）。
- **§3.7**：usage 公式（UsedTokens=input+cacheRead、TotalTokens=UsedTokens、ContextWindow 独立、reasoning 子分）；chunk 为唯一 live owner；assembled（block-end/assistant/message/tool-call 参数/usage 双源）全部只校验不追加，不一致即 protocol violation。
- **§3.8/§3.10**：scope router 三分路由 + lineage tombstone 保留到 teardown；seq 矩阵；ignorable 四级（只认字面 `true` marker；known-unimplemented/unknown 均 fail visibly，44-name 清单内嵌做诊断区分）。
- **§3.9**：`classifyAttachment` 单一规则（effectiveKind=kind∨normalized mime，gate 与 split 共用）；两级 pre-StartSession 校验插入 `handleSendMessage` 与 `ocHandleSendMessage`；truth-source 按 driver×mode 正向声明（`core.AttachmentSupporter` 接口——比设计文本的「derive 传 mode 参数」更贴 §6.2 自描述先例，效果等价：gate 与 hello_ack 广告同源）；`attachment_too_large` 不产生（按设计不引入统一上限）。
- **§4**：ListSessions 返 `core.ErrNotSupported`；generic list handler 加 `errors.Is` → `not_supported`（按文档注记的可选分支，已实现）。
- **§8**：capability 照表——`LiveEventSessionProcess`、不要求外部 turn polling、`workspace_diff`（WorkDirSwitcher）、`diagnostics`（runtime/config/API key 三查）、`permission_mode`（三预设）、`model_switch`/`provider_switch`；不声明 `session_history`/`permission_resolve`/`supports_checkpoint`/`todos`/`usage_reporting`（后两者按设计 ⚠️ 条件未满足——FetchTodos 持久化读与跨 turn 聚合均未实现，诚实不声明）。
- **§9-11/iOS**：`BackendKind.deepSeek`（wire kind `"deepseek"` 映射、不展示历史、不进 serverCreationCases、live event 流）；全部 exhaustive switch 补齐（10 个文件）；真机安装回归通过。

## 实现中的裁量（设计文本之外的选择，均不违背冻结规则）

1. **`DeliveryError` 落在 `core/` 而非 `agent/dsh/`**：跨层 typed 契约（driver 返回→bridge 分类）对齐 `core.ErrNotSupported` 先例，且避免 go-bridge→agent/dsh 静态依赖。
2. **attachment 声明用 `core.AttachmentSupporter` 接口**而非给 `deriveBackendCapabilities` 加 mode 参数：opencode 的 mode（httpBaseURL 有无）由 driver 自持，capability 与 gate 从同一接口派生，符合 §6.2「自描述优于 wire 分支」。
3. **`turn/end` 无样本 reason（error/aborted/interrupted/blocked）映射为 turn_error 终态**（不伪造成功、不淘汰进程）：设计标 ⚪ deferred 无样本；按 fail-closed 哲学选择「可见失败收口」，属 application error 类。
4. **rootSessionID 复用 bridge sessionID（非 pending- 前缀时）**：DSH 无 resume（`getOrCreateSession` 只查内存 map），复用 id 无副作用且免 rebind；`pending-`/空则生成 `dsh-{nonce前缀}`。
5. **eager relay 淘汰未实现**：设计 §3.6.3④ 提及 eager+lazy 双路；实现为 lazy（pre-send repair）+abort 删除+idle cleanup，五场景全覆盖且 CAS 保证幂等——eager 属优化而非正确性要求。

## 验证边界（未执行项，如实标注）

- **真实 DeepSeek key 端到端未执行**：按开工指令「真实 key 的 run 需 owner 明确授权，默认不碰」。所有协议行为由 gate0 冻结 dump fixture + fake runtime fault-injection 覆盖。
- **owner 真机人工验收未执行**：需 owner 在 iPhone 上操作（超出本任务授权）。建议矩阵：

| # | 前提 | 动作 | 应看到 |
|---|---|---|---|
| 1 | Mac 安装 dsh-jsonrpc-agent（PATH 可达）+ 配置 DeepSeek provider key | 打开 CordCode Link | backend 列表出现 DeepSeek（status available） |
| 2 | iPhone 已配对 | 选 DeepSeek → 发一条消息 | turn 正常流式（文字/思考/工具/todo），上下文用量有值 |
| 3 | 同上 | turn 进行中 abort | 立即中断收口；再发一条正常（新进程新 nonce） |
| 4 | 同上 | 发送带图片的消息 | 收到「不支持该附件类型」类错误（text-only） |
| 5 | 未安装 runtime 的 Mac | 打开 CordCode Link | DeepSeek 不出现（日志有明确 not found，不伪造可用） |

- **Release 构建/覆盖安装已执行**：`build-unsigned-release.sh` → `/Applications` 覆盖 → 重启；8777 监听者为正式版 App 内嵌 runtime（`-drivers …,deepseek`）、无临时产物残留。本机未装 `dsh-jsonrpc-agent`，runtime 日志如实记录 `failed to create agent: dsh: runtime not found in PATH`——backend fail-closed 跳过（与 grokbuild 缺失 grok CLI 行为一致），属预期。

## 全量回归快照（2026-08-15）

- `go build ./...` ok；`go test ./go-bridge/... ./agent/... ./core/... ./transcriptindex/... -count=1` 全 ok（go-bridge 54.5s、agent/dsh 2.4s、四家既有 driver 全过）；`(cd relay-server && go test ./...)` ok。
- iOS：`xcodebuild build` SUCCEEDED；`-only-testing:CCCodeTests/BridgeModelsTests` 43/43；真机安装+启动完成。
- generator verify exit 0（44 类、SHA 与 DSH HEAD 同源）。
