# 监工指令 6 号：Canonical Stage 0 evidence pack（E1–E7 + WP-FIX）

> 路线图：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`（唯一 canonical；commit `904a0dd`）  
> 范围：仅 OpenCode 1.18.18 隔离取证、脱敏样本、独立 checker  
> 下发时间：2026-08-20T16:08:13Z  
> 状态：待开发 agent 执行  
> kind：diagnostic / evidence-only

## 0. 指令替代关系

本指令**替代并撤销 directive-005 的产品集中实施授权**。directive-005 标记 `superseded`；其 source-first、single-writer、fail-closed、watchdog 原则继续有效，但 C2 decoder 修补及 C3–C7 产品实现、协议、WireDescriptor、capability advertisement、iOS production code、Release 安装全部冻结。

本轮只有一个交付目标：把 canonical §4.1 / §6 中仍标为 `PENDING-SAMPLE` 的真实物理 shape 取证完成。开发 agent 不负责写转换规则或修改架构。样本报告回来后，由设计 owner 更新 canonical dossier；在新的实施指令下发前，不进入 translator。

## 1. 先读与唯一事实边界

开始前完整读取：

1. `docs/2026-08-20-opencode-web-source-first-convergence-plan.md`，尤其 §0、§4.1–4.3、§6；
2. `docs/2026-08-20-opencode-web-1.18.18-sample-inventory.md`，尤其 §0.1；
3. pinned official checkout `/Users/jacklee/Projects/opencode` commit `2cba7e227d` 的对应 UI call site 与 server/schema；
4. 现有 harness、A1–A10 样本与独立 checker，只复用取证基础设施，不把产品 fake fixture 当外部证据。

事实优先级固定为：

```text
official UI call site + official server/schema
                    + same-version raw HTTP/SSE/reload
                    + independent checker derivation
