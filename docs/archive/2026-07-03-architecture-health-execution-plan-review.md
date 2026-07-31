# 架构健康度治理执行计划 — 评审报告

- **评审日期**：2026-07-03
- **被评审文档**：[docs/2026-07-03-architecture-health-execution-plan.md](../2026-07-03-architecture-health-execution-plan.md)
- **评审者**：评审 agent（以代码为唯一真相源）
- **方法**：读执行计划全文 → 对照代码核验技术主张（capability 双写差异、config 依赖、toml 库、字段映射、函数名、配置路径、iOS 工程名/scheme、测试 filter）→ 评估可施工性。**本评审核验的是"计划是否可施工"，不是"施工结果"**；施工完成后应另起一轮结果评审（按被评审文档第 8 节清单）。
- **关联**：被评审计划源自 [2026-07-03-architecture-health-assessment.md](../2026-07-03-architecture-health-assessment.md)。

---

## 总体判断

**可批准施工。** 执行计划质量高、范围收得正确（第一轮只 A + B1）、技术主张与代码高度一致（核实 **0 处硬事实错误**）。2 个 P1 应在施工前补齐（不阻塞但强烈建议），其余为增强建议。

可施工性评级：
- **A（capability 单源化）**：低风险，可直接施工。
- **B1（切断 config 生产依赖）**：中风险，改法可行，注意 TOML `map[string]any` 解码等价性。

---

## 一、技术主张核实（文档 vs 代码真相）

逐条核验，**全部成立**：

| 文档主张 | 代码真相 | 核实 |
|---|---|---|
| 双写差异仅 `question_reply`（BackendList 有、deriveCapabilities 漏） | `handlers.go:352-410` vs `agent_descriptor.go:103-160` 逐行对比：除 `handlers.go:400` 多 `question_reply` 外完全等价（同 model_switch/session_state/provider_switch/session_history/workspace_diff/memory_read/diagnostics/usage_reporting/permission_mode/session_mutation/content_chunking/session_delete/permission_resolve/todos/compression 序列） | ✅ |
| 生产代码仅 `provider_switch.go` import config | `provider_switch.go:10` `ccconfig ".../config"`；全仓生产仅此 1 文件（另 2 个 `agent/*/provider_*_test.go` 是 test） | ✅ |
| 实际依赖 4 符号 Config/Load/ProjectConfig/ProviderConfig | `Load`(:44)、`Config`(:68)、`ProjectConfig`(:68,94)、`ProviderConfig`(:153) | ✅ |
| 配置路径 `~/.cc-connect/config.toml` 或 `CC_CONNECT_CONFIG` | `provider_switch.go:58`(CC_CONNECT_CONFIG)、`:65`(~/.cc-connect/config.toml) | ✅ |
| B3 字段覆盖 name/api_key/base_url/model/thinking/env/models/codex.wire_api/codex.http_headers | `providerConfigsToCore` :153-174 映射字段完全一致 | ✅ |
| B3 保留函数 5 个（loadProviderSeedForAgent/ccConnectConfigPath/findProviderProject/projectMatchesWorkDir/providerConfigsToCore） | 全部真实存在（:32/:57/:68/:94/:153） | ✅ |
| 根 module 已有 BurntSushi/toml | `go.mod`: `github.com/BurntSushi/toml v1.6.0` | ✅ |
| iOS 工程 `OpenCodeiOS/CordCode.xcodeproj -scheme CordCode` | 实际即 `CordCode.xcodeproj` + `CordCode.xcscheme` | ✅ |
| opencode/codex 不得宣告 permission_resolve | `agent_descriptor.go:147` `if id != "opencode" && id != "codex"` | ✅ |

**0 处硬事实错误**——这是这份计划最值得肯定的地方：施工 agent 照着做不会被错误前提带偏。

---

## 二、必须修正（P1，施工前补）

### P1-1：A5 / 第 7 节验收 filter 含 `TestBuildAgentDescriptor`，但当前仓库无此前缀测试

- **现状**：`grep TestBuildAgentDescriptor go-bridge/*_test.go` = **0 命中**。现有相关测试是 `TestFullAgentCapabilities` / `TestCodexAppServerCompressionCapability` / `TestCodexExecNoCompressionCapability`（agent_descriptor_test.go）+ `TestBackendList*`（events_test.go / handlers_test.go）。
- **风险**：`go test -run '...|TestBuildAgentDescriptor'` 匹配 0 个**不报错**，验收命令该段空跑，等于没验证 `BuildAgentDescriptor` 路径——而它正是 A2 的核心目标（hello_ack.backends[] 漏 question_reply）。
- **修正**：A4 明确要求"至少新增一个以 `TestBuildAgentDescriptor` 开头的测试"（断言 codex app_server descriptor 含 `compression` + `question_reply`），让验收 filter 兑现；或在验收命令处注释标明该段依赖新增测试。

