# dsh-web 单实例 3080 收敛方案 评审报告

- 评审对象：`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`（提案，未实施，未 commit）
- 评审日期：2026-08-19
- 评审方法：audit-plan 纪律（凡内容形状 / 代码行为 / 活体拓扑断言必须有本 session dump，禁止靠记忆或类比）适配到架构方案——本文不是外部格式解析稿，核验对象改为「现行代码锚点、原设计 §4.2 原文、协议错误面、本机双实例活体」
- 核对树：main `5f9237b`（与提案自称的 2026-08-19 main 行号一致）
- 活体：本机 *此时* 正处于提案描述的分裂态（见 §3）

## 1. 结论

**修改后通过。** 路线方向正确（把实例身份从「谁 spawn 的」改成「3080 这个位子」），事故根因经源码 + 本机活体双重坐实，比提案写的还更硬：今晚 19:59 的一次 3080 重启，停机大约 17 秒，流重连 2 秒内就粘到了 8 月 18 日留下的 3096 孤儿；hello / Management 缓存仍写「external 3080」，活 `InstanceStatus` 已是 `managed 3096 pid 1406`。

四条现行缺陷表、排除的三条替代路线、关停「不杀 + 下次收养」、3096–3196 退役，都站得住。owner 已裁过「应该尝试启动 3080」，本提案是对该裁决的正确落点。

但提案把最快触发器写成了 60s watcher，漏掉了已接线时 **2s 流重连** 才是主路径；「实例重连中」没有 wire 形状；`Resolve` 持锁跨 30s spawn 与「不阻塞 RPC」冲突；`WithProbeURLs` 与「永远 spawn 3080」未对齐。这些都是文档层必改，不推翻路线。

## 2. 核验总表

| # | 提案断言 | 本 session 证据 | 评级 |
|---|---|---|---|
| 1 | ladder 锚点 `resolver.go:321` / `:47`、`sessions.go:22`、`session_discovery.go:33` | 行号、常量、函数名全部对上 | 🟢 |
| 2 | 粘滞：缓存活着永不重探 3080 | `Resolve` 第一步 `probeInstance(cached)` 成功即 return；注释原文即 S3 | 🟢 |
| 3 | `clientFor` 每次目录操作都走 Resolve | `sessions.go:22-27`；`ListSessions:89` 等全部 `clientFor` | 🟢 |
| 4 | 60s watcher 戳 `ListSessions` | `sessionDiscoveryInterval = 60 * time.Second`；dsh-web 走 `agent.ListSessions` | 🟢 |
| 5 | `managedBootTimeout = 30s` | `resolver.go:94` | 🟢 |
| 6 | 原设计 §4.2 S3「探测仅启动时 / 后来起 3080 则共存」 | 原设计 :100 原文 + 实现注释 :319-320 一致 | 🟢 |
| 7 | `run_diagnostics` 能判别 `managed` + 3096–3196 | `diagInstance` 文案「托管实例 %s（…pid %d）」；`SourceManaged` 分支 | 🟢 |
| 8 | 翻转静默、hello_ack 不报警 | 今晚活体：缓存 agents=`external 3080`，live=`managed 3096`，status 仍 `available` | 🟢 现象 / 🟡 归因见 M2 |
| 9 | 「停机 >60s ⇒ watcher 近必然翻转」 | 数学对 watcher 成立；但今晚 **17s 停机 + 2s 流重连** 已翻转 | 🟡 充分非必要，主触发写错 |
| 10 | 重启 3080 当修复无效 | 3096 仍活 → 粘滞不看 3080；今晚 20:00:03 3080 回来后仍粘 3096 | 🟢 |
| 11 | 孤儿累积 | 3096 pid 1406 自 2026-08-18 11:37 活到现在（33h+），state 文件仍指向它；`Resolve` 收养注释 :358-361 明示「下次 spawn 换端口」 | 🟢 |
| 12 | boot-wait 已是端点语义 | `spawnManaged` 循环 `probeInstance(baseURL)`，不看 PID | 🟢 |
| 13 | `Stop()` = 杀本进程 spawn 的 managed | `resolver.Stop` → `managedStart.Stop`；收养实例 `cmd==nil` 不杀 | 🟢 且 §5 推荐与现行「收养不杀」同向 |
| 14 | `WithProbeURLs` 口子保留 | `dshweb.go:109-110` `opts["dsh_web_url"]` | 🟢 存在 / 🔴 与「永远 3080」未对齐 |
| 15 | 「实例重连中」可见错误 | 协议无此码；`AgentStatus` 只有 `available`/`not_configured`/…；iOS 无对应 UI | 🔴 未设计 |
| 16 | workspace.json 导致「列表也不出现」 | 今晚两边 `session.list` 都是同一 20 个 id；分歧在 turns/updatedAt。坑 5 是「未分组」不是「不在 list」 | 🟡 正交成立，归因过满 |
| 17 | 共享磁盘 store | 两边 list 集合相等；两会话 stats 分叉（31 vs 32 turns 等） | 🟢 |

