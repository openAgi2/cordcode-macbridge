# codex-web Backend 设计三轮评审报告（r3，收口轮）

- 日期：2026-08-21
- 评审对象：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md) **v1.3**（含 §18/§19 两轮采纳记录）
- 一轮评审：[2026-08-21-codex-web-backend-design-review.md](2026-08-21-codex-web-backend-design-review.md)
- 二轮评审：[2026-08-21-codex-web-backend-design-review-r2.md](2026-08-21-codex-web-backend-design-review-r2.md)
- 评审方式：全文重读 v1.3；逐项核验二轮 R2-M1/R2-S1/R2-S2 落实；对 R2-M1 部分采纳的理由按官方源码独立复核（包括对二轮评审报告自身论断的复核）；检查 §18 原文是否同步更新、有无新引入矛盾。
- 补充（同日）：收口后追加与 dsh-web 设计文档的质量对比分析（§6），并在其中补读了前置分析文档 `2026-08-21-codex-web-backend-feasibility-analysis.md` 全文核对。

---

## 1. 最终结论

**APPROVE**

二轮三项问题（1 必改 + 2 建议）全部正确落实；R2-M1 的部分采纳理由经源码独立复核**成立，且校准优于一轮/二轮评审报告中的原有措辞**（见 §3 专项复核——二轮报告对 `additional.model_providers` 组成的判断本身有误，v1.3 的谨慎处理恰好规避了它）。v1.3 未引入新的内部矛盾、事实错误或分级不一致。

设计的文档前置条件已收口：**可进入 Phase 0（证据冻结与共享运行时 Gate）**。本评审对"满足文档前置"的认定不构成对共享 event stream、`config.additional` 物理形状、Desktop/VS Code 覆盖面或任何 experimental 能力已实测通过的背书——这些仍由 Phase 0 真实样本裁决。

## 2. 二轮修订落实核验

| 二轮问题 | v1.3 落点 | 核验结果 |
|---|---|---|
| R2-M1 config/read 事实修正 | §7 ⛔ 行（typed `model_provider` + flatten `additional` "可能携带未建模的 `model_providers`，精确存在性/组成/形状以 Phase 0 样本为准，一期禁止递归提取"）；§12 models/config 样本组（采集 config/read、确认 additional 组成、敏感值脱敏、"只验证事实不授权递归提取"）；§18 M2 行**原文同步更新**（消除新旧记录矛盾） | ✅ 三项要求（不写"只返回当前 provider"、Phase 0 采样点、⛔ 政策不变）全部满足 |
| R2-S1 字段名 + experimental 门控 | §7 effective provider 行改为 `config/read → config.model_provider` 并标注"v2 `Config` 是 snake_case 特例"；effective provider/config 两行改 🧪 只读；§11.2 新增专条："请求参数无 experimental 字段 ≠ 响应字段稳定……不能由请求可发送推导响应字段稳定" | ✅ 与 config.rs:254（snake_case）、config.rs:361-369（ConfigReadParams 仅 include_layers/cwd）、config.rs:252/371（ExperimentalApi 标记）逐一吻合 |
| R2-S2 provider capabilities 正面证据 | §7 ⛔ 行补 `modelProvider/capabilities/read` "只读当前 configured provider" | ✅ 与 README:243 一致；"官方唯一 provider 级 RPC"的表述经 method 注册表核对成立 |

另核验：effective provider/config 改 🧪 后无下游失配——§6.2 能力就绪序列（transport→initialize→initialized→thread/list→model/list→contract）不含 config/read；§13.3 行 9 与 §15 门槛 11 的"effective provider 可读"以 Phase 0 样本冻结为前提，与 🧪 定义一致；§19 处置描述准确。

## 3. 对 R2-M1 部分采纳理由的专项复核（含对二轮评审报告自身的更正）

v1.3 §19 拒绝把 `additional.model_providers` 写成"必然完整包含内置+自定义 provider 的稳定目录"，理由是 typed schema 无承诺、且源码注释存在语义差异。逐条复核：

| v1.3 的理由 | 官方源码证据 | 结论 |
|---|---|---|
| "当前 `ConfigToml.model_providers` 注释指向用户自定义项" | `config/src/config_toml.rs:284-285`："User-defined provider entries that extend the built-in list. Built-in IDs cannot be overridden." | ✅ 属实 |
| "runtime `Config` 才明确称 combined map" | `core/src/config/mod.rs:818-819`："Combined provider map (defaults plus user-defined providers)" | ✅ 属实 |
| "server 转换后的精确组成必须由样本确认" | `config/src/state.rs:455-462`：`effective_config()` 返回**逐层 merge 的原始 TomlValue**；`app-server/src/config_manager_service.rs:132-141` 由该 toml 视图反序列化 `ConfigToml`，再整体序列化为 JSON 进入 v2 `Config`——该路径**不经过** runtime `Config`（combined map） | ✅ 属实 |

