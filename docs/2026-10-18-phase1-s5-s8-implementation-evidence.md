# 第一阶段（S-5 + S-8）实施与证据记录

日期：2026-10-18
方案：v30 §3.5/§3.6/§6/§9 第一阶段；exec-plan `plan-b70583acde4f.json`
来源：Mac `feat/claude-long-session-projection-loading`（基线 6a76d92e + 本文件所列改动）；iOS `feat/claude-long-session-projection-loading`（基线 b77faf3c + 对应改动）

## S-5 单次解析管线（iOS）

### 改动
- 新增 `Services/Bridge/BridgeInboundRoutingScanner.swift`：路由头字节扫描（零对象图、转义感知、`data` 值字节跳过、`data.headRev`/`data.projection.syncRev` 定点提取）+ `BridgeRPCResultEnvelope<Payload>` 单次 typed 信封。
- `ProjectionWireDecoder` 5 方法 + `CCCodeBridgeClient.request<T>`：改单次 typed 解码（信封失败/data 缺失回退「整帧即 Payload」旧形状）。
- `CCCodeBridgeTransport`：hello_ack 快路径（仅路由头命中才 typed decode）；result/error 快路径（零对象图路由直达 continuation）；`inboundEnvelopeJSON`（trace re-encode）删除，headRev 由扫描器或既有对象图直读；`logProjectionRPCCompletion` 签名 `json:[String:Any]?` → `headRev:Int?`。
- E-5 分段计时（`PerformanceTracer`，DEBUG signpost / Release 零开销）：`projection.decode.*`、`replica.applyFull`、`replica.applyWindowPage`、`presentation.apply`、`store.commit`。

### 修复过程记录
- 扫描器 `readString` 首版用默认 options 的 `JSONSerialization` 解码带引号切片——顶层 fragment 默认被拒（返回 nil），测试 9 例失败暴露；修复为 `.fragmentsAllowed` 后 16/16 全绿。

### 测试（§7.2.6/7.2.7 + F-35）
`SinglePassDecodePipelineTests` 16/16 PASS（iPhone 17 Pro Max 模拟器，`test-without-building`）：
- 扫描器 8 例（路由字段/headRev 双路径/id 回退/event 帧/error 对象/转义/2MB 嵌套大值跳过/null+标量 data/5 种畸形帧 nil 回退）；
- 解码等价 5 例（full 含 running execution / delta / window / preview / 无信封回退——新旧管线输出深度相等）；
- F-35 护栏 2 例（running / requires_action 经 decode 不丢 execution）；
- trace 口径 1 例（扫描器 headRev ≡ 旧 envelope re-encode 口径，4 形状）。

### E-5 组件级取证（macOS 宿主 -O，33.6MiB 合成 result 帧 / 4352 turns）
| 管线 | avg（3 轮） |
| --- | --- |
| 旧（Any 全量解析→取 data→再编码→typed decode） | **0.137s** |
| 新（单次 typed envelope decode） | **0.032s** |
| 加速 | **4.34×**（输出等价：turns 计数一致） |

注：真机绝对值更高、比例同构；生产中还省去 hello 尝试解析、envelope Any 对象图、trace re-encode 等额外轮次（本基准未计入）。真机 receive/gunzip/decode/apply/presentation/commit 全分布归 S-6 owner 矩阵。

## S-8 direct_gzip_v1（Mac + iOS）

### 冻结批次
- `docs/protocol/bridge-v1.md` 新增 §Direct Frame Compression（FROZEN）：opcode 自描述（text=原始 JSON / binary=gzip(JSON envelope) 整帧，仅 Mac→client 且已协商）、协商（hello 声明 + echo，DIRECT-only）、阈值（≥32KiB 且更小，sender policy）、接收方要求（opcode 分流；解压失败=真实 transport 失败）、冻结注记（「未协商连接零 binary 帧」限 Mac→client；client→Mac binary 为既有现状）。
- 冻结样本 `samples/direct-gzip-v1/`：`compressed-frame.json.gz`（188B，mtime=0 字节稳定，sha256 `09e4ce2b…`）+ `compressed-frame.decoded.json`（278B，sha256 `63371826…`）+ README（哈希、provenance=synthetic-spec-fixture、双仓副本纪律）。iOS mirror 四文件 + `bridge-v1.md` 字节一致核对 ✓。
- `schemaRevision` 2026-09-05 → 2026-10-18（`bridge_v1_schema.go` 追加日期条目）。
- **随 bump 更新冻结观测物**：`samples/read-file-v2/hello-ack-read-file-v2.json` 经仓库自带 `CCCODEGEN_FIXTURES=1` 再生成机制更新（schemaRevision + producer 当前形状 `backends:[]`），roundtrip 测试恢复绿；iOS mirror 同步。README `2026-10-06-r1` 既有分歧不在本批边界（维持登记不修）。