## 3. 本机活体 dump（今晚，提案事故的完整复现）

### 3.1 双实例活体

```
3080  LISTEN  pid 21312  node /opt/homebrew/bin/dsh web
                 STARTED 2026-08-19 20:00:03
3096  LISTEN  pid 1406   node /opt/homebrew/bin/dsh --profile web --host 127.0.0.1 --port 3096
                 STARTED 2026-08-18 11:37:05   (已 33h+)
```

`~/Library/Application Support/CordCode Link/dsh-web-managed-server.json`（0600）：

```json
{
  "version": 1,
  "source": "managed",
  "url": "http://127.0.0.1:3096",
  "port": 3096,
  "pid": 1406,
  "updated_at": "2026-08-18T03:37:13Z"
}
```

### 3.2 `host.describe` 两个宇宙

| | 3080（用户） | 3096（managed / 孤儿） |
|---|---|---|
| cwd | `/Users/jacklee/Projects/cordcode-ios` | `/` |
| attachedSessions | 6 | 2 |
| provider/model | opencode-go / mimo-v2.5 | 同 |
| session.list | 20 | 20（同一 id 集合） |
| 分叉例 | `session-6d807c35…` turns=31 updatedAt=…874584 | 同 id turns=**32** updatedAt=…015339 |
| 分叉例 | `session-d8ac5e0c…` turns=5 | 同 id turns=**1** |

同一磁盘 catalog、两套内存投影——提案「两个进程、两个内存宇宙」原文级坐实。

### 3.3 今晚翻转时间线（比 60s 窗口更硬）

```
19:38:54  dsh-web: instance resolved  source=external  baseURL=http://127.0.0.1:3080
19:38:54  stream open mux + host
19:59:46  stream ended mux/host  close 1006 unexpected EOF     ← 用户重启 3080
19:59:48  stream open host / mux                               ← 2.1s 后 clientFor→Resolve
20:00:03  新 3080 进程启动（pid 21312）                        ← 比重连晚 15s
```

19:59:48 那一刻：缓存 3080 已死、新 3080 还没起来、state 里的 3096 活着 → `loadState` 收养 → 粘死。**没有新 spawn，17 秒停机足够，2 秒流重连就是触发器。**

后续 Resolve 不打 `instance resolved` 日志（只有 `backgroundResolve` 打），所以翻转在日志里也是静默的。

### 3.4 判别器：缓存 vs 活体

| 面 | 输出 |
|---|---|
| `GET /internal/agents`（Management 缓存，近似 hello 当时） | `status=available` `reason=external dsh web instance at http://127.0.0.1:3080` |
| `POST /internal/agents/dsh-web/test`（`InstanceStatus()` → `Current()`） | `status=available` `reason=managed dsh web instance at http://127.0.0.1:3096 (pid 1406)` |

