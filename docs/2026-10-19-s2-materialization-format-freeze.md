# S-2 物化存储格式冻结与测试计划（bbolt）

日期：2026-10-19
方案：v30 §3.2（S-2）；exec-plan `plan-b70583acde4f.json` → `s2-storage-freeze`
基线：Mac `feat/claude-long-session-projection-loading` @ cbd0a109（本切片纯 Mac；iOS 冻结——owner 2026-10-19 调度约束）

## 0. 定案范围（Phase A）

按方案 §3.2.2 的 Phase 划分与 §3.2.4：

- **Phase A（本切片）**：`turns/*` 行级 turn_blob_json + head/segments + **重启恢复与增量重建**
  （替换 34MB JSON checkpoint 的读写路径）；window/anchor 查询继续从 kernel in-memory
  snapshot 切片（Phase B 才把查询下推到 store）。
- **Phase A 显式不做（登记）**：① live patch flush 的行级增量更新（§3.2.4 第 2 条的完整
  形态）——正确性不依赖它（重启时 suffix 折叠覆盖 live 窗口内容，reducer upsert 幂等），
  且避免「行内容超前于 committed cursor」的一致性问题，归 Phase B；② `items/*` 拆行
  （bucket 布局预留，不写入）；③ window 页查询下推 store（Phase B）。
- **OD-9 = 默认档 (a)**（growth-trust 不实现、不加开关）：重启/推进校验一律走
  source-ledger prefix digest（(ii) 分类），无跳过校验路径。

## 1. 物理布局（frozen）

```text
文件：<dataDir>/session-projection/materialization.bbolt
      （旧 checkpoints/*.json 保留不删除、不迁移——OD-5；回退 = 关闭本 store）

bucket 树（单文件嵌套）：
<backend_id>/                      顶层 bucket = backend id
  <session_id>/                    每会话一个嵌套 bucket
    head        -> 行（见 §2）
    segments/                     bucket；key = 8B 大端 ordinal（uint64）
                                      -> SegmentRow（见 §3）
    turns/                        bucket；key = 8B 大端 ordinal ‖ turnId UTF-8
                                      -> turn_blob_json = json.Marshal(TurnProjection)
    items/                        bucket（Phase B 预留；Phase A 创建但恒空）
                                      计划 key = turnId ‖ 0x00 ‖ 8B ordinal ‖ itemId
```

- turns key 的 8B 大端 ordinal = 该 turn 在 `SessionProjection.Turns` 中的下标
  （投影序即内核 committed 序）；bbolt key 有序 → 正向 Seek 游标即投影顺序，
  反向 `Prev` 即尾部分页（Phase B window/older 页直接复用）。
- **单写者**：每次 Save 一个 bbolt `Update` 事务——head、segments、turns 差分、items
  同事务原子提交（与 kernel 单 writer 对齐）；bbolt 自身 B+tree 写锁保证并发
  Update 串行化。
- 文件权限 0700 目录 / 0600 文件（对齐 management-token/tls-cert 纪律）。

## 2. head 行（frozen）

```json
{
  "schemaVersion": 1,
  "backendId": "claude",
  "sessionId": "…",
  "syncRev": 531,
  "bridgeEpoch": "…（provenance only——不作行有效性判据；重启有效性按
                  schema/backend/session/membership/cursor/prefix 判定）",
  "updatedAt": "RFC3339Nano",
  "scalarStateJson": "…（execution/planMode/sessionMode/collaborationMode/
                        codexGoal/goal 的完整标量 JSON）",
  "sources": [SegmentRow…],          // 单源 = 1 项；composite 按段序
  "claudeSourceState": {…}|null,     // 源账本状态（EncodeClaudeSourceState）
  "codexProducerState": {…}|null     // codex-remote 懒历史 seed（原侧文件并入 head）
}
```

- `ProjectionRev ≡ syncRev`（与旧 checkpoint 的 `ProjectionRev == Projection.SyncRev`
  校验同构）。
- `HydrateState` 恒为 Ready（只保存 committed 状态；恢复侧 LoadValidated 只接受 Ready
  语义——非 Ready 从不入库）。

## 3. SegmentRow（frozen）

```json
{
  "identity": "…",           // segment 稳定身份（Claude continuation 段 id）
  "path": "…",               // 物理文件绝对路径
  "cursor": 12345,           // 已消费字节边界（本 store 的 committed cut）
  "prefixSha256": "…",       // [0, cursor) 的 sha256（保存时计算）
  "observedSize": 12345,     // 保存时文件 size
  "observedMTimeN": 1699…    // 保存时 mtime UnixNano
}
```

## 4. 来源四态分类（LoadValidated 判定，frozen；§3.2.3 (i)-(iv)）

对每段按序执行：

1. **membership**：identity/path 集与序必须与当前 `source.Segments` 完全一致——
   任何变化（含 Claude continuation 链增删段）→ 诚实失效重建（(iv)）。
