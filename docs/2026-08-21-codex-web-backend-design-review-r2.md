# codex-web Backend 设计二轮评审报告（r2）

- 日期：2026-08-21
- 评审对象：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md) **v1.2**（含 §18 一轮评审采纳记录）
- 一轮评审：[2026-08-21-codex-web-backend-design-review.md](2026-08-21-codex-web-backend-design-review.md)
- 评审方式：全文重读 v1.2；对一轮 15 条修订逐项核验落实情况；对 M2 唯一部分不采纳项按官方源码独立复核；核验 §9.1 接线清单符号在当前仓库的真实存在性；核对 v1.2 新增的全部源码引用。
- 评审边界：纯文档评审，未修改代码与设计文档。共享运行时、Desktop/VS Code 覆盖面、`0.148.0-alpha.21` 物理载荷仍按设计自身纪律属 Phase 0 未证事实。

---

## 1. 最终结论

**APPROVE WITH CHANGES（收口级）**

一轮 1 阻断 + 3 必改 + 5 建议的处置：**B1、M1a/b/c、M3、S1–S5 共 10 项经逐条核验全部正确落实**，且新增内容（§9.1 十一处接线清单、§9.2 尾封口、§8.2 受控 Gate 矩阵、§13.3 行 3–5、§15 门槛 11/12）均与源码/当前仓库代码对得上，未发现新引入的内部矛盾。

M2 的部分不采纳：**结论（一期 ⛔ provider 列表/切换）经独立复核成立**——`model/list` 的 `Model` 无 provider 字段、`turn/start` 只有 `model` 无 `model_provider`、`modelProvider/capabilities/read` 只读当前 provider，"协议无 provider-list / running-thread provider-switch RPC"属实。**但采纳记录中"config/read 只返回当前 provider"这一条事实论证在 wire 层不成立**（详见 R2-M1）：`config/read` 响应经 flatten 兜底字段很可能完整携带 `model_providers` 表。策略结论不变，事实表述必须修正，否则 Phase 0 样本将与文档直接矛盾。

剩余：1 必改（R2-M1）+ 2 建议（R2-S1/S2）。修完即可进入 Phase 0。

---

## 2. 阻断问题

无。

---

## 3. 必改问题

### R2-M1：M2 不采纳理由中"config/read 只返回当前 provider"在 wire 层不成立，须改写并列入 Phase 0 验证点

- **严重级别**：必改（错误事实被写入 §18 采纳记录与 §7 ⛔ 行的论证，Phase 0 样本必然与之冲突）
- **文档位置**：§18 M2 行（"`config/read` 只返回当前 provider"）；§7 "list/switch provider" ⛔ 行说明
- **官方源码证据**：
  - typed 面属实：`v2/config.rs:261` `Config.model_provider: Option<String>`（当前 provider），`Config` 无 typed `model_providers` 字段；
  - 但 v2 `Config` 有兜底字段：`config.rs:286-287` `#[serde(default, flatten)] pub additional: HashMap<String, JsonValue>`——serde flatten 会捕获一切未匹配键；
  - server 端构造路径：`app-server/src/config_manager_service.rs:133-141`——把完整 effective `ConfigToml` 序列化为 JSON 再反序列化进 v2 `Config`；
  - `ConfigToml` 携带完整 provider 表：`config/src/config_toml.rs:286-287` `pub model_providers: HashMap<String, ModelProviderInfo>`，注释 "Combined provider map (defaults plus user-defined providers)"（内置 + 用户自定义全量）；
  - 因此 `config/read` 响应的 `config.additional` 大概率原样携带完整 `model_providers` map（含各 provider 的 base_url / env key 名）。
- **为什么造成实现错误或返工**：一期 ⛔ 的策略结论本身正确且应保留——从 flatten JSON 提取 provider 列表违反 §16"不为未知版本写递归字段猜测"，且该兜底字段无稳定性承诺。但采纳记录把"只返回当前 provider"写成已证事实后，Phase 0 的 config/read 样本一旦出现 `additional.model_providers`（按上述源码链几乎必然），就形成"文档说没有、样本里有"的矛盾，触发无谓的评审循环，或诱导实施者绕过门控直接消费该字段。
- **建议修订**：§18 M2 行与 §7 ⛔ 行说明改为——"config/read 的 typed 字段只有当前 `model_provider`；其响应另有 flatten `additional` 兜底字段，源码链显示可能携带完整 `model_providers` 表（config_manager_service.rs:133-141 → config_toml.rs:286-287 → config.rs:286-287），以 Phase 0 config/read 真实样本为准；一期 ⛔ 结论不变，禁止从 `additional` 递归提取 provider 列表"。并在 §12 samples 的 models 组补一行"config/read：确认 `additional` 是否携带 `model_providers` 及其形状"。

