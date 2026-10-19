# S-4 暖开修复验收矩阵（2026-10-19，owner 单轮验收）

配套：iOS `f74b96fe`/`00fe7d1f`，Mac `61de6909`（本仓），双仓分支
`feat/claude-long-session-projection-loading`。Codex 独立分析：
`docs/2026-10-19-ios-long-session-reopen-cache-root-cause-analysis.md`。

## 性能口径（措辞已纠正）

- 「iPhone 磁盘恢复几十毫秒」**未证明**——组件基准是 Mac（M4）：22.9MB 编码 ~76ms、
  解码 ~140-175ms、原子写 ~45ms；≤4MiB 有界文件解码 ~25ms。iPhone 侧以本轮真机
  验收实测为准（fg-trace 时间戳：`open S4 hit … loadMs=` 与 `[ProjOpen] pull AFTER`）。
- 「anchor 响应必然 KB 级」**不成立**——anchorRefresh 携带完整末轮终态（never-split），
  末轮大时响应可以很大；服务端还需对整个投影计算 token/尺寸。以本轮实测
  `response_bytes` 为准。
- 之前「第二次打开等待全部是网络」更正为：**主要等待发生在全量传输阶段**
  （Mac 入队 → 手机收到完整大帧 ~27s 实测；接收后处理为亚秒级，但秒级日志无法
  精确拆分各阶段）。

## 验收矩阵（单轮完成，日志由 agent 事后拉取，owner 无需采日志）

前置：App 已安装并连接（21:0x hello_ack 确认）；iPhone 与 Mac 同 WiFi。

| # | owner 操作 | 预期（可见） | agent 事后取证（fg-trace + Mac log） |
|---|---|---|---|
| 1 | 打开长会话 7f389d08（若缓存已被 schema v2 失效，首轮为全量播种） | 首次全量加载（时长随链路；提示可能先报超时后内容到达） | `[ProjOpen] open S4 miss reason=cache-miss`；`FULL ok`；播种 `[BaseCache]` 无 skipped；Mac `response_enqueue` |
| 2 | 切到任一短会话 | 即时（短会话全量小） | `switchSession` 清内存态（设计内） |
| 3 | **切回 7f389d08** | **内容迅速出现（本地基底恢复）+ 顶部「正在同步…」胶囊；对账完成后胶囊消失；无十几秒加载层** | `[ProjOpen] open S4 hit rows=N loadMs=X`；**Mac 首次出现 `get_session_projection_anchor_newer`**；`reconciled head=`；**无新的 `get_session_projection` 全量请求** |
| 4 | 停留观察顶部 | 若本地窗口被裁剪：显示「更早的 N 轮未加载」 | `truncatedOlderTurnCount`（22.8MB 会话预期 N≈40） |
| 5 | 再切走→切回一次 | 与 #3 相同（重复验证稳定性） | 第二次 hit；anchor 请求第二次出现 |
| 6 | （可选）Mac 端 Claude 同会话发一条新消息 | iOS 前台收到增量 patch，正常追加 | `projection_patch` 事件 → patch 应用（非重开） |

## 交付记录字段（验收后填写实测值）

| 字段 | 来源 |
|---|---|
| 缓存首屏时间 | `open S4 hit` 行时刻 − 切回操作时刻（或 `pull BEFORE` 行） |
| anchor 对账完成时间 | `reconciled`/`pull AFTER` − `open S4 hit` |
| 实际响应大小 | Mac `projection_rpc` trace + fg-trace `[WS-RAW] size=`（anchor 页） |
| 是否出现全量请求 | Mac log 中 #3/#5 窗口有无 `method=get_session_projection` |

## 异常分支（已有真实错误路径 + 定向测试）

| 分支 | 行为 | 测试 |
|---|---|---|
| anchor typed stale（锚点离保留区） | FallbackFull（合法回退，计一次全量） | testOpenTypedStaleFallsBackToFull |
| oversized 家族（reconciliation.oversized / full.oversized） | 显式终态失败 UI，不假成功不重试 | testTransientReconcileFailureClassification |
| 中途重连（epoch 变化） | 丢弃本轮从当前 epoch 重开（≤1 次），再变 FallbackFull | 控制流锁定（真机时序属待验证影响） |
| 客户端被替换（代际变化） | `.superseded` 整组作废 | 代码路径 + 日志分光 |
| 巨型尾轮（单行 >4MiB） | 该会话播种不可用（诚实跳过 + fgTrace）；已知限制 | testGiantTailTurnSeedSkipped |
| 复查集 >256 | 按投影序保最新 256 项再请求 | testCappedDigestsKeepsNewestByProjectionOrder |

## 已知限制（本轮不解决，不冒充完成）

1. 内存 LRU（同进程快速切换直读内存）——owner 裁决留待后续，先验证磁盘恢复路径。
2. 巨型尾轮会话暖路径不可达（预算 4MiB frozen；放宽需方案变更过评审）。
3. 跨重连混代时序、纯 correction 续页多页分布等边界未做真机时序实验（源码控制流已锁定）。


## 验收结果（2026-10-19 20:42-20:49 owner 单轮，App f74b96fe）

owner 结论：基本符合预期 ✅（长会话重开即时显示；Mac 新消息后切回即时显示旧+新消息）。

### 四字段交付记录（实测）

| 字段 | 实测值 | 证据 |
|---|---|---|
| 缓存首屏时间 | **磁盘 loadMs=23-33ms（iPhone 实测，6 次 hit）**；内容与 hit 同秒上屏 | fg-trace4 `open S4 hit rows=8/10 loadMs=23-33` |
| anchor 对账完成时间 | Mac 侧 receive→enqueue **0-179ms**（6 次样本；多数 <120ms） | go-bridge.log req_82/95/125/149/191/209 |
| 实际响应大小 | anchor 页当轮实测 **KB 级**（6-21KB 帧域；本会话末轮小）——注意这是本样本值，anchorRefresh 携带完整末轮时可以更大 | fg-trace4 WS-RAW 12:46:43-44 |
| 是否出现全量请求 | **干净重开窗口（20:42-20:45，5 次切回）7f389d08 零全量**（全部 anchor）；窗口内仅有的全量属其它会话冷开（7afc3bd7 / 433c20a8） | Mac log `mac_receive` 分 session 计数 |

### 新发现缺口（不阻塞本轮验收，登记下轮）

**恢复期并发全量竞态**（20:46:46 窗口，Mac 新消息后首次切回）：anchor req_16
mac_receive 后 Mac 侧 response_enqueue 未落（handler 在新消息 hydrate 后的路径待查），
533ms 后客户端并发发出全量 req_17（疑为恢复窗口内 projection_patch 触发的
scheduleRecoveryPull 未被恢复门拦截——MISMATCH 分支 vs holdPatchDuringRecoveryIfNeeded
覆盖面）；该全量 43ms 入队快速补齐内容（UX 正确），但 wire 层出现 anchor+full 并发、
open 侧另有一路 pullFull 15s 硬帽 timedOut（late apply 收口）。修复方向：恢复门扩大到
MISMATCH 触发的 recovery pull（restore→对账窗口内禁并发全量）+ Mac anchor handler 对
刚 hydrate 会话的响应缺口取证。
