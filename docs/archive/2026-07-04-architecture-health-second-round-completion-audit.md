# 架构健康第二轮完成情况 — 独立审计报告

日期：2026-07-04
被审计报告：`docs/2026-07-04-architecture-health-second-round-development-brief完成情况.md`
关联 brief：`docs/2026-07-04-architecture-health-second-round-development-brief.md`
Exec-Plan state：`.exec-plan/state/plan-f55b6e5f795e.json`
审计性质：独立复核完成报告、工作树与可重跑验证。未运行 UI tests、snapshot tests、simulator automation 或真机验证。

---

## 结论

**通过。第二轮完成报告的核心结论成立：16/16 todo 为 done + proven，P0/P2/P1 的可重跑验证 fresh 复跑通过。**

本次审计没有发现会阻塞交付的问题。完成报告中的主要工程声明可复现：

- P0：`shared-message-renderer` 现覆盖 5/5 目标组件，iOS 三包 typecheck/build 与共享包 6 文件 20 测试通过。
- P2：BridgeProvider 净增长 gate 默认 warning-only、strict 无增长 exit 0、模拟增长 exit 1；CI macbridge job 已接入 strict step。
- P1：`handlers.go` 4559 → 3269 行，新增 `handlers_opencode.go` 488 行、`handlers_relay.go` 829 行；`go build`、定向 Go 测试、全量 `go test ./go-bridge/...` 通过。

需要修正的只有一个**报告口径细节**：completion report 说 state 顶层 todo 是 `required:true × 16`，实际字段在 `verification.required` 下；按真实 schema 复核为 `verification.required=true × 16`。这不影响 16/16 proven done 的结论。

---

## Findings

### P3 — 完成报告把 required 字段位置说成顶层

完成报告“结论”段写 `required:true × 16`。审计发现 exec-plan state 的 todo 顶层没有 `required` 字段，真实字段是 `verification.required`。

复核结果：

```text
todos=16
verification.required=true=16
verification.required=false=0
required absent=0
done=16
verification.status=present=16
bad=[]
```

影响：低。完成状态成立，只是报告中的字段路径应理解为 `verification.required`，不是 todo 顶层 `required`。

---

## 复核证据

### Exec-Plan 结构

命令：

```bash
jq '{todos:(.todos|length),
  required_explicit_true:(.todos|map(select(.verification.required==true))|length),
  required_explicit_false:(.todos|map(select(.verification.required==false))|length),
  required_absent:(.todos|map(select(.verification|has("required")|not))|length),
  done:(.todos|map(select(.status=="done"))|length),
  proven:(.todos|map(select(.verification.status=="present"))|length),
  bad:(.todos|map(select(.status!="done" or .verification.status!="present"
    or ((.verification.summary // "")|length==0)
    or ((.verification.artifacts // [])|length==0)))|map({id,status,verification}))}' \
  .exec-plan/state/plan-f55b6e5f795e.json
```

结果：`todos=16`、`required_explicit_true=16`、`done=16`、`proven=16`、`bad=[]`。

### P0 Web Shared Renderer

复跑命令：

```bash
cd ../cordcode-ios/shared-message-renderer
npm run typecheck && npm run test

cd ../cordcode-ios/message-web
npm run typecheck && npm run build

cd ../cordcode-ios/remote-web
npm run typecheck && npm run build
```

结果：

- shared package：6 个 test files、20 tests passed。
- message-web：typecheck 通过，Vite build 通过，307 modules transformed。
- remote-web：typecheck 通过，Vite build 通过，343 modules transformed；保留既有 dynamic/static import warning，不是本轮新增失败。

代码抽查：

- `shared-message-renderer/src/components/blocks/ReasoningBlock.tsx`、`NarrativeBlock.tsx`、`components/turns/ProcessGroup.tsx` 存在。
- `message-web` / `remote-web` 对应组件已变成 thin wrapper 或 re-export。
- `ProcessGroup` 正确进入 `shared-message-renderer/src/components/turns/`，没有被塞进 `blocks/`。
- `index.ts` 导出 Reasoning / Narrative / ProcessGroup，shared package 覆盖 5/5 目标组件。

残余风险与完成报告一致：未跑视觉、snapshot、simulator、真机；P0 视觉/UX 仍需 owner 验收。

