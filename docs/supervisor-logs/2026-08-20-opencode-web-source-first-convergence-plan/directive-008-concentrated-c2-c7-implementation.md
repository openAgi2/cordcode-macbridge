# 监工指令 8 号（C2-review-fix + C3–C7 集中实施）

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`
> **范围**：MacBridge + iOS 双仓（按 canonical §6.2–§6.11.2）
> **下发时间**：2026-08-20T17:11:51Z
> **状态**：待转发给开发 agent
> **kind**：implementation

## 任务

把剩余 C2 strict-decoder 补洞与 C3–C7 作为一个集中开发批次完成。允许内部按波次和 reviewable commits 推进；除非触发本文停止线，不要在每个小功能后停下来等待监工。全部实现、定向测试、安装和自审完成后，只交一次集中完成报告，等待一次独立审计。

## Roadmap diff

相对指令 7 前的 canonical：E1b/E4b/E5b 已由 `211bb27` 真实取证并经 audit-007 verified；§6.4、§6.6、§6.11.1 已固化最终 variant/provider/config/selection mapping；§6.11.2 明确剩余工作为一个集中批次。E2 reasoning 仍是明确 unsupported，不得夹带实现。

## 实施波次（同一指令内连续完成）

### Wave 1 — Foundation

1. 完成 §6.2 C2 strict generation-118 `/project` decoder 补洞：只接受 WP-FIX 已证明的 bare-array shape；envelope/null/scalar/non-object row、缺失/错误/空 `id` 或 `worktree` 全部 fail closed，无 trimming/fallback。
2. 按 §6.11.1 canonical-first 修改 Mac `docs/protocol/`/schema，再同步 iOS protocol mirror/models：model item 可选 `variants:string[]`；`send_message.params.model.variant?:string`。不得新增其他 wire field。
3. 修复现有 `send_message.params.agent` 在 Swift bridge 边界被丢弃的问题。

### Wave 2 — Turn core

1. C3：实现 session-scoped `core.PromptOptionsSender` / `SendWithOptions`，原子携带 agent/provider/model/variant；Mac 生成一次 stable `messageID`；HTTP 204 只表示 admission，零 timeline write。
2. C4：history 只进 Kernel hydrate transaction；live 只经唯一 pre-Kernel normalizer 和唯一 Kernel ingest；nested `sync` 明确跳过；一个 backend-instance global SSE subscriber 支持 E3 external turn；A3/A4/A5 的 error/abort/reconnect 终态必须保持真实。
3. C5：严格解码 `/provider`、`/config`、`/agent`，只暴露 connected catalog。模型选择严格执行 §6.6：current → agent model → `resolveDefaultModel(providerDefault, config.model)` → recent → first-connected provider default/first catalog model；存在 provider default 时它优先于 legacy config。每次 prompt 必须显式发送已验证 model；variant 仅接受当前 model 的 live key。

C4 与 C5 可在 C3 shared boundary 稳定后并行推进，不互相等待最终收口。

### Wave 3 — Independent surfaces

1. C6：按 A6/A7/A8 完成 permission、question、todo。三者保持不同 control-plane ownership；raw control 不写 `messages[]`；canonical event 最多一次进 Kernel；reject 不得伪装健康完成；todo 不造 ID、不进 SessionProjection timeline。
2. C7：只实现 rename/archive/delete。HTTP 成功只刷新 catalog metadata；archive 遵守 OD-1；delete 以 response + list/by-ID convergence 为准，不等待也不制造 `session.deleted`。

C6、C7 与 Wave 2 无共享语义依赖，可在 Wave 1 完成后独立推进。OD-3 的十四项 future/unsupported 不得夹带。

### Wave 4 — Activation and delivery

1. 汇总所有 owning positive/negative/regression tests 后，才更新 truthful WireDescriptor/backend capability advertisement 和 iOS UI availability。
2. `external_turn_streaming`、permission/question/todo、mutation、model variant 等能力逐项以真实 end-to-end owning path 为准；缺一层就不广告。
3. 执行 Mac Go/Swift 定向回归、build/vet，并按仓库规则 Release 构建、覆盖安装到 `/Applications/CordCodeLink.app`，确认 8777 runtime 来自该路径。
4. 因本批必然修改 iOS protocol/model/bridge 代码，执行定向 build + 相关 test class；不得跑全量 2072 tests，不得跑 UI tests/simulator automation。探测到可用 physical iPhone 时按 iOS 仓规则自动 build/install/launch；不自动点击、输入或视觉验收。
5. 最终提交一份集中报告，不在中间 feature commit 后请求逐项监工。

## 硬约束

- 官方事实只能来自 pinned 1.18.18 source + A1–A10/WP/E1–E7/E1b/E4b/E5b 样本；不得抄 legacy adapter 语义。
- 不实现 E2 reasoning；遇到 reasoning shape 必须按 §6.3 显式 unsupported。
- Mac Projection Kernel 是唯一 timeline truth；iOS ProjectionStore 是唯一 client `messages[]` writer。禁止第二 reducer、raw history/SSE timeline route、optimistic placeholder writer、kernel-nil fallback。
- 不恢复 v2/unknown generation、递归 JSON search、silent fallback、fake data、cached legacy snapshot。
- 不把 sandbox invalid-model 400 当健康首轮，不用 fake server 替代缺失真实 shape。
- 第一轮修复对症状无影响时，立即执行 §4.3 方法升级；禁止在同一状态机模型里猜第二轮。
- 不修改 owner-managed 4096 serve，不使用真实 provider 账号，不部署 Relay/VPS。
- 不运行 UI tests、snapshot tests、simulator automation 或自动操作真机 UI。
- 所有 build/test 有真实 watchdog、日志、最终汇总和本任务进程回收；不得因后台进程存在而等待数小时。

## 只在以下情况暂停并报告

1. 新 raw 证据与 canonical mapping 冲突；
2. 必须新增本文未定义的 protocol field、capability 或产品语义；
3. 实现需要新增 timeline writer/reducer/fallback；
4. 第一轮修复对真实症状无影响，尚未找到第一 divergence；
5. build/test 超时、hang 或后台控制丢失，且按仓库 watchdog 规则收尾后仍无法得到最小可复验结果。

某 slice 暂停时，不共享争议边界的独立 slice 可以继续；最终报告必须把该 slice 标为 blocked，不能用 partial 冒充全批完成。

## 完成报告必须包含

- commit 列表（evidence/protocol/product/tests/closeout 分清）及每个 commit 的 diff scope；
- C2–C7 每个 dossier 的 source + sample + bridge mapping + SSV2 owner 对照表；
- exact request/response/event fixture 来源与 owning test 名称；
- 正向、负向、回归命令及真实输出，包含 timeout 与进程收尾结果；
- capability before/after 表，证明未实现项未广告；
- Mac Release build/install/runtime 8777 证明；iOS 定向 build/test 和连接真机安装结果；
- 两仓 `git status --short`，以及未覆盖/blocked/owner UI matrix 待验收边界；
- 明确声明 exec-plan proven 不等于 supervisor verified，也不等于 owner 真机 done。

## 退出判据

- C2 strict decoder、C3/C4/C5 turn core、C6 interactions、C7 mutations 全部符合 canonical 且 owning tests 通过；
- additive protocol canonical/mirror/schema round-trip 一致，agent/variant 不再在任一 bridge 层丢失；
- single-writer/single-normalizer/single-ingest、external-turn/reconnect、zero-POST/zero-writer 负向测试全绿；
- capability advertisement 与实际完整路径一致，E2 与 OD-3 仍不可用；
- Mac 正式安装与 iOS 必需安装完成，双仓工作树 clean；
- 没有未处置的 §4.2/§4.3 stop signal。

## 不授权

- 不授权 owner 真机 UI 点击、视觉验收或 UI automation；集中代码审计通过后再一次性给 owner 测试矩阵。
- 不授权真实账号、生产 Relay/VPS、OpenCode v2、reasoning、OD-3 future surfaces。
- 不授权开发 agent 改写 canonical mapping；发现冲突只能带 raw 证据暂停。
