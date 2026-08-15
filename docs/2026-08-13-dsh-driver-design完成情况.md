# DSH Driver 设计实现完成情况

- **对象**：`docs/2026-08-13-dsh-driver-design.md`（v13，round12 APPROVE）+ 2026-08-15 owner 产品决策附记（探测形态指令 v2）
- **日期**：2026-08-15（首版报告同日；本版为 p1–p7 终态收敛重写）
- **分支**：MacBridge `dsh/driver`（9555562…2755a7f，16 commit）；iOS `dsh/driver`（4e51ef1、3bc597a，2 commit）
- **执行队列**：`.exec-plan/state/plan-8ba00fc3460a.json`（7 phase / 24 todo 全部 proven done，hash e26aaed664a8）
- **协议基线**：设计 pin `deepseek-harness@47f9438`（协议 serverInfo 0.0.1）；实测运行面为本机安装的 `@deepseek-ai/dsh@0.1.0-rc.6`（用户真实形态）。全程未 re-pin、未改动 checkout。

## 结论

设计 v13 的十三个模块全部落地为生产代码；runtime 侧按 owner 指令 v2 收敛为**探测-复用-未启动**
形态（探测用户已装 harness、复用其凭据、永不代装）。§16 八类验收门槛全部有同批交付的自动化
测试证据；产品路径（用户全局 npm dsh + 影子树 + vendor SDK 层）在本机以 mock key 完整实测
通过，正式版 App 日志实证 backend 注册。**未执行**项只剩：真实 DeepSeek key 的 turn（owner
按文末矩阵在 iPhone 验收）。

## 交付物总览

| 层 | 内容 |
|---|---|
| `agent/dsh`（driver） | 进程/握手/codec（§3.3 映射、active-turn 状态机、identity 矩阵、source.kind 分流）；scope/lineage + seq/ignorable（§3.8/§3.10）；nonce/typed death/at-most-once（§3.6.3）；usage 公式 + chunk/assembled 双写 peer 校验（§3.7）；探测链 + 影子树 + 凭据链（指令 v2） |
| `agent/dsh/vendor` | SDK stdio 层四包 rc.6（server/protocol/demo 胶水/agent-spine-demo），npm pack 原样 vendor（MIT），来源注明 |
| `core` | `AttachmentSupporter` 接口、`DeliveryError{Stage}` typed 契约 |
| `go-bridge` | attachment 两级 pre-StartSession 校验（`classifyAttachment` 单一规则）；handleSendMessage 交付分类 + CAS 淘汰；capability truth-source 同步（五 backend）；detectDSHRuntime 与 driver 共用探测；generic list `not_supported` 分支 |
| iOS | `BackendKind.deepSeek`（wire kind `deepseek`）+ 10 文件 exhaustive switch 补齐 + 43/43 单测 + 真机安装回归 |
| 产物 | `known-event-types.txt`（pin 44）+ `known-event-types-rc6.txt`（本机安装包实测 44）双清单，gen 脚本 `--package-source` 模式 |
| 协议 | canonical `unified-bridge-protocol.md` attachments 章节（additive，无 wire 破坏）→ iOS mirror 同步 |

## 产品形态终态（owner 指令 v2）

**用户效果**：`npm i -g @deepseek-ai/dsh` + `dsh web` 存过 key → CordCode Link 的 DeepSeek
直接可用，零额外安装/配置；未装任何形态 → backend 如实「未启动」。MacBridge 永不安装、
编译、下载任何 runtime，永不要求 MacBridge 侧 key 配置。

- **探测链**（probe-only，先命中先得）：① PATH `dsh-jsonrpc-agent`（用户显式安装）→
  ② **用户全局 `@deepseek-ai/dsh`**（`npm root -g` 只读查询 + homebrew/usr-local/nvm/pnpm
  已知根；dsh 与 dsh-app-boot 存在且 entry 可解析才命中，双版本进日志）→ ③ pip wheel
  （`dsh-jsonrpc-agent-pkg-<plat>` + Python Resolution API，3s 有界）→ ④ nvm 最新版 bin →
  ⑤ 源码 checkout 仅 `DSH_DEV_SOURCE_ROOT` 显式 opt-in（标注 dev-only，源码只是参考材料）。
