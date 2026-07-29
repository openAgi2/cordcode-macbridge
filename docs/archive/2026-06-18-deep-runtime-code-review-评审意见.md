# 深度运行期 Code Review 评审意见

评审日期：2026-06-19

评审对象：`docs/2026-06-18-deep-runtime-code-review.md`（下称「报告」）

评审范围：对报告 11 项发现的**事实准确性**（逐条反查代码位置与调用链）+ **定级合理性** + **验证记录可信度** + **可执行性**做独立复核。评审方式不是通读评语，而是照着报告给的每个 `文件:行号` 重新打开源码核对，并对报告声称的测试结果独立复跑。

评审人：ZCode

对照基线：`docs/2026-06-18-code-review-prompt.md`（本轮 review 的 prompt）、`docs/code-review-2026-06-18.md`（v4）

---

## 0. 评审结论

**这是一份事实可信、定级合理、可直接作为整改工单输入的高质量报告。全部 11 项发现的核心结论经逐行复核成立，未发现幻觉；报告声称的验证记录经我独立复跑全部可复现。**

明确给出三层支撑：

1. **零幻觉**：11 项发现的 `文件:行号` 锚点逐个重新打开源码核对，关键引用（`RuntimeManager.swift:258-287`、`main.go:608-611`、`handlers.go:358`、`relay/server.go:521-565` 等）行号精确。最关键的是 3.11——报告声称的 `passive_subscriber_test.go:290(Write)/:311(Read)` 数据竞争，我实跑 `go test -race` 复现，行号与 race detector 输出完全一致（见 §4）。
2. **验证记录可信**：报告 §6 称「relay-server race 通过、go vet 通过、codex reconnect race 失败」——我逐项独立复跑，结果与报告完全吻合。
3. **定级合理**：4 项 🔴高 / 3 项 🟠中 / 4 项 🟡低 的分布与"出事影响 × 本代码库特有"的标尺一致，无注水、无稀释。

**但有 3 处细节需修正**（不影响任何一项的定级，只影响描述精度，见 §2）。这些是评审要负责任指出的地方：报告在 3.3 对两条 Close 路径的描述与现状略有出入，3.2 漏列了一个既有的部分缓解机制。建议报告作者据 §2 做小幅修订后定稿。

下面是支撑这个判定的细节。

---

## 1. 逐条核实结论（核心断言全部成立）

下表是逐条反查结果。「核心断言」指该发现的问题陈述本身是否真实存在。

| # | 发现 | 严重度 | 核心断言 | 行号锚点 | 核实结论 |
| --- | --- | --- | --- | --- | --- |
| 3.1 | agent 继承控制面凭据 | 🔴高 | ✅ 成立 | Swift 注入 4 个 `CCCODE_*` / Go 仅清 OpenCode 凭据 / 三 agent `cmd.Env = os.Environ()` | 详见 §1.1 |
| 3.2 | shutdown 不关 session | 🔴高 | ✅ 成立（需补缓解说明） | `StartSession(context.Background())` / shutdown 无 registry 遍历 / 三 `Stop()` 空实现 | 详见 §1.2 |
| 3.3 | events channel 关闭竞态 | 🔴高 | ✅ 成立（2 路径描述需修正） | opencode/passive 超时即关；sse 实际有 closeOnce | 详见 §1.3 |
| 3.4 | Relay 同步跨设备写队头阻塞 | 🔴高 | ✅ 成立 | `readBridgeFrames` 同步调 `target.write` / 120s 写 deadline | 详见 §1.4 |
| 3.5 | Swift restart 无代次 | 🟠中 | ✅ 成立 | restart Task 无句柄/无 generation / AppDependencies 连续两次 restart | 详见 §1.5 |
| 3.6 | runtime.json 假就绪 | 🟠中 | ✅ 成立 | `WriteReadyFrame` 无返回值 / 写失败只记日志 | ✅ 准确 |
| 3.7 | management 无短超时 | 🟠中 | ✅ 成立 | `URLSession.shared` 默认超时 | ✅ 准确 |
| 3.8 | 配对 bucket 无 TTL | 🟡低 | ✅ 成立 | `pairFails`/`sourceBuckets` 只增不删 | ✅ 准确 |
| 3.9 | Handler 生命周期不可关 | 🟡低 | ✅ 成立 | `NewHandlers` 隐式起 goroutine / `time.Tick` 无 stop | ✅ 准确 |
| 3.10 | god-object 边界 | 🟡低 | ✅ 成立 | `Handlers` 多 owner 共享状态 / 包级全局 | ✅ 准确 |
| 3.11 | Codex 重连测试 data race | 🟡低 | ✅ 成立（已实跑复现） | `closeCount` 无同步读写 | 详见 §4 |