---

## 4. 建议问题

### R2-S1：§7 "config/read.modelProvider" 字段名与 serde rename 不符；config/read 响应带 experimental 标记应入 §11.2 门控说明

- 级别/位置：建议；§7 "effective provider" 行。
- 证据：`v2/config.rs:254` `Config` 使用 `#[serde(rename_all = "snake_case")]`——wire 字段是 **`model_provider`**，不是 `modelProvider`（v2 多数类型是 camelCase，此结构是例外）；`config.rs:252/371` `Config` 与 `ConfigReadResponse` 均派生 `ExperimentalApi`（`#[experimental(nested)]`），属 §11.2 "experimental 能力逐项开关"的管辖范围（请求侧不拒绝——`ConfigReadParams`（config.rs:361-369）无 experimental 字段——但响应内容的稳定性承诺按 experimental 对待更稳妥）。
- 修订：字段名改为 `model_provider`；在 §7 该行或 §11.2 注明"config/read 响应类型带 experimental 标记，字段形状按 Phase 0 样本冻结"。

### R2-S2：§7 ⛔ 行补 `modelProvider/capabilities/read` 作为正面证据

- 级别/位置：建议；§7 "list/switch provider" 行。
- 证据：`app-server/README.md:243` "modelProvider/capabilities/read — read provider-level capabilities for **the currently configured** model provider"——官方唯一的 provider 级 RPC 也只面向当前 provider，恰好支持 ⛔ 结论。
- 修订：说明中补一句，防止后续实施者发现该 RPC 时误判为 provider 列表面而绕过设计裁决。

---

## 5. 一轮 15 条修订的落实核验表

| 一轮问题 | v1.2 落点 | 核验结果 |
|---|---|---|
| B1-1 §3.3 事实重写 | §3.3 四点源码事实清单 | ✅ 与源码一致：autostart 仅 agents 路径（cli/main.rs:2588-2602）；默认配置复用（lib.rs:437-459/850-920、startup_orchestration.rs:138-190，引用行号经复核覆盖实际函数）；覆盖启动隔离；多连接订阅模型（thread_state.rs:533-581） |
| B1-2 §8.2 受控矩阵 | Terminal 拆三行 + 前提条件列 + 判定规则 | ✅ "Embedded 两条隔离对照符合预期时不算 FAIL"与一轮建议一致 |
| B1-3 §12/Phase 0 变量 | §12 external host 行、Phase 0 第 4 步 | ✅ 均含 daemon 状态 × 启动配置受控采样与"不从 Terminal 类推" |
| B1-4 §0 命题 2 收窄 | §0 第 2 条 | ✅ 限定"默认启动配置、且已复用同一 daemon"，指向 §3.3 边界 |
| M1a requestUserInput 🧪 | §7 行 441 | ✅ "README/类型均标 experimental；core config 默认启用且不由 experimentalApi 门控"双重事实准确 |
| M1b approval 字段归属 | §7 行 438/440 | ✅ command approval 拆 ✅/🧪 并注明剥除（transport.rs:174-192）；permission request 改为 `RequestPermissionProfile`/`GrantedPermissionProfile + scope`（与 permissions.rs:773-807 一致） |
| M1c plan 分级 | §7 行 436 | ✅ item 生命周期 ✅、`item/plan/delta` 🧪 |
| M2 修订 8（部分采纳） | §7 四行（effective provider / new-thread provider ⚠️ / switch_model / list/switch provider ⛔）+ §15 门槛 11/12 + §18 | ⚠️ 结论成立（见 §6 专项复核），但"config/read 只返回当前 provider"论证错误 → R2-M1 |
| M3 SSV2 接线清单 | §9.1 十一处 + §9.2 + Phase 2 第 3/4 步 | ✅ 清单可执行：`backendSupportsProjectionHydrate`（handlers_projection.go:345）、`prepareProjectionHydrateSource`（:578）、`produceProjectionHydrateRange`（:1045）、`streamBackendRichHistoryProjectionEvents` allow-list（:1110-1116）、`pathlessRichHistoryBackend`（projection_kernel.go:691）、forceCold/sourceChanged（handlers_projection.go:113/496）、agent_descriptor.go/main.go/server.go 均在；§9.2 以官方 thread/turn status 封口 + "官方已覆盖则不造本地 detector"与官方 status 语义一致 |
| S1 steer expectedTurnId | §7 行 428 | ✅ 必填前置 + active turnId 跟踪 |
| S2 elicitation 三 variant | §7 行 442 | ✅ Form/openai-form/Url + `mcpServerOpenaiFormElicitation` capability |
| S3 §17 路径与 itemsView | §17 新增 4 条入口；§7.1 行 463-464 | ✅ `protocol/v2/` 目录、app-server-transport、tui lib/startup_orchestration 齐备；`itemsView` Full/Summary/NotLoaded 与 thread_data.rs:350-383 一致 |
| S4 provenance 补 transcriptindex | §13.1、§9.1 行 11、Phase 2 第 6 步 | ✅ 三处一致 |
| S5 daemon 副作用 | §6.3 | ✅ 诊断区分 `external-daemon-reused` 与 `cordcode-started-daemon` |

