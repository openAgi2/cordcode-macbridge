# Grok Build Leader 模式开关设计：第三轮评审报告

- 评审日期：2026-08-28
- 评审对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v3
- 文档实测行数：825 行（不是送审说明中的 713 行）
- 评审方式：只读源码与脱敏配置样本核查；未修改设计稿，未构建、未运行测试

## 1. 来源核对结果

评审前门与写报告前门各执行一次，结果相同：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=仅以下三个预期未跟踪文档：
  docs/2026-08-28-grokbuild-leader-mode-design.md
  docs/2026-08-28-grokbuild-leader-mode-design-review.md
  docs/2026-08-28-grokbuild-leader-mode-design-review-r2.md
任务预期分支=main
配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
预期产品特性=给既有 grokbuild leader 只读观察链路增加 Mac 端配置开关；不新建 backend，不删除 file tailer，不实现 follower 写方向
```

两仓 HEAD 与指定来源完全一致，未发生来源漂移，可以继续评审。评审严格按文档 §0.4 的既有链路基线执行，没有要求新增 API 客户端、事件泵、协议翻译或 follower 写能力。

## 2. R2 十一项处置复核

| R2 项 | v3 结论 | 证据 |
| --- | --- | --- |
| B1 配置 merge 与 confinement | 已闭合 | 文档 §2.1-1/2、§6-5；官方 `config_layers.rs:20-75,177-205` 与 `app/mod.rs:431-481` 一致 |
| B2 observer 生命周期 | 已闭合 | 文档 §2.1-3、§4.9；`handlers_relay.go:158-185,206-250` 确为 `context.Background()`，无 subscriber 取消；`leader/server.rs:1697-1761` 以剩余 client 决定退出 |
| B3 stale socket | 已闭合 | 文档 §2.2-1、§6-7；`grokbuild.go:174-198` 每次只 stat 后选路，连接失败不删 socket、不回退 tailer |
| B4 compare→rename TOCTOU | 已闭合 | 文档 §3.3-7、§5 已降级为 best-effort；官方 `persist.rs:7-13,91-95,206-216` 确有仅限本进程的 `SAVE_LOCK` |
| M1 Phase 0 路径/effective 核查 | 部分闭合 | 已改成解析后的 `$GROK_HOME/config.toml`，但 `grok inspect` 不能证明 effective `use_leader`，见 R3-B2 |
| M2 缺失目录裁决 | 已闭合 | §3.3-5/9 与 T11 明确 0700/0644 |
| M3 解析机制与 symlink | 未闭合 | 已作方向裁决，但没有选定依赖，也没有证明其可区分原始 TOML 写法；见 R3-B1、R3-M3 |
| M4 absent / explicit false | 已闭合 | §3.4 #1/#5、OFF 可达路径、T16/T20 一致 |
| M5 F-7 落点 | 已闭合 | Phase 0 第 8 步、§6.1、§7.3 第 10 行均改为 leader crash/kill |
| M6 编号/来源/措辞 | 已闭合 | §0.2 为第 1–10 步，§3.3 为十条规则，来源路径逐项列出 |
| S1 不拨测理由 | 已闭合 | §3.4 正确表述为扰动 client 生命周期并复制握手职责 |

R2 的四项事实阻断均已实质修正；本轮阻断来自 v3 新增的 TOML 基础设施方案与仍不可执行的 effective-config 证明，不是重复否定已完成处置。

## 3. 分级发现清单

### B（阻断）

#### R3-B1：所谓“受控 TOML 解析依赖”尚未形成可实施、可核验的设计，且当前职责描述在语义上不成立

**核查结论：不符。**

文档 §3.2、§3.3-3、Phase 1 只写“引入一个纯 Swift、版本 pin 的成熟 TOML parser”，没有给出 package URL、产品名、精确版本/commit、license、Swift/macOS 兼容范围、传递依赖，也没有指定解析 API。更关键的是，§3.3-3 同时要求 parser：

1. 把点键归一为嵌套表以读取语义值；
2. 再区分该值原来究竟来自 canonical key、点键、quoted key 还是 inline table，以决定 F2；
3. 为外科文本编辑提供安全定位。

普通 value-tree parser 在完成第 1 步后会丢失第 2、3 步需要的原始语法来源和 source range。除非选定库提供保留 trivia/source span 的 concrete syntax tree，单凭“真实 TOML parser”不能证明这三项职责。当前仓库也没有既有 TOML package（`MacBridge/project.yml:1-72` 无 packages）。

依赖接线同样未闭合：实际构建入口是已跟踪的 `MacBridge/CordCodeLink.xcodeproj/project.pbxproj`；§11 却限定 diff 只有 `project.yml` 的“依赖行”，没有纳入 target package product dependency、重生成的 `project.pbxproj` 与 `Package.resolved`。照文档实施，Swift target 不会凭一行 `project.yml` 自动获得 module。

**必须修改：**在设计期冻结具体 package 与精确 revision/version，记录 license/维护状态/传递依赖/最低 Swift 和 macOS；用该库真实 API 证明它是否提供 source ranges/CST。若只提供 semantic value tree，就把职责拆成“parser 负责整文档合法性与语义值 + 经真实样本验证的 syntax-aware locator 负责唯一 canonical assignment”，不得继续声称归一化后的 tree 能反推出原始写法。同时明确 XcodeGen top-level package、app/test target product dependency、`project.pbxproj` 重生成与 `Package.resolved` 提交范围。

#### R3-B2：Phase 0 指定的 `grok inspect` 证据入口不能证明 effective `[cli].use_leader`

**核查结论：不符。**

文档 §0.2 第 2 步、§13 R2-M1 把官方 `grok inspect` 作为 effective 值证据入口。pin 源码中，`InspectReport` 只输出 `config_sources` 和 warnings（`xai-grok-shell/src/inspect/mod.rs:55-82,265-290,310-325`）；`list_config_sources` 只报告 layer 的 role/path/note（`:1067-1175`），不输出 merged config，也不输出 `cli.use_leader` 或最终 confinement veto。评审机安装版 1.0.12 的脱敏实测同样只有 `configSources.layers=[{role:"user", path:"/Users/jacklee/.grok/config.toml"}]`，搜索不到任何 leader 字段。

“Phase 0 先核对输出形状再采信”能避免误信，却没有提供核对失败后的可执行替代方案；因此十步门在第 2 步仍不闭合。“或等效 effective config dump”也没有给出命令、字段和判据。

**必须修改：**把证据改成 pin/安装版确实可观察的最终解析结果，例如启动日志中 `leader mode resolved` 的 `use_leader`、`policy_disable_reason`、`leader_disabled_by_sandbox` 字段（来源 `xai-grok-pager/src/app/mod.rs:705-720`），并给出日志位置/命令/期望字段；再用 `grok inspect --json` 仅证明参与的 layer 路径，不得称其证明 effective 值。requirements/MDM 场景需要至少一条可执行的负例判据。

#### R3-B3：零 Go 路线仍缺 owner 裁决，且“接受 stale socket”与 fail-visible 红线冲突

**核查结论：设计代价披露已核实，但产品门未闭合。**

§13.1 正确披露四项代价，但明确把任一项的接受权留给 owner；送审材料也称本轮需要 owner 表态，当前文档没有该表态。尤其第 2 项不是单纯体验降级：`grokbuild.go:174-198` 在 stale 文件存在时每次都选 leader，`LeaderSubscriber.Run` 在 `leader_subscriber.go:141-145` dial 失败后结束；上层仅写 debug 日志（`grokbuild.go:193-195`），UI §3.4 #3/#4 仍只能看到“检测到 socket”。结果是外部观察持续丢失，用户可见状态无法区分 live leader 与 stale 文件。这与文档 §1、§11 的 fail-visible 要求直接冲突。

**必须修改/裁决：**owner 需在正文明确接受或否决四项代价。评审建议至少否决 stale socket 的零-Go代价：在 Go 侧把“socket 存在但 dial/握手失败”做成可诊断的 tailer fallback（保留 file tailer，不删除它），或提供同等强度的用户可见失败/恢复机制后重新定级。observer 续命与 interaction 等待可以作为单独产品裁决，不能用对 stale 的接受一并打包。

### M（必改）

#### R3-M1：新增依赖按仓库纪律属于 D4，不是“全程 D2”

**核查结论：不符。**

文档头部、§7.4、Phase 1 仍称“全程 D2 + 一项依赖”。仓库活文档 `CLAUDE.md:221-229` 明确把“工程配置、依赖”列为 D4。引入 SPM package 会修改工程解析、供应链与 Release 构建输入，不能用括号降回 D2。

应将基础设施子任务定为 D4，并补精确验证门：XcodeGen 重生成一致性、locked resolution、app/test target 编译、Release package、依赖 license/传递依赖审计；这不意味着默认跑全量测试或 UI automation。

#### R3-M2：完整 config 备份会复制凭据，安全声明与权限规则不闭合

**核查结论：不符。**

§3.3-10 备份整个 `config.toml`，§5 却说“不读写 provider key”，且只笼统写“权限跟随目录（0700）”。官方配置明确允许在 user config 中直接存凭据，例如 `xai-grok-pager/docs/user-guide/05-configuration.md:275` 的 `api_key` 与 `:282-287` 的 MCP token。完整备份必然读取并复制这些值，最近三份还扩大留存面。

应如实声明“备份可能包含完整用户配置及 secret”，使用专用备份子目录 0700、每份文件 0600、禁止记录内容；备份创建/权限收紧失败必须在改原文件前 fail closed。补测试覆盖权限、轮转删除失败、备份失败不写配置。若继续保留精确回滚，不能用 redaction 破坏备份内容。

#### R3-M3：symlink 写入策略缺少目标身份与属性闭合

**核查结论：存疑。**

§3.3-4 只定义“沿链到普通文件，在目标目录 temp+rename”，T18 只覆盖外部普通文件、悬空/循环。尚未定义：相对 symlink 的解析基准、多级链中途变化、最终目标不是普通文件、resolve 后 link/target 被替换、写后校验与回滚究竟绑定初始 canonical target 还是重新解析后的 target，以及仅保留 POSIX mode 时 ACL/xattr/owner 的处理。

这不要求承诺无竞态，但必须明确 best-effort 身份模型并 fail visible。至少补：相对/多级链接、非普通目标、link swap/target replace、目标 mode 保留；如明确不保留 ACL/xattr，也应列为已接受边界。当前 T19/T20 分别是 TOML parse 与状态测试，并非 symlink 补充覆盖。

#### R3-M4：“开关不生效只有三因”仍遗漏显式高优先级 flag

**核查结论：不符。**

§2.1-1 正确列出 `--no-leader` 为最高优先级，官方 `app/mod.rs:451-469` 也如此；但 §2.1-2:240-241、§6-5、§11 排障清单把不生效原因收窄为 requirements/MDM、confinement、版本漂移，漏掉用户启动时显式 `--no-leader`。`--chat` 冲突已另列 excluded，不应混入；普通 TUI 的 eligibility 固定为 true（`app/mod.rs:709-715`），也无需泛化。

应在诊断归因与 Phase 0 启动身份中加入“实际启动参数含 `--no-leader`”这一可观察原因。

#### R3-M5：外部格式样本门仍未满足支持范围

**核查结论：部分核实。**

本轮脱敏实样本：`/Users/jacklee/.grok/config.toml` 为 regular file、mode 0644、ASCII、30 个 LF/0 个 CRLF；`[cli]` 位于首节且没有 `use_leader`。SHA-256 为 `c78c74d2b63b801c6f591c291a5df2425fc83d7edd7c5d82f928b29b4d8ea464`。官方 fixture `mcp.rs:2208-2293` 证明 canonical true/false/absent 的官方解析行为，但不是用户现场样本。

§3.3-9 已诚实声明 CRLF、尾随注释没有真实样本，却又把 T12-T14 的自生成输入作为支持主张的冻结依据；symlink 也只有计划测试。按 sample-first 纪律，自造 fixture 可证明实现自洽，不能证明外部内容形态。应建立脱敏 sample manifest（来源、版本、hash、是否真实/官方 fixture/合成），把无真实样本的 CRLF、尾随注释、quoted/dotted/inline、symlink 明确标为 synthetic compatibility tests，而不是现场已验证支持；任何依赖具体 source-range 行为的 parser 主张须在选定 package 后用这些样本和至少一个真实非常规样本验证。

#### R3-M6：仍有一处源码范围行号漂移

**核查结论：不符。**

§4.9 写 `grokbuild.go:176-199`，实际路径选择实现为 `:174-198`，`:199` 已在函数外。按本评审“行号漂移即不符”的纪律，应修正。其余重点行号（日志 `177/192/147/199`、WorkspaceView `496/506-513/528-545`、test target/source directory）本轮复核吻合。

### S（建议）

#### R3-S1：同步修正文档元数据与送审行数

设计稿当前实测 825 行，不是送审说明中的 713 行。建议在后续送审时从文件现状生成行数，避免 reviewer 对错版本。

#### R3-S2：把 owner 决策写成单独、可签署的 gate

§13.1 目前把技术事实、设计方建议和 owner 裁决混在一列。建议增加四行 `Owner decision = accept/reject + date`；其中 stale fallback、observer cancellation、interaction follower 能力应允许独立选择，避免“维持零 Go”成为打包式默认。

## 4. 外部内容形态样本核查

| 内容形态 | 样本结果 | 评级 |
| --- | --- | --- |
| 真实安装配置：普通 `[cli]`、键 absent | 本轮脱敏 dump 已确认 regular/0644/LF，`[cli]` 有 installer/auto_update/channel，无 `use_leader` | 🟢 已核实 |
| 官方 canonical `true` / `false` / absent | pin 源码 fixture `mcp.rs:2208-2293` 已核实；属于官方 fixture，不是现场 dump | 🟢 可作为官方契约证据 |
| dotted / quoted / inline table 等价形态 | 无真实样本；设计选择 fail-visible，但尚无 concrete parser API 证明能识别原始形态 | 🔴 未证明 |
| CRLF、节头尾随注释、行内注释 | 无真实样本；T12-T14 只是计划中的合成 fixture | 🟡 应降级标注 |
| symlink（相对、多级、外部目标、悬空/循环） | 本机真实配置不是 symlink；只有计划 T18 | 🔴 未证明 |

本轮没有复杂嵌套抽取或数量归因，故不需要两套 dump 脚本交叉计数；对真实 config 只输出结构元数据与 `[cli]` key 名，避免泄露值。当前最危险的未验证内容类型不是 wire event，而是“parser 能保留/识别 TOML 原始写法”的语法树能力。

## 5. 五个维度结论

### 事实核查

R2 的 merge 方向、confinement veto、observer 生命周期、interaction 广播、stale socket 与官方 SAVE_LOCK 均已按 pin 源码改正。新增事实错误有两项：`grok inspect` 不输出 effective leader 值；“不生效只有三因”漏掉最高优先级 `--no-leader`。另有一处 `grokbuild.go:176-199` 行号漂移。

### 设计闭合性

TOML 编辑仍未闭合：依赖未选定，semantic parser 与 syntax-preserving locator 的职责矛盾，Xcode/SPM 接线缺失；备份 secret 权限与 symlink 身份模型也未闭合。T1-T20 数量增加了，但测试表无法替代真实样本，也未覆盖上述依赖/备份/symlink 门。

### “零 go-bridge 改动”独立验证

健康路径的链条成立：下一次订阅 stat 到 live socket 后会走既有 LeaderSubscriber、共用 codec 与 relay loop，不需 Go 新实现；不热切换披露准确。可接受性仍未成立：stale socket 会让每次打开持续失败且 Swift UI 无法获知 dial/握手结果，只有 debug 日志，违反 fail-visible。评审建议 owner 否决这一项零-Go代价并加入 Go fallback；其他三项应独立表态。

### 纪律一致性

§0.4 正确声明 wire 样本冻结、新旧 backend 并存、API 客户端/事件泵等不适用，也正确保留 file tailer 与 follower 另案边界。仍遗漏/违反的适用纪律是：依赖变更应按 D4、外部 TOML 内容形态需 sample-first、fail-visible 必须到用户可观察层而不能只有 debug 日志、供应链与生成工程文件应可复现。

### 内部一致性

§4.3/§4.9 与 §6 对 observer/stale/F-7 已一致，§6.1 与 §7.3 第 10 行也对齐。当前矛盾集中在：§3.3 声称 parser 可识别原始等价形态但未定义 CST 能力；§5 称不读 provider key却备份全文件；§7.4/Phase 1 称 D2 而活文档将依赖列为 D4；§13.1 接受 stale 但 §1/§11 要求 fail-visible。

## 6. 总结论

**退回。**

必改项：

1. 冻结并验证具体 TOML package/CST 或拆分 semantic parser 与 syntax locator，补齐 XcodeGen、pbxproj、Package.resolved 与 target 接线；
2. 用真实可观察字段替换 `grok inspect` 的 effective-value 证明；
3. 取得 owner 对四项零-Go代价的逐项书面裁决；评审建议至少否决 stale socket 的持续阻断并加入 Go 侧 tailer fallback；
4. 按 D4 重新定义依赖子任务的供应链与构建门；
5. 补齐完整配置备份的 secret/权限/fail-closed 规则和 symlink 身份测试；
6. 修正 `--no-leader` 归因、样本分级与行号漂移。

本结论不要求新增 backend adapter，不要求 follower 可写，不建议删除或绕过 file tailer；建议的 stale 恢复恰恰是保留并在 leader 连接失败时安全回到现有 tailer。
