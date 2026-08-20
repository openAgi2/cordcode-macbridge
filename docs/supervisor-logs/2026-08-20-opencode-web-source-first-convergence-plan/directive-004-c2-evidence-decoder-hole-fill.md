# 监工指令 4 号（补洞 C2 evidence/decoder fail-closed）

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`
> **范围**：Mac-only C2 evidence + project decoder/tests
> **下发时间**：2026-08-20T15:43:18Z
> **状态**：待转发给开发 agent
> **kind**：hole-fill

## 任务

修补 audit-003 的三个洞：重新捕获 WP，使 checker 真正只从 `http[]` 的真实 response 推导；让 1.18.18 `/project` translator 对 envelope、malformed row、缺 required fields fail closed；纠正 roots/limit over-limit 的测试叙事。完成后停止并等待 audit-004，C3 继续禁止。

## 背景与路线图依据

- audit-003 verdict=`partial`：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/audit-003-c2-official-list-get.md`。
- convergence plan §4/§5.1：样本 shape 必须来自同版本 raw evidence，不能信任旁路 summary。
- convergence plan §6 C2 + Gate S C2 failure presentation：project/list shape drift 必须暴露 catalog failure，不得静默变成 empty/partial success。
- 本指令是 Partial Path A 的 hole-fill，不是新阶段；只收口 directive-003，不授权 C3。

## 路线图 hash 变化说明

state 旧 hash=`2ebd546cf6f9281d9fc40b979365dc4594ac2696`，当前 hash=`06fa4bc0f788a25455ce1ad13488f4f6adc9a610`。变化来自 `105b393`：状态行记录 C2 已实施、等待 directive-003 audit；§6 C2、OD-1/OD-2、Gate S C2 契约未变化。audit-003 已按当前文本强制重读，本指令据新 hash 下发。

## Hole A：真实 HTTP evidence