另核验：§7 新图例 ⚠️ 定义并用于 new-thread provider 行，无歧义；§13.3 行 3/4/5 与 §8.2 受控变量一一对应；行 9 改为"iOS 不显示未实现的 provider 切换"诚实降级；§15 新增门槛 11/12 把 provider/memory 差距显式交由 owner 裁决——均正确。

## 6. 对 M2 部分不采纳的专项复核（本轮核心）

逐条独立核对 v1.2 §18 M2 行给出的四条协议事实：

| 断言 | 官方证据 | 结论 |
|---|---|---|
| "`model/list` 只返回模型" | `v2/model.rs:92-121` `Model` 结构无 provider 字段 | ✅ 成立 |
| "`turn/start` 只允许 model" | `v2/turn.rs` `TurnStartParams`：`model: Option<String>`（stable，"Override the model for this turn and subsequent turns"），无 `model_provider` 字段 | ✅ 成立（故 "switch_model 不改变 provider" 亦成立） |
| "协议没有 provider-list RPC" | method 注册表中唯一的 provider 级 RPC `modelProvider/capabilities/read`（README:243）也只读**当前** provider | ✅ 成立 |
| "协议没有 running-thread provider-switch RPC" | `turn/start` 无 provider 字段；`thread/settings/update`（experimental）与 `thread/resume` 虽可带覆盖，但均非"运行中 thread 的 provider 切换"语义 | ✅ 成立 |
| "`config/read` 只返回当前 provider" | typed 面属实（config.rs:261），但响应经 flatten `additional` 大概率携带完整 `model_providers`（config_manager_service.rs:133-141 → config_toml.rs:286-287 → config.rs:286-287） | ❌ 不成立 → R2-M1 |

**复核结论**：不采纳的方向正确——照一轮建议原样把 `list_providers` 映射到 `thread/start{modelProvider}` 会虚报能力（round-1 建议的本意是"补映射行防能力倒退"，v1.2 用"能力差距显式化 + 退役门槛 11 + ⚠️ new-thread provider 行"达到了同一目的，处置等价且更诚实）。仅事实论证中 config/read 一条需按 R2-M1 修正。

## 7. 应保留的 v1.2 新增内容

1. §8.2 受控 Gate 矩阵（前提条件列 + 三条 Terminal 对照行 + "Embedded 隔离不算 FAIL"判定规则）——一轮 B1 的完整落地形态；
2. §9.1 十一处接线清单——每个符号都能在当前 go-bridge 代码中定位，直接可用作 Phase 2 code review 与测试清单；
3. §9.2 官方 status 尾封口规则（含 `itemsView=NotLoaded` 不得本地封口、Phase 0 fixture 覆盖 active/idle/failed/interrupted/NotLoaded）；
4. §7 的 ⚠️ 图例与 new-thread provider 行——"字段存在但证据/目录缺口不得广告"的第三态，填补了 ✅/🧪/⛔ 的表达空隙；
5. §15 门槛 11/12——把 provider/memory 能力差距从"静默倒退"变为"owner 显式裁决项"；
6. §6.3 daemon 副作用与诊断分级（`external-daemon-reused` vs `cordcode-started-daemon`）。

## 8. 可直接执行的逐条修订清单

| # | 位置 | 修订动作 | 对应问题 |
|---|---|---|---|
| 1 | §18 M2 行 + §7 "list/switch provider" 行说明 | "config/read 只返回当前 provider"改为"typed 字段只有当前 `model_provider`；响应 flatten `additional` 可能携带完整 `model_providers` 表（附源码链），以 Phase 0 样本为准；一期 ⛔ 不变，禁止从 `additional` 递归提取" | R2-M1 |
| 2 | §12 models 样本组 | 补一行"config/read：确认 `additional` 是否携带 `model_providers` 及其形状" | R2-M1 |
| 3 | §7 "effective provider" 行 | `config/read.modelProvider` → `config/read.model_provider`（config.rs:254 snake_case）；注明响应类型带 `ExperimentalApi` 标记，形状按样本冻结 | R2-S1 |
| 4 | §7 "list/switch provider" 行说明 | 补 `modelProvider/capabilities/read`（README:243）作为"官方 provider 级 RPC 也只面向当前 provider"的正面证据 | R2-S2 |

修订完成后，设计即满足进入 Phase 0（证据冻结与核心 Gate）的全部文档前置条件。
