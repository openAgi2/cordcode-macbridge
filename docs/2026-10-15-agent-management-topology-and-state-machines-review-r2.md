# 方案评审报告 r2：设备—服务—Agent 拓扑与接入状态管理（v3 复审）

日期：2026-10-15（Asia/Taipei）。评审员：plan-reviewer（延续模式，持有 r1 上下文；按复审纪律逐条核验闭合，r1 结论不作免检依据）。评审契约：plan-contract-v1.1。

## 结论

- **verdict: APPROVED（通过）**
- 方案：`docs/2026-10-15-agent-management-topology-and-state-machines.md`，v3（含 r1 处置表）
  评审时内容 SHA-256：`e49e2d5d00e6216bd70d0127c02949351be96474aef1c8b6cd0b14f91d74121a`（与派发记录一致，本轮 `shasum -a 256` 复算）
- 本轮范围：full（简报字段 7），首要是 r1 意见 F1–F4 的闭合核验 + 修订回归检查
- blockers: **0**；advisories: **2**（r2 新增 F5/F6，均为建议级）
- 一句话：r1 四条意见全部按声明闭合（F1 补采四样本 SHA/内容/证据更正纪律逐一核验通过；F2/F3 行号修正与 r1 亲核一致；F4 锚点与 S0 登记项落地），修订范围与声明一致无暗中改动，I 仓 HEAD 漂移经文件级验证对本方案锚点零影响；方案在声明的 5 产品、显式接入、已取证版本源与合法空连接范围内设计自洽、验收完备，予以通过。
- implementation_readiness：通过 ≠ 实施授权。S0 开工前必须按 E1 fresh 重新生成来源清单（**特别注意：I 仓 HEAD 已漂移至 `540e1f91…`，见下文来源漂移记录**）；E3/E5/E6 维持为实施期/条件门。

## 来源与覆盖（r2 重核）

| 别名 | 简报/方案冻结身份 | 本轮实测 | 一致性 |
| --- | --- | --- | --- |
| M | `feat/ios-native-message-timeline` @ `a97fee828…`，干净 | 同分支同 HEAD；9 项未提交全为本轮合法产物（v3 方案+审计+索引修改、5 个补采样本、r1 报告 untracked） | 一致（方案修订属修订轮授权内） |
| I | 同分支 @ `e479378f…`，274 项 dirty 全在 docs/.exec-plan | 同分支 @ **`540e1f91…`（漂移，+4 提交）**，275 项 dirty 仍全在 docs/.exec-plan | 分支/工作树/dirty 分类一致；HEAD 漂移，影响已验证为零（见下） |
| G | `main` @ `a36319d6…`，7 个 .DS_Store | 同 | 一致 |
| U-Codex / U-Grok / U-DSH / U-OpenCode | cc7ba336… / 2bdd1d6a… / d7432673… / 0d61729b… | 全部同上、干净 | 一致 |

### I 仓 HEAD 漂移记录与影响分析（P0 来源门处置）

- 事实：`git log e479378f..540e1f91` = 4 个提交，全部属另一任务（UU-G0 文字渐隐测试：`WordFadeIncrementalTests.swift` + 2 个 docs）；方案 v3 §2.1 明确标注为"v1 完整来源清单（历史冻结，未回写）"，即方案未声称 I 的当前 HEAD，其冻结身份声明真实。
- 影响验证（本轮执行）：对方案 §2.3 引用的全部 6 个 I 文件逐一 `git diff --quiet e479378f..540e1f91 -- <file>`——**全部 UNCHANGED**；`git status --porcelain -- <同6文件>` = 0 项（当前工作树亦无重叠 dirty）。即 §2.3 的 I 锚点在 I 仓当前 HEAD 540e1f91 下逐字有效。
- 裁定：漂移是共享工作树的正常并行开发，非"读错工作树"来源事故；方案自身的 E1/S0 已强制实施前 fresh 重核。结论对当前快照成立，**S0 实施时必须重新记录 I 的当时身份**（届时若再前进，重复同样的文件级影响验证）。
- 附带过时表述（不构成缺陷）：v3 §10/§12 仍写"I 既有 274 项 dirty"，当前实际 275 项（外部任务新增一项 docs/.exec-plan 文件）；方案自身未触碰任何一项。