**更正二轮评审报告**：r2 报告称 `additional.model_providers` "大概率原样携带完整 provider map（内置+自定义全量）"——组成判断有误。config/read 的实际路径是 layer-merge toml → ConfigToml，其 `model_providers` 按注释只承载各配置层中的用户自定义条目，内置 provider 目录并不在这条路径上。因此即使样本确认该键存在，其内容也与"完整 provider 目录"不同。v1.3 的"精确存在性/组成/形状以 Phase 0 样本为准"是唯一正确的措辞，部分采纳处置**优于**二轮原建议。

## 4. 剩余问题

就**设计文档本体**而言：无阻断、无必改、无建议。v1.3 全部事实断言（含本轮流经的 §3.3、§7 全表、§8.2、§9.1/9.2、§11.2、§12、§18/§19）均与官方源码或当前仓库代码吻合。

收口后补充的 §6 对比分析在**文档集层面**发现一项应修事项（§6.1，前置分析文档未降级纠错）与三项低成本增强（§6.2–§6.4）。它们针对 companion 文档与设计配套物，不针对设计本体，因此不改变本报告对设计本体的 APPROVE 结论，但建议在进入 Phase 0 前一并处理。

## 5. 收口状态与移交

- 三轮累计：1 阻断（B1）+ 4 必改（M1–M3、R2-M1）+ 7 建议（S1–S5、R2-S1/S2）全部处置完毕；两处部分采纳（一轮 M2、二轮 R2-M1）均以官方源码为据且经独立复核成立。
- 设计满足进入 Phase 0 的全部文档前置条件。Phase 0 出口判定仍以本文 §8.2/§12/Phase 0 为准；若样本与文档断言冲突（如 `config.additional` 形状、共享 event stream 覆盖面），按 §3.0 证据优先级以样本为准并回写设计。
- 下一步是执行性的，不再需要设计评审轮次：生成目标二进制 schema → 建 call-site 索引 → 采集 §12 样本包 → 受控 Gate 实验 → 输出 PASS/PARTIAL/FAIL。

## 6. 与 dsh-web 设计文档的质量对比（收口后补充）

对比对象：[2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md)（四轮评审后的终版）。
总体判断：**codex-web v1.3 的设计本体质量高于 dsh-web 终版**，但 dsh-web 有四个用真机故障换来的质量构件未被继承，其中 §6.1 应在进入 Phase 0 前修复。

### 6.1 前置分析文档未降级纠错（应修）

dsh-web 的所有 companion 文档都有明确角色限定（"不能授权什么"逐项写明；opencode-web 收敛计划 §0 更是专设 companion 角色表），而 codex-web 的前置分析 `2026-08-21-codex-web-backend-feasibility-analysis.md` 被设计头部无标注地链接为"前置分析"，但其内容与设计文档存在多处直接矛盾或错误：

| 前置分析原文 | 事实/设计裁决 | 证据 |
|---|---|---|
| §3 对比矩阵把现状写成 `codex exec --json` 子进程、"每次 turn 启动 stdio 子进程"、"SIGKILL 强杀"、"直接遍历磁盘" | 产品默认是 `app_server` 模式（每 session 一个 stdio app-server）；设计 §2.1 专门纠正过这个叙事（"不能写成'终于从 codex exec 升级到官方 API'"），但纠错只发生在设计里，前置分析原文未动 | 本仓 CLAUDE.md「Backend runtime model」、GO_BRIDGE_ARCHITECTURE.md「Codex」节 |
| §2.2 `item/reasoning/delta`（或 `item/reasoning`） | 实际通知是 `item/reasoning/summaryTextDelta` / `item/reasoning/textDelta` / `item/reasoning/summaryPartAdded` | protocol/common.rs 注册表；README:1674-1676 |
| §2.2 `tool/requestUserInput` | 实际 method 是 `item/tool/requestUserInput` | common.rs 注册表；README:278 |
| §5.3 "MacBridge 通过 thread/list 和事件订阅可实时感知"外部 turn | 同一命题在设计中是核心未证 Gate（§8.2）；前置分析把它当作既得收益陈述 | 设计 §0/§8.2 |
| §6.3 "以 `agent/codex-web`（或重构 `agent/codex`）形式推进，对外保持 wire kind 统一" | "或重构 agent/codex" 与 "wire kind 统一" 均与设计 §0/§5.1 的独立身份裁决相反 | 设计 §0、§2.2、§5.1 |
| §4.2 探测端口 `ws://127.0.0.1:3845 或 4141` | 臆测值；设计已改为官方 daemon control socket 优先（§6.1 选择顺序） | 设计 §6.1；daemon 绑定默认 socket（app-server-daemon/src/lib.rs:263-266） |