- **vendor SDK 层 + 影子树**（②的运行方式）：已安装 dsh@rc.6 的 61 依赖/194 嵌套包**不含**
  dsh-sdk-jsonrpc-server/-protocol/-demo 与 dsh-agent-spine-demo（机器实测，见「事实修正」）——
  四包 rc.6 vendor 入仓；driver 数据目录构建影子 node_modules：四包真实文件 + 其余 family
  全量 symlink 到用户全局树（Node 默认 realpath 语义解析回用户已装版本）。spawn：
  `node <shadow>/…/dsh-sdk-jsonrpc-demo/lib/bin.js` + `DSH_CORDIS_CONFIG`（上游 runner 官方
  env 通道）；cwd/DSH_CWD/DSH_SESSION_ROOT/DSH_PERMISSION_MODE 注入沿用 driver 逻辑；
  不写用户全局目录、`~/.dsh` 只读。
- **凭据链**（镜像 DSH 自身信任序）：MacBridge provider key（显式）> `$DSH_HOME/
  .credentials.yaml`（dsh Web UI Models 页写入的严格扁平映射，最小解析子集、flow 集合诚实
  miss）> `$DSH_HOME/.env`；per-key 独立下落；自定义 `DSH_HOME` 转发子进程。注入走 env——
  DSH 层级里 inherited env 本就第一，语义等价于 runtime 自解析。
- **版本兼容边界**：vendor 胶水针对 rc.6 的 app-boot `boot()` 签名；用户 dsh 升级导致签名
  漂移时 spawn fail-closed 呈现，不静默。

## §16 八类验收门槛逐项证据

（attestation: re-verified——本报告撰写时全部重新执行通过）

| # | 门槛 | 测试与证据 | 结果 |
|---|---|---|---|
| 1 | attachment matrix | `go-bridge/attachment_matrix_test.go`+`attachments_test.go`：valid file/image 逐 backend；`{kind:"file",mime:"image/png"}` mismatch（实例化具体 subtype）逐 backend——image-capable 走 image path（断言 images=1）、OC-server/grok/DSH 拒；malformed MIME 三例+`image/*` 字面值+空 kind+坏/空 base64+mixed 全 backend `invalid_params`；**全部断言 pre-StartSession（starts=0 零 Send）** | ✅ PASS |
| 2 | capability truth source | 机制半：derive 与 gate 同源测试；真相半（in-package）：claude/codex `image+file`、grok `[file]`、opencode 双模式 `[file,image]`/`[file]`、DSH 不实现接口+静态能力无 image/file+Send sentinel `errors.Is` | ✅ PASS |
| 3 | seq fixture | `scope_seq_test.go`：首帧 0/首帧非 0（missing prefix）/exact replay 幂等（canonical 键序无关）/conflicting duplicate/gap/倒退/负数——后四者 fail visibly | ✅ PASS |
| 4 | notification scope | 同文件 §3.8 冻结 11 场景：parent 内 child 完整 turn 零污染、child idle 不收口 parent、两级 descendant、foreign event/status 双终止、self-loop/空 id/foreign parent/循环 started 拒注入、finished 缺 lastAssistantMessage 正常、finish 后迟到 child 仍 descendant、grandchild、重复 started 幂等、id reuse 重挂、scope 先于 seq | ✅ PASS |
| 5 | delivery fault matrix | driver：python fake runtime（pre-write/response lost/zero-byte/partial write 分类）；bridge：PreWrite 修复一次（respawn 后恰好再发一次、Close 一次、registry 持新 session）、Awaiting 活 session 不重放不淘汰/死 session 淘汰供下一条、partial 不重放、plain error 行为不变 | ✅ PASS |
| 6 | process lifecycle | application error（turn/end reason=error）收口 turn 保留进程且下一 turn 可继续；framing 违例可见 terminal+进程死亡+单一 terminal；CAS Close 恰好一次/stale 淘汰者无法误杀 replacement/16 并发单 winner；abort 路径 | ✅ PASS |
| 7 | reducer frozen samples | `dsh_pipeline_test.go` 真实管线（fake runtime 进程→`dsh.New`→handleSendMessage→relayEvents→mapAgentEvent→EventPublisher→ProjectionReducer Snapshot）：user/assistant 同 turn、plugin 不入 timeline、turn mismatch 污染帧不落 timeline 且不 settled、双 spawn nonce 不冲突 | ✅ PASS |
| 8 | generator/protocol sync | 双清单 verify exit 0：pin 源（44 类+SHA 同源）+ 本机安装包 `--package-source`（44 类）；canonical protocol pack attachments 章节（1abec61）→ iOS mirror（3bc597a）；无 bridge-v1 破坏性变更 | ✅ PASS |