同一时刻两套说法、都是 `available`。这就是缺陷 2「翻转完全静默」的活体形状。

> 核验时调用了 `/test` 与 `/refresh`，只读 `InstanceStatus`/`Current()`，**没有**走 `Resolve()`，没有 spawn。`/refresh` 会改写 Management 缓存（此后 GET 也显示 3096）。

## 4. 问题清单

### 必改

**M1. §0.3 主触发器写成了 60s watcher，漏掉已接线时的 2s 流重连**

`streams.go:104-111`：`runStreamLoop` 断线后 `streamReconnectBackoff = 2s` 再 `clientFor` → `Resolve`。`shouldStartPassiveSubscription("dsh-web")` 为 true，iOS 连着时 mux/host 常驻。

今晚的翻转走的就是这条路，不是下一个 60s tick。60s 论证对「无客户端、无流」仍成立，但是充分条件，不是主路径。

连带：§10.2「杀 managed 后等下一个 60s tick 回到 3080」同样偏慢——流还在的话约 2s 就会重解析。

**修改建议**：§0.2 触发面改成三层表：RPC `clientFor` / 流重连 2s / watcher 60s。§0.3 改成「已接线 ⇒ 3080 停机超过 ~2s 且启动未完成即翻；无接线 ⇒ 停机超过 ~60s 必翻」。§8 增补「流重连行」。§10 恢复等待改成「任一 Resolve（流/RPC/tick）」。

**M2. 「hello_ack 的 InstanceStatus 只镜像启动时结果」归因过窄**

`InstanceStatus()` 读的是活的 `Current()`（`dshweb.go:164-176`）。`hello_handler.go:164` 每次 hello 都 `BuildAllAgentDescriptors`，会看到翻转后的 managed。真正冻住的是：

1. 已连着的 iOS 不再 hello，拿不到新 reason；
2. `GET /internal/agents` 的 `cachedAgentDescriptors`（今晚 GET=3080、`/test`=3096）；
3. 翻转后 status 仍是 `available`，没有拓扑变更事件。

「静默」成立；「只镜像启动时」不成立。

**修改建议**：改成「已连接客户端看不到拓扑变更；hello 重连会看到 source 字符串变了但 status 仍 available；Management GET 有一份独立缓存」。

**M3. 「实例重连中」没有协议形状——宽限期无法落地**

宽限内要对调用方「如实返回可见错误、不阻塞 RPC」。现行面：

- `AgentStatus`：`available` / `not_configured` / `service_not_running` / `port_conflict` / …，没有 reconnecting；
- 协议错误表有 `backend_unavailable`，没有「实例重连中」；
- iOS 对 `not_configured` 会按「未配置」藏入口，宽限 90–120s 绝不能走这条。

同时必须写清：

- `send_message` / 进行中 turn：坑 8 红线——3080 失联必须有终态（`turn_error` / send 错误），不能卡「执行中」；
- `ListSessions` 失败时 `snapshotBackendSession` 已保留 last-good fingerprint（`session_discovery.go:210-217`），宽限内列表不会被清空——这是该保留的，写进方案防实施者「出错就清 catalog」。
- 宽限中途新 hello：`InstanceStatus` 现在会落到 `Current()==nil` + 空 `resolveErr` → 「probe/managed spawn in flight」，语义是冷启动不是重连。

**修改建议**：§3 增一小节「宽限的 wire 契约」：建议复用 `service_not_running` 或 `backend_unavailable` + 稳定 message（不要新造 iOS 不认识的 code，除非同步改 iOS）；明确 **禁止** `not_configured`；in-flight turn 必须终态；`InstanceStatus` 增加 reconnecting 文案；hello_ack 在宽限内保持 backend 可见。

**M4. `Resolve` 持锁跨整个 spawn/探活，与「不阻塞 RPC」冲突**

