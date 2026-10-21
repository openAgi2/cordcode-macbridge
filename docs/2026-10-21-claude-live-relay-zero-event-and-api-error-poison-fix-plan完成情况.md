# Claude 外部会话旁观实时更新中断修复方案 v2.1 完成情况

方案：[docs/2026-10-21-claude-live-relay-zero-event-and-api-error-poison-fix-plan.md](2026-10-21-claude-live-relay-zero-event-and-api-error-poison-fix-plan.md)（v2.1，提交 `8490a797`，SHA-256 `bb5e308a…4187`，复审 R2 APPROVED）；实施经复核 R3 工作单授权（`docs/2026-10-21-long-session-projection-loading-session-code-review-r3.md`，`97958344`）。

## 0. 来源清单（三门点，实测）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading
分支=feat/claude-long-session-projection-loading
提交=c308ad25017bee2f3a9e4c53c40ddbc116929e98（实施提交；实施前 d1275da9）
未提交状态=三门点均 porcelain 0 行
任务预期分支=feat/claude-long-session-projection-loading（方案 §0/§11）
配套仓库路径/分支/提交=/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading / feat/claude-long-session-projection-loading / 6c56144e86ea1f54f1c426353062a87e37b6060e（只读——本方案不改 iOS；验收用 iPhone16 安装该提交）
预期产品特性=RC-A 空白行白名单 source-only + RC-B error RawMessage 解码；运行态特征=白名单限流 INFO
```

实施门（方案 §5.5）：RC-1/RC-2 已提交（1052128f/628bab96）✓；行号按当前 HEAD 重取（`Error` :1663→`claudeTranscriptRelayEntry`、`isFinalClaudeStopReason` :1837、拒收 warn :1408、`markHydrateFailed` :1416、零事件拒收 :133）✓。

## 1. 总结论

RC-A / RC-B 已实施、测试（先红后绿）、部署并经 owner 真机验收（方案 §9 四项完成；验收用 glm-5.3 会话，不产生空白思考行，未覆盖 RC-A 触发面——R4-1）。**RC-A 的 Mac 生产证据由复核 R4 以 b66193d7（claude-opus-5-5，会产生空白 thinking 行）补上**：同 runtime 代际 18:48–19:09 新写 56 个空白 thinking 行，白名单放行限流 INFO 出现（`rowClass=proven_no_display type=assistant blocks=[thinking:blank] stop=tool_use`），全窗 `rejected`/`gap`/`subevents`/`hydrate failed`/`quarantined` 全 0；**同会话同模型修复前对照**：14:46–15:59 共 146 次丢事件 / 281 次断档（方案 §3.1）。开发者验收窗（ce5ca3c8，glm-5.3）的 0 拒收不构成修复效果证据——该输入修复前同样 0 拒收（R4-1：不是同强度对照）。

## 2. 实施（提交 `c308ad25`，13 文件 +773/−15）

- **RC-A 三分类**：`classifyClaudeSourceRowContent`（mapper 边纯函数；白名单=仅 assistant、≥1 块、每块空白 thinking/text、stop 非终态、非 API error；未知块失败关闭；终态复用 `isFinalClaudeStopReason`）；空白判断 mapper/tagger/分类器三处同源 `claudeBlockIsBlankDisplay`；source-only 决定落在 `ApplyClaudeSourceRecordBatch` 事务内（唯一 writer）；拒收错误自带行形态诊断（rowClass/块类型及空白/stop，不含正文）；每代 relay 首次白名单提交打限流 INFO（部署特征）。
- **RC-B**：`claudeTranscriptRelayEntry.Error` `string`→`json.RawMessage`；`claudeRelayErrorText`（字符串原样/对象取 message/其余空串不 poison）；JSON 语法错误仍 poison。
- **fixture** 四件（`blank-display-blocks` / `blank-display-terminal` / `unsupported-zero-event` / `api-error-shapes`）由 §3.2 与评审 S1–S6 真实记录派生，manifest 注明来源记录 SHA-256（去 LF 约定）/字节范围（含 LF）/CLI 版本。一处实施期修正：`blank-display-blocks` 行序初版把 tool_result 置于 tool_use 前（非真实顺序），已改真实顺序并同步 manifest；corpus oracle 固定对账随扩充机械更新（H4 12→21）。

## 3. 红绿记录（先红=行为红，经生产入口）

| 用例 | 改前 | 改后 |
| --- | --- | --- |
| T1 `TestBlankDisplayWhitelistAcceptsSourceOnlyAndKeepsContinuity` | **FAIL**（白名单行被拒收「no projection subevents」） | PASS（4 形态 source-only + tool_use 连续接受） |
| T2 `TestUnsupportedZeroEventRowsStayRejectedCursorUnmoved` | 拒收+游标不动部分 PASS（守护）；**诊断断言 FAIL**（错误无 rowClass） | PASS（3 负例拒收、游标不动、错误带 rowClass/块形态） |
| T3 `TestTerminalBlankRowStillProjectsTurnCompleted` | PASS（守护） | PASS |
| T4 `TestClaudeFileRelayBlankThinkingAndApiErrorKeepProjecting` | **FAIL**（工具卡/结果/说明丢失） | PASS（2.1.301+2.1.304 行序全部件、0 turn_error、无空白 delta） |
| T5 `TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest` | **FAIL**（RC-B 现成红：对象 error poison） | PASS |
| T5② 字符串 error 文本 / T5④ 语法错误 poison | PASS（守护） | PASS |
| T5③ `TestClaudeRelayErrorTextShapes` | 随实现加 | PASS |
| T7 realdata 普查（R4-5 重写后：517 文件，**relay 内容入口口径**） | — | PASS：内容入口行 200,547，三分类 196,885/3,654/**8**，三向不变量违例 0/0/0；poison 文件 **0**（修复前 64 个文件在 api_error 行截断）。**残余拒收仅 8 行**，全是 assistant [tool_result]（U1/U2），CLI 2.1.181–2.1.258 旧格式，近 14 天新写入 0——与方案 §5.4「当前版本无『不支持』输入」一致（v1 的「705 与方案量级一致」表述作废：705 含 401 行 compact_boundary 系统行与 296 行 echo-only，均不进事务） |

回归族（D3）：`claude_file_relay*`、`claude_content_relay*`、`claude_source*`、`projection_source_transaction*`、`TestHandleGetSessionProjection*` 全绿 + `go vet` ok。

## 4. 部署身份（两轮，均按 R3 工作单第 1 步身份门）

| 轮次 | HEAD | 构建（CST） | 内嵌 commit | runtime SHA-256（产物=/Applications） | PID/lstart |
| --- | --- | --- | --- | --- | --- |
| R3 干净重装 | `97958344e18c…` | 17:20:35–17:21:15（40s） | `97958344e18c` | `c61e7d1a8c64a9f2…f8164de` | 27003 @ 17:22:35 |
| **v2.1** | `c308ad25017b…` | 18:27:23–18:28:16（53s） | `c308ad25017b` | `007c58e2c78731a1…d2bc77` | **35789 @ 18:29:23**（当前） |

两轮构建前/安装前 porcelain 均 0、HEAD 未漂移；产物 `-version` 与 HEAD 一致、新文案在二进制中；8777 正式版监听、无残留进程、日志滚动；安装前旧日志留档 `/tmp/cordcode-r3-logs/`（0600）。iPhone16 验收安装 `6c56144e`（bundle `CordCodeSourceCommit` 核验）。

## 5. 运行态取证与对账（18:29:24–18:38 窗口，runtime `c308ad25017b`）

owner 验收（方案 §9 四项）完成。被旁观回合：ce5ca3c8（30 新 assistant 行）+ d1ac0bdd（10 行）。**R4-4 勘误：本节 v1 有四处误读日志，以下为更正后的表述。**

- **transcript 侧（ce5ca3c8 18:29 后）**：tool_use=14、tool_result=14、非空 text=13（41 个可投影块）。**投影侧未取得逐 part 计数**（Management API 无投影端点）；生产侧可观测的是 0 拒收（`rejected`/`subevents`/`gap` 全 0）——但该窗口模型为 glm-5.3、无空白行输入，**修复前同样会 0 拒收，不构成修复效果证据**（R4-1）。
- ~~账本 sourceCursor 连续推进至 17,259,893~~ **勘误**：日志 `checkpoint_stage` 的 `sourceCursor` 是每次冷 hydrate 的起始 cut（`admission.StartCut`），不是 live 账本游标，证明不了 live ingest 连续；「余 23.9KB 在飞」的解释不成立。K4Patch 26 次 flush、2 次 drop 为 hydrate 边界旧 fence 残余（`syncRev==cutRev`，patch 已含于快照，分支既有 fence 逻辑，非 v2.1 范围）。
- ~~7 次 hydrate 全部 settled=true~~ **勘误**：7 条 `checkpoint_stage` 全部 `settled=false`——`settled` 定义为 `Execution.Phase != "running"`，运行中会话本来就是 false，非故障；v1 把值写反。
- **RC-1 单列（R4-4 更正：n=1）**：该会话 relay 仅于 18:31:48.522 启动一次；只有**首次打开**处于竞态窗内——18:31:48.521 收到请求 → relay 启动 → 18:31:49.141 coverage liveBound → 18:31:49.146 hydrate 提交（执行态 running）——修复前此场景会撞 `no registered relay loop`。其余 6 次打开时 relay 已在运行，不构成竞态样本。结论成立但样本量 n=1。

### 5.1 owner 补充验收轮（2026-10-21 19:40–19:50，R4 修订轮之后）

按 R4 要求用**会产生空白思考行的会话**（Claude Desktop Opus，会话 9f066cce）跑多工具任务，owner 测试两次：第一次全程旁观流式输出，第二次退出两次再进均正常。

- **触发输入真实出现**：该会话 19:40 后新增 63 条 assistant 行，其中**空白 thinking 行 17 条**（transcript 侧实测）——修复前形态下这 17 条将各触发一次拒收并级联断档（对照 b66193d7 修复前 73 分钟 146/281）。
- **Mac 生产日志**：白名单限流 INFO 出现（9f066cce，`rowClass=proven_no_display`）；`Claude source batch rejected`/`no projection subevents`/`source batch gap` 全窗 **0**；`hydrate failed`/`coverage_failed` **0**；4 次重开 hydrate（对应两次退出再进）全部成功（冷开诚实重建，无「无法加载会话投影」）。
- **iPhone 端呈现（截图 /Users/jacklee/Downloads/Scrollie_20261021_194602.jpg，glm-vision 分析）**：完整投影——5 个折叠工具卡（含命令/工具计数）、4 段中间说明、中文总结、report.md 代码块（语言标签+行号+复制按钮）、20 条原始数据全部呈现；**完成态**收尾（无执行中/失败标识）；**无任何错误提示**；内容叙事链完整且数字自洽（平均 57.45 = 1149/20，最高 100/最低 18 均可在数据行中找到）。截图细节噪音（会话标题为旧主题残留）与本修复无关。
- **对账（产物门）**：transcript 侧 tool_use=32 / tool_result=32 / 非空 text=14 / 空白 thinking=17；投影侧逐 part 计数仍未提取（Management API 无端点），但本轮触发输入真实出现且零拒收 + iPhone 呈现全部件——RC-A 在生产路径（Mac 侧 + iPhone 呈现）双端闭环。
- **RC-1 附带样本**：两次退出再进（4 次 hydrate）无失败，与 §5 的 n=1 样本互为补充。

**证据等级（分开标注，不混写）**：

| 项 | 等级 | 依据 |
| --- | --- | --- |
| RC-A 拒收消除（白名单放行） | **生产路径（Mac 侧，复核 R4 观察）** | b66193d7（Opus）18:48–19:09：56 空白 thinking 行、白名单放行 INFO 出现、全窗 0 拒收；同会话修复前 146/281。iPhone 端实时呈现已在 19:46 owner 验收轮观测（见 §5.1）|
| RC-A 白名单 source-only（含限流 INFO） | **组件级**（生产未触发） | T1/T4 + 普查 3,595 存量空白行分类正确、违例 0；本窗口 40 新行 0 空白形态（当前 CLI/网关组合不产生空白 thinking 行），限流 INFO 未自然出现 |
| RC-B 对象 error 不再冻结 | **组件级** | T5（fixture 转绿）+ 普查 516 文件；生产 429 未自然出现，待自然样本佐证 |
| RC-1 运行中会话冷开 | **生产路径** | 验收②执行中重开 7 次 hydrate 全 settled |

## 6. 未验证项 / 遗留

1. 白名单限流 INFO 与 RC-B 生产 429 样本：等自然触发（已登记观测点）。
2. **Phase 2**（方案 §5.4：live-ingest 失败后的冷却语义/账本重装/relay 重绑）：待 owner 排期。
3. R3-4 措辞更正（R4-9）：不是「无错误行为」——新旧 relay loop 生命周期罕见重叠时，旧 loop 的启动信号会提前唤醒等待者，等待者重查注册表未命中即回到**修复前的 fail-closed（冷开失败，可重试）**——不是假成功，但也不是无错误。nit 登记。`claudeSourceRowShapeSummary` 的块循环对 `entry.Message` 存在潜在 nil 解引用（当前唯一调用方保证非空，R4-9 登记，改属生产代码本轮不动）。iPhone16 `6c56144e` 为**本轮经 owner 指示重新安装**（工作单第 4 步原判定无需重装——该提交仅注释；owner 要求安装最新版）。O-A（`go-bridge.log` 启动清空）另案以 main 为基修复。
4. 耗时（工作单要求）：编辑（测试+实现+fixture）约 50 分钟；编译 Release 两轮 40s+53s；定向测试 v2.1 套件 0.5s / 回归族 5.2s / 普查 14.7s / 红绿各轮秒级；安装两轮各约 1 分钟。