## 两处事实修正（实测证据，偏离指令「已验证事实」，如实报备）

1. **SDK stdio 层不在用户 dsh 闭包内**：指令事实 #2 称「全 family 闭包都在」——family 21 个
   必需包确实全在全局树（含 schemastery/cordis），但 `dsh-sdk-jsonrpc-server`/`-protocol`/
   `-demo`/`dsh-agent-spine-demo` 不在 dsh@0.1.0-rc.6 的 61 项依赖、194 个嵌套 @deepseek-ai
   包中（已装 package.json 与目录双核对）。因此「vendor 启动器」扩展为 vendor 这四包——
   这是对指令的唯一实现扩展，机制（vendor、复用全局树、零安装）完全在指令框架内。
2. **rc.6 事件清单实测仍为 44 类**：指令事实 #4 预期 44→39（移除 tool-workflow×4 +
   agent-preset/selected）；本机 rc.6 `dsh-session` 的 `KNOWN_SESSION_EVENT_TYPES` 实测
   Set(44)（node 动态导入）且编译字面量 44 条（grep 双证据），五类仍在。两种结果对 driver
   均无风险（③ 类清单为 44 全集，无 fail 误触发）；第二 artifact 以实测为准。

## 实现裁量（设计/指令文本之外的选择，均不违背冻结规则）

1. `DeliveryError` 落 `core/`（跨层 typed 契约，对齐 `ErrNotSupported` 先例）。
2. attachment 声明用 `core.AttachmentSupporter` 接口（opencode 模式自持），capability 与 gate 同源。
3. `turn/end` 无样本 reason（error/aborted/…）→ turn_error 收口不伪造成功、不淘汰进程。
4. rootSessionID 复用 bridge sessionID（非 pending- 前缀时）；DSH 无 resume，无副作用。
5. eager relay 淘汰未实现：lazy（pre-send repair）+abort+cleanup 已覆盖五场景，CAS 保证幂等。
6. 影子树 symlink 而非复制 family（realpath 解析回用户版本；用户升级 dsh 后重启即生效）。

## 演进与撤回记录（如实）

- p6 曾按「自动搞定」实现 managed runtime（MacBridge 自行 `npm install` 钉版本项目）——owner
  指令 v2 明确**禁止代装**后全数删除（`ensureRuntimeProject`/`npmInstallFunc`/runtime_dir
  接线），并以 fake-npm + 源码 grep-lock 测试锁死「探测链永不安装」。设计文档附记已撤回相关表述。
- 源码 checkout 曾进默认发现链（d9a9887）——v2 后移出，仅 `DSH_DEV_SOURCE_ROOT` opt-in。

## 验证边界（未执行项，如实标注）

- **真实 DeepSeek key 的 turn 未执行**：owner 授权事项；本机实测全部走 mock key（env 覆盖
  凭据链，HTTP headers 实证 `Bearer dsh-conn-fake-key`，未触真实 API、未读真实 key 内容）。
- **owner 真机验收矩阵未执行**（需 iPhone 操作，超出授权）：

| # | 前提 | 动作 | 应看到 |
|---|---|---|---|
| 1 | Mac 已 `npm i -g @deepseek-ai/dsh` 且 dsh Web UI 存过 key | 打开 CordCode Link | backend 列表出现 DeepSeek（available，来源 npm-global） |
| 2 | iPhone 已配对 | 选 DeepSeek 发一条消息 | turn 正常流式（文字/思考/工具/todo），上下文用量有值 |
| 3 | 同上 | turn 进行中 abort | 立即中断收口；再发一条正常（新进程新 nonce） |
| 4 | 同上 | 发送带图片的消息 | 被拒「不支持该附件」（text-only，pre-StartSession） |
| 5 | 未装任何 harness 形态的 Mac | 打开 CordCode Link | DeepSeek 不出现（「未启动」+获取指引，不伪造可用） |