### r2 实际执行的核验

1. 方案哈希复算（e49e2d5d… 与派发一致）；M/G/U/I 身份重核（上表）。
2. F1 闭合：5 个新归档文件（含 stdout 空文件）逐一 `shasum -a 256` 与索引 9 条对账（全部一致）；逐文件读内容与 v3 §2.2/§2.3/§7.1 声明逐字比对（见闭合表）；索引 chatgpt 条目 `note` 字段与 §2.2 的证据更正记录核对。
3. F2/F3/F4 闭合：v3 文本核对 + r1 亲核结果复用（两侧文本与依赖未变：M 仓 a97fee828 与 r1 评审时相同，f7dee495..a97fee828 零产品代码改动的结论直接适用）。
4. 修订回归：`git diff a97fee828 -- <方案>` 与 `-- <审计文档>` 全量读取，改动逐块归类；R/OD/E/S ID 与范围、§5.1 OD2 语义、§11 验收矩阵确认未动。
5. 审计文档 v3 批注核对（证据更正纪律）。

## r1 意见闭合核验表

| r1 意见 | 声明处置 | 本轮核验证据 | 闭合判定 |
| --- | --- | --- | --- |
| **F1（阻塞）** 四项命令语义观察无归档 | 补采四项 raw 并入索引（9 条） | `claude-install-help.txt`：SHA `4dc358a4…` 与索引一致，原文 `Usage: claude install [options] [target]`＋"stable, latest, or specific version"——精确支持 §2.3 声明；`opencode-upgrade-help.stderr.txt`：SHA `0f0348cf…` 一致，原文 `opencode upgrade [target]`、`-m, --method [choices: "curl","npm","pnpm","bun","brew","choco","scoop"]`、`--major`——精确支持（v3 新增的 --method 枚举值有据）；`opencode-upgrade-help.stdout.txt` 0 字节（SHA=空文件哈希）——支持"help 全走 stderr"附带事实；`grok-update-help.txt`：SHA `7bb1d51d…` 一致，原文 `--check  Check for updates without installing`——精确支持；`chatgpt-infoplist-observed.txt`：SHA `c9fe28cf…` 一致，内容 `com.openai.codex / ChatGPT / 26.1007.71326 / 20398`——精确支持 §2.2 修订值。**证据更正纪律**：v1 数值 26.1007.30912/20156 未被无声删除——索引 `note` 字段显式记录其未归档不可复核、被官方自动更新替代、影响范围（§2.2 已修订）与"生产不得硬编码时点版本"约束；§2.2 正文同步披露。审计文档 v3 批注显式声明"v1 轮此句曾无归档，是 r1 评审 F1 的一项"，未把 v1 审计改写为当时已核验 | **闭合** |
| **F2（建议）** `:463` 行号漂移 | 改 `:412,431,454,677,718` | v3 §2.3 安全重启行已改（diff 确认）；`:454`（applyConfigAndRestart）与 `:718`（quiesce/commit 主体）为 r1 本轮亲核值，M 仓身份未变，复用成立 | **闭合** |
| **F3（建议）** `:381` 未写文件名 | 补 `relay_bridge_client.go:381` | v3 §2.3 已补全文件名＋符号名（`pruneDeadDevice`）；与 r1 亲核（relay_bridge_client.go:380-385）一致 | **闭合** |
| **F4（建议）** 命令 catalog 来源未冻结 | S0 加登记项；Grok 锚 README.md:46 | v3 §10 S0 新增"安装／更新命令 catalog 逐条登记官方来源锚点"（Grok=README.md:46、Claude=help 归档、OpenCode/dsh=npm 样本＋upgrade help、Codex Desktop=官方链接无命令行）＋失败处理"命令无官方锚点的入口只给官方文档链接"；§7.3 Grok 命令带 `README.md:46`＋commit 锚点；U-Grok README.md:46 内容 r1 已亲核（`curl -fsSL https://x.ai/cli/install.sh \| bash`） | **闭合** |

