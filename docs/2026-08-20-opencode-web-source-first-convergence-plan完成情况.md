# OpenCode Web source-first convergence 完成情况

> **产品状态（2026-08-21）**：directive-014 E2b 技术修复已经 exec-plan proven + 监工 audit-014 verified；现在放行 owner 真机重新打开 OpenCode session。owner 回报前产品仍不得标记 done。

## 1. 权威与范围

- canonical：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md`
- 同版本事实源：OpenCode 1.18.18源码 + A1–A10/WP/E1–E7/E1b/E4b/E5b真实样本 + bridge mapping。
- 已实施：C1–C7、SSV2单写者/单ingest约束、严格shape、协议variant/agent镜像、iOS variant UI、能力真实性。
- 新证据：E2b 已证明真实 1.18.18 HTTP history 含 populated reasoning；原“遇到 reasoning 整体 hydrate fail”的设计结论已撤回。direct-SSE reasoning、v2 generation、OD-3 future/unsupported项仍未实施。

## 2. 最终review-fix链

| 审计 | 发现 | 最终状态 |
|---|---|---|
| audit-008 | C4双ingest、question identity、strict shape、variant UI | directive-009/010修复 |
| audit-009 | question source identity/cold recovery与strict tail | directive-010修复 |
| audit-010 | SSE gap terminal reconciliation与真实并发缺口 | directive-011修复 |
| audit-011 | terminal后late asked/stale recovery重开reply mapping | directive-012修复 |
| audit-012 | lifecycle拒绝GET row后lookup仍无条件返回 | directive-013修复 |
| audit-013 | 无新增缺口 | **verified** |
| owner UI / directive-014 | 任意抽样 OpenCode session 立即 `projection.hydrate_failed`；根因为 E2 结论错误地拒绝真实 HTTP persisted reasoning | **technical repair verified；owner retest pending** |

## 3. 最终独立证据

- `go test ./go-bridge -run 'TestAudit01[123]' -count=20 -timeout 6m`：PASS（201.7s）。
- `go test -race ./agent/opencode-web -run 'TestAudit01[0123]|TestQuestion' -count=20 -timeout 5m`：PASS（12.3s）。
- `go test ./... -count=1 -timeout 10m`、vet、build：PASS。
- fresh隔离OpenCode 1.18.18 `TestSandboxC6C7`：question、pending reload、answer/reject reopen、todo、permission、mutation全部PASS。
- runtime：8777来自 `/Applications/CordCodeLink.app`；4096 owner-managed保持；4398/4399回收。
- iOS相关代码沿用directive-010已验证的11/11定向测试与真机安装证据；audit-011至013无iOS改动。

## 4. 核心架构结论

- OpenCode 1.18.18是唯一verified generation；v2/unknown fail closed。
- timeline只有一个EventPublisher/Kernel ingest owner；iOS ProjectionStore仍是唯一messages writer。
- question pending、terminal、recovery与resolve由同一lifecycle admission控制；terminal不会回pending、恢复mapping或经stale GET再次提交。
- pending fresh-process reload恢复原interaction Dock；resolved fresh-process history没有原 `que_…` ID，只hydrate证据支持的tool activity，不伪造Dock。
- provider/config/agent/todo/mutation均严格按同版本shape处理；无legacy fallback、递归shape搜索或假成功。

## 5. 三轨状态

| 轨道 | 状态 |
|---|---|
| exec-plan proven | ✅ 75/75 |
| supervisor verified | ✅ audit-014 |
| owner真机UI矩阵 | ⏳ message-page 重试已放行 |

developer完成directive-014、监工独立审计通过、owner重新执行canonical §8测试矩阵并逐项回报后，才能宣布OpenCode Web产品验收done。