`Resolve` 一进门 `r.mu.Lock()`，`spawnManaged` 的 30s boot-wait 也在锁里。现行一次补拉会卡住所有 `clientFor`（mux、host、ListSessions、发消息）最多 30s。

提案只写了宽限不得把 RPC 挂起两分钟，没写：

- 探活（`probeTimeout=2s`）不得在锁内串行打满每个调用方；
- 宽限到期后的 3080 spawn 同样不得持锁等待；
- 宽限内对 3080 的反复 `host.describe` 应有短负缓存，避免 mux+host+RPC 每人付 2s。

**修改建议**：§3 加实现约束：`r.mu` 只护缓存/`lostAt`；网络 I/O 与 spawn wait 在锁外；宽限探活失败缓存 ≤1s；补拉异步或在锁外等端点。§4 竞态 1「无错误暴露到 iOS」只有在 spawn 不等锁时才成立。

**M5. 竞态 1「认 external」与现行 `spawnManaged` 标签矛盾**

现行 boot-wait 端点命中后**恒**打 `SourceManaged` + 自己孩子的 PID（`resolver.go:391-396`）。孩子已因 EADDRINUSE 死掉、应答的是用户实例时，仍会标成 managed、PID 是尸体。`processIsAlive` 已写好但未调用。

「探测保持端点语义」和「标签按所有权」不冲突：探的是端点，贴标签时可以看自己的孩子还活不活。

**修改建议**：§4 红线拆成两句——boot-wait 探测 = 端点；source = 孩子仍占用该端口才是 managed，否则 external。§5 若采用「不杀」，标签只影响诊断，也仍不该显示死 PID。

**M6. 「每次探 3080 / 永远 spawn 3080」与 `WithProbeURLs` 打架**

`opts["dsh_web_url"]` 可以把权威地址改成非 3080。§3 状态机写死 3080，§9 又说保留探测列表。实施者会 spawn 3080、同时用户实例在 4000，分裂复现。

**修改建议**：不变量改成「探测列表的第一个 / 默认 3080 才是位子；spawn 只绑这个位子」。用户显式配了 4000，身份就是 4000。§9 非默认端口写成这个特例，而不是「再分裂一次」。

### 建议

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| S1 | 「本 bridge 生命周期内从未有过实例」在 Link 重启 / 120min 自动重启后恒为真 | `autoRestartIntervalMinutes` 默认 120；新进程无内存 | §3 写明：进程重启 = 冷启动。3080 在则收养；不在则立刻 spawn。与用户同时重启 3080 走竞态 1，不走进宽限 |
| S2 | `run_diagnostics` 会 `Resolve()`，判别器本身能造成翻转 | `diagnostics.go:61` | §0.1 / §10 把判别器分成只读（`lsof` + state 文件 + `/internal/agents/{id}/test`）和会突变（`run_diagnostics` / 任何 `Resolve`） |
| S3 | 「列表也不出现」推到 workspace.json / 坑 5 | 今晚 list 集合相同；坑 5 原义是未分组 | 改成：3080 已在跑时看不见对方内存里的新会话；冷启后 list 应能从磁盘冒出；仍缺则再查归组（未分组 ≠ 不在列表） |
| S4 | 文首「初衷（…永不自托管）不变」与冷启动 spawn 3080 并列 | 08-16 设计已经用 managed spawn 修订过「未启动」 | 改成：零迁移 / 双向接力 / 不代装 npm 不变；「未启动」按 owner 裁决改为「缺位则补到权威端口」 |
| S5 | 翻转无日志 | 只有 `backgroundResolve` 打 source | 实施时 Resolve 每次 source 变迁打 INFO（from→to、原因=cache-dead/adopt/spawn/grace） |
| S6 | 一次性清理按 state PID 杀 | PID 复用 | 杀前核对 cmdline 含 `dsh` 且仍听在记录端口；对不上只删 state、告警 |
| S7 | 验收矩阵缺几行 | 见今晚活体 | 补：流重连翻转回归；Link/120min 重启收养 3080；宽限错误码 + turn 终态；升级杀 3096 孤儿；`dsh_web_url` 非 3080；宽限内 catalog 不清空 |
| S8 | 活文档仍写 3096–3196 | `GO_BRIDGE_ARCHITECTURE.md:237-240` | 实施 PR 同步改活文档，不必阻塞本提案 |
| S9 | §5 诊断「PID + 启动时间」 | `host.describe` 无启动时间 | 用 `ps` / 进程启动时间；写一句即可 |
| S10 | 现行 `Stop()` 已对收养实例不杀 | 3096 活过多次 Link 重启 | 推荐「不杀」是把「本进程 spawn 的 3080」也改成与收养相同；写清差异，避免实施者以为现在会杀孤儿 |