风险：实施者顺着"前置分析"链接读进去，会吸收已被否定的基线、错误的方法名和与设计相反的路线选项。dsh-web 三轮评审教训（R3-1/R3-2：修订动作里未查源码即臆造机制）正是文档集内矛盾表述被后续实施重新拾起的实例。
修订建议：给前置分析文档头部加"历史输入：基线、事件名与路线选项已被 [设计文档] 修正，以设计为准"的状态标注；或在设计头部链接处注明。低成本，一次性。

### 6.2 缺"历史故障 → 新路线结构性消除"映射表（增强）

dsh-web §2.2 的坑表（9 个真实故障，逐条判定"新路线是否结构性消除"）是其最有价值的构件之一。codex-web 只在 §2.4 点名四类旧故障（rollout identity、EOF 假完成、delta/completed 重复、provider 非流式），消除逻辑散落在 §7.1 红线与 §8.1 中，没有一张"故障 → 根因 → 消除机制 → 回归测试落点"的表。§13.1 的"回归"项只保证旧 backend 行为不变，没有"新 backend 不重蹈旧故障"的显式测试项。补一张四行的表即可把历史故障回归变成可验收项。

### 6.3 iOS 侧改动量未审计（增强）

dsh-web 的 S10（评审逼出）产出了 11 个穷举 switch 文件清单与 2 个行为决策点；codex-web 只在 Phase 5 写了一句"穷举 switch 与 cache scope"。dsh-web 的经验是缺这份审计会系统性低估 iOS 工作量。不需要现在做完，但设计应承诺 Phase 5 前产出文件级审计，或直接补清单。

### 6.4 已核实的协议形状未沉淀进设计（增强）

dsh-web 把字段级方法形状写进了设计本体（§3.5）。codex-web 三轮评审已核实大量 shape 事实（`Model` 无 provider 字段、`TurnStartParams.model` 语义、`Config` snake_case、requestUserInput 批结构、elicitation 三 variant、steer 必填 `expectedTurnId` 等），但它们只存在于三份评审报告里；实施者需跨四份文档拼装。建议增加一份"shape 附录：源码 pin 推导、以 Phase 0 样本为准"——自我声明为待确认推导，不违反样本冻结纪律。

### 6.5 codex-web 反超 dsh-web 的地方（对比的另一面）

1. 证据纪律更系统：§3.0 六级证据优先级 + 完整证明元组（dsh-web 是 5 条过程纪律）；
2. 核心不确定性建成受控 Gate：§8.2 前提条件矩阵 + PASS/PARTIAL/FAIL + 退役联动（dsh-web 无等价物——其外部 turn 广播在设计期已核实成立）；
3. §9.1 十一处接线清单从设计就带（dsh-web 靠四轮评审 M4 才补）；
4. 五级稳定性分级（✅/🧪/⚠️/♻️/⛔）+ experimental 剥除行为写明（transport.rs:174-192）；
5. 退役门槛 12 条 + provider/memory 能力对照（dsh-web 无退役门槛章节）；
6. provenance 禁令更严：空目录 + `transcriptindex` + 禁止旧 fixture 反向定义 API 形状；
7. §3.4 bug 修复纪律更可执行（七步 + 第一处分歧 + 一次无效即停）；
8. §13.2 帧级性能指标超出 dsh-web；
9. §9.2 尾封口以官方 thread/turn status 为权威，优于 dsh-web 的 `SessionActivityProbing` 运行徽标探测方案。

### 6.6 有意取舍、不算欠缺的差异

- **设计期零活体核实**：dsh-web 设计前完成了含活体实验的四项前置核实（426/101 WS upgrade、provider 目录活体等）；codex-web 把全部活体验证推迟到 Phase 0。这是更严的路线选择（pin commit + 二进制版本 + Gate 硬门槛），代价是设计期事实密度低于 dsh-web 同期、Phase 0 修正面可能更大，方向正确。
- **无 owner 兜底许可**：dsh-web §7 允许"官方 API 实现不了时复制旧件只读成果"；codex-web 刻意移除此阀门。属收紧而非欠缺。

### 6.7 小结

处理 §6.1（必做）并视情采纳 §6.2–§6.4 后，codex-web 的**文档集**质量将全面超过 dsh-web；目前是设计本体超过、配套物有欠。