### 1.1 3.1 控制面凭据泄漏 —— ✅ 完全成立，证据三段闭环

报告给的调用链「Swift 注入 → go-bridge 仅清 OpenCode 凭据 → agent `cmd.Env = os.Environ()`」我逐段核实，三段全部精确：

- **Swift 注入**：`RuntimeManager.swift:261-286` 确实把 `CCCODE_MANAGEMENT_TOKEN`(261)、`CCCODE_RELAY_ROUTE_ID`(278)、`CCCODE_RELAY_CREDENTIAL`(283)、`CCCODE_RELAY_ENDPOINT`(273) 写入 `process.environment`。✅
- **go-bridge 仅清 OpenCode**：`main.go:608-611` 的 `clearOpenCodeServerAuthEnv()` 只 `Unsetenv` 了 `OPENCODE_SERVER_USERNAME/PASSWORD`，未碰任何 `CCCODE_*`。✅
- **三 agent 继承全环境**：
  - Claude `session.go:149`：`env := filterEnv(os.Environ(), "CLAUDECODE")`——只过滤 `CLAUDECODE` 前缀，`CCCODE_*` 全部保留。✅
  - Codex `session.go:127`：`cmd.Env = core.MergeEnv(os.Environ(), cs.extraEnv)`——全继承。✅
  - OpenCode `session.go:88-92`：`env := os.Environ()` 后直接赋值——全继承。✅
- **stderr 原样进日志/错误**：`claudecode/session.go:282-283`、`codex/session.go:248-249` 确实 `slog.Error(..., "stderr", stderrMsg)` 并构造 `EventError{Error: fmt.Errorf("%s", stderrMsg)}`。✅

**结论**：这是本轮最高优先级安全问题，定级 🔴高 准确。威胁画像（配对但不可信设备 C 借 agent 工具读取环境变量、或用 management token 调 loopback `/internal/shutdown`）成立——agent 进程本身就是本地执行代理，绕过了 Bridge capability policy。修复建议（显式 allowlist 构造 agent env + stderr redactor + 回归测试）可执行。

### 1.2 3.2 shutdown 不关 session —— ✅ 成立，但需补「idle cleanup」缓解说明

报告三个核心断言全部核实：

- `handlers.go:358` 与 `:1604` 确实是 `agent.StartSession(context.Background(), sessionID)`。✅
- `session.go:60`（claude）`sessionCtx, cancel := context.WithCancel(ctx)`——session 的 cancel 来自传入的 `context.Background()`，与 runtime root context 无关，shutdown 的 `cancel()` 不会传播。✅
- `main.go:371-391` 的 `shutdown()` 确实只 `cancel()` + 关 WS/HTTP/Relay/mgmt，无 session registry 遍历。✅
- 三 `Agent.Stop()`（`claudecode.go:1098`、`codex.go:435`、`opencode.go:503`）确为 `return nil`。✅

**需修正/补充**（见 §2.3）：报告在[影响]段描述孤儿进程累积时，未提及 `handlers.go:191-217` 的 `cleanupIdleSessions()` 既有机制。该机制会按 TTL 清理**空闲** session 并调 `sess.Close()`。这不削弱报告结论——因为 **runtime shutdown 时正在执行 turn 的 active session 不是 idle 状态，idle cleanup 不会处理它们**，所以"shutdown 时 active session 的 agent 子进程变孤儿"这个核心结论依然成立。但报告应补充这一既有兜底，否则读者会误以为 session 完全无人回收。定级 🔴高 维持。