1. 用 pinned official OpenCode `2cba7e227d` / 1.18.18 重新运行隔离 WP capture；仍只用 harness-owned HOME/XDG、4398/4399、临时 git worktree；4096 不得触碰。
2. 每个 GET `/project` 的 `http[]` row 必须保存实际 request/query/status/**response**；after-create/after-delete status 不得硬编码 200，必须记录 request 返回值。
3. `check_workspace_project.py` 的所有 shape、id、worktree、field-presence、growth、delete-still-registered 结论必须从 `http[]` 中相应 GET `/project` 的 status+response 推导。不得读取 `rawPayloads`、`baseline`、`afterCreate`、`afterDelete` summary 作为真相源。
4. 可删除 `rawPayloads`，或只作为非权威 debug 副本；若保留，checker 必须故意篡改它并证明分类不受影响，同时 summary/rawPayloads 与 HTTP response 不一致要报告 mismatch，不能形成双真相。
5. `--self-test` 至少篡改：GET status、HTTP response top-level、HTTP response project id、HTTP response worktree isolation、HTTP response deleted-worktree presence；五类都必须被捕获。
6. raw/sanitized leak scan、4398/4399 精确回收、4096 listener/PID 不变；evidence commit 必须先于 decoder product commit。

## Hole B：`/project` 1.18.18 fail-closed decoder

1. `fetchProjects` 使用 generation-118 专用 project-list decoder：只接受同版本样本坐实的 bare JSON array。`{"data":[…]}`、object/null/string 等 top-level 全部返回可诊断 catalog error；不得复用 v2 envelope 兼容路径。
2. 每个 project row 必须是 JSON object，且 `id`、`worktree` 为非空 string；malformed JSON/type、missing/empty required field 必须带 row index 返回 error。未知额外字段继续忽略。
3. official global pseudo-project `worktree="/"` 是合法 registry row，只在 CordCode visibility overlay 阶段排除，不能在 decoder 当 malformed。
4. 同一 decoder 同时服务 global aggregation 与 `ListProjectSuggestions`，两条路径都不得静默裁剪坏 row。
5. 删除/修正 `visibleProjectDir` 中“official desktop would not show them”这类未由同版本 client source/sample证明的表述；明确 missing-worktree 是 CordCode overlay，server registry 仍保留。

必须新增负向测试：

- `/project` envelope → global list 与 project suggestions 均失败；
- row 非 object / missing id / missing worktree / wrong type → 均失败，不返回空/partial catalog；
- unknown extra fields → 仍成功；
- global `worktree="/"` → decoder 成功、visibility overlay 排除。

## Hole C：roots/limit 测试诚实度

- 当前 `TestOfficialRootsLimit` 构造 101 IDs，却只把 `big[:100]` 喂给 fixture；删掉“over-limit 已由该 101st row证明”的虚假注释/断言。
- 不要为了 synthetic fake server 本地再造第二次 truncation。over-limit 的权威证据使用 A10 raw checker：4 个 roots 存在时，`limit=1/2/3` 分别返回 1/2/3，`limit=10` 返回全部 4，child 不进入 roots。
- 新增一个明确命名的 C2 regression test/checker断言或把现有 A10 checker owning assertion 在 exec-plan artifact 中逐行引用；完成报告必须区分：adapter unit test证明“请求发送 limit=100”，A10 live replay证明“官方 serve 执行 limit/root semantics”。

## 验证与安装

至少真实运行：

```bash
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_workspace_project.py
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_workspace_project.py --self-test
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_samples.py --require-all
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_samples.py --self-test
go test ./agent/opencode-web/ -count=1 -timeout 180s
go test -race ./agent/opencode-web/ -run 'Test(OfficialRootsLimit|Project|GlobalAggregate|MissingWorktree)' -count=1 -timeout 180s
go test ./go-bridge/ -run 'OpenCodeWeb|ListSessions|SessionCatalog|Projection' -count=1 -timeout 180s
go test ./core/ -count=1 -timeout 180s
go vet ./agent/opencode-web/
go build ./...
```

产品 decoder 改动后按仓库规则重新构建/覆盖安装 `/Applications/CordCodeLink.app`，核对 8777 runtime commit/path；不得运行临时 app，不得操作 UI。

## Commit 与停止线

1. WP re-capture + HTTP-derived checker/self-test 独立 evidence commit；
2. project decoder fail-closed + tests/roots-limit honesty独立 product/test commit；
3. exec-plan/handoff 收口可单独 docs/state commit。

完成后停止。不得启动 `c3-prompt-impl`，不得改 protocol/WireDescriptor/capability/session_pagination/iOS writer。

## 完成报告需含

- 每个 commit hash、file list、diff stat、clean status。
- 一张字段来源表：每个 checker conclusion 对应哪个 `http[]` row/status/response；证明 `rawPayloads`/summary 不再是 evidence owner。
- five-mutation self-test 真实输出。
- project decoder top-level/row/extra/global-negative tests 的真实输出。
- roots/limit 的两层证据明确分开（adapter request vs A10 official behavior）。
- 全部 test/vet/build/Release 安装真实输出；4398/4399 回收、4096 未动、8777 正式 app路径。
- 声明 C3 未进入、无协议/capability/iOS/UI/Relay 改动，不自称监工 verified。

## 退出判据

- WP checker 零读取旁路 summary/rawPayloads，五类 HTTP mutation 全捕获。
- `/project` 只接受 1.18.18 bare array，坏 row fail closed，extra fields 允许，global `/` 合法后再被 overlay 排除。
- roots/limit 证据表述与实际 fixture一致，无 unused 101st-row 假证明。
- 所有回归与正式 Release 安装通过，工作树干净，C3 未开始。

## 不授权

- 不授权 C3–C7 或重新设计 C2 产品行为。
- 不授权 fake server 替代 official re-capture、从旁路字段反推 HTTP response、恢复任何 fallback。
- 不授权 UI tests、真机点击、生产 Relay/VPS、owner 4096 或真实账号/provider 操作。
