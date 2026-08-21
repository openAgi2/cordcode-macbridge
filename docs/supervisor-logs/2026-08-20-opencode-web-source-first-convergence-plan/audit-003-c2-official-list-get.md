# 监工审计报告 3 号（对应监工指令 3 号）

> **审计对象**：开发 agent 对监工指令 3 号的完成报告
> **审计时间**：2026-08-20T15:41:49Z
> **审计严格度**：严格（独立复跑 + grep 源码 + git show + 现场进程核对）
> **Verdict**：partial

## 0. Audit Context

- 监工指令：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/directive-003-c2-official-list-get.md`
- 开发报告：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/report-003-c2-official-list-get.md`
- 核实提交：`69f26bc`、`ef21db1`、`105b393`
- 独立命令：`git show --stat/name-status/unified`、源码 `sed/rg`、WP/A1-A10 checker、三组 Go test、race 定向测试、`go vet`、`go build ./...`、`lsof/pgrep/strings`。
- 设计文档 hash 从 `2ebd546cf6f9281d9fc40b979365dc4594ac2696` 变为 `06fa4bc0f788a25455ce1ad13488f4f6adc9a610`；本次已强制重读 §6 C2、Gate B OD-1/OD-2 与 Gate S C2。变化仅为 `105b393` 状态行记录 C2 已实施等待审计，没有改写 C2 路线。

## 1. Overall Verdict

产品实现的大头正确：提交顺序真实；roots/limit、OD-1/OD-2、global/scoped/by-ID、empty/error、missing-worktree 和 catalog-only 边界均有代码与测试；监工独立复跑全部通过；Release app 的 8777 listener/commit/path 现场可复证；4096 未动。

但不能判 `verified`，有两个实质性收口洞和一个测试诚实度洞：WP checker 并未从 `http[]` response 推导（`http[]` 根本没保存 response），而是信任另一个复制字段 `rawPayloads`；`fetchProjects` 对 unverified envelope 和 malformed required row 仍会接受/静默跳过，能把 shape drift 伪装成空/partial registry；`TestOfficialRootsLimit` 声称 over-limit，但构造的第 101 行没有进入 fixture。它们不推翻已实现主路径，故 verdict 是 `partial`，走 Path A 开新号 hole-fill，C3 继续禁止。

## 2. 逐项核实矩阵

| 检查项 | 开发自述 | 监工核实方式 | 结论 |
|---|---|---|---|
| 三提交与顺序 | evidence → product → closeout | `git log/show` | ✅ `69f26bc` 早于 `ef21db1`，再到 `105b393` |
| diff 隔离 | 5 / 7 / 3 files | `git show --name-status` | ✅ 样本、产品、docs/state 分离 |
| roots+limit | directory + roots=true + limit=100 | `sessions.go` + `TestOfficialRootsLimit` | ✅ 请求 shape 与唯一 core 常量成立 |
| OD-1 | default hide, by-ID keep | 源码 + 两个 archive tests | ✅ |
| OD-2 | registry fan-out、fail whole、empty is empty | 源码 + aggregate/error/empty tests | ✅ |
| missing worktree | global overlay only；scoped/by-ID 不改写 | 源码 + `TestMissingWorktreeRule` | ✅ |
| catalog-only | 零 POST/event/timeline writer | 源码 `rg` + owning test | ✅；生产路径无 Kernel/EventPublisher 引用 |
| C3/protocol/capability/iOS 边界 | 未改 | commit 文件清单 + protocol/WireDescriptor diff | ✅ |
| WP official pin/source | 1.18.18 / `2cba7e227d` | official checkout HEAD + cited source read | ✅ |
| WP checker | 从 raw HTTP 独立推导 | 读 sample/checker/capture | ❌ checker 读 `rawPayloads`; `http[]` 只有 step/method/path/status，无 response |
| project fail-closed | registry decode/row failure暴露 | 读 `fetchProjects` | ❌ `{data:[…]}` 仍被 shared decoder 接受；row JSON error 或缺 id/worktree 被 `continue` 静默丢弃 |
| root/child/exact/over | C2 owning boundary | C2 test + A10 checker | ⚠️ A10 checker真实覆盖 root/child、4 roots 下 limit 1/2/3；但 C2 test 的“over-limit”101st row 未喂入 fixture，注释/主张失真 |
| WP/check_samples/test/vet/build | 全绿 | 独立复跑 | ✅ WP checker/self-test PASS；A1-A10 10/10；agent 2.515s；go-bridge 4.327s；core 0.039s；vet/build clean |
| race 定向 | 未声称 | 独立补跑 8 个 C2 tests | ✅ `go test -race ...` 1.050s |
| Release/8777/4096 | 正式 app、commit 对齐、owner serve 未动 | `lsof` / `pgrep` / `strings` | ✅ runtime PID 13878 from `/Applications`, contains `ef21db183148`; 4096 PID 71333 |
| 工作树 | clean | `git status --short` | ✅ 审计落盘前干净 |

## 3. 路线图偏离检查（八条判据）

| # | 判据 | 命中? | 证据 |
|---|---|---|---|
| 1 | 删除性测试 | ❌ | 主路径删除 stale/current-dir/partial fallback，不依赖旧裁判代码 |
| 2 | 三环归属 | ❌ | 变更属于 C2 catalog adapter/control plane |
| 3 | 待删清单特征 | ❌ | 15s success cache 是既有 catalog cache，不是 error fallback；无新阈值猜测 |
| 4 | 半接管最差状态 | ❌ | 未增加 timeline 双 writer/referee |
| 5 | 覆盖面诚实 | ✅（软） | 报告把 checker 描述为“从 raw HTTP 推导”且称 over-limit owning test 已完成，与实况不符 |
| 6 | 叙事真实性 | ❌（未达硬否决） | 报告明确未自称监工 verified；差异属于边界证据过度表述，按 partial 修补 |
| 7 | 控制变量污染 | ❌ | 三提交范围干净，无 C3/协议/UI 混包 |
| 8 | 根因锁死门 | ❌ | 先有同版本 live capture，再实施 translator；capture-before-translator 顺序成立 |

## 4. 驳回理由

不适用；本次不是 `rejected`。核心 C2 产品路线没有偏离，缺口可独立补齐。

## 5. Partial 处理

- 未覆盖边界 1：WP 样本必须把真实 `/project` response 落在 `http[]`，checker 只从该 response 推导；不能信任旁路 summary/rawPayloads。
- 未覆盖边界 2：project translator 必须只接受已捕获的 1.18.18 bare array，并对 malformed row / missing id/worktree fail closed；unknown extra fields继续允许。
- 未覆盖边界 3：修正 over-limit 测试/报告，使 fixture 真有 `available > limit`，或明确把 A10 replay作为 owning evidence并删掉虚假的 101-row 主张。
- 路径选择：A（监工指令 4 号 `hole-fill`）；完成后 report/audit 与 4 号一一对应。
- C3 在 4 号 verified 前继续禁止。

## 6. 给 owner 的下一步

- 不需要真机操作。把监工指令 4 号转发给开发 agent，补完证据链与 project fail-closed 后再复审；不要开始 C3。

## 7. Three-Track done

- exec-plan proven：yes（但需 hole-fill 后更新）
- 监工 verified：partial
- owner 真机矩阵 ✅：本 C2 阶段不要求；Release 安装已由监工现场核验，不等于 owner UI 验收
- 结论：C2 尚未监工收口，C3 不放行。