## 修订回归检查

- `git diff a97fee828 -- 方案` 全量读取：改动块＝版本行/处置表（新增）、§2.2、§2.3 两行、§2.3 Claude 段、§7.1 头部＋两行、§7.3、§9.2 E4、§10 S0、§12、末尾作者自检块——**全部落在"F1–F4 与必要一致性维护"声明范围内，无章节增删、无其他章节暗中改动**。
- R/OD/E/S 的 ID 与产品范围未变（v3 声明属实）；OD2 迁移语义（§5.1）与 §11 验收矩阵未动——r1 已显式审查认可的语义不受影响。
- 修订新增的三处事实声明（opencode `--method` 枚举、ChatGPT 71326/20398、"help 走 stderr"）全部有归档逐字支持（上表）。
- 作者自检块自披露 SHA 自引用漂移并以实际 `shasum -a 256` 为准——本轮复算 e49e2d5d 与派发一致，处理诚实。

## 新意见（本轮新发现，均为建议级）

### F5［建议］§2.2"四个 CLI 版本在 v3 补采时逐一复测"——dsh 复测无归档证据

- **位置**：§2.2（"（四个 CLI 版本在 v3 补采时逐一复测，均未漂移）"）。
- **证据**：`version-sample-index.json` 中 v3 时段（2026-10-15T02:36–02:38Z）条目仅三个携带 `runtimeVersion`（claude `2.1.296`、opencode `1.19.0`、grok `1.0.52`），grep 确认无任何 dsh 的 v3 条目；dsh 版本仅有 v1 探针＋v2 package.json 双证据。
- **影响**：版本事实本身可信（且 §4.3/§7.1 要求运行时真实探针、§2.2 禁止硬编码时点版本），影响限于执行声明的可复核性——与 r1 F1 同类的轻度过度声明。
- **修订方向**：补一条 `dsh --version` 复测归档，或把括号收窄为"claude/opencode/grok 随 v3 help 采集复测（见索引 runtimeVersion），dsh 沿用 v1 探针＋v2 package.json"。
- **闭合标准**：声明与索引可对账。

### F6［建议］作者自检交接块 `open_gates: []` 与 §9.2 门状态不一致

- **位置**：方案末尾代码块（`open_gates: []`）。
- **证据**：§9.2 明确 E5 为 pending 条件门、E3/E6 为实施后完成门；本评审两轮的 open_gates 均为 [E5, E3, E6]。
- **影响**：该块模仿评审交接块格式，若被后续会话/脚本当真读取，会误判"无未满足门"、跳过 S0 fresh 重核义务（而 I 仓 HEAD 已实际漂移，见来源记录）。
- **修订方向**：改为如实列出（E5/E3/E6）或删除该字段。
- **闭合标准**：自检块与 §9.2 一致。

## 剩余门（与阻塞意见分列；方案通过 ≠ 实施就绪）

- **E5**（pending 条件门）：未支持发行源的新 latest provider、自动更新、Grok 不同输出 generation——不卡本版。
- **E3**（实施完成门）：snapshot/rescan codec、空 ACK JSON producer/consumer 测试——S3/S4 完成前置。
- **E6**（实施完成门）：disabled 零后台、空集端到端、撤销、direct+Relay 观测的定向单测＋部署后真实证据＋owner 矩阵。
- **S0 fresh 来源门**：实施开始时重新记录 M/I（当前 `540e1f91…` 或更新）/G/U 身份；I 再漂移时重复本轮的文件级影响验证。
- F5/F6 为建议，不阻塞通过、不构成下轮必改项；可在实施期顺手处置。

## 评审员交接块

```
verdict: APPROVED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-15-agent-management-topology-and-state-machines.md
plan_sha256: e49e2d5d00e6216bd70d0127c02949351be96474aef1c8b6cd0b14f91d74121a
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-15-agent-management-topology-and-state-machines-review-r2.md
scope: full
blockers: 0
advisories: 2
open_gates: [E5, E3, E6]
round_complete: true
contract_version: plan-contract-v1.1
```