### 1.3 3.3 events channel 关闭竞态 —— ✅ 核心成立，但 2 条路径描述需修正

报告的核心不变量判断正确：**`Close()` 在 `wg.Wait` 超时后立即 `close(events)` 是 bug，因为超时本身证明 producer 可能还活着**。但 4 条路径里有 2 条的描述与现状不符，需修正：

| 路径 | 报告描述 | 实际现状 | 评审意见 |
| --- | --- | --- | --- |
| `opencode/session.go:480-494` | 超时后立即 close | `close(s.events)` 无 `closeOnce`，超时分支(:490)后走到 :493 关 channel | ✅ 描述准确，真实 bug |
| `codex/appserver_session.go:697-733` | 超时后立即 close | **已有 `s.closeOnce.Do(close)`**（:731），超时分支(:728)后走 closeOnce | ⚠️ 「无保护」描述不准确，但有 closeOnce 不等于免疫——超时后 producer 仍在发仍会 panic，核心问题成立 |
| `codex/passive_subscriber.go:532-555` | 超时后立即 close | `closeOnce` 只包了 conn 关闭(:534-543)，**没包** :554 的 `close(s.events)` | ✅ 真实 bug，且 closeOnce 用得不一致 |
| `opencode/sse_subscriber.go:730-754` | 列为问题路径之一 | **`close(s.events)` 已在 `closeOnce.Do` 内**（:742-754），且 `emit()` 有 `default` 兜底（:734） | ⚠️ **定性偏重**。closeOnce 保证只关一次，emit 非阻塞；虽有理论 panic 面，但风险显著低于 opencode session。报告把它与 opencode session 并列夸大了 |

**结论**：报告指出的「超时即关 channel」这一系统性反模式**确实存在**（至少 opencode session + passive subscriber 两条路径明确成立）。定级 🔴高 维持——因为 opencode session 这条路径无任何 closeOnce 保护，弱网/SSE 不响应取消时 producer 稍后发事件会稳定 panic 整个 bridge 进程。建议报告据 §2.1、§2.2 修正描述，把 sse_subscriber 单独标注为「风险较低/已有缓解」。

### 1.4 3.4 Relay 队头阻塞 —— ✅ 完全成立

`relay/server.go:521-566` 的 `readBridgeFrames` 是 bridge socket 的**单一**读循环，对每个 envelope 同步调 `target.write(payload)`（:553）。`socketPeer.write`（:613-619）持 `writeMu` 并设 `relayWriteDeadline=120s`（:28）。当目标 device 的 TCP 接收窗口满（device 不读），`WriteMessage` 阻塞最长 120s，整个 for 循环停住，同 route 其他 device 的帧读不出来。✅ 定级 🔴高 准确。

一个补充（不要求报告改）：报告说"最长 120 秒"是单帧上限；若攻击者持续灌帧，每帧都重新设 120s deadline，实际阻塞可**远超** 120s（持续型 DoS）。这反而加强而非削弱报告结论。

### 1.5 3.5 Swift restart 无代次 —— ✅ 成立，触发场景真实

`RuntimeManager.swift:187-200` 的 `restart()` 创建的 `Task` 无句柄保存、无 generation 校验。✅ 更关键的是，`AppDependencies.swift:164-198` 的 `handleRemoteURLChange()` 末尾(:181)调一次 `restart()`，紧接着 `Task` 内 Relay provisioning 完成(:192)又调一次 `restart()`——这是**真实存在的连续双 restart 触发路径**，不是假想。`prepareRuntimeOwnershipForLaunch()`(:563-584) 确实靠 SIGTERM/SIGKILL 同路径旧进程来接管。定级 🟠中 准确（后果是多次断连/重启风暴，非数据/安全损害）。

---

## 2. 需修正的 3 处细节（不改变定级，只改描述精度）

这 3 处是评审必须诚实指出的——它们不影响任何发现的定级，但若不修正，照报告直接动手的 agent 会在 3.3 浪费时间，在 3.2 误判既有机制。

