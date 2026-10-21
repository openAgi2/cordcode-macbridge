# Claude 长 session 投影加载分支——复核 R3（RC-1/RC-2 + N 项修复轮，独立 · 只读）

> **结论：代码层 `PASS`，交付层未闭合 → 总评 `PASS-WITH-FINDINGS`。**
>
> - **代码修复全部成立**：RC-1、RC-2、N1、N2、N3 各撤回一处修复后，对应测试都变红；恢复后变绿，均独立复现。原先在测试族中稳定失败的 `TestHandleGetSessionProjectionRunningSessionWithinBudget`，现在族跑 3/3、单独 8/8 全部通过。
> - **R3-1（P0 构建身份门，必须处理）**：当前安装的 runtime 内嵌提交是 **`8490a7970666`**（我那次只改文档的提交）。二进制生成于 16:30:06，修复提交在 16:32:20 / 16:32:51 才产生，说明这个包是用**未提交的工作区**构建的；回执说的「内嵌本轮 HEAD」不成立。缓解证据：已提交的生产源文件哈希与评审 R2 在约 16:10 记录的工作区哈希逐个一致，内容大概率相同。但按 P0 身份门，必须**从已提交的 HEAD 重新构建、安装**，再核对内嵌提交。
> - **其余为文档与观察项**：think.md 中 O2 的登记已过时（O2 已排查完毕，修复方案 v2 已经复审通过）；回执的提交计数又有出入；runtime 在 16:52:18 又重启过一次，上一代日志因既有缺陷被截断（范围外，见 §4）。
>
> **owner 下一步（仅建议）**：① 让开发者从 `6cea855b`（干净工作区）重建 Release，覆盖安装，核对内嵌提交 = 代码提交，并做运行态五项检查；② 之后 RC-1 发布门即可关闭；③ 已通过的 O2 修复方案（v2.1）可以开始实施。

---

## §0 来源清单（实测时点）

