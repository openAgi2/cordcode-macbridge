# 监工审计报告 14 号（对应监工指令 14 号）

> **审计时间**：2026-08-21T04:58:21Z  
> **审计严格度**：严格（源码/diff、owning full path、20轮 affected race、full/vet/build、基线 race 隔离、runtime只读核验）  
> **Verdict**：**verified（技术轨）；放行 owner 真机 message-page 重试**

## 0. 独立复验

- `go test ./agent/opencode-web -run 'Reasoning|RichSessionHistory|History' -count=1 -timeout 3m`：PASS。
- `go test ./go-bridge -run 'OpenCodeWeb|Projection|Hydrate|Audit01' -count=1 -timeout 5m`：PASS（16.3s）。
- `go test ./... -count=1 -timeout 10m`：PASS（go-bridge 64.9s，其余全部绿）。
- `go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`、canonical checker self-test：PASS。
- `go test -race ./agent/opencode-web ./go-bridge -run 'TestAudit014|ReasoningExplicitlyUnsupportedOnLiveCarriers|GetRichSessionHistoryMapsParts' -count=20 -timeout 5m`：PASS。
- 指令原 broad race 正则：FAIL，命中 `withFastClaudeFileRelay` 测试全局变量清理与后台 Claude relay 读取的 race。监工用 `git archive 04d54e2` 建立不修改当前仓库的基线副本，独立复跑 `TestClaudeFileRelayStartedBeforeHydrate|TestHandleGetSessionProjectionRunningSessionWithinBudget`，同一 source location race 复现。因此这是既有 Claude 测试夹具债务，不是 `be6a1d7` 引入，也不覆盖 E2b 产品路径。
- 安装态：`/Applications/CordCodeLink.app/.../cordcode-bridge-runtime -version` 返回 commit `be6a1d72d9d3`；8777 PID 32592 路径来自 `/Applications`；4096 PID 48742 仍 owner-managed；4398/4399 无 listener。

## 1. Overall Verdict

Directive-014 的根因修复成立：`mapRichHistoryEntry` 不再用失败的 synthetic live E2 结论拒绝已由 E2b 证明的 HTTP persisted reasoning，而是把非空正文放入既有 `RichHistoryEntry.Parts`，随后由原有 `openCodeRichHistoryEntryToProjectionEvents` 产生 `reasoning_delta` 并进入唯一 private Kernel hydrate transaction。没有新增 reducer、writer、fallback、协议字段或 iOS 路径。

full-path owning test 穿过真实 `handleGetSessionProjection`，snapshot 中两个 turn 各有且只有一个 reasoning part，answer text 不含 reasoning，step markers 不进入 Projection。测试运行日志的 `headRev=8` 与 2×（user + reasoning + text + terminal）唯一事件序列一致；affected race 20轮无报警。

Direct-SSE reasoning 仍由三条 live carrier 测试钉为 explicit unsupported，且 descriptor 没有新增 reasoning capability。本轮没有把 HTTP history 证据外推成 live 顺序。

开发侧 owner-data preflight 未保留可独立复跑脚本/日志，因此其“100中87、重度643”等统计仍属 self-attested，不提升为监工证据。不过，监工在指令下发前已只读确认两个真实失败 session 均是 E2b shape；本轮又独立验证了相同 shape 的 full-path hydrate、安装 binary commit 和运行态。因此它不阻塞 owner UI 放行，最终产品结论由 owner 点击重试给出。

## 2. 主张与实况

| 主张 | 独立结论 | 证据 |
|---|---|---|
| E2b reasoning 不再整页失败 | ✅ | adapter + handler full path 独立 PASS |
| reasoning 保序、恰好一次、不污染 answer | ✅ | `TestAudit014_*` 源码审查 + 20轮 affected race |
| step markers 零 Projection part | ✅ | explicit switch + full-path snapshot assertion |
| 单一 Kernel owner/无 fallback | ✅ | diff 仅 history mapper/tests；既有 hydrate pipeline 未改 |
| live reasoning 未偷开 | ✅ | live carrier negatives + descriptor 源码 |
| full/vet/build/canonical | ✅ | 监工独立复跑 |
| broad race 全绿 | ❌，但非本轮回归 | 04d54e2 基线同一 Claude fixture race 可复现；affected suite 20轮绿 |
| owner read-only heavy preflight | ⚠️ self-attested | 无持久脚本/日志；不冒充监工复验 |
| 安装包正确 | ✅ | binary `-version` + 8777 process path |
| owner UI 已恢复 | ⏳ | 尚未点击；本审计仅放行重试 |

## 3. 路线图与漂移检查

- 根因锁死门满足：owner 日志 `projection.hydrate_failed`、真实 E2b HTTP shape、旧 mapper 唯一显式 failure branch 三方闭环后才改。
- 修改落在 canonical §6.3 C4 hydrate 环；未进入 live E2、协议、capability、iOS 或其他 backend。
- 没有阈值、sleep、裁判表、双源比较、半接管或回退。
- 红灯/full path 与产品实现同一提交，提交范围单一；报告没有把 proven 写成 owner done。

## 4. 非阻塞债务与诚实勘误

1. broad race 实际可在两个 Claude projection 测试中暴露同一 `withFastClaudeFileRelay` 全局变量 race，不只报告正文点名的一个测试；根因/source location 相同，且修复前基线已存在。另立 Claude 测试基础设施任务处理，不夹入 E2b 修复。
2. `history.go` 的旧函数注释仍写“reasoning part fails the mapping”，已与实现不符；这是注释债务，不影响运行与本轮 owner 重试，不因此扩大产品补丁。
3. 审计开始后出现未跟踪 `handoffs/handoff-20260821-1255.md`。它不在产品/报告提交中，监工保留未动；因此“commit tip 无产品 WIP”成立，但当前字面 worktree 不再完全 clean。

## 5. 收口

- exec-plan proven：✅（75/75，自证轨）。
- supervisor verified：✅ directive-014 技术轨。
- owner 真机 UI：⏳，现在放行 message-page 重试；未通过前产品仍不算 done。
