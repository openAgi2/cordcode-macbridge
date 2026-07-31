# 代码走读级评审：MacBridge Claude list_sessions Runtime CPU Plan

Date: 2026-07-05
被评审文档：`../cordcode-ios/docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`
评审范围：MacBridge 侧（`go-bridge/` + `agent/claudecode/`）实际代码路径
评审方式：逐条对照源码核实论断，标注 file:line，附遗漏问题与修正建议

## 评审结论

文档的核心诊断**成立且证据链清晰**：高 CPU 根因是 `list_sessions` 热路径里对 Claude transcript 的重复全量解析。`enrichSessionStateWithAgent` 在每个被列出的 session 上各调用一次 `GetRunningSessionIDs`，并对每个不在 running map 的 session 回退到 `findClaudeSessionFile` + `detectClaudeTranscriptState`，二者都是逐行 `json.Unmarshal` 的 O(文件大小) 扫描。在 144 sessions / ~116MB transcript 的现场，单次请求 ~9.5–11.8s、~100% CPU 与该假设完全自洽。

但文档存在 **4 处遗漏/偏差**，会直接影响修复的有效性与优先级：

1. **遗漏了一处每次请求都执行的 `/tmp/bridge-sessions.json` 调试写入**，它落在文档反复引用的 `wire_mapping_ms` 计时窗口内，使该指标的解释偏移（见 §3.1）。
2. **读路径里有写副作用**：list 路径会对每个 idle session 调 `markIdle` 改 registry，文档未提及（§3.2）。
3. **"用 owned runtime state 替代 transcript inference" 与 Claude 外部 turn 的架构现实冲突**：另一个 Terminal 发起的 turn MacBridge 没有 owned state，文档的长期方向需要补一个边界说明（§3.3）。
4. **6 条修复项缺少优先级**，最大收益项（#3 杀 transcript fallback）与次收益项（#2 hoist running map）混在一组里，落地时容易被等价对待（§4、§5）。

总体可进入实现，但建议按 §5 的优先级重排，并补 §3.1 的清理。

---

## 1. 代码走读：文档论断逐条核实

### 1.1 热路径调用链 — ✅ 属实

文档所述调用链与源码一致。Claude 分支的 list 入口在 [handlers.go:1918-1932](../../cordcode-macbridge/go-bridge/handlers.go):

```go
mappingStarted := time.Now()
allSessions := h.claudeSessions.list(projectKey, metrics.context())
for i, s := range allSessions {
    allSessions[i] = h.enrichSessionStateWithAgent(s, agent)   // ← 每 session 一次
}
result := paginateSessionList(allSessions, extractStringParam(msg, "cursor"), limit)
if data, err := json.MarshalIndent(result, "", "  "); err == nil {
    _ = os.WriteFile("/tmp/bridge-sessions.json", data, 0644)  // ← 见 §3.1
}
metrics.wireMapping += time.Since(mappingStarted)
```