| 门点 | 时点（CST） | 结果 |
| --- | --- | --- |
| ① 读源码前 | 2026-10-21 16:47:51 | Mac HEAD `6cea855b`、iOS HEAD `6c56144e`，两仓均干净 |
| ② 写本文件前 | 2026-10-21 16:55:08 | 两仓 HEAD 未变、干净；目标文件不存在 |
| ③ 构建/安装 | — | 不适用（只读复核） |

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=6cea855b35afa341f120072ebf50813b24ce13dd
未提交状态=干净
任务预期分支=回执指定的复核范围 c585c2f2..6cea855b
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / 6c56144e86ea1f54f1c426353062a87e37b6060e（干净）
预期产品特性=运行中会话冷开不再因 coverage 注册竞态失败；Claude session_turn_items 被服务端门拒绝；后缀读取 [from,cursor)；schema bump 被测试钉住
```

**运行态（只读）**

- 16:47 时：PID 17706，PPID 17662，`lstart 16:30:10`，监听 8777。
- 16:53 时：已变为 **PID 24383**，`lstart 16:52:18`，同一个父进程、同一个二进制。
- 二进制：`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`，mtime 16:30:06；`strings` 中的内嵌提交是 `8490a7970666`；含新文案 `claude relay coverage: relay start wait timeout`、`session_turn_items supports codex-remote, not`。
- 没有非 `/Applications` 的实例。

**证据类别**

- 【源码】绑定 `6cea855b`。
- 【scratch】仓库外 `git archive 6cea855b` 副本及 5 个红灯变体（`/tmp/cr-r3/`），只跑定向测试。
- 【生产日志】只读。
- 【推断】单独标注。

---

## §1 复核范围

- **Mac**：`c585c2f2..6cea855b` 共 **7** 个提交。
  - 其中 3 个是我的方案文档提交：`fd4777e6`、`e2e06c91`、`8490a797`；
  - 开发者 4 个：3 个代码提交 `1052128f`、`628bab96`、`741535e0`，1 个文档提交 `6cea855b`。
  - 回执写的「5 提交：4 代码 + 1 文档」计数有误，不影响结论。
- **iOS**：`64a386d6..6c56144e` 共 1 个提交，内容是 `ProjectionBaseCacheStore.swift` 的注释和 CHANGELOG（已核：没有非注释的 Swift 改动）。

---

## §2 逐项复核

| 项 | 修复 | 独立红绿（撤回该处修复 → 测试） | 结论 |
| --- | --- | --- | --- |
| RC-1 | `1052128f`：`markRelayStarting` 在 `go loop` 前同步登记（`handlers_relay.go:141/148`）；loop 在 `register` 之后 `finishStarting`，defer 覆盖全部早退路径；`requestCoverage` 无注册但有启动信号时，在锁外有界等待 `claudeRelayCoverageStartWait=2s` 再重查；无信号或超时仍 fail-closed（`claude_relay_coverage.go:208-237`，等待逻辑在 `:219-232`） | 让 `hasStarting=false` 后，`-run 'TestHandleGetSessionProjection'` 3/3 FAIL；HEAD 3/3 PASS，单独 ×8 全 PASS | **关闭** |
| RC-2 | `628bab96`：服务端 backend 门只放行 `{codex-remote}`（`session_turn_items_handler.go:71`），在 hydrate 和引擎之前返回 `unsupported_capability` | 放开门后 `TestSessionTurnItemsClaudeRejectedByBackendGate` FAIL；HEAD PASS（测试使用 chunks 连接，两种字面量都断言，投影保持 `not_requested`） | **关闭** |
| N1 | `741535e0`：`io.NewSectionReader(file, from, cursor-from)`（`continuation.go`） | 恢复 LimitReader+Seek 后 `TestSuffixReadRespectsCursorWhenFileGrew` FAIL；HEAD PASS | **关闭** |
| N2 | 测试改写字面量 1，并加前置断言 `materializationSchemaVersion >= 2` | 常量改回 1 后 FAIL（此前的 red4 现在能变红了）；HEAD PASS | **关闭** |
| N3 | 断言改为 `h.sessions.lastOutcomeFor(sessionID) != "failed"`（`types.go:618` 是列表徽标的读取源） | 删除命令回显豁免后，两条 MAJOR-1 测试都 FAIL；HEAD PASS | **关闭** |
| N6 | 双仓措辞更正：承认协议有 `removedTurnIds`；改用 bump 的理由改为「对账能否自愈未经验证」 | 文档核对 | **关闭** |

**广泛定向测试（真实工作树，HEAD）**

- `go test ./go-bridge -run 'Projection|Hydrate|Anchor|TurnDetail|SessionTurnItems|Materialization|Coverage|Claude|Checkpoint|Fence|Snapshot'` 跑了 2 轮，都只有 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` 失败。
- `go test ./agent/claudecode` 全包通过；`go vet` 通过。

**RC-1 对抗核对**

- 锁序：等待在 `r.mu` 之外进行，醒来后重新加锁、重查注册表，再沿原逻辑在持锁状态下继续，正确。
- 无信号的情况保持原有 fail-closed 语义；等待不会合成任何事件，也不会授权 commit。
- 2s 等待落在 coverage 的 `ctx`（6s）之内。
- d78100ee 对悬挂 tool_use 的修复语义未被弱化。

---

## §3 发现

### R3-1（P0 构建/安装身份门，未闭合）：安装产物是在提交之前用未提交工作区构建的

- **证据**：
  - 二进制 mtime 16:30:06；进程 lstart 16:30:10（后来 16:52:18 又重启，二进制不变）；
  - 内嵌提交 `8490a7970666`；
  - `1052128f` / `628bab96` 的提交时间是 16:32:20，`741535e0` / `6cea855b` 是 16:32:51；
  - 回执写的「内嵌本轮 HEAD」不成立；
  - 完成报告 §8 和 exec-plan 都没有记录本轮的内嵌提交，只写了「以部署核验为准」。