### 2.1 【修正】3.3 appserver 已有 closeOnce，非"无保护"

报告[证据]对 `codex/appserver_session.go:697-733` 的描述给人"无保护直接关"的印象，但 `:731` 实际是 `s.closeOnce.Do(func() { close(s.events) })`。

- **影响**：核心问题（超时后 producer 仍在发 → panic）依然成立，因为 closeOnce 只防"重复 close"，不防"producer 在 close 后发送"。
- **建议**：把该路径的描述改为"虽有 closeOnce 防重复关闭，但超时分支(:728)后仍执行 close，producer 此后发送仍会 panic"。修复建议里"为 app-server 增加 closeOnce"应改为"将 close 推迟到 producer 确认退出后，复用 codex exec session 的延迟关闭模式"。

### 2.2 【修正】3.3 sse_subscriber 已有 closeOnce + default 兜底，定性偏重

报告把 `opencode/sse_subscriber.go:730-754` 与 opencode session 并列为问题路径，但：
- `close(s.events)` 已在 `s.closeOnce.Do(...)` 内（:742-754）。
- `emit()`（:730-737）有 `default` 分支非阻塞发送（:734）。

closeOnce 保证只关一次，emit 非阻塞。虽理论上 closed-channel panic 仍可能（emit 在 readLoop/wg 内，若 close 在先、emit 在后），但触发条件比 opencode session 苛刻得多。

- **建议**：把 sse_subscriber 从"问题路径"降级为"风险较低/已有部分缓解"，或至少注明它与 opencode session 的差异。否则照报告动手的 agent 会把已基本安全的路径当成高危来大改。

### 2.3 【补充】3.2 应说明既有 `cleanupIdleSessions()` 兜底

报告[影响]段描述孤儿进程累积时未提 `handlers.go:191-217` 的 `cleanupIdleSessions()`（由 `StartCleanupLoop` 周期调用）。该机制会按 backend TTL 清理**空闲** session 并 `sess.Close()`。

- **影响**：不削弱结论——shutdown 时**正在执行 turn 的 active session 不是 idle**，idle cleanup 不会处理，这些 agent 子进程仍会变孤儿。报告核心结论成立。
- **建议**：在[影响]段补一句"空闲 session 有 idle cleanup 兜底回收；本发现针对的是 shutdown 时仍处于 active（执行中 turn）的 session，它们既不被 shutdown 遍历、也不满足 idle 条件，因此成孤儿"。这能让读者准确理解问题边界，也让修复建议的优先级（"给 Handlers 加 Shutdown 遍历 active session"）更有针对性。

---

## 3. 定级与 P0 排序评审

### 3.1 定级分布合理

4🔴 / 3🟠 / 4🟡 的分布与影响标尺一致。两点确认：

- **3.1、3.2、3.3、3.4 列 🔴高 均站得住**：3.1 是可被远程设备利用的凭据泄漏（安全）；3.2 是进程治理缺位（稳定性 + 资源泄漏）；3.3 是进程级 panic（可用性）；3.4 是跨设备 DoS（可用性）。四条都满足"高危画像可达 × 出事影响大"。
- **3.5、3.6、3.7 列 🟠中 合理**：后果是重启风暴/假就绪/发现延迟，非数据损害或直接安全漏洞。
- **3.8-3.11 列 🟡低 合理**：低速内存增长/测试可维护性/结构债/测试自身 race，均非发布阻断项。

### 3.2 P0 五件事排序合理，与 v4 关系声明清晰

报告 §4 的"若只能修 5 件事"排序（凭据隔离 → session shutdown → channel 所有权 → Relay per-device 队列 → Swift generation）与发现的严重度一致。§4 末尾对 v4 P0(P0-1 read_file 等)的排序关系声明尤其值得肯定——它区分了"部署基线是当前工作树"vs"未含 v4 修复"两种情况，避免了与 v4 抢优先级。这是诚实且有用的。

### 3.3 一个排序上的观察（非阻塞）