`enrichSessionStateWithAgent` 内部对 claudecode 在 [handlers_opencode.go:123-156](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 调用 `GetRunningSessionIDs`（每 session 一次），并在 session 不在 running map 时回退：

```go
if lister, ok := agent.(core.RunningSessionLister); ok {
    runningMap, err := lister.GetRunningSessionIDs(context.TODO())  // ← 每 session 一次
    if err == nil {
        if runningMap[sessionID] { state = "running" }
        else {
            _, sessPath := findClaudeSessionFile(sessionID, "")     // 遍历所有 project dir
            if sessPath != "" {
                state = h.detectClaudeTranscriptState(sessPath)     // ← 全量 transcript 扫描
                ...
            }
            h.sessions.markIdle(sessionID)                          // ← 见 §3.2
            usedTranscriptFallback = true
        }
    }
}
```

### 1.2 "GetRunningSessionIDs 每 session 重复调用" — ✅ 属实

[handlers_opencode.go:124](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 的 `GetRunningSessionIDs(context.TODO())` 在 `enrichSessionStateWithAgent` 内部，而后者在 list 循环里被调 N 次（N=被列出的 session 数）。文档 §Fix#2 描述准确。

补充：`GetRunningSessionIDs` 本身（[claudecode.go:587](../../cordcode-macbridge/agent/claudecode/claudecode.go)）只读 `~/.claude/sessions/*.json`（小的 pid/sessionId/cwd stub），并仅对**存活 PID** 调 `isSessionExecuting` 做一次 transcript 全量扫描（[claudecode.go:631-636](../../cordcode-macbridge/agent/claudecode/claudecode.go)）。因此它的单次开销 ≈ `读 sessions 目录` + `K × transcript 全扫`（K=存活 Claude 进程数）。重复 N 次后 = `N × (sessions 目录读 + K × transcript 全扫)`。

### 1.3 "idle session 全部回退到 transcript 扫描" — ✅ 属实，且这是真正的成本大头

文档 §Fix#3 抓对了要点，但措辞偏保守。实际代码对**每一个不在 running map 的 session**（在 144 个全 idle 的现场即全部 144 个）都走 `detectClaudeTranscriptState`（[handlers_relay.go:465-535](../../cordcode-macbridge/go-bridge/handlers_relay.go)）：

```go
scanner := bufio.NewScanner(f)
buf := make([]byte, 0, 64*1024)
scanner.Buffer(buf, 1024*1024*16)            // 单行缓冲上限 16MB
for scanner.Scan() {
    var entry claudeTranscriptRelayEntry
    if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil { continue }
    ...
}
```

`isSessionExecuting`（[claudecode.go:501-585](../../cordcode-macbridge/agent/claudecode/claudecode.go)）同样逐行 `json.Unmarshal` 全文。两个函数都是 **O(transcript 字节数)**，且对同一文件在 list 路径上没有任何缓存。

数值对账：`dataset_bytes=116363936`（~116MB）即所有 transcript 总字节。每次 `list_sessions` 把这 ~116MB 全部 `json.Unmarshal` 一遍（每个 session 一份），按 Go json 行解析 ~10–50 MB/s 估算约 2–12s，正好落在观测的 9.5–11.8s 区间。**这与 `cache_hit=true` 并不矛盾**：catalog 的 fingerprint 命中时确实跳过了 `parseSession`（[claude_session_catalog.go:192-195](../../cordcode-macbridge/go-bridge/claude_session_catalog.go)），所以 catalog 不再是成本来源，成本全在 enrichment。文档对此的判断正确。

### 1.4 "不是 Claude 模型/Relay 卡住" — ✅ 论证有效

文档用"02:11–02:14 窗口内没有新的 `send_message`/`text_delta`/`turn_completed`，而上一轮真 turn 已于 01:44 完成"作为判别依据，逻辑成立。`sample` 落点在 transcript 解析而非网络/HPKE 解密，也支持该结论。

---

## 2. 文档准确的部分（建议保留）

- 现场证据（PID、CPU、metrics、sample 热栈）记录完整，可作为回归基线。
- "即时缓解"段没有夸大重启的作用，明确写了"重启不消除底层 bug"。
- 验收标准里的"catalog cache hit 时 list_sessions 不再秒级耗在 wire_mapping_ms""GetRunningSessionIDs 不再每 session 一次"都是可度量、可回归的正确指标。
- 与历史 spurious-idle 问题（[think.md:163-204](../../../cordcode-macbridge/think.md)）做了边界区分，没有把两个问题混淆。

---

## 3. 文档遗漏的问题

### 3.1 【P1】`/tmp/bridge-sessions.json` 调试写入落在 `wire_mapping_ms` 窗口内

[handlers.go:1924-1926](../../cordcode-macbridge/go-bridge/handlers.go) 在**每次** `list_sessions`（Claude 分支）都执行：

```go
if data, err := json.MarshalIndent(result, "", "  "); err == nil {
    _ = os.WriteFile("/tmp/bridge-sessions.json", data, 0644)
}
```

`git blame` 显示这两行由 commit `2b9c1550`（2026-06-24）整体引入，明显是开发期调试残留。问题：

1. 它落在 `mappingStarted := time.Now()` … `metrics.wireMapping += time.Since(mappingStarted)` 计时窗口内（[handlers.go:1918,1927](../../cordcode-macbridge/go-bridge/handlers.go)）。文档反复用"`wire_mapping_ms ~= request_total_ms` ⇒ 成本在 enrichment"做推断，但该指标其实**也被这次 MarshalIndent+磁盘写入污染**，会使"enrichment 是唯一成本"的结论略有偏移。
2. 文档明确写"The expensive work is not catalog enumeration or **JSON response encoding**"，但此处确实存在一次全量 `MarshalIndent`（虽然 marshal 的是分页后的 wire 元数据，不是 116MB transcript 内容，量级有限，但仍是每次请求都做的无用功）。
3. 写 `/tmp` 与 CLAUDE.md 的日志/数据规范也不一致（日志已统一到 `~/Library/Application Support/CordCode Link/`，见 CLAUDE.md「日志」段）。

**结论**：这不会是 9.5s 的主因（主因仍是 §1.3 的 transcript 全扫），但是文档完全没提到它，且 6 条修复项没有一条会顺手删掉它。建议作为独立小改动先行清理。

**对文档的修正**：在 §Incident Evidence 或 §Fix Direction 增加一条"移除 `/tmp/bridge-sessions.json` 调试写入"，并注明它会使 `wire_mapping_ms` 指标更准确地反映纯 enrichment 成本，便于后续回归度量。

### 3.2 【P2】读路径有写副作用：list 路径对每个 idle session 调 `markIdle`

[handlers_opencode.go:140](../../cordcode-macbridge/go-bridge/handlers_opencode.go)：

```go
} else {
    state = "idle"
}
h.sessions.markIdle(sessionID)   // ← 在 list（读）路径里改 registry
usedTranscriptFallback = true
```

`markIdle` 会取 `sessionRegistry` 的互斥锁并改写 `trackedSession.state`（[types.go:266](../../cordcode-macbridge/go-bridge/types.go)）。这意味着每次 `list_sessions` 都会对所有 idle session 做一次锁竞争 + 状态写。在 144 sessions 的 burst 刷新下：

- 读路径里持锁写共享状态是代码气味（idempotent，但仍是不必要的锁开销）；
- 文档 §Fix 方向里"用 owned runtime state 替代 transcript inference"如果落地，这个 `markIdle` 副作用也应一并从读路径剥离（registry 的 running/idle 应由 send/turn_completed/process exit 等**事件**驱动，而非由 list 读驱动）。

文档未提及该副作用，建议在 §Fix#5 里显式补一句。

### 3.3 【P2】"owned runtime state 优先"与 Claude 外部 turn 的现实冲突

文档 §Fix#5 写："Transcript inference should be a fallback for cold recovery and history load, not a hot list path." 方向正确，但需要与 CLAUDE.md 的架构现实对齐：

> Claude 没有共享 server 端口…用户在另一个 Terminal 发起的 Claude turn 只能通过共享 JSONL 历史被发现。

也就是说，**对"另一个 Terminal 起的 Claude turn"，MacBridge 根本没有 owned state**，transcript 是唯一信号源。文档 §Fix#1 的失效触发条件里列了"send_message / turn_completed / abort / Claude process exit / file relay 状态翻转"，但这些都是**本 bridge 自己驱动的 turn** 的失效信号，对**外部 turn** 全部失效——外部 turn 的发现只能靠 TTL 到期后重扫 `~/.claude/sessions/*.json` + transcript。

**对文档的修正**：

- §Fix#1 应明确：TTL 不仅是"折叠刷新 burst"的优化，更是**外部 turn 检测的正确性兜底**；事件驱动失效只能加速本 bridge turn 的收敛，不能替代 TTL。
- §Fix#5 应区分两条路径：
  - **list 热路径**：不依赖 transcript inference，显示 cached/last-known 状态即可（外部 turn 的精度由低频后台轮询或开 session 时的按需检查补足）；
  - **后台/按需路径**：transcript inference 仍是外部 turn 的唯一来源，不能被砍。

这与 CLAUDE.md"不能照搬 Codex/OpenCode 的'收到广播后停止 polling'策略"的原则一致，建议在文档里点名引用。

### 3.4 【P3】修复项缺少优先级与收益估算

文档把 6 条修复并列给出，没有标注各自对 9.5s 的边际收益。实际收益差异巨大：

| 修复项 | 对 9.5s 的边际收益 | 实现复杂度 |
| --- | --- | --- |
| #3 避免 list 路径的 transcript fallback | **极高**（直接消去 ~116MB/请求 的全扫） | 低-中 |
| #2 每 request 只算一次 running map | 中（消去 N×K 的冗余 transcript 扫描，K=存活进程数） | 低 |
| #4 transcript state 按 file identity 缓存 | 中（仅在仍有 transcript inference 残留时生效） | 中 |
| #1 GetRunningSessionIDs TTL 缓存 | 低-中（单次 GetRunningSessionIDs 不是主因） | 低 |
| #5 owned state 优先 | 长期架构收益，短期对 list CPU 收益=与 #3 重合 | 高 |
| #6 请求合并/背压 | 低（治标，且 iOS 侧刷新频率不在 MacBridge 控制面内） | 中 |

不做 #3 而先做 #1/#6，CPU 不会明显下降。建议文档补这样一张表。

---

## 4. 修复方案的修正与补强

### 4.1 增加：先删 `/tmp` 调试写入（独立小改）

见 §3.1。这一步应在做任何性能修复**之前**完成，使 `wire_mapping_ms` 指标先回到"纯 enrichment"的真实口径，否则后续度量会失真。

### 4.2 修正 Fix#2 的实现形状

文档给的 shape：

```go
runningMap := getCachedClaudeRunningMap(ctx, agent)
for _, session := range allSessions {
    enrichSessionStateWithKnownRunningMap(session, runningMap)
}
```

方向对，但需注意：当前 `enrichSessionStateWithAgent` 是被 **多个调用点**复用的（`handleListSessions` 的 claude 与非 claude 分支、`ocHandleListSessions`、`ocHandleGetSession`、`ocHandleCreateSession`、`ocHandleResumeSession` 等，见 [handlers_opencode.go:211,248,330,349](../../cordcode-macbridge/go-bridge/handlers_opencode.go) 与 [handlers.go:1898,1921](../../cordcode-macbridge/go-bridge/handlers.go)）。重构时建议：

- 保留 `enrichSessionStateWithAgent` 单 session 的签名给单 session 路径（get/create/resume）；
- 新增 `enrichSessionStatesWithAgent([]map, agent) []map` 批量版本，内部只取一次 runningMap，专供 list 路径；
- list 路径（Claude 分支 + 非 claude 分支 + ocHandleListSessions）切到批量版本。

避免为了优化 list 路径而把单 session 路径的语义也改了。

### 4.3 修正 Fix#3：fallback 触发条件要可证伪

文档列的"只对 current/open / registry 里 recently running / transcript 自上次缓存后变化 的 session 做 fallback"是对的方向，但缺一个明确兜底：**list 路径里宁可报 `idle`（或 `unknown`），也不要触发 fallback**。理由：

- list 的职责是给 sidebar 一个状态点，不是给"正在执行的当前 session"的精确执行态；
- 精确执行态应由用户**打开**该 session 时（`get_session` / `get_session_messages` 路径）按需计算，那里天然只涉及 1 个 session，全扫一次可接受；
- 这样 list 的成本就从 O(总 transcript 字节) 降到 O(sessions 数) 的纯元数据。

建议把"list 路径不做 transcript fallback"作为验收硬条件之一（见 §6）。

### 4.4 补强 Fix#4：缓存键的边界条件

`sessionID + path + size + mtime` 是合理的廉价指纹，但需补两条：

- **运行中的 session**：transcript 每个 token 都追加，size/mtime 持续变化 → 缓存基本永远 miss。因此该缓存只对 idle session 有意义；running session 应走 owned state（#5），不要依赖该缓存。
- **跨进程一致性**：`mtime` 在不同文件系统（APFS vs 外接盘）分辨率不同，极短间隔的两次写可能 mtime 不变但 size 变。指纹里 `size` 必须与 `mtime` 同时比较，单看 mtime 不够。文档已经同时用了 size+mtime，OK，但建议在文档里点明"不可只看 mtime"。

### 4.5 补强 Fix#6：catalog 已有 inFlight 去重，不要重复造

[claude_session_catalog.go:85-97](../../cordcode-macbridge/go-bridge/claude_session_catalog.go) 已经有 catalog 快照级别的 inFlight 去重。文档 Fix#6 的"请求合并"如果再加一层 list 级 coalescing，需明确两层边界，避免：

- catalog 去重只保护"快照构建"，不保护"enrichment"；
- list 级 coalescing 要小心：iOS 不同 cursor/limit 的 list 请求语义不同，不能简单"identical request 才合并"，否则会破坏分页语义。建议默认**不做** list 级 coalescing，而是靠 #2+#3 把单次 list 压到几十 ms 内，burst 也就不再是 convoy。

---

## 5. 建议的落地优先级

1. **先删 `/tmp/bridge-sessions.json` 写入**（§3.1），让 `wire_mapping_ms` 回到真实口径。Trivial。
2. **Fix#3 + Fix#2 一起做**：list 路径不做 transcript fallback；`GetRunningSessionIDs` 在 list 里每 request 只调一次。这两条合起来是把 list 从 O(总 transcript 字节) 降到 O(sessions 数) 的关键，预期单次 list 回到毫秒级。
3. **Fix#4**：对仍需 transcript inference 的少数场景（如 open session 路径）按 size+mtime 缓存。
4. **Fix#1**：给 `GetRunningSessionIDs` 加 1–3s TTL，TTL 同时作为外部 turn 检测兜底（§3.3）。
5. **Fix#5**：长期，把 registry 事件驱动化，把 `markIdle` 从读路径剥离（§3.2）。
6. **Fix#6**：除非 #2+#3 后仍有 convoy，否则不做。

每一步都应能用文档 §Acceptance Criteria 里的指标回归（CPU、`wire_mapping_ms`、`GetRunningSessionIDs` 调用次数、transcript 重解析次数）。

---

## 6. 测试与验收的补强

文档的测试计划方向正确，补几点：

1. **加一条"list 路径不触发 transcript fallback"的硬断言**：在 fixture 里放一个 idle session 的 transcript，断言一次 `list_sessions` 后该文件被 `Open`/读 0 次（可用 `os.Open` 的 hook 或把 `detectClaudeTranscriptState`/`isSessionExecuting` 抽成可计数的 seam）。这条比"unchanged transcript 不被 reparse"更绝对，因为它对应 §4.3 的设计决定。
2. **回归 fixture 的总量校验**：文档建议"144 sessions、>100MB 等效"。建议同时断言 **catalog cache hit 路径下 `list_sessions` 的 `request_total_ms` < 某阈值**（例如 < 200ms），而不是只断言"不 O(total bytes) 解析"——后者不易在 CI 里自动度量，前者可。
3. **外部 turn 兼容性测试**：构造一个"另一个 PID 持有 `~/.claude/sessions/x.json` 且 transcript 最后一条是 user"的 fixture，验证 TTL 到期后 list 能把该 session 标为 running（§3.3 的兜底），避免 #3 把外部 turn 检测一起砍掉。
4. **去掉 `/tmp` 写入后**，加一条"list_sessions 不写任何 `/tmp` 文件"的断言，防止调试代码回潮。
5. 文档建议的命令 `go test ./agent/claudecode ./go-bridge -run 'RunningSession|ListSessions|SessionState'` 可用；注意 `agent/claudecode` 的 codex 相关 case 需要前置 PATH（见 memory `codex-cli-path-resolution`），与本次 Claude-only 改动无冲突，但若用 `./...` 跑全量要记得。

---

## 7. 附录：关键代码行号索引

| 文档论断 | 代码位置 |
| --- | --- |
| list Claude 分支入口 | [handlers.go:1883](../../cordcode-macbridge/go-bridge/handlers.go) |
| enrichment 循环 + /tmp 写入 | [handlers.go:1918-1927](../../cordcode-macbridge/go-bridge/handlers.go) |
| `enrichSessionStateWithAgent` | [handlers_opencode.go:100](../../cordcode-macbridge/go-bridge/handlers_opencode.go) |
| 每 session 调 `GetRunningSessionIDs` | [handlers_opencode.go:123-144](../../cordcode-macbridge/go-bridge/handlers_opencode.go) |
| idle 回退 + `markIdle` 副作用 | [handlers_opencode.go:131-141](../../cordcode-macbridge/go-bridge/handlers_opencode.go) |
| running 分支二次 transcript 校验 | [handlers_opencode.go:145-156](../../cordcode-macbridge/go-bridge/handlers_opencode.go) |
| `findClaudeSessionFile`（遍历所有 project dir） | [handlers.go:1850-1881](../../cordcode-macbridge/go-bridge/handlers.go) |
| `GetRunningSessionIDs` | [claudecode.go:587](../../cordcode-macbridge/agent/claudecode/claudecode.go) |
| `isSessionExecuting`（全量行扫） | [claudecode.go:501-585](../../cordcode-macbridge/agent/claudecode/claudecode.go) |
| `detectClaudeTranscriptState`（全量行扫） | [handlers_relay.go:465-535](../../cordcode-macbridge/go-bridge/handlers_relay.go) |
| catalog inFlight 去重 | [claude_session_catalog.go:83-115](../../cordcode-macbridge/go-bridge/claude_session_catalog.go) |
| catalog fingerprint 命中跳过解析 | [claude_session_catalog.go:192-195](../../cordcode-macbridge/go-bridge/claude_session_catalog.go) |
| registry `markIdle`/`markRunning` | [types.go:242,266](../../cordcode-macbridge/go-bridge/types.go) |
| 历史 spurious-idle 复盘 | [think.md:163-204](../../../cordcode-macbridge/think.md) |
| /tmp 写入引入 commit | `2b9c1550`（2026-06-24） |

---

## 一句话给 owner

文档诊断正确、可进入实现；落地前先删 `/tmp/bridge-sessions.json` 调试写入并按 §5 的顺序做（#3+#2 是真正决定 CPU 能否降下来的那一步），同时把"list 路径不触发 transcript fallback"补成硬验收项，外部 turn 的检测改由 TTL 兜底而非 list 热路径。
