# 终审定向复核报告 #2：OpenCode v2 独立接入与共享服务设计方案（v1.10）

- 评审轮次：**终审定向复核 #2**（按复核 #1 报告 §7 的闭合路径进行：只核对修订单元格，不重做全量评审）
- 评审日期：2026-10-21（来源门 21:26，Asia/Shanghai）
- verdict：**APPROVED（通过）**
- blockers：**0**；advisories：**0**（本轮没有新增意见）
- 评审对象：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan.md` v1.10，
  SHA-256 `319905c6df2b05b1fd0c56a4b1ea9f2fd0a99e04f29e719484dc4dc493f3e372`，1399 行，**未提交**。
  修订前 v1.9 的 SHA-256 为 `4492c9ce…d471`。
- 对比基准：v1.9 快照取自 `~/.claude/file-history/db23b46f-9203-4969-ae11-2e561f33bb22/583a522a9ad71e4e@v7`
  （本地 Claude Code 文件历史，SHA-256 与复核 #1 的评审对象一致）。v1.9→v1.10 共 83 行增删、10 个改动块。
- 上轮报告：`docs/2026-10-19-opencode-web-v2-shared-service-attach-plan-review-final-recheck.md`。
- 评审员模式：**同一评审员延续**（终审评审员）。独立性限制：本轮复用了自己终审与复核 #1 的源码核验结果。
  范围由调用方按闭合路径指定为修订单元格；本轮没有重做全量评审。
- 边界：只写本报告。未改方案与历轮报告，未改业务代码，未 commit，未 build/test/install，
  未触碰 49374/4096，无 UI automation/截屏。**本结论不构成实施授权。**

## 1. 结论

F-26（阻塞）以及 F-27、F-28、F-29 都按复核 #1 给出的闭合标准落实了。v1.10 的改动全部落在 A.19 列出的
修订单元格和状态记录处，没有改出范围，也没有引入新问题。

结合终审对设计主体的判断，以及复核 #1 对 F-18~F-25 的闭合判定，**v1.10 可以作为实施冻结基线**。
冻结身份为 SHA-256 `319905c6…3e372`。这份文件目前还没有提交，建议作者或 owner 提交后，以提交哈希作为冻结身份。

## 2. 来源身份（21:26 fresh）

| 别名 | 绝对路径 | 分支/状态 | 完整提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `bd240b80ec6eceb61a99f23120086c388ebeed2b` | ` M` 方案文档（v1.10）；`??` 终审报告与复核 #1 报告 |
| I | `/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading` | `feat/claude-long-session-projection-loading` | `04fc269bdb8a297b1578ac4b9f0d4b82a98c7997` | 空 |
| U | `/Users/jacklee/Projects/opencode-v2` | detached，tag `v2.0.32` | `843a6c514e606b436a7fe713422ace0a93ad3d61` | 空 |

三仓的源码身份与终审、复核 #1 时完全相同，因此终审与复核 #1 亲核过的源码事实可以按命题复用，包括：
- `configureOpenCodeDesktopServerIfNeeded` 只有 `:582` 一个调用点、没有测试引用；
- 静态函数 `configureOpenCodeDesktopSettings` 另被 `OpenCodeManagedServer.swift:324` 和 4 处测试调用；
- `ServerStorage.swift:96-101` 对整表用 `try?` 解码。

## 3. 闭合核对（逐格）

| 意见 | 复核 #1 的闭合标准 | v1.10 实际文本（节选） | 判定 |
| --- | --- | --- | --- |
| **F-26**（阻塞） | 第 5 行只保留「删除」；E-11b（或第 5 行措辞）与 S-1 一致；U9 补一句 | §3.11 第 5 行：「**唯一处置＝删除 `configureOpenCodeDesktopServerIfNeeded()` 及 `launchBridgeProcess :582` 调用**」；「改为只对显式旧 opencode-web 来源生效」选项「**作废**」，并写明理由；静态函数 `configureOpenCodeDesktopSettings`（`:1345`）保留，只供 v1 managed `syncDesktopConfig` 既有路径与既有测试使用，且「**不得**改为对 opencode-web 任何来源生效」。E-11b 断言列新增「选择/启动/接入新 opencode 或自 v1 切换后 `ai.opencode.desktop/` 不被 CordCode 改写（Desktop 负例）」。U9 新增「external/legacy 来源启动时不新增对 `ai.opencode.desktop/` 的写入（与现状一致）」。第 5 行的「应看到」同步为 U4/U9/U10 | **闭合** |
| F-27 | 第 7 行处置文字更新 | 「`Models/Server.swift:113` 缺 kind 的历史记录**继续解为旧 `.openCode`**……**不得**解为 `.openCodeV2`，也**不得**改为严格解码，如确需迁移按单条容错」，并引用了 `ServerStorage.loadServers()` 整表 `try?` 的风险 | **闭合** |
| F-28 | §3.11、§6、§5 三处一致 | S-7 行：「remote-web marker 为并行交付项，**不阻断 S-2b**」；§5 表后：「S-2b在S-3~S-6与S-7的Mac/iOS部分组件完成后执行（S-7 的 remote-web marker 部分不阻断 S-2b）」；与 §3.11 协商节、§6 风险表一致 | **闭合** |
| F-29 | OD 表与 Gate B 更新；S-0 写明决定流转规则 | 新增 OD-9：切换时自托管的 managed 4096 是否停止；决定为沿用既有行为（停止）；状态 pending，owner 实施启动时确认；影响 S-1/S-7；验收对应 U10。Gate B 列入 OD-9，并写入「S-0 枚举的『需产品决定』项登记为 OD，在依赖它的 S-7 子项开工前决定」；S-0 验证列写入同一规则 | **闭合** |

附带一致性检查：
- §3.11 第 5 行写的是「S-1/E-11b 负例」，现在 S-1 验证列与 E-11b 断言列都有这条负例，二者一致。
- S-2b 验证列的旧文字「S-7跨端组件完成后产品活体验证」说的是放行**之后**的活体验证，不是放行前置；
  与 F-28 的处置不冲突，可以读通，不单列意见。

## 4. 范围核对

v1.9→v1.10 的 10 个改动块逐一对应如下：

| 改动块 | 内容 | 是否在范围内 |
| --- | --- | --- |
| 头部 | 版本、状态、版本史、本轮改动范围（声明「其余零改动」） | 状态记录，合理 |
| OD 表 | 新增 OD-9 | F-29 |
| §3.11 第 5 行 | Desktop 写入器的处置收窄为「删除」 | F-26 |
| §3.11 第 7 行 | iOS `Server.swift` 解码处置 | F-27 |
| E-11b | 新增 Desktop 负例 | F-26 |
| Gate B / Gate C | OD-9 与决定流转规则；状态回写 | F-29；状态记录 |
| S-0 | 「需产品决定」流转规则（只加了一个子句） | F-29 |
| S-7 与 §5 表后文字 | remote-web 不阻断 S-2b | F-28 |
| U9 | v1 显式来源不新增 Desktop 写入 | F-26 |
| 阻塞清单、附录 A.19 | 状态与修订记录 | 状态记录 |

R、OD-1~OD-8、E-1~E-10、E-11a、E-11c、S-1~S-6、S-8、S-9、其余 U 行、§3.0~§3.10 的设计正文都**没有改动**。
头部「其余零改动」的声明属实。

## 5. 全程意见账（终审 → 复核 #1 → 复核 #2）

| 意见 | 来源轮次 | 严重性 | 最终状态 |
| --- | --- | --- | --- |
| F-16、F-17 | r6 | 建议 | 闭合（终审核定 v1.8 处置正确） |
| F-18 | 终审 | 阻塞 | 主体闭合于 v1.9；残留转为 F-26 |
| F-19、F-20、F-21、F-23、F-25 | 终审 | 建议 | 闭合于 v1.9 |
| F-22、F-24 | 终审 | 建议 | 正文闭合于 v1.9；残留转为 F-29、F-28 |
| F-26 | 复核 #1（修订引入，源自终审修订方向的措辞） | 阻塞 | **闭合于 v1.10** |
| F-27 | 复核 #1（修订引入） | 建议 | **闭合于 v1.10** |
| F-28、F-29 | 复核 #1 | 建议 | **闭合于 v1.10** |

当前没有未闭合的阻塞，也没有未闭合的建议。

## 6. 冻结后的剩余门与授权边界

通过只说明设计可以冻结，**不代表**证据已经采齐、产品已经裁决，也**不代表**批准实施。

- **证据门**：E-1 verified（2026-10-19 活体）；E-2~E-11 维持 pending，按各自的阻塞列分别挡住 S-1~S-9 的实施或完成。
- **产品决定**：
  - OD-2/5/7 已 decided；
  - OD-9 pending，须在 owner 启动实施时确认；
  - OD-1/3/4/6/8 为工程决定或延续；
  - S-0 枚举新产出的「需产品决定」项，按 F-29 登记为 OD，并在对应 S-7 子项开工前决定。
- **实施次序**：
  - S-0 先行：全量消费者枚举、实施 HEAD 锚点重建与漂移表、隔离取证配方、依赖图；
  - S-1、S-2a、S-7 的组件部分依赖 E-11a；
  - S-3~S-6 分别受 E-2、E-4、E-3+E-5、E-7+E-10 阻塞；
  - S-2b 放行前置为 E-2~E-5 + S-3~S-6 + E-11b，remote-web 不阻断；
  - S-8 交付后产出 E-11c；S-9 须满足 §3.12 稳定门并有 owner 的明确退役指令。
- **授权**：**无**。实施、写类取证、服务启停与安装都须 owner 另行授权。开工时须按项目 P0 来源门重新 fresh 记录来源，
  不得沿用本报告中的提交身份。
- **外部格式专项**：本轮没有新增外部形状断言；E-2~E-5 维持待捕状态，对应实现被门控。按 audit-plan 严格口径，
  活体复测仍未完成（49374/4096 按指令未触碰）。

## 7. 终审结论

**verdict: APPROVED。** 方案 v1.10（SHA-256 `319905c6df2b05b1fd0c56a4b1ea9f2fd0a99e04f29e719484dc4dc493f3e372`）
可以作为实施冻结基线。实施启动、OD-9 的确认以及各证据门的取证授权，仍由 owner 决定。