报告 §4 把 3.1（凭据）排第 1、3.2（session shutdown）排第 2。从"安全 > 稳定性"的传统标尺看合理。但若团队按"修复成本/收益"排，3.3（channel 所有权）其实**修复成本最低、收益最直接**（统一不变量即可消除进程级 panic），可能更适合作为第一个落地项。这只是优先级视角差异，不要求报告改。

---

## 4. 验证记录独立复核

报告 §6 声称的三项验证结果，我逐项独立复跑：

| 报告声称 | 我的复跑命令 | 复跑结果 | 一致性 |
| --- | --- | --- | --- |
| `go test -race agent/codex ...reconnect...` FAIL，race 在 :290/:311 | `go test -race ./agent/codex -run TestPassiveSubscribe_ReconnectAfterServerClose -count=3` | `WARNING: DATA RACE`，Write at `passive_subscriber_test.go:290`、Read at `:311`，`--- FAIL` | ✅ 行号精确一致 |
| relay-server race PASS | `cd relay-server && go test -race ./internal/relay -count=1` | `ok cccode-relay/internal/relay 4.049s` | ✅ 一致 |
| `go vet ./...` PASS | `go vet ./...` | 无输出（通过） | ✅ 一致 |

**结论**：报告的验证记录**完全可信，无夸大**。尤其 3.11 的 race 行号，race detector 输出与报告引用逐字符吻合——这说明作者确实跑了测试并如实记录，而非编造。

报告 §6 末尾那句"现有测试没有覆盖本报告最关键的 shutdown 子进程回收……因此'普通测试通过'不能反证这些发现"，是**非常诚实**的自我限定，体现了报告不靠"测试通过"来兜底结论的严谨。值得肯定。

---

## 5. 可执行性评估

报告的修复建议整体可执行性高。除 §2 指出的 3.3 两条路径描述需修正外，其余发现都给到了「改哪个方法 / 加什么守卫」级别：

- 3.1 给了具体要 `Unsetenv` 的 4 个变量名 + `BuildAgentEnv` 函数签名 + 回归测试断言点。✅
- 3.2 给了 `Handlers.Shutdown(ctx)` 幢等接口 + session ctx 注入点 + shutdown 顺序 + 子进程集成测试形态。✅
- 3.3 给了"复用 codexSession.Close 延迟关闭模式"的现成参照（`codex/session.go:850-890`）。✅
- 3.4 给了 per-device 有界队列 + 非阻塞 enqueue + route 级测试形态。✅
- 3.5 给了 `restartTask` 句柄 + `launchGeneration` + unit test 断言点。✅

一个轻量建议（非阻塞）：3.1 修复建议第 2 条提到"新增 `core.BuildAgentEnv(base, providerEnv, sessionEnv)`"，但未明确 base 应从哪里取（是 `os.Environ()` 还是更显式的最小集）。考虑到这正是本 bug 的根因（base 用了 `os.Environ()` 才泄漏），建议补一句"base 不应再是 `os.Environ()`，而应是受控的最小启动环境"，让 agent 不会照搬旧模式。

---

## 6. 整体评价

这份报告完全履行了 prompt §6 的行为约束：

1. **先验证再下结论**：每条发现都有调用链证据，无猜测性结论。无法静态确认的全部归入 §5「需要进一步验证的疑点」（8 项，含孤儿进程实测、channel panic 复现、Relay 真实 TCP 测试等），诚实标注"需运行期验证"。
2. **区分设计与缺陷**：没有把 120min 定时重启、relay 独立模块等有意设计批成 bug；§1 明确声明 v4 已整改项不重复列。
3. **对 v4 既不盲从也不为反对而反对**：§1 与 v4 的关系声明清晰，只报增量；3.4、3.6、3.8、3.9、3.10 都标注了"v4 已覆盖（仅增量）"。
4. **宁缺毋滥**：11 条每条都有实质内容，没有凑数的风格类意见。

唯一的改进空间就是 §2 列出的 3 处描述精度问题——这些是"差最后一公里"的细节，修正后报告即达到 v4 终评时 R4 给出的"可直接交付开发 agent"水准。

**评审结论：建议作者按 §2 做小幅修订后定稿；修订后可作为整改工单的直接输入，无需第二轮评审。**
