# 监工审计报告 12 号（对应监工指令 12 号）

> **审计时间**：2026-08-21T04:08:00Z  
> **审计严格度**：严格（源码路径、barrier fixture、独立 20 轮/race/full 回归、runtime 只读核验）  
> **Verdict**：**partial**

## 0. 独立复验

- `go test ./go-bridge -run 'TestAudit01[12]' -count=20 -timeout 5m`：PASS，200.8s。
- `go test -race ./agent/opencode-web -run 'TestAudit01[012]|TestQuestion' -count=20 -timeout 5m`：PASS，12.4s。
- `go test ./... -count=1 -timeout 10m`、`go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- 报告中的第一条命令未引用 `[12]`，在仓库默认 zsh 下会被 glob 拒绝；独立复验使用引用后的可复现命令。该项是报告命令勘误，不是产品失败。

## 1. Overall Verdict

`gateAdmitRequested` 的原始漏洞已经正确封住：terminal/duplicate 判定确实早于 mapping 写入；stale recovery response也不会污染 `pendingQuestions`。两个 owning negatives确实穿过真实 handler 和具体 responder，Projection与 POST计数断言有效。

但完成报告关于 GET fallback 的主张不成立。`lookupQuestion` 对每个 GET row执行 `notePendingShape(...)`；该函数在 lifecycle 已 terminal 时正确拒绝写 mapping，却不返回 admission结果。调用者随后无条件执行 `if p.ID == interactionID { return &p, nil }`。因此当 terminal fact 已落、内存 mapping 已空，但当前权威 GET仍暂时返回同一 stale row时，`ResolveUserInput` 仍拿到 request shape并发出二次官方 POST。

这直接违反指令 12 §2 的“`lookupQuestion` 不得绕过 lifecycle terminal状态”，也是一个可执行产品路径，不是文档问题。范围仍然单点、无需新样本，按 **partial + directive-013** 处理。

## 2. 已验证通过

| 项目 | 结论 | 证据 |
|---|---|---|
| late asked memory mapping seal | ✅ | terminal check在 mapping 写入前；2秒窗口零 POST |
| stale recovery memory pollution | ✅ | parked stale response释放后不恢复 `pendingQuestions` |
| terminal Projection invariant | ✅ | answered/rejected保持一个原位 part |
| 20x/race/full regression | ✅ | 独立复跑全部 PASS |
| GET fallback terminal gate | ❌ | admission拒绝后 `lookupQuestion` 仍无条件返回 row |
| zero second POST with stale current GET | ❌ 未覆盖 | 测试在二次 resolve前把当前 `/question` 改为空 |

## 3. 测试为何没有抓到

`TestAudit012_BarrierStaleRecoverySecondResolveZeroPOST` 的第二次 StartSession确实捕获了一份 stale row（fixture在 gate 前复制 response body），因此正确验证了 stale recovery不污染内存。但测试在释放 gate 前调用 `setPendingQuestions("[]")`；后续 resolve的 GET fallback读取的是新的空列表，不会执行 `notePendingShape`/无条件 return旁路。

必须增加一个独立用例：terminal已落且 mapping为空，当前 `/question` 仍返回同 interaction stale row；真实 resolve handler必须拒绝、零 POST。测试不能预先把 endpoint改空，也不能用 server 404/409替本地 terminal判定兜底。

## 4. Partial 处理

- 监工指令 13 号只让 GET fallback返回 lifecycle admission结果，并增加 stale-current-GET owning negative。
- 不改 lifecycle来源、history、Projection、协议、能力、canonical或 iOS。
- 完成后重跑 directive-011/012/013 20轮、race/full与真实 question sandbox，Mac Release重装，然后停等 audit-013。