### P2 BridgeProvider 净增长 Gate

复跑命令：

```bash
./scripts/check-architecture-hygiene.sh
CORDCODE_HYGIENE_STRICT=1 ./scripts/check-architecture-hygiene.sh
```

结果：

```text
default_exit=0
strict_exit=0
BridgeProvider.swift baseline -> current:
  lines:      1967 -> 1967
  funcs:      88 -> 88
  forTesting: 36 -> 36
Result: STRICT passed — no BridgeProvider net growth.
```

模拟增长命令使用 `/tmp` 临时 iOS tree，不修改仓库文件：

```bash
tmpdir=$(mktemp -d /tmp/cordcode-ios-growth.XXXXXX)
mkdir -p "$tmpdir/OpenCodeiOS/OpenCodeiOS/Services/Bridge"
cp ../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift \
  "$tmpdir/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift"
printf '\nfunc auditGrowthForTesting() {} // ForTesting\n' >> \
  "$tmpdir/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift"
CORDCODE_IOS_ROOT="$tmpdir" CORDCODE_HYGIENE_STRICT=1 ./scripts/check-architecture-hygiene.sh
```

结果：exit 1，输出 `STRICT FAILED — BridgeProvider net growth detected`，lines/funcs/forTesting 三项均检测到净增长。

代码抽查：

- `scripts/hygiene-baseline.json` 基线为 lines=1967、funcs=88、forTesting=36。
- `scripts/check-architecture-hygiene.sh` 默认仍 exit 0；strict 只对 BridgeProvider 净增长 fail。
- `.github/workflows/ci.yml` 的 macbridge job 已 checkout `openAgi2/cordcode-ios` 到 `cordcode-ios`，随后运行 `CORDCODE_IOS_ROOT="$GITHUB_WORKSPACE/cordcode-ios" ./scripts/check-architecture-hygiene.sh`。

### P1 handlers.go 物理分发

复跑命令：

```bash
go build ./go-bridge
go test ./go-bridge -run 'Test.*Session|Test.*Message|Test.*Backend|Test.*Capability|Test.*Pagination' -count=1
go test ./go-bridge/... -count=1
```

结果：

- `go build ./go-bridge` 通过。
- 定向测试通过：`ok github.com/openAgi2/cordcode-macbridge/go-bridge 0.857s`。
- 全量 go-bridge 测试通过：`ok .../go-bridge 13.257s`，runtime cmd 无测试文件。

结构复核：

```text
3269 go-bridge/handlers.go
 488 go-bridge/handlers_opencode.go
 829 go-bridge/handlers_relay.go
```

`handlers_opencode.go` 包含 `handleOpenCodeRPC` 与 `ocHandle*` 簇；`handlers_relay.go` 包含 relay / session-file relay / transcript relay 相关函数与 transcript helper。三文件同属 `package gobridge`，符合“物理分发，不做逻辑解耦”的 brief 边界。

---

## 残余风险

- **P0 视觉验收仍未完成**：构建和组件测试绿，但没有 owner 授权的 UI/snapshot/simulator/真机验证。需要在 iPhone 与 web 客户端目测 reasoning / process-group / narrative，尤其是 message-web git directive summary。
- **CI strict gate 的远端可访问性是运行时条件**：本地脚本与 workflow 配置正确；若 GitHub Actions 上 checkout `openAgi2/cordcode-ios` 失败，脚本会按设计 graceful skip。这是 brief 允许的取舍，但意味着“CI 一定执法”依赖 iOS 仓对 runner 可读。
- **`handlers.go` 仍有 3269 行**：本轮只拆 OpenCode 与 relay 两个高凝聚块，未继续拆 sessions/messages/agents/files。完成报告对此表述诚实。
- **双仓工作树仍未提交**：审计时 MacBridge 与 `../cordcode-ios` 均有未提交改动。提交边界应保持两仓分别提交。

---

## 审计判定

通过，带一个低优先级报告口径修正建议。

建议 owner 下一步按完成报告 checklist 做 P0 视觉验收；验收后再决定是否让执行 agent按两仓分别 commit。第三轮启动前，需要先为 `BridgeProvider` 选定一个拆分子域并列出行为不变量测试。

