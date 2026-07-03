# 本轮任务完成情况：OpenCode project-first session 列表改造

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge`(MacBridge)+ `../cordcode-ios/`(iOS)
- Plan: `docs/2026-07-02-opencode-project-first-session-list-plan.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge/.exec-plan/state/plan-2b62a78bbb5f.json`
- Legacy State File: `none`(state_store.legacy_path 指向 `.claude/exec-plans/...`,磁盘不存在,origin=new)
- Completion Report Verdict: `proved-complete`
- Queue Summary: `21/21 todos done, 21/21 proven, of which tests + 构建/安装/日志/gitleaks re-verified;t06/t07 真机视觉为 owner 自陈(self-attested)`
- Related Commits: 工作树未提交(全部为 uncommitted 改动);Mac Release 内嵌 runtime 元数据 `commit: 1e30b20ecd89, built: 2026-07-02T11:47:43Z`
- Generated At: `2026-07-02T11:54:16Z`

## 1. Overall Conclusion (总体结论)

OpenCode project-first session 列表改造跨 MacBridge + iOS 两仓全量完成并落盘验证。21 个 todo(7 个工作单元 × impl/tests/regression 三元组)全部 `done` 且 `verification.status==present`,无未决降级。

两类证据来源:
- **re-verified(本 session 重跑)**:go-bridge OpenCode list 测试、iOS `SessionLoadOwnershipTests`(9/9)、Mac Release 构建+覆盖安装、iOS 真机构建安装、runtime 日志(`opencode list_sessions directory=Chat limit=500 result_count=459`)、gitleaks(两仓 exit 0)。
- **self-attested(无法重放)**:t06 owner 真机视觉验证(项目分组/全局排序/无闪烁),由 owner 在 iPhone 16 Pro 上确认。

**关键说明:t06/t07 的 regression 原本 blocked 在「owner 真机手验」。owner 本次真机验证时发现三个真实缺陷,本 session 已修复并复验**:
1. **OpenCode 列表 ~15s 周期性闪烁** —— 根因是每次 `loadSessions()` 轮询/事件触发都整体清空 `openCodeProjectBuckets` + `sessions` 再重建。修复:刷新时保留已加载 bucket(`mergeOpenCodeBuckets`),冷启动行为不变。owner 真机确认不再闪烁。
2. **session 大量缺失 + 无加载更多** —— 根因:iOS 每项目 `limit=10` + go-bridge 上限 50 + OpenCode 1.17.13 array-only(无 cursor)。实测 Chat 有 459 条 session,被截断到 7 条且无法翻页。修复:go-bridge 上限 50→1000,iOS 项目桶 `limit=10→500`(常量 `openCodeBucketPageLimit`)。runtime 日志实证 Chat 现返回 459 条。
3. **项目目录只显示少量项目** —— 根因是 iOS `CCCodeBridgeProject` 仍按旧字段 `path` 解码,而 MacBridge canonical/proxy `list_projects` 均发 `directory`。修复:`CCCodeBridgeProject.path → directory`,调用方同步使用 `proj.directory`。2026-07-02 晚间真机复验:bridge 解码 `raw=30 mapped=30`;SessionVM merge 后 `fetched=30 manual=2 merged=29 sorted=29`,首批排序样本包含 `cc-switch`/`cdxswitch`/`Chat`,证明 §12 Desktop 项目目录可见成立。

另外修复了上一轮 feature 遗留的测试债:`SessionLoadOwnershipTests` 中两个用例 stub 漏 `projects`、两个用例引用已被取代的 root/project-scoped fetchSessions 实现(会挂起)。详见 §3/§4。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| Phase 0 — cursor gate | proven-done | proven-done | proven-done | 完整 | curl 实测 OpenCode 1.17.13 array-only/self-attested |
| Phase 1 — T02 pagination + T03 roots/diagnostics | proven-done | proven-done (re-verified) | proven-done | 完整 | go-bridge OpenCode list 测试 re-verified ok |
| Phase 2 — T04 protocol sync | proven-done | proven-done (re-verified) | proven-done | 完整 | iOS bridge client 解码 nextCursor/hasMore;doc 审计 self-attested |
| Phase 3 — T05 iOS project bucket | proven-done | proven-done (re-verified) | proven-done | 完整 | SessionLoadOwnershipTests 9/9 re-verified |
| Phase 4 — T06 load-more/cache/global | proven-done | proven-done (re-verified) | proven-done | 完整 | owner 真机手验(self-attested)+ 闪烁/覆盖修复复验 |
| Phase 5 — T07 delivery build/install/secret-scan | proven-done | proven-done (re-verified) | proven-done | 完整 | Mac/iOS 构建+安装+gitleaks+日志全 re-verified |

## 3. Key File Changes (关键文件变更)

**MacBridge(`/Users/jacklee/Projects/cordcode-macbridge`)**
- `go-bridge/handlers.go` —— `ocHandleListSessions` 的 limit 上限 `50 → 1000`,使客户端能一次拉满整个 OpenCode 项目(array-only 服务端返回 `min(limit,total)`)。
- `go-bridge/opencode-proxy.go` —— `OpenCodeSessionListOptions{Directory,Limit,Cursor}` / `OpenCodeSessionListResult`;`listSessions` 构造 `GET /session?limit=&cursor=`、发送 `x-opencode-directory`、双解码 array 与 `{data,cursor}` envelope。
- `go-bridge/handlers.go`(OpenCode handler)—— `ocHandleListSessions` 提取 limit/cursor、拒绝 `rootsOnly`+分页、输出 `opencode list_sessions` 结构化诊断。
- `docs/protocol/bridge-v1.md`、`docs/protocol/schema/bridge-v1.types.ts` —— 拆分 list_sessions 分页与 get_session_messages 的 `session_pagination` capability;list params 增 `rootsOnly`。
- `CHANGELOG.md` —— 记录 limit 上限提升与 project-first 协议。

**iOS(`/Users/jacklee/Projects/cordcode-ios`)**
- `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift` ——
  - **闪烁修复**:新增 `mergeOpenCodeBuckets(fresh:)`,刷新时保留已加载 bucket 的 sessions/cursor/hasMore/didLoad,只更新 project 元信息,并从存活 bucket 重新派生 `sessions`(替代无条件清空)。
  - **覆盖修复**:新增常量 `openCodeBucketPageLimit = 500`,冷启动/按需/翻页三处统一用它(原 `limit=10`)。
  - `ProjectSessionBucket` 状态、project-first 冷启动(≤3 非 global 项目首页)、global 排序最后、`loadOpenCodeBucketIfNeeded`/`loadMoreOpenCodeSessions`。
- `OpenCodeiOS/OpenCodeiOSTests/SessionListColdCacheTests.swift` ——
  - `testOpenCodePathUsesProjectFirstSessionBucketsAndNormalizesProjectDirectory` 断言 limit 由 10 改 500。
  - `testMultiProjectPath_scopeGuardHoldsAcrossFetchProjectsAwait` / `...staleRequestBailsBeforeFetchSessionsAfterScopeSwitch`:stub 补齐 `projects`(原漏掉导致项目优先路径取不到项目)。
  - `testMultiProjectPath_staleOwnerStopsProjectScopedSubtasks`:重写为 project-first 版(`fetchSessionPage` 中段提交 guard),用新 `fetchSessionPageDelayNanoseconds`/`onFetchSessionPageStarted` barrier。
  - `testMultiProjectPath_staleRootFetchDoesNotContinueToGlobalFetch`:移除(引用已被取代的 root→global fetchSessions 实现,与 project-first 不兼容会挂起;不变量已被另两个用例覆盖)。
  - `ColdCacheStubClient`:新增 `fetchSessionPageDelayNanoseconds` + `onFetchSessionPageStarted` barrier hook。
- `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendClient.swift`、`.../Bridge/CCCodeBridgeClient.swift`、`.../Bridge/CCCodeBridgeBackendClient.swift`、`.../Components/SidebarView.swift` —— `BackendSessionPage`/`BackendSessionListPaging`、bridge `listSessionsPage`、bucket 渲染(加载中/空态/查看更多)。
- `docs/protocol/...`、`remote-web/src/protocol/bridge-v1.ts` —— 镜像 Mac canonical 协议。
- `CHANGELOG.md` —— 记录覆盖修复与闪烁修复。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests
- Commands:
  - `go test ./go-bridge -run 'OpenCodeListSessions|OpenCodeListProjects|OpenCodeListDirectory' -count=1`
  - `xcodebuild test -only-testing:CCCodeTests/SessionLoadOwnershipTests`(以及 `-only-testing:CCCodeTests/SessionListColdCacheLoadSessionsTests`)
- Result: go-bridge OpenCode list 测试 `ok 0.916s`;iOS `SessionLoadOwnershipTests` 9/9 passed(1.725s),`SessionListColdCacheLoadSessionsTests` 7/7 passed。
- Attestation: `re-verified`(均在本 session audit 阶段重跑)
- Main test files: `go-bridge/handlers_test.go`、`OpenCodeiOS/OpenCodeiOSTests/SessionListColdCacheTests.swift`
- Artifact paths: `cmd:xcodebuild ... -> 9 passed`(2026-07-02T11:54Z);`/tmp/cordcode-flicker-fix/ios-session-tests.log`
- 备注:`go test ./go-bridge/...` 全量存在 `codex CLI not found in PATH` 的环境性失败(Codex 分页用例),与本次改动无关、为历史环境前置缺失。

### 4.2 Regression evidence
- **owner 真机验证**(iPhone 16 Pro,OpenCode 模式,真实 `external_http` 共享 server `127.0.0.1:4096`):项目分组、全局排序、加载/空态、**闪烁已消失**、session 列表从每项目 ~7 条变为可拉满(查看更多展开)。Attestation: `self-attested`(owner 视觉确认,无法重放)。
- **§12 项目目录复验**(re-verified,2026-07-02T21:03-21:04 +0800):临时诊断包安装到 iPhone 16 Pro 后通过 `devicectl --console` 抓取真实 bridge 路径日志:`bridge fetchProjects backend=opencode raw=30 mapped=30`;`SessionsVM projects fetched=30 manual=2 merged=29 sorted=29 ... mergedSample=.../cc-switch .../cdxswitch .../Chat`。随后移除临时诊断日志并重新构建安装干净包。
- **runtime 日志**(re-verified):`opencode list_sessions directory=/Users/jacklee/Projects/Chat limit=500 cursor_present=false result_count=459 next_cursor_present=false duration_ms≈50`;`opencode-cc-connect` result_count=57。证明 bridge 上限提升 + iOS limit=500 后真实项目一次拉满。
- **Mac 安装核查**(re-verified):`lsof -nP -iTCP:8777` → `cordcode-bridge-runtime` PID 89619 来自 `/Applications/CordCodeLink.app`;runtime.json 新 bridgeEpoch、drivers=claude,opencode,codex;relay connected;启动日志无 error/panic,OpenCode proxy 注册到 `127.0.0.1:4096`。
- **iOS 安装**(re-verified):`scripts/run.sh device --device BFC431AC-...` → `Launched application with org.openagi.cordcode`。
- Artifact paths: `/tmp/cordcode-flicker-fix/mac-build.log`、`/tmp/cordcode-flicker-fix/ios-rebuild-install.log`、`~/Library/Application Support/CordCode Link/logs/go-bridge.log`

### 4.3 Secret scan
- Command: `gitleaks detect --source=.`(MacBridge 与 iOS 各一次)
- Result: 两仓均 exit 0(no leaks)
- Attestation: `re-verified`(本 session 重跑)

## 5. Known Limits / Honesty Notes (边界与自陈)
- 本报告由执行工作的同一 agent 撰写,证据默认 self-attested;凡标注 `re-verified` 者为本 session 内重跑命令所得。owner 真机视觉为 self-attested,无法机器重放。
- `agent/opencode/opencode.go` 生产缓存写入顺序(disk-then-memory)未改;仅加固了对应测试竞态。
- OpenCode 1.17.13 仍为 array-only/no-cursor:无服务端「加载更多」是正确行为;覆盖靠一次性 `limit=500`(bridge 上限 1000)。单项目超过 500 条(罕见)仍会被截断;待上游支持 cursor 后可零改动启用真正的分页加载更多。
- go-bridge 全量 `go test` 的 Codex 用例需本机装有 `codex` CLI,属环境前置,非本次回归。

## 6. 真机复验记录 (2026-07-03)

owner 真机验收 OpenCode 模式 session 列表后确认：项目标题已为 basename（不再是全路径）；每个项目按真实 root session 数量展示；`Chat` 等大项目首页 5 条且「加载更多」可翻页（cursor 分页验证通过）；小项目（cdxswitch 2 条、cligate 3 条）显示真实数量、不再卡在「加载中」。go-bridge.log 无 ERROR/WARN（relayEvents idle timeout 属正常空闲）。

最终修复方案偏离了设计文档中 array-only/no-cursor 的保守判断：实际通过阅读 OpenCode 源码（`packages/opencode/src/session/session.ts` `roots` SQL 过滤、`packages/server/src/handlers/session.ts` limit 默认 50/100）并参考 Codex/Claude Code 模式的 session 加载路径，改为 MacBridge 对每个项目一次性拉取 100 条 root session、在内存排序后用 `paginateSessionList`（与 Codex/Claude 完全相同）做 cursor+limit 真分页。完整方案见根目录 `think.md`。
