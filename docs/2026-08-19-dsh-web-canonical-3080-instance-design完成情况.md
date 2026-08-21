# 本轮任务完成情况:dsh-web 单实例 3080 收敛方案(实例分裂根因 + 端口即身份重设计)

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-dsh3080`(分支 `dsh/canonical-3080`,自 `opencode/web` `e60c141` 起)
- Plan: `docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`(v4 定稿,三轮评审 APPROVE)
- Canonical State File: `.exec-plan/state/plan-ae1b5ef7c726.json`
- Legacy State File: none(本队列为新建,origin=new)
- Completion Report Verdict: **proved-complete**(队列内 16/16 required todos 全 done 且 proof present;真机验收行显式移交 owner 矩阵,非队列范围,见 §5)
- Queue Summary: 16/16 todos done,16/16 proven,其中 7 项 tests/全量回归证据 `re-verified`(收口前 `go test ./... -count=1` 全量重跑),其余 `self-attested`
- Related Commits: `8d04573`(文档入档)→ `7dc3bfd`(s1-impl)→ `f571e6d`(s1-tests)→ `cea924c`(s2-impl)→ `b66ab64`(s2-tests)→ `dbbe7de`(s3-impl)→ `b8cdf34`(s3-tests)→ `ad167de`(s4-impl)→ `c1a0b2f`(s4-tests)→ `aa61e37`(s5-impl+tests)
- Generated At: 2026-08-20T00:40+08:00

## 1. Overall Verdict (总体结论)

设计 §12 五件切口 + §12.1 四条工程注记全部落地:resolver 重写为权威端口状态机(宽限不收养不补拉、冷启动座位 spawn、锁纪律、single-flight、M5 标签、变迁日志、3096–3196 退役),handler 层 `backend_unavailable` 显式映射(send×3+list×4),失联边沿对 running 会话推幂等 turn 终态,Stop 留任 + 孤儿 PID 安全迁移清理,诊断与活文档对齐座位模型。全部自动化验证通过:根模块 `go test ./... -count=1` 零失败、`go build`/`go vet` 通过、越界路径(relay-server/MacBridge/iOS)零改动。

诚实边界:**真机验收行(设计的 §8.1 真机形态、§8.3 浏览器实时、§8.9/8.10 端口占用提示与留任)需要 owner 真机矩阵执行,本队列不覆盖也不冒充**;单测层的对应形态(§8.1/§8.5 必测下限)已按三轮评审要求覆盖。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| phase1 resolver 状态机 | proven-done | proven-done | proven-done | **done** | `7dc3bfd`/`f571e6d`;包测试 + 全量(re-verified) |
| phase2 宽限 wire | proven-done | proven-done | proven-done | **done** | `cea924c`/`b66ab64`;协议 pack 零改动(re-verified) |
| phase3 turn 终态 | proven-done | proven-done | proven-done | **done** | `dbbe7de`/`b8cdf34`(re-verified) |
| phase4 Stop 留任+迁移 | proven-done | proven-done | proven-done | **done** | `ad167de`/`c1a0b2f`(re-verified) |
| phase5 诊断+活文档 | proven-done | proven-done | proven-done | **done** | `aa61e37`;活文档一致性 self-attested(自查 diff) |
| phase6 全量回归 | n/a(无独立 impl) | n/a | proven-done | **done** | `go test ./... -count=1` exit 0(re-verified) |

## 3. Key File Changes (关键文件变更)

- `agent/dsh-web/resolver.go`:整体重写——座位=probeURLs[0] 唯一位子;`ErrInstanceReconnecting` 类型化宽限错误;`lostAt`/`gracePeriod`(默认 120s)/`lossSeq` 边沿序号;锁纪律(mu 只护缓存字段、探活与 spawn 锁外、single-flight、≤1s 负缓存、spawn 退避 5s);`processIsAlive`(EPERM=存活)标签所有权永不记死 PID;dark 分支先探座位再 spawn;`Stop()` 改留任不杀;`GraceState`/`SetLostCallback`/`LossSeq`/`LastSpawnErr`/`dataDirOf` 新增;3096–3196 常量与收养路径删除。
- `agent/dsh-web/migrate.go`(新):`CleanupLegacyManaged` 一次性孤儿清理——仅 PID 活 + cmdline 含 dsh + 记录端口仍在听才 TERM→KILL;任何不符(PID 复用)删 state+告警绝不误杀;座位纪 record 保留。
- `agent/dsh-web/dshweb.go`:`handleSeatLost`(running 会话推 EventError 终态,边沿序号幂等+重武装,通道满如实 Warn);`InstanceStatus` 宽限特判 `available=true`+reconnecting detail(§12.1-4);New 挂失联回调 + 同步执行迁移清理。
- `agent/dsh-web/session.go`:`Send` 开头查 `GraceState`(绑定会话不经 Resolve,§12.1-2);`sessionBindings.snapshot()`。
- `go-bridge/handlers.go`:`wireErrorWithReconnect`/`sendWireError`/`listWireError`——`errors.As(ErrInstanceReconnecting)` → `backend_unavailable`,其余错误保持现行码;send 三处 + list 四处统一接入。
- `agent/dsh-web/diagnostics.go`:宽限窗口如实报告(座位+截止+wire 行为)、spawn 失败区分"端口被非 dsh 进程占用"、托管行 pid+ps 启动时间+留任语义、突变性标注(S2)。
- `agent/dsh-web/proc_unix.go`/`proc_windows.go`:`processCommandLine`/`processStartTime` 跨平台(unix=ps;windows 如实降级)。
- `GO_BRIDGE_ARCHITECTURE.md`:实例生命周期段整体改写为座位模型(S8)。
- 测试:`seat_lifecycle_test.go`/`grace_wire_test.go`/`turn_terminal_test.go`/`migrate_test.go`/`diagnostics_seat_test.go`/`wire_reconnect_map_test.go`(go-bridge)/`lifecycle_test.go`(旧收养测试改写座位语义)。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands: `go test ./... -count=1`(根模块全量);`go vet ./agent/dsh-web/ ./go-bridge/`;`go build ./...`
- Result: 全部通过,exit 0,零失败(收口审计时全量重跑)
- Attestation: **re-verified**(收口前一次性重跑全部测试)
- Main test files: 见 §3 测试清单;必测下限:§8.1 形态 = `TestSeatLossGraceNoAdoptNoSpawnThenRebind`(流重连场景的单测形态:失联宽限不收养侧端口孤儿不 spawn、用户回座重绑);§8.5 = `TestSendDuringGraceReturnsTypedError` + `TestWireErrorWithReconnectMapping`(绑定会话 send → `backend_unavailable`,非 `send_failed`)
- Artifact paths: 本文件 §0 所列 commits

### 4.2 Regression evidence

- 各 phase regression todo 证据:`agent/dsh/...`(旧件零改动)+ `go-bridge/...` 全量反复通过;根模块全量 `go test ./... -count=1`(re-verified)。
- 真机行(设计 §8 矩阵的设备部分):**owner 于 2026-08-21 真机验收通过四行**(部署 commit `252e78d4219f`,Release 覆盖安装 `/Applications`):
  1. App 运行中杀掉 dsh → bridge 在宽限到期后于座位(3080)补拉 → iOS 发消息、Mac 浏览器实时同步 ✅(= §8.2 watcher/宽限到期行 + 单向实时行的设备面);
  2. 先退 App(留任:dsh 存活)再杀 dsh → 3080 浏览器不可达、iOS 显示"正在重新连接" ✅(= §8.10 留任行 + 无 bridge 时无人补拉的如实行为;iOS 的"重新连接"是 bridge 传输层重连,非实例宽限);
  3. 重新打开 App → 冷启动自动在座位拉起 dsh → iOS 可发消息、Mac 同步 ✅(= §8.3 冷启动行);
  4. 部署首启日志 `instance resolved source=external reason=seat-adopt`(收养用户自启 3080,全机单实例) ✅(= §8.4 进程重启=冷启动收养行的设备面)。
  Attestation: **owner 真机回报(2026-08-21,对话记录)**,非 agent 自测。
  **仍未实测**:§8.1 宽限窗口内的 17s 重启行(backend_unavailable 气泡 + grace-rebind)与 §8.9 EADDRINUSE 提示文案——前者单测已覆盖同形路径,设备面待后续顺带观察即可。

### 4.3 Audit downgrade summary

- Downgraded todos: none
- Why they were downgraded: n/a(内部退出审计未发现无凭据 done;收口重跑无失败)

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

- **真机矩阵待 owner**:上述四类真机行;尤其"用户重启 3080 约 17s"的实机复现(单测已覆盖同形状态机路径)。
- **宽限时长 120s 未实测校准**:设计挂账项②(用户手搓实例冷启动分布未测);2s 触发器下不再承重,保留常量可调。
- **双实例写 workspace.json**(挂账项③):旧孤儿被一次性清理后理论上不再出现双写;历史损伤未检验,升级后观察 web 侧栏归组即可。
- **launchd 收尸依赖**:Stop 留任后,座位实例的生命周期归用户/系统;升级 dsh 需手动重启一次(诊断已给出 pid+启动时间)。
- **`go test ./...` 含 5s 量级的 kill-grace 用例**(全匹配清理测试),CI 时长略增,可接受。

## 6. Audit Focus (建议审核重点)

1. `resolver.Resolve` 的锁纪律与 single-flight(§3.3):确认所有探活/spawn 路径确实在 mu 外,并发调用无 30s 阻塞面。
2. `loseSeatLocked` 边沿唯一性:宽限进入、流 1006、watcher 三条路径是否结构性汇于单次转移(终态幂等的根基)。
3. handler 七处映射:除 `ErrInstanceReconnecting` 外的错误是否仍落 `send_failed`/`list_failed`(不误伤官方 RpcError 透传,坑 7)。
4. `CleanupLegacyManaged` 的三重核对(PID 活/cmdline/端口)与座位纪 record 保留分支——误杀面与误删面。
5. `InstanceStatus` 宽限特判与 `detectInstanceStatusProber` 的链路(§12.1-4):hello_ack 在宽限内必须是 `available`。

## 7. Constraints (关键约束)

- 设计红线全数保持:spawn 恒 `--host 127.0.0.1 --port <座位>`,永不 `--trusted-host`/`0.0.0.0`;宽限错误禁 `not_configured`;3096–3196 仅存在于迁移清理的识别常量。
- 协议 pack(`docs/protocol/`)零改动——`backend_unavailable` 用既有错误表行,无新 wire 码,iOS 零改动(三轮评审判定)。
- `agent/dsh`(旧件)零改动;`relay-server`、`MacBridge`、iOS 零改动(git diff 审计为空)。