2. **truncation/retreat**：`stat.size < stored.cursor` → 失效重建（(iv)）。
3. **(iii) 全等快速匹配**：`stat.size == stored.cursor && stat.mtime == observedMTimeN`
   → 零读通过（覆盖「停止后无 append」）。
4. **(ii) 默认（推进或 mtime 变化）**：digest `[0, stored.cursor)` 与 `prefixSha256`
   比对——**接受 cursor ≤ 当前 size 的推进**（IO = committed 前缀顺序读，如实计入；
   OD-9 档 (a)：无跳过校验路径）；不匹配（原地改写）→ 失效重建。
5. 单源（无 Segments）路径同构（source.Path 一段）。

恢复返回的 checkpoint 携带 **stored cursors**（非当前 cursor）——kernel
BeginHydrateTransaction 以其为折叠基线：

- 单源：`startCursor = stored.cursor` → 既有 `produceProjectionHydrateRange
  [startCursor, startCut)` 字节区间遍历（现成 suffix 机制）。
- composite（Claude）：**新增 per-segment 续读**——`CompositeRichHistoryOffsetProvider`
  可选接口（core），`GetRichSessionHistoryAtSegmentsFrom(ctx, sessionID, segments,
  fromOffsets)`；agent/claudecode 以 `io.SectionReader(file, from_i, cursor_i - from_i)`
  逐段续读、沿用 dedup-replace 合并。bridge 侧 produce 在 admission 携带 per-segment
  committed cursors 时走续读；provider 不支持 → 回退全前缀读（行为不劣于现状）。

## 5. 与 kernel/handler 的接线（frozen）

- **store 装配**：`SetDataDir` 处按 `materializationProductionEnabled`（发布门 const，
  rollback=false → 逐字节回到旧 JSON checkpoint 路径；旧文件仍在磁盘，直接弃用
  bbolt 文件即可）选择 `ProjectionMaterializationStore`（bbolt）或
  `ProjectionCheckpointStore`（JSON）。两者实现同一 `projectionCheckpointPersistence`
  接口。
- **Save**：仅 hydrate/catch-up commit 后 StageCheckpoint（既有去抖策略不变）→
  `Update` 事务差分写：head/segments 全量覆盖 + turns 按行 diff（新增/变更 upsert、
  消失 delete）。不再序列化整棵 34MB JSON。
- **CodexProducerState**：kernel 的 Load/Save 由具体类型断言改为接口
  （`projectionProducerStateStore`）；bbolt store 并入 head 行（原侧文件逻辑保留在
  JSON store 内不变）。
- **F-14 冷检等价门（window/anchor handler）**：`forceColdInspection` 计算处增加
  「kernel Ready 且 source 未变则跳过」——`projectionSourceUnchangedSinceCommit`：
  committedSource 每段 `stat(path).size == committed.cursor`（stat-only；append-only
  语义下 size==cursor 即未推进，size<cursor 触发冷检由 (iv) 重建兜底）。
- **live patch flush 行级更新**：Phase A 不做（§0 登记）；重启正确性由 suffix 折叠
  + reducer upsert 幂等保证。

## 6. 不变量测试清单（s2-materialization-tests 冻结范围）

1. append suffix only：seed @cursor N → append → LoadValidated 接受（stored cursors）→
   折叠仅 suffix（单源 range + composite offset 续读两路）。
2. restart：(iii) 全等零读命中；(ii) 推进后 digest 校验路径（读量如实 = committed 前缀）。
3. composite membership 变化（增删段/换路径）→ 失效重建。
4. truncation / cursor 回退 → 失效重建。
5. prefix 原地改写（等长 + size 增长两种）→ digest 捕获 → 失效重建（fail-closed）。
6. schema version 不符 / backend/session 身份不符 → 失效重建。
7. 差分写正确性：连续 Save（turn 变更/删除/追加）→ 行集与最终投影一致；重载投影 ==
   保存投影（含 scalar/execution/ClaudeSourceState round-trip）。
8. 单写者/并发：Save 与 Save 并发（kernel 异步 writer 语义）→ bbolt 串行、无交错。
9. window/anchor 冷检门：全等 → 跳过冷检（Ready 直接服务）；size 推进 → 冷检照旧。
10. 崩溃恢复：Save 中途构造失败 → 事务回滚，store 保持上一致点（bbolt 事务原子性）。
11. OD-9 负对照：任何推进路径不出现「跳过 prefix 校验」的读量（digest 必经）。
12. 旧 checkpoint 共存：bbolt 启用后旧 .json 文件不被读/写/删（OD-5）。

## 7. 风险登记（对照方案 §8）

- bbolt 大 value 写放大（6.35MB turn blob）：Phase A 接受（行级差分已消整树重写）；
  Phase B items 拆行缓解；必要时按容量阈值后台重写紧凑化。
- 新依赖 `go.etcd.io/bbolt`（纯 Go、无 CGO、MIT；CI govulncheck 已覆盖根模块）。