### P1-2：A4 / B4 未把"现有相关测试不回归"列为显式验收项

- **现状**：单源化会动到 `deriveCapabilities` / `BackendList`，config 切断会动到 `provider_switch.go`；现有大量测试覆盖这些路径：
  - capability 侧：`TestBackendList*` 10+ 个（events_test.go:848/867/883、handlers_test.go:382/397/412/608/636/660/687/1595/1615/1652…）、`TestFullAgentCapabilities`、`TestCodexAppServerCompression*`、`TestRegisterAck*`。
  - provider 侧：`TestLoadProviderSeedForAgent_ResolvesProviderRefsForMatchingWorkDir`、`TestApplyProviderSeed_SetsProvidersAndActiveProvider`。
- **风险**：A4 / B4 只列了"新增测试点"，没强制"现有测试全绿"。施工 agent 若只跑新增测试、不跑 `go test ./go-bridge/...`，可能漏回归。
- **修正**：把"现有 capability/provider 相关测试全部不回归"写进 A6 / B7 完成证据；第 7 节的 `go test ./go-bridge/... -count=1`（已列）应在 A6 / B7 也点明"全量 `./go-bridge/...` 通过"作为强制项，不只放总清单。

---

## 三、建议改进（P2，增强可施工性，不阻塞）

### P2-1：A3 单源函数的输出顺序需与现有 deriveCapabilities 一致
现有 `TestFullAgentCapabilities` 等可能顺序敏感。A4 建议了"转 set 比较"（好），但单源函数本身应保持 `deriveCapabilities` 现有追加顺序（model_switch → session_state → … → compression → question_reply），BackendList 侧把 `question_reply` 并入同一位置，避免顺序漂移引发假阴。

### P2-2：B1 切换 TOML parser 有 `map[string]any` 解码等价性风险
B3 的 `providerSeedAgent.Options` 用 `map[string]any`。BurntSushi/toml 解码 `map[string]any` 时，数字可能是 int64/float、嵌套 table 行为可能与原 config 包不一致，影响 `optionString(Options["provider"])`(:54) 与 `Options["work_dir"]`(:99) 的 string 提取。建议 B4 增加一条"options 解码等价性测试"：用一个含 provider/work_dir/混合类型的 options 实例，断言切换 parser 后提取结果与原行为一致。

### P2-3：B4 补"agent type 不匹配的 project 不被选中"
`provider_switch.go:77` 用 `project.Agent.Type != agentType` 过滤。B3 结构有 `providerSeedAgent.Type` ✅，但 B4 应明确加一条"agent type 不匹配的 project 不被选中"防回归测试。

---

## 四、小瑕疵（P3，可选，不展开）

- 第 0 节硬约束"不重新启用 session_pagination"与 A2 一致；A4 已列"descriptor 不含 session_pagination"断言 ✅，无需改。
- C4 web 验收命令自注"以 package.json scripts 为准，缺失则记阻塞"——好护栏；C 暂缓，不深审。
- A5 iOS destination `generic/platform=iOS`：A 任务预计不动 iOS，此命令仅条件触发，合理。

---

## 五、被评审文档第 8 节核验清单 — 当前状态回填

| # | 清单项 | 当前状态（施工前） |
|---|---|---|
| 1 | 单一 capability 来源 | 计划要求 ✅，待施工后核验 |
| 2 | question_reply 同时在 BackendList + BuildAgentDescriptor | 计划要求 ✅（A2），待施工 |
| 3 | session_pagination 未误启用 | 计划明确禁止 ✅（第 0 节 / A2），待施工后核验代码注释仍关闭 |
| 4 | provider_switch.go 不再 import config | 计划要求 ✅（B1），待施工后 rg 核验 |
| 5 | provider seed TOML 覆盖 model/env/codex 子字段 | 字段表与代码一致 ✅（已核实） |
| 6 | 不引入 fallback/mock/placeholder | 计划禁止 ✅（第 0 节） |
| 7 | 不改 wire protocol 字面量 | 计划禁止 ✅（第 0 节） |
| 8 | 不误跑 UI automation | 计划禁止 ✅（第 0 节 / A5 / C4） |
| 9 | CHANGELOG 记录 | 计划要求 ✅（第 7 节） |

**施工前的所有前提主张均成立。** 清单 1/2/3/4 需在施工后用代码核验。

---

## 六、小结

执行计划**可施工、可批准进入第一轮（A + B1）**。施工前补 P1-1（验收 filter 兑现）与 P1-2（现有测试不回归列为强制验收）两项；P2 可在施工中顺手处理。施工完成后，建议另起一轮**结果评审**，按被评审文档第 8 节清单逐条用代码核验，并跑第 7 节最终命令。

**未修改任何代码。** 本评审仅核验"计划可施工性"。