```

源码提供词汇和捕获入口，raw transport 提供物理 shape。开发 agent只记录二者；**不得决定 bridge 字段、SSV2 ownership、truth owner 变更、fallback、unsupported 产品策略或 capability activation**。若已有文档决定与样本冲突，停在该场景并报告冲突，不自行选择一边。

## 2. 隔离与安全边界

- 版本固定 OpenCode `1.18.18` / source `2cba7e227d`。
- 使用隔离 `HOME`/XDG、harness-owned workspace、`127.0.0.1:4398/4399` 和 deterministic local provider。
- `127.0.0.1:4096` 是 owner managed serve：不得写、停、重启或复用作样本源；开始和结束记录其 listener PID 未变。
- 不得使用真实 provider 账号、外部付费请求、owner 工作目录数据、旧日志或缓存样本冒充本轮取证。
- raw 与 sanitized 分离；sanitized 保留全部 key/type/order，替换 ID、路径、时间和认证值；跑 owner-path、Authorization/Basic、secret、4096 泄漏扫描。
- 可复用 A1–A10 harness 结构，但不得用 fake server 填补 official sample。无法由隔离官方 serve 产生则标 `BLOCKED`，附方法和失败现场。
- 每个 capture/checker 命令设置明确超时；最终汇总后立即回收本任务创建的进程组。4398/4399 必须释放；不得按名称全局 kill。

## 3. WP-FIX：先修正 C2 证据所有权（仍不改 decoder）

重新捕获或规范化 WP，使每次真实 `GET /project` 的 status 与实际 payload 位于样本 `http[].response`。然后改 `check_workspace_project.py`：

1. 所有 top-level、row、id/worktree、growth、deleted-still-registered 结论只从 `http[]` 的 request/status/response 推导；
2. `rawPayloads`、summary、`meta.captureStatus` 不得参与成功分类；若保留副本，篡改副本不能改变 derived result，但副本与 raw 不一致必须显式 `summary-mismatch`/FAIL；
3. self-test 至少破坏 status、top-level、project id、worktree isolation、deleted row，并逐一捕获；
4. 样本 inventory 的 WP 状态改为 `captured` 仅在上述 checker 与 self-test 都通过后；
5. **不修改 generation-118 project decoder**。它的严格 bare-array/required-row 修复留给 canonical 更新后的产品批次。

WP-FIX 可以与 E1–E7 同一 evidence commit，也可独立 evidence commit，但必须先于任何未来 decoder commit。

## 4. E1–E7 批量取证

可以一次启动隔离 sandbox 连续捕获，但每个场景必须独立归档、独立分类、可单独 replay。

| ID | 场景 | 必须产生的真实观察 | checker 必须独立拒绝的假证明 |
|---|---|---|---|
| E1 | selected variant | 非空 variant 的 prompt request、SSE、reload；另保留 unset omission 对照 | 只凭 SDK optional 字段或 A1 unset omission 宣称支持 |
| E2 | reasoning | populated reasoning 的 direct SSE delta/update、terminal、message reload | 把 answer text、手写 reasoning fixture 或空 reasoning 当样本 |
| E3 | external official-Web turn | 第二 client create/send；capture client 仅经 global SSE 观察到完整 session/message/status/terminal；最终 reload 收敛 | polling、同一 client、capability 字符串或 fake broadcast 代替外部 turn |
| E4 | providers | 真实 `GET /provider` request/status/response，包含 deterministic configured provider、connected set、models | 递归搜索 JSON、只截取 models、summary 代替 top-level shape |
| E5 | configured default model | valid / invalid / absent 三个隔离配置观察，并与 E4 catalog 对齐 | first-connected 或首模型冒充 configured default |
| E6 | rename | PATCH method/path/query/body/status/response；list、by-ID、SSE/catalog refresh 与失败观察 | 只以 HTTP 2xx 或本地标题变化宣称收敛 |
| E7 | delete | DELETE method/path/query/body/status/response；随后 list absence、by-ID 404、`session.deleted`/catalog invalidation 与失败观察 | 只以 DELETE success boolean 或本地列表移除宣称收敛 |

每个 E-row 必须交付：

- official UI 与 server/schema `file:line`；
- raw + sanitized HTTP/SSE/reload；
- inventory 行（状态只由 checker 推导）；
- 独立 checker 正常输出；
- destructive self-test，至少逐项破坏该 row 声称的 request field、response field、关键 event/order、reload convergence；
- 场景结论只写“观察到什么/未观察到什么”，不写 bridge 应如何转换。

如果 deterministic provider/harness 无法产生 E1/E2/E5 等官方路径：保留 source/capture 尝试与真实失败，标 `BLOCKED`，继续其他互不依赖场景。不得改 official source、注入产品 mapper、恢复 legacy fallback 或手写结果来“让 checker 绿”。

## 5. 本轮允许与禁止修改的文件

允许：

- `agent/opencode-web/testdata/official-1.18.18/harness/` 的隔离捕获与 checker；
- `agent/opencode-web/testdata/official-1.18.18/samples/` 的 raw/sanitized evidence；
- sample inventory 的事实状态/来源；
- 本指令的 evidence report、exec-plan/handoff 状态（只能标 evidence，不得把产品 slice 标 done）。

禁止：

- `agent/opencode-web` 产品 `.go` translator/client/decoder；
- `go-bridge`、`core`、Mac app 或 iOS production/test product wiring；
- `docs/protocol/`、WireDescriptor、capability advertisement；
- fallback、递归 decoder、placeholder/mock product data、第二 reducer/writer；
- Release build/install、iOS build/install、UI tests、simulator automation、真机 UI 操作、生产 Relay/VPS。

若为了捕获必须修改 official checkout 或产品代码，本场景应标 `BLOCKED` 并报告，不得越界。

## 6. 完成门与停止点

完成时运行并报告：

```bash
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_canonical_execution_design.py
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_canonical_execution_design.py --self-test
python3 agent/opencode-web/testdata/official-1.18.18/harness/check_samples.py --require-all
# WP-FIX 与 E1–E7 各自 checker + --self-test
git diff --check
git status --short
```

`check_samples.py --require-all` 继续证明 A1–A10，不得通过篡改它把 E1–E7 混作原 Gate A。E1–E7 应由新的 evidence queue checker（或七个场景 checker）独立汇总 captured/blocked/missing。

完成报告必须给出：

1. WP-FIX、E1–E7 的 captured/blocked/missing 表；
2. 每项 official source、raw/sanitized 路径、checker/self-test 命令和真实输出；
3. 每项从 raw 独立推导出的物理 shape 与关键顺序，明确不含 mapping 决策；
4. leak scan、4398/4399 回收、4096 listener PID 前后不变；
5. commit/file list、`git status --short`；
6. 明确声明零产品代码、零协议/WireDescriptor/capability/iOS 改动。

提交 evidence report 后**停止**。不得自动进入 C2 decoder、C3–C7 translator、capability activation、构建安装或 owner 测试。下一步是设计 owner依据真实样本修订 canonical §6，然后另发一次集中产品实施指令。
