# 监工指令 11 号：Question terminal reconciliation 最终收口

> **路线图**：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md` §6.8、§8 场景 6  
> **依据**：audit-010 partial  
> **范围**：MacBridge opencode-web + owning tests；iOS 不变  
> **kind**：hole-fill

## 目标

保留 10 号已验证的 source identity、pending cold reload、strict tail、安装结果。只修 Question lifecycle 在 recovery 与 authoritative resolution 并发时可能留下 stale pending 的最后一个缺口。一次实现、一次集中报告，然后停等 audit-011。

## 1. 先钉死红灯

在改产品代码前加入可控 barrier 的 destructive tests，至少复现：

1. pending 已投影；recovery GET 已取到 pending、尚未 emit；live `question.replied/rejected` 先进入；释放 recovery 后最终必须 resolved，不能 requested 覆盖 terminal。
2. recovery GET 取得旧的空 snapshot；新的 live asked 随后到达；释放 recovery 后新 pending 必须保留，不能被旧 snapshot 清除。
3. pending 已投影；SSE 断开；官方 server 在 gap 内 reply/reject；重连 GET `/question` 为空、A7 history 带 terminal question tool；同一 part 必须原位 answered/rejected，不残留 pending。
4. resolved 后重复/迟到 asked 不得重新 arm/projection。

测试必须是真并发或明确 barrier 控制交错，不接受“函数 A 返回后再调用 B”的顺序测试冒充 race。第一轮修复若不改变上述红灯，立即停并报告，不继续猜状态机。

## 2. Lifecycle 与 source fence

- 删除或取代不能表达 terminal precedence 的 `projectedQuestions map[string]bool` 裸 claim；不得在它外面再加时间窗口、sleep 或第二 referee。
- Question lifecycle 至少按 `(sessionID, interactionID)` 区分 identity、pending、resolved 与 source ordering。server terminal 永远胜过较早的 pending recovery。
- recovery snapshot 与 live stream 必须有可证明的 source cut/version fence，或进入同一个串行归约顺序：
  - recovery 不能在较新的 resolved 之后补发旧 requested；
  - 旧 empty snapshot 不能清除较新的 live asked；
  - 每个 source fact 最多一次 EventPublisher/Kernel ingest，不新增 reducer/writer。
- 成功 decode 的 GET `/question` 是 pending set truth，但“absence”本身不能猜 answered/rejected。对本地已知 pending、server 已无该 ID 的项，必须从同一次权威 A7 history transaction 匹配 `messageID + callID` 并读取 evidence-proven tool terminal：
  - completed + captured answer metadata → answered；
  - A7 captured reject error shape → rejected；
  - 未知/畸形 terminal fail closed并记录诊断，不得凭空选择状态。
- resolved cold hydrate/reopen 也必须保持同一 interaction/turn 原位语义；不得把 question tool只降成普通 activity 而丢失已经验证的 structured state。若现有 hydrate transaction 无法表达该映射，触发停止线并提交证据，不得开 raw second path。

## 3. Owning full-path tests

- 增加真实生产链测试：`resolve_user_input` handler → concrete opencode-web responder → official reply/reject POST fixture → server broadcast/recovery → EventPublisher/Kernel → handler 返回 authoritative `headRev/currentStatus`。answer 与 reject 都要覆盖。
- 覆盖 Web/other-client resolution、断线漏 terminal 后恢复、进程 cold reopen resolved history、GET/live/resolve 三方交错。
- 断言：一个 interaction 始终至多一个 part；terminal 后不回 pending；resolved 原位、turn/call identity不漂移；错误 identity/未知 history shape 零 phantom；单 Kernel ingest owner不回退。
- race tests 用 barrier，至少 `-count=20`；再跑 `go test -race ./agent/opencode-web`、相关 go-bridge、`go test ./...`、vet/build。

## 4. 真实 1.18.18 与安装

- 隔离 serve 至少验证：pending reload、answer 后 cold reopen、reject 后 cold reopen；若无法稳定制造 TCP gap，可以用真实 resolved history + GET empty证明 terminal reload，gap ordering由 barrier owning test证明。不得伪造真实 serve 已做 TCP 掐断。
- Mac 产品代码变化后重新 Release build/install，确认 8777 来自 `/Applications`；保持 4096 owner-managed，回收 4398/4399。
- iOS 无产品变化：不重复 iOS build/test/install，也不进入 UI automation；10 号安装证据继续有效。

## 5. 报告与停止线

- 新报告更正实现 commit：10 号实际实现为 `4a2afb6`，不是不存在的 `1e9a714`。
- 报告列出四个 barrier 交错的 before/after、A7 terminal history映射、actual resolve RPC headRev、真实 sandbox结果、Mac runtime与 clean 状态。
- 不得改 canonical/protocol/WireDescriptor/capabilities，不实现 E2/OD-3，不进入 owner UI 矩阵。
- 若 A7 history 无法无猜测地区分 terminal、需要第二 writer/reducer、或第一轮修复不改变红灯，立即停止等待 owner/监工裁决。