### 实现
- Mac `server.go`：`Conn.SendJSONReport` 改 `json.NewEncoder` 缓冲 + `WriteMessage` 分流（text 路径与旧 `WriteJSON` 逐字节一致含尾随换行；binary=`gzipPayload` 整帧，复用 relay 侧 level-1 实现；压缩失败如实上抛不静默降级）；`negotiateDirectGzipV1` 仅挂 direct hello 路径（声明 + ack.Ok + `directGzipProductionEnabled` const → echo + `SetDirectGzipEnabled`）；`directGzipThreshold = 32KiB`。
- iOS：`BridgeWebSocketConnection.receiveData()` 签名返回 `BridgeInboundWebSocketMessage(data, isBinary)`（URLSession `.data`→binary / Network 首片 opcode / Relay 解密明文恒 text）；transport receiveLoop binary→`RelayGzip.decompress`（复用 bounded gunzip，50MiB 上限）；hello 声明 `direct_gzip_v1` 仅 `!isRelay`。

### 测试（§7.2.18 + Mac spec）
- Mac `direct_gzip_spec_test.go` 4/4 PASS：fixture roundtrip+双哈希、协商 echo 语义（未声明不 echo/声明+ok echo/失败 ack 不 echo）、阈值边界（恰下 32KiB→text；恰上→binary+gunzip 逐字节相等+更小断言）、未协商连接零 binary 帧。
- iOS `DirectGzipSpecTests` 4/4 PASS：binary→gunzip 与 decoded.json 逐字节+深度等价、README 哈希（两仓副本防漂移）、hello 声明 direct-only 纪律（relay 不声明）、非 gzip 字节 typed 错误（不重解释为明文）。
- go-bridge 全包：仅剩 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest`（system-subtypes.jsonl scanner/manifest mismatch）——**预存失败**（native-timeline worktree f868af18 基线同样失败，与本任务改动正交，登记待另行处置）。

### 部署运行态核验（2026-10-18 21:44-21:45）
| 项 | 结果 |
| --- | --- |
| Release 构建 | `scripts/build-unsigned-release.sh` BUILD SUCCEEDED；runtime 身份 `0.1.0 (commit: 6a76d92e06b0, built: 2026-10-18T13:40:42Z)`；dist zip 生成 |
| 覆盖安装 | killall CordCodeLink + pkill -f cordcode-bridge-runtime → rm -rf /Applications → cp -R → open（21:44:49） |
| 进程代际 | runtime PID **17004**（PPID 16964），lstart **21:44:54** > 构建 21:41 ✓ |
| 8777 监听者 | PID 17004 = /Applications 内嵌 runtime ✓；无违规残留 ✓ |
| runtime.json | 新 epoch `ce3020aa…`、新 management 端口 59373、pid 17004 ✓ |
| binary 身份 | strings 含 `6a76d92e06b0`（= 任务 HEAD）✓ |
| 日志 | 新 runtime 正常服务（21:45 起 list_models/list_sessions 请求处理，transport_route=direct 客户端已重连） |

已知边界：当前 iPhone 安装的 iOS app 未声明 `direct_gzip_v1`（E-3 根因 = 旧安装），该连接不压缩（符合回退契约：wire 逐字节不变）；capability 的活体 echo 与压缩帧首次出现待新 iOS 包安装后（S-4/S-6 交付时）验证。压缩比组件级估算沿用方案 §1.1（level-1 ≈ 11.2MB / 34MB）。

## 队列状态
s0 / s5-impl / s5-tests / s5-regression / s8-refreeze / s8-impl / s8-tests / s8-regression = **done（证据在案）**；下一就绪 = `s7-refreeze-batch`。