- **缓解证据（【推断】，未证明）**：
  - 评审 R2 在约 16:10 记录了工作区文件的 SHA-256。与 `6cea855b` 中的提交版本对比，**生产源文件全部一致**：`continuation.go`、`claude_relay_coverage.go`、`handlers_relay.go`、`session_turn_items_handler.go`。只有一个测试文件不同，它不进二进制。
  - 二进制里也有新增的文案。因此部署内容大概率等于已提交代码。
  - 但产物的身份标签是 `8490a797`，并且不对应任何提交，P0 身份门判为未闭合。
- **处理建议**：
  1. 在干净的 `6cea855b` 上重建 Release，覆盖安装；
  2. 核对内嵌提交是否等于代码提交（`741535e0` 或 `6cea855b`）；
  3. 做运行态五项检查：进程代际、8777 监听者、特征输出、无临时实例、新代际日志；
  4. 在完成报告 §8 中写明本轮的部署身份。
- RC-1 作为发布门：**代码层已关闭**，部署层等重建安装之后再关闭。

### R3-2（文档，minor）：think.md 中 O2 的登记与事实不符

- `6cea855b` 把 O2 登记为「是否排查待 owner 裁决、pending」，计数用的是 10-21 单日志 107 次。
- 实际情况：O2 已按 owner 的要求在本会话排查完毕，根因是 RC-A 与 RC-B。修复方案 `docs/2026-10-21-claude-live-relay-zero-event-and-api-error-poison-fix-plan.md` v2 已获复审 R2 **APPROVED**（`/Users/jacklee/Projects/cordcode-ios-native-message-timeline` 中的 `496ea340`），v2.1 是 `8490a797`。这些提交都在本分支上。
- 建议把该行改为「方案 v2.1 已通过，待实施」，并附方案路径。

### R3-3（文档，minor）：回执把 shape fixture 测试的失败称为「既有问题（你的 O1）」

- O1 的工具链推测已在 `fd4777e6` 中勘误：这个失败是 RC-B（135ba20a 引入的 `Error string` 解码回归），属于已通过方案的修复范围，**不是可以忽略的环境问题**。实施该方案后这条测试应转绿，T5 已把它列为闭合测试。

### R3-4（nit）：启动信号没有按代际区分

- `finishStarting(sessionID)` 只按 sessionID 关闭信号。如果一个较老的 loop 退出时（defer）正好晚于新 loop 的 `markRelayStarting`，就会提前唤醒等待者；等待者重查后发现新 loop 还未注册，按 fail-closed 处理，也就是回到修复前的行为。只会在新旧 loop 生命周期重叠时出现，属罕见情况，不会造成假成功。
- 建议：`markRelayStarting` 返回 token，`finishStarting` 只关闭自己创建的那个信号。不阻塞。

### R3-5（nit）：提交计数

见 §1。

---

## §4 范围外观察（不计入结论）

- **O-A：每次 runtime 重启都会截断 `go-bridge.log`**
  - `MacBridge/MacBridge/Services/RuntimeProcessController.swift:216` 在打开日志前执行 `manager.createFile(atPath: path, contents: nil, …)`。文件已存在时这个调用会覆盖原内容；只有文件 ≥8MiB 时才先轮转到 `.1`（`:222-233`）。
  - 因此每次重启都会清空上一代际的日志。今天 22:14、02:38、11:36、16:30 各代的日志都因此丢失，16:52 那次重启的原因也无从查起。
  - main 上同样存在，始于 `d39c391d`（2026-08-10）。
  - 这条直接削弱了 CLAUDE.md 要求的「部署后运行态验证」证据链。建议单独修复：文件不存在时才创建，存在时按追加打开。
- **O-B**：16:52 起的新代际中，暂无 Claude hydrate，也没有 coverage 失败或 source-batch 拒收（统计于 16:53，样本为 0，不作为证据）。

---

## §5 总结论

- **`PASS-WITH-FINDINGS`**：RC-1、RC-2、N1、N2、N3、N6 的代码与文档修复全部成立，均有独立红绿复现。
- **发布前必须处理 R3-1**：从已提交的 HEAD 重建、安装，并核对身份。处理完后 RC-1 发布门即可关闭。
- R3-2、R3-3 是文档更正，R3-4 为可选改进。
- 本复核没有修改业务代码，没有构建、安装、部署或 push，仓库内只新增本文件。