## 5. 未核验 / 明确标假设

| 项 | 状态 |
|---|---|
| 用户手搓 `dsh web` 的真实冷启动分布（是否经常 >30s） | 未测。30s 是我们自己的 spawn 上限，只能类比。2s 流重连使这个数字不再承重 |
| 双实例写 `workspace.json` 是否损坏 | 原设计 S3 已标不确定；本提案正确挂账。今晚未做写实验 |
| iOS 对 `service_not_running` / `backend_unavailable` 的具体 UI | 未跟到 Swift 渲染。故 M3 要求提案自己选定并写后果 |
| 今晚 19:59:48 重连后的 `OpenStream` 目标 URL | 日志没打 baseURL。用时间线 + 现在 `Current()=3096` + 3080 20:00:03 才起来，反推为收养 3096。若要铁证，实施时补 M5 的 source 日志 |

跨策略核对：`host.describe` 走 HTTP POST 信封；`session.list` 两边同形 `result.value.items[]`。两套抽取一致，无脚本分歧。

## 6. 修订优先级

**P0（改完才能开写代码）**

1. M3 宽限 wire 契约（错误码、hello 可见性、turn 终态、禁止 `not_configured`）
2. M4 锁 / 非阻塞探活与 spawn
3. M1 把 2s 流重连写进事故与验收

**P1（同一稿改掉，避免实施分叉）**

4. M5 source 标签规则
5. M6 权威端口 = 探测列表首位，不是写死 3080
6. M2 静默面的准确归因
7. S1 进程重启 = 冷启动
8. S2 / S7 判别器只读性与验收补行

**P2（不挡方向）**

9. S3 / S4 措辞、S5 日志、S6 PID 安全、S8 活文档、S9/S10 关停细节

## 7. 对路线本身

不推翻。粘滞防横跳的辩护成立；真缺陷是「缓存死亡后在别的端口再立山头」。端口即身份删掉的是问题族，不是修补丁。排除同步桥 / 只告警 / 完全不 spawn，与 v1 能力和 owner 裁决一致。

实施时建议最小切口：`resolver.go` 状态机 + 诊断/InstanceStatus 文案 + 生命周期单测（粘滞、宽限不 spawn、到期 spawn 3080、EADDRINUSE 认端点、权威端口可配置）+ 退役 3096–3196。不必先动 iOS，除非 M3 选了新错误码。

## 8. 修复落地前：本机此刻即可按 §10 恢复

当前就是提案写的分裂态：iOS 走 3096，浏览器 3080 是另一个进程。

1. 确认（只读）：`lsof -nP -iTCP:3080,3096 -sTCP:LISTEN`；`POST /internal/agents/dsh-web/test` 若含 `3096`/`managed` 即坐实。不要用会 `Resolve()` 的 `run_diagnostics` 当第一步。
2. 恢复：3080 已经在听的话，杀掉 3096 上的 `dsh`（pid 1406）——进行中的 iOS turn 会随 3096 死；约 2s 流重连后会重新绑上 3080。或者先保证 3080 起来再重启 CordCode Link（新进程第一步探 3080）。
3. 不要先重启 3080 当修复：3096 活着就永不回头。
