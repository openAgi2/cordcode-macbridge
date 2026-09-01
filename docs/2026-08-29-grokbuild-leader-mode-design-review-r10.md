# Grok Build Leader 模式开关设计第十轮评审报告（v10）

评审日期：2026-08-29

评审对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v10（1525 行）

评审范围：按第九轮报告约定，仅机械核对 R9 最终有限清单（1B + 4M）及其测试、编号和来源落点；不重新开放 R1–R8 已闭合事项。

评审方式：只读源码与文档；未构建、未测试、未修改设计稿。

## 1. 来源核对结果

### 1.1 cordcode-macbridge

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=基线源码干净；仅 docs/ 下设计稿及历轮评审报告为未跟踪新增文档
任务预期分支=main
预期产品特性=在既有 grokbuild leader 只读观察链路上增加 Mac 配置开关；保留 file tailer；D-G1/D-G2 为已签署的受控 Go 修正
```

### 1.2 grok-build

```text
配套仓库路径=/Users/jacklee/Projects/grok-build
分支=main
提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
未提交状态=干净
任务预期分支=main
预期产品特性=提供既有 leader/config/protocol 行为基线，不是本设计的实施仓库
```

两仓 HEAD 均与评审指令冻结值一致，未发生来源漂移。设计稿 SHA-256 为 `886fc4e76a7c361942cc7b45f5bbeb99277abeb9e8e82ef02ef4626e79ac1e17`，`git diff --check` 通过。

评审前已先读 §0.4：本设计不是新建 backend adapter；既有协议客户端、codec、relay loop、catalog、history 不是本设计交付物；不要求 follower 可写方向，也不删除或绕过 file tailer。本轮未按错误模板扩项。

## 2. 分级发现清单

### B（阻断）

无。

### M（必改）

无。

### S（建议）

无新增建议。本轮是已约定的最终机械闭合核对，继续扩展开放式建议会破坏有限收敛边界。

## 3. R9 最终有限清单逐项核对

| 项目 | 结论 | 证据 |
| --- | --- | --- |
| R9-B1：fence 四分支与正常终态缓存回归 | **已核实，闭合** | 文档 §3.5.2-7（约 `:766-788`）明确列出 claim running、正常 terminal→idle、真 source 断开→idle、self-cancel release→unknown/delete 四类有效状态变化，统一调用现成 `FenceBackend("grokbuild")`；G7（`:1078`）覆盖真断开前预热 running 快照、fence 后 page-0=idle 与旧 cursor stale；G8-①（`:1079`）覆盖正常 terminal 的同类预热断言。源码 `go-bridge/handlers_relay.go:225-243` 证明确有真断开 `markIdle` 分支；`go-bridge/catalog_wire_snapshot.go:200-221` 证明现成 fence 会推进 generation 并清除 committed/in-flight snapshot。 |
| R9-M1：typed release outcome | **已核实，闭合** | §3.5.2-2（约 `:720-730`）冻结 `passiveClaimReleaseOutcome` 的 `Noop / Deleted / Unknown`，定义 `claimReleased = outcome != Noop`，并禁止 release 后二次读取 registry 推测结果；G7（`:1078`）逐项断言三种返回值及 `registryOutcome` 日志一致性。 |
| R9-M2：编号、unknown 术语与 §10 行号 | **已核实，闭合** | §7.4（`:1114`）与 Phase 1（`:1134`）均为 T1–T33；规范区 §0–§11 未发现 `T1–T30` 或把 unknown 称为“第三态”的残留，§3.5.2（`:705-710,739-747`）明确 idle/running/closing 为既有三态、unknown 为第四状态；§10（`:1220`）已改为 `types.go:226-230,243-377`，并与源码声明及 registry 范围一致。历史审计区的旧原文依 §12 前规范性边界保留，不构成实施规范冲突。 |
| R9-M3：连续负样本不重置锚点 | **已核实，闭合** | §3.5.2-3（`:657-674`）明确仅在负样本且 `firstNegativeAt` 为零时记录 `now`，连续负样本保持原锚点，正样本清零；G5（`:1076`）包含第二、第三个连续负样本不移动锚点以及 59.9s、转正清零等边界断言。 |
| R9-M4：manifest 登记 T31–T33 | **已核实，闭合** | §3.3-9 manifest（`:542-543`）登记 T31/T32 的节边界与无尾随换行 synthetic 样本，以及 T33 的合法 TOML/非法 Bool synthetic 样本，并限定后者只冻结 fail-closed 行为；§7.1（`:1062-1064` 附近）存在对应可执行用例。符合 source-first：synthetic 与真实样本分级清楚，没有把自造输入冒充现场证据。 |

## 4. 五个维度结论

### 4.1 事实核查

本轮有限清单涉及的源码事实均与冻结提交一致。真 source 断开的 defer 确实在 `go-bridge/handlers_relay.go:225-243` 执行 `markIdle`；现成 `FenceBackend` 的 generation 推进与 committed/in-flight 清理语义见 `go-bridge/catalog_wire_snapshot.go:200-221`；registry 既有 idle/running/closing 声明见 `go-bridge/types.go:226-230`，registry 定义从 `:243` 开始。R9 修订引用的行号和语义均已核实，无行号漂移。

### 4.2 设计闭合性

R9-B1 的遗漏分支已经补全，并由 G7/G8 分别固定“真断开”和“正常 terminal”两条缓存穿透路径；release 的三值结果成为 fence、日志与 `claimReleased` 的单一事实源；计时 helper 的锚点转换规则不再有“每 tick 重置”的实现歧义；T31–T33 已同时进入 manifest 和测试表。本轮检查范围内没有未闭合分支。

### 4.3 Go 改动边界与可交付性

设计已不再宣称绝对“零 Go 改动”，而是把 Owner 已签署的 D-G1/D-G2 限定为 `agent/grokbuild/grokbuild.go`、`go-bridge/handlers_relay.go`、`go-bridge/types.go` 及定向测试；catalog 仅调用现有 fence，不修改 catalog 实现。Phase 2 的拆分、G1–G8、独立提交要求和 D-G1/D-G2 互锁均足以让开发 agent 按规范实施，不需要其自行补产品裁决。

### 4.4 纪律一致性

§0.4 继续正确排除新 backend adapter 的 wire 冻结、新旧 backend 并存、follower 写方向等不适用纪律，并保留 source-first、fail-visible、可逆备份、file tailer 与防偏航门。T31–T33 的 synthetic 身份明确，未把设计生成样本提升为外部事实。R9-S1 采用的“§0–§11 规范、§12–§21 审计追溯”边界也足以避免历史旧口径误导实施。

### 4.5 内部一致性

§3.5.2、G5/G7/G8、§7.4、Phase 1/2、§10、§11 与 §21 对 R9 五项使用同一口径：四类 fence、typed outcome、unknown 第四状态、T1–T33、首负样本锚点及 manifest 登记彼此一致。未发现会让开发 agent 选择错误分支的残留规范文本。

## 5. 总结论

**APPROVE**。

R9 最终有限清单 1B + 4M 已全部逐行命中，当前 v10 可以交给开发 agent。按文档 §0.2，下一步是先执行 Phase 0 十步现场拓扑证明；Phase 0 通过前不得进入产品代码实施。此次批准的是设计可实施性，不代表 Phase 0、构建、测试或生产路径已经验证。
