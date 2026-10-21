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

RC-A / RC-B 已实施、测试（先红后绿）、部署并经 owner 真机验收（方案 §9 四项完成）。生产运行态取证：验收时段 `Claude source batch rejected` = **0**、`no projection subevents` = **0**、`source batch gap` = **0**（对照修复前 b66193d7 单会话 73 分钟 146/281，方案 §3.1）。白名单 source-only 路径在本窗口未自然触发（40 个新增 assistant 行 0 空白形态——当前 CLI/网关组合不产生空白 thinking 行；证据等级见 §5）。

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
| T7 realdata 普查（516 文件） | — | PASS：admitted 200,932 行，「可投影 ⇔ 事件数>0」**违例 0**（三分类 196,632/3,595/705；口径=扫描器 admitted 行，方案 §3.2 为内容行口径，量级一致） |

回归族（D3）：`claude_file_relay*`、`claude_content_relay*`、`claude_source*`、`projection_source_transaction*`、`TestHandleGetSessionProjection*` 全绿 + `go vet` ok。

## 4. 部署身份（两轮，均按 R3 工作单第 1 步身份门）

| 轮次 | HEAD | 构建（CST） | 内嵌 commit | runtime SHA-256（产物=/Applications） | PID/lstart |
| --- | --- | --- | --- | --- | --- |
| R3 干净重装 | `97958344e18c…` | 17:20:35–17:21:15（40s） | `97958344e18c` | `c61e7d1a8c64a9f2…f8164de` | 27003 @ 17:22:35 |
| **v2.1** | `c308ad25017b…` | 18:27:23–18:28:16（53s） | `c308ad25017b` | `007c58e2c78731a1…d2bc77` | **35789 @ 18:29:23**（当前） |

两轮构建前/安装前 porcelain 均 0、HEAD 未漂移；产物 `-version` 与 HEAD 一致、新文案在二进制中；8777 正式版监听、无残留进程、日志滚动；安装前旧日志留档 `/tmp/cordcode-r3-logs/`（0600）。iPhone16 验收安装 `6c56144e`（bundle `CordCodeSourceCommit` 核验）。

## 5. 运行态取证与对账（18:29:24–18:38 窗口，runtime `c308ad25017b`）

owner 验收（方案 §9 四项，含 RC-1 生产级证据②执行中重开）完成。被旁观回合：ce5ca3c8（30 新 assistant 行）+ d1ac0bdd（10 行）。

- **transcript 侧（ce5ca3c8 18:29 后）**：tool_use=14、tool_result=14、非空 text=13（合计 41 个可投影块，40 个 admitted 新行）。
- **投影侧（来源=Mac 生产日志）**：`Claude source batch rejected`=**0**、`no projection subevents`=**0**、`source batch gap`=**0** → **41/41 可投影块全部进事务（n/N）**；账本 `sourceCursor` 连续推进至 17,259,893 ≈ 文件 17,283,793（余 23.9KB 为 checkpoint 周期后在飞行，非丢失）；headRev 5111→5142 持续前进；K4Patch 26 次 flush、2 次 drop 均 `fence_stale_or_invalidated invalidated=false`（hydrate 边界旧 fence 残余，非内容丢失）。per-part 级投影数字未从生产提取（Management API 无投影端点）——组件级 T4 已证生产入口等价，生产侧以「零拒收+账本连续+headRev 前进」为等价证据链。
- **修复前对照**：方案 §3.1——b66193d7 单会话 73 分钟 subevents=146 / gap=281。
- **RC-1 单列**：ce5ca3c8 验收窗 7 次 hydrate（含执行中重开）全部 `settled=true`，无 `no registered relay loop` / `coverage_failed`——生产路径已证。

**证据等级（分开标注，不混写）**：

| 项 | 等级 | 依据 |
| --- | --- | --- |
| RC-A 拒收消除 | **生产路径** | 验收窗零拒收/零 gap/零 subevents + 账本连续 |
| RC-A 白名单 source-only（含限流 INFO） | **组件级**（生产未触发） | T1/T4 + 普查 3,595 存量空白行分类正确、违例 0；本窗口 40 新行 0 空白形态（当前 CLI/网关组合不产生空白 thinking 行），限流 INFO 未自然出现 |
| RC-B 对象 error 不再冻结 | **组件级** | T5（fixture 转绿）+ 普查 516 文件；生产 429 未自然出现，待自然样本佐证 |
| RC-1 运行中会话冷开 | **生产路径** | 验收②执行中重开 7 次 hydrate 全 settled |

## 6. 未验证项 / 遗留

1. 白名单限流 INFO 与 RC-B 生产 429 样本：等自然触发（已登记观测点）。
2. **Phase 2**（方案 §5.4：live-ingest 失败后的冷却语义/账本重装/relay 重绑）：待 owner 排期。
3. R3-4（`finishStarting` 信号未区分 relay 代际，无错误行为）nit 登记；O-A（`go-bridge.log` 启动清空，`RuntimeProcessController.swift:216`）另案以 main 为基修复。
4. 耗时（工作单要求）：编辑（测试+实现+fixture）约 50 分钟；编译 Release 两轮 40s+53s；定向测试 v2.1 套件 0.5s / 回归族 5.2s / 普查 14.7s / 红绿各轮秒级；安装两轮各约 1 分钟。