- Release 构建/覆盖安装已执行（BUILD SUCCEEDED → `/Applications` → 重启；8777 由正式版内嵌
  runtime 监听、无临时产物残留）。生产日志实证 route②：`runtime via user-global npm dsh
  dsh=0.1.0-rc.6 app-boot=0.1.0-rc.6` + `agent registered backendId=deepseek`。

## 本机实测记录（2026-08-15，真实样本）

- 环境：`/opt/homebrew/bin/dsh`（npm 全局 `@deepseek-ai/dsh@0.1.0-rc.6`）+
  `~/.dsh/.credentials.yaml`（含 DEEPSEEK_API_KEY）。
- **driver E2E（产品路径全事件面）**：`dsh.New`（探测命中②）→ StartSession（影子树构建+
  node 拉起 vendor 胶水）→ Send → 完整 turn：`turn_started / user_message / text /
  context_usage_updated / result(done)`，进程回收正常。
- **协议级 census（手动驱动同 composition）**：initialize OK、prompt 收据、4 类 notification、
  事件流与 §3.3 映射逐项一致（block-start/delta/block-end/usage/finish/message/step/turn_end
  completed）；请求 headers 实证 env 凭据覆盖生效。

## 审计 §8 阻断项修复（2026-08-15，iOS fa371a3）

owner 真机验收发现：切 DeepSeek 模式列表直出「backend does not support session
listing」错误横幅（独立审计 e701ecc 定性为阻断：live-only 列表空态缺失，计划盲区+
审计失察）。已修复并复验：

- **入口短路**：`BackendKind.isLiveOnlySessionList`（deepSeek）——`loadSessions` 对
  live-only backend 不发任何 list/projects/pinned RPC，发布空列表并清除错误/加载/
  陈旧缓存态；
- **通用兜底**：列表 RPC 收到 wire `not_supported` → 空态而非错误横幅（保护未来
  live-only backend 与遗漏路径）；其他错误码（`list_failed` 等）仍 fail visibly 走
  错误横幅（对照测试锁定，不掩盖真实故障）；
- **空态文案**：侧栏显示「此对话后端为实时模式：没有历史会话列表，直接发送消息即可
  开始新对话」而非「暂无会话」；
- **go-bridge 无改动**（审计确认其 not_supported 行为正确）；
- **测试**：`LiveOnlySessionListTests` 4 用例全过（deepSeek 零 RPC 空态 / 语义仅
  deepSeek / not_supported→空态 / list_failed 仍报错）；受影响套件
  （ColdCache/AutoRefresh/BridgeModels）50/50；真机安装+启动完成（fa371a3）。
- **验收行（审计 §8 补充）**：切到 DeepSeek 模式 → 列表空态提示、无错误横幅、
  MacBridge 日志无 list_sessions RPC——待 owner 真机复核。

## 全量回归快照（2026-08-15，报告撰写时重跑）

- `go build ./...` ok；`go test ./agent/dsh/... -count=1` ok（5.8s）；go-bridge 定向
  （DSH/attachment/delivery/Evict/SendMessage/Seq/Scope/Lifecycle/classify）ok（2.4s）；
  此前全量 `./go-bridge/... ./agent/... ./core/... ./transcriptindex/...` 13 包 ok +
  `(cd relay-server && go test ./...)` ok。
- 双清单 generator verify 双模式 exit 0。
- iOS：`xcodebuild build` SUCCEEDED；`-only-testing:CCCodeTests/BridgeModelsTests` 43/43；
  真机安装+启动完成（iPhone 16 Pro）。

## 证据索引（MacBridge dsh/driver 分支）

`9555562` driver 骨架/codec/identity → `739ea1a` codec 测试 → `9dade9d` scope/seq/ignorable →
`d2158a9` typed death/at-most-once/CAS → `5837fbe` attachment 全链+truth-source → `1abec61`
canonical 协议章节 → `af46f3e` usage/双写+门槛7 → `c71c692` Mac App drivers 默认 →
`a90b75a`/`d9a9887` 发现形态（后被 v2 收编/降级）→ `2755a7f` 探测-复用终态+vendor 影子树。
iOS：`4e51ef1` BackendKind.deepSeek、`3bc597a` protocol mirror。
