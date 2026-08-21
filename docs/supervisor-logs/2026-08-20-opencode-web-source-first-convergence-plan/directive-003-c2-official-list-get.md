# 监工指令 3 号（C2 official list/get）

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`
> **范围**：Mac-only C2（同版本样本、`agent/opencode-web` catalog adapter、必要的 `go-bridge` 定向回归）；不改 iOS 产品代码
> **下发时间**：2026-08-20T15:11:05Z
> **状态**：待转发给开发 agent
> **kind**：implementation

## 任务

先补齐 OpenCode 1.18.18 `workspace.project` 的真实样本闭环，再实施 C2 official list/get：每个 project worktree 做带 `roots=true` 和有界 `limit` 的官方目录 scoped list；落实 OD-1/OD-2；保留 archived session 的 by-ID get；封死列表错误、空 registry 和缺失 worktree 被旧 fallback 静默伪装的路径。完成后停止，等待独立审计，不进入 C3。

## 路线图依据

- convergence plan §4 / §5.1：任何 `supported now + source-only` translator 必须先有同版本真实样本；不能用源码引用或 fake server 代替样本。
- convergence plan §6 C2：复现 official root/limit；多目录聚合与 archive visibility 是 bridge 产品规则；by-ID 保留；filesystem existence 不得静默冒充 server/project truth。
- Gate B owner decisions：OD-1=`hide-in-default-list-keep-by-id`；OD-2=`aggregate-global-list-keep-scoped-list`。
- Gate S S3 C2：list/get 只写 catalog metadata，HTTP 错误必须作为 catalog failure 暴露；空 catalog 不推断 session；不得写 `messages[]` 或制造 turn。
- Gate S S4：`workspace.project` 的固定顺序为 `capture/checker → translator test`。

## 路线图 hash 变化说明

监工 state 记录的旧 hash 为 `777591c8334a76987474ab6d52ee481606ce8a4c`，当前文档 hash 为 `2ebd546cf6f9281d9fc40b979365dc4594ac2696`。差异是 commit `40452cc` 将状态行更新为 C1 经 `audit-002-recheck` closed，并明确 C2 需单独授权、不得夹带 C3+；C2 §6、Gate B 决策与 Gate S C2 契约没有改变。本指令按新 hash 下发。

## Phase 0：`workspace.project` 同版本样本硬门

在任何 C2 产品代码修改前完成；样本 commit 与产品 commit 分开。

1. 使用官方 checkout `/Users/jacklee/Projects/opencode` 的 pinned commit `2cba7e227d`（OpenCode 1.18.18），继续使用隔离 HOME/XDG、`127.0.0.1:4398/4399` 和 deterministic local provider；不得触碰 owner 的 `127.0.0.1:4096`。
2. 通过真实 official serve 捕获 `/project`：保存 raw HTTP、sanitized sample、请求上下文、官方源码 `file:line` 映射。至少生成两个不同临时 worktree 的 project 条目；字段/shape 只能从 raw 独立推导。
3. 若可在 harness 创建的临时目录内安全完成，增加“worktree 捕获后删除临时目录，再重读 `/project`”观察，以区分 server registry truth 与本机存在性 overlay。做不到则把此观察标成 `blocked`，不得伪造样本，也不得阻塞 C2 已由 owner 决定的 visibility overlay。
4. 新增独立 checker：只从 raw HTTP 推导 top-level shape、project identity、worktree、vcs/time/sandboxes 的实际存在性；sanitized summary 不得作为真相源。`--self-test` 至少篡改 top-level shape、project id、worktree/目录隔离并降级失败。
5. 扫描 raw/sanitized：不得含 owner 路径、Authorization、密码、4096 或外部账号数据；回收 4398/4399 且只回收本 harness 创建的进程。
6. checker PASS、负向 self-test PASS、样本 commit 完成以后，才允许开始 Phase 1。fake server 只能用于 translator unit test，不能充当此证据门的 official sample。

## Phase 1：C2 实现边界

### 1. Official scoped list

- `ListSessionsInDirectory` 与 global fan-out 的每个 upstream request 必须显式携带：directory scope、`roots=true`、`limit=100`。
- `100` 复用仓内已冻结的 `openCodeSessionFetchLimit` / default bridge list budget；不得再发明第二个 fetch 数字，也不得把 client page cursor/limit 直接当 upstream cursor。
- 同版本 A10 已证明 1.18.18 支持 `limit`。不得复制 official UI “失败后省略 limit 再试”的兼容 fallback；请求失败直接返回可诊断 catalog error。
- top-level/required row shape malformed 时失败，不返回静默裁剪后的假成功；未知额外字段仍可忽略。

### 2. OD-1 archive visibility

- global/default enumeration 与 directory-scoped default enumeration 都不得返回 `time.archived` 行。
- `FetchSessionInfo` / GET-by-ID 不受默认 visibility 过滤影响，archived row 必须仍可读取并带 `ArchivedAt`。
- archived 只是从默认 catalog 隐藏，不得当成 delete，不得写 timeline；本轮不修改 archive PATCH 行为（属于 C7 mutation）。

### 3. OD-2 aggregation

- global list 只按真实 `/project` worktree registry 做逐目录 scoped aggregation；directory request 保持单目录，不得“global fetch 后本地过滤”。
- registry worktree 去重；合并结果稳定按 `ModifiedAt DESC, ID ASC`。如同一 stable ID 意外跨 bucket 重复，只保留一条确定结果并以测试钉住，不能向 iOS 发重复 session。
- `/project` 获取/解码失败必须向上返回 catalog failure；不得复用 stale cache 冒充本轮成功。
- 任一 directory bucket 获取/解码失败，整个 global list 失败；不得静默丢掉失败 bucket 后返回 partial success。
- `/project` 返回空集合时，global list 就是空集合；不得回退 `GetWorkDir()`、headerless `/session`、history、缓存快照或 legacy backend。

### 4. Missing-worktree 产品规则

- 明确记录这是 **CordCode catalog visibility/safety overlay**，不是 official server truth：`/project` 仍是 registry fact owner。
- global aggregation和 project suggestions 可排除 `/`、非绝对路径、重复项及本机已不存在/非目录的 worktree；不得声称 official serve 已删除这些 project。
- filesystem 检查不得删除 server session、不得阻断 GET-by-ID，也不得改写 timeline。
- 显式 directory-scoped list 不得仅因本机 `os.Stat` 失败就偷偷改查其他目录或 global；仍按请求目录访问 serve，并忠实返回 serve 结果/错误。
- 增加命名测试，分别证明 global visibility overlay、scoped request 不改 scope、by-ID 不受存在性过滤。

### 5. SSV2/catalog ownership

- list/get/create metadata、project registry 与 `signalCatalogRefresh` 仍只属于 request/control catalog。
- 不得调用 `ProjectionKernel.IngestLive`、hydrate transaction、`EventPublisher.publish`、iOS `ProjectionStore` 或 `ChatViewModel.messages`。
- HTTP 200/空列表不是 turn completion；不得制造 assistant/user/tool message。
- session create body `{}` 的现有 1.18.18 契约只补回归测试，不得夹带 C3 的 messageID/agent/model/parts/variant 实现。

## 必须新增/补强的测试

测试名可因现有文件组织做等义微调，但每项必须有独立 owning assertion，不能一个“大测试绿了”代替边界证明：

1. `TestOfficialRootsLimit`：请求真实 shape 含 directory + `roots=true` + `limit=100`；root/child、exactly-at-limit、over-limit。
2. `TestArchivedHiddenInDefaultListKeptById`：global/scoped default 均隐藏 archived；by-ID 仍返回。
3. `TestGlobalAggregatePerWorktree`：每个 registry worktree 一次 scoped fetch、无 headerless global、去重且稳定排序。
4. `TestProjectRegistryAndBucketFailuresSurface`：registry error、单 bucket error、malformed payload 均失败，不得 stale/partial fallback。
5. `TestEmptyProjectRegistryIsEmpty`：零 `/session` fallback。
6. `TestMissingWorktreeRule`：visibility overlay、scoped scope、by-ID 三条边界。
7. `TestListGetDoesNotWriteMessages`：list/get/create-metadata 不触发 Kernel/EventPublisher/timeline writer。
8. `TestCreateBodyEmptyMatchesV1`：仅钉现有 `{}`，不得扩进 C3。
9. 既有 A10 replay 与新 `workspace.project` sample checker 必须纳入 C2 regression；checker 自测必须真实运行。

## 验证命令与任务终态

至少运行并报告真实输出：

```bash
python3 agent/opencode-web/testdata/official-1.18.18/harness/<workspace-project-checker>.py
python3 agent/opencode-web/testdata/official-1.18.18/harness/<workspace-project-checker>.py --self-test
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_samples.py --require-all
go test ./agent/opencode-web/ -count=1 -timeout 180s
go test ./go-bridge/ -run 'OpenCodeWeb|ListSessions|SessionCatalog|Projection' -count=1 -timeout 180s
go test ./core/ -count=1 -timeout 180s
go vet ./agent/opencode-web/
go build ./...
```

- 每条命令必须有明确 timeout/终态；若主体已完成而诊断收集超过 60 秒，终止本任务创建的诊断进程并报告边界。
- 不跑 UI tests、snapshot tests、simulator automation 或真机 UI automation。
- C2 产品代码完成后，按仓库规则构建 Release、覆盖安装并启动 `/Applications/CordCodeLink.app`，核对 8777 listener 与进程路径；不得启动临时 DerivedData/build app，不得触碰 owner 的 OpenCode serve 4096。
- 安装/启动不是 owner 真机 UI 验收；本阶段不要求 owner 点击 iPhone。若安装被环境阻塞，保留真实错误，不得用旧 app 冒充。

## Commit 与停止线

至少拆为以下可审计提交，禁止混包：

1. official 1.18.18 `workspace.project` raw/sanitized sample + checker + inventory/state；
2. C2 implementation + owning tests；
3. C2 regression/exec-plan/handoff 收口（若无必要产品改动，只含 docs/state）。

完成 `c2-list-get-{impl,tests,regression}` 后立即停止。不得启动 C3 todo，不得顺手修 C4/C5/C6/C7。

## 硬约束（不许）

- 不许 Gate SP1 未通过就提交 C2 translator/product patch。
- 不许用 fake server、手写 JSON、旧日志、A10 summary 或源码引用替代 `/project` 同版本 raw sample。
- 不许保留/新增 headerless global list、current-workdir fallback、stale-cache-on-error、partial-bucket success、history fallback 或 legacy backend fallback。
- 不许把 archived 过滤放到 by-ID path。
- 不许用 filesystem existence 覆盖 server identity，或把 missing worktree 当 session deletion。
- 不许修改 `docs/protocol/`、WireDescriptor、capability advertisement、`session_pagination`、iOS writer 或 timeline ownership。
- 不许夹带 C3+、owner 4096 操作、UI automation、真机点击、生产 Relay/VPS 改动。
- 不许以 exec-plan `proven` 或“tests passed”自称监工 verified；完成后等待独立审计。

## 完成报告需含

- 三个 commit hash + 每个 `git diff --name-only` / diff stat；若提交拆分不同，解释为何仍满足 evidence/product/closeout 分离。
- `workspace.project` raw 来源、sanitization 结果、official source `file:line`、checker 与负向 self-test 真实输出。
- roots/limit 的实际 request 证据；OD-1/OD-2、missing-worktree、error/empty semantics 逐条对应测试。
- writer 负向证明：列出 list/get 路径为何不触碰 Kernel/EventPublisher/messages。
- 上述测试/build/vet/Release 安装命令的真实输出和超时边界。
- 4398/4399 回收、4096 未动、8777 最终 owner `/Applications` app 路径证明。
- `git status --short` 为空；已知未覆盖边界诚实列出。
- 明确声明：C3 未进入；protocol/WireDescriptor/capability/iOS writer 未改；没有 owner 真机 UI 验收声明。

## 退出判据（可证伪）

- `workspace.project` 同版本 raw sample + 独立 checker + self-test 全绿，且证据 commit 早于产品 commit。
- C2 八类 owning tests 与 A10/new sample regression 全绿；global/scoped/by-ID/missing/error/empty 边界均可单独失败。
- 无 headerless/global/current-dir/stale/partial/history/legacy fallback；list/get 零 timeline writer。
- Release app 安装后 8777 来自 `/Applications/CordCodeLink.app`；无临时 app/runtime 残留。
- 工作树干净，C3 未启动，等待监工独立 audit。

## 不授权

- 不授权 C3–C7、协议或 capability 扩展。
- 不授权 UI tests、simulator automation、真机点击/输入/视觉验收。
- 不授权操作生产 Relay/VPS、owner OpenCode serve 4096 或真实外部 provider/account。
- 不授权修改设计路线；若同版本样本推翻 Gate B/S3 的事实声明，立即停止，提交证据给 owner 裁决，不得自行“适配”。
