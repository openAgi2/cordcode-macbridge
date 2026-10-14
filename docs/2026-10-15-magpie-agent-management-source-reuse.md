# Magpie Agent 管理：源码复用调查与方案修订

日期：2026-10-15（Asia/Taipei）。结论：优先移植 Go 的安装来源识别、探测去重和相关回归用例；界面复用交互与几何算法。Magpie 的接入开关管理工具配置，不能替代 CordCode 的驱动生命周期。本次只有源码调查与文档修订，未复制产品代码。

开发入口：[主方案 v2](2026-10-15-agent-management-topology-and-state-machines.md)。本文补充源码依据；R1–R10、OD1–OD5、E1–E6、S0–S7 仍由主方案管理，未取得独立评审通过。

## 1. 来源与调查边界

本轮在源码分析前、首次文档编辑前核对路径、分支、完整提交、dirty 与 worktree 元数据。完整逐项状态及 21 个参考文件 SHA256 见 [本轮来源清单](assets/2026-10-15-agent-management/magpie-reuse-source-manifest.json)。主方案 v1 的来源清单与版本原始样本保留，不回写历史身份。

| 别名 | 绝对工作树 | 分支 | 完整提交 | 首次修订前未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `2d9836247f8ce2dbda3afd81d1b821aadaccf4ea` | 干净 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `e479378f8c88338219ad734a7571e41ade356575` | 274 项，全部在 docs/.exec-plan；每项在 manifest，未编辑／提交 |
| G | `/Users/jacklee/Projects/magpie` | `main` | `a36319d607d97e4754e025b82070a32d1d1a58ac` | 仅 7 个未跟踪 `.DS_Store`，逐项如下；未编辑／清理 |

G 的全部 dirty：`.DS_Store`、`.github/.DS_Store`、`build/.DS_Store`、`docs/.DS_Store`、`internal/.DS_Store`、`plugins/.DS_Store`、`site/.DS_Store`。

任务预期：M/I 为已确定的同功能分支族；配对线索与产品来源调查见主方案 §2。G 是用户明确指定的只读参考仓库，不是配套产品工作树。预期产品仍为 `claude`、`codex-remote`、`grokbuild`、`dsh-web`、`opencode-web`；不因 Magpie 支持更多工具而扩大产品集合。本轮 M 新提交仅增加了 v1 方案与证据，产品源码仍可归属主方案的 v1 源码身份。

已阅读 G `AGENTS.md`，沿 GUI API→Agent/进程 helper→测试追踪；没有运行 Magpie、安装依赖或执行其 Go／Playwright 测试。截图中的 Magpie 二进制与 G 提交是否一致未证明，以下结论仅描述 G 此提交的实现。本文所有 G 相对路径均以该完整来源组合为前缀。

## 2. 可以直接借的实现与用例

| 复用项 | G 的实际入口／拥有者 | 对应测试或调用证据 | CordCode 处理 |
| --- | --- | --- | --- |
| 安装来源识别 | `internal/agent/cliupdate.go:199 howInstalled`→`fromBrew:258`／`fromNodeModules:303`；按真实路径和本产品 package/formula 白名单分类 | `cliupdate_test.go:113 TestHowInstalled`、`:515 TestGrokBrewCask`；`CLI` 汇总后由 `internal/gui/api.go` 的 GET agents/cli 输出 | **移植最小 macOS 子集**。先查本产品原生／brew 证据，再查精确 npm package；保留 prefix 和对应 npm/Node 的身份，不仅看目录名 |
| 减少重复探测 | `cliupdate.go:471 memo.get`、`:524 installedVersion`、`:549 latestVersion`，同 key 合并在飞请求，路径/stat 变更失效 | `internal/proc/version_test.go:19 TestVersionRunsOnce` 验证失败指纹；GUI `app.js:2193 loadCLIs` 合并一次请求 | **复用去重与指纹思路，改返回模型**。typed observation 带 error、freshness、时间，不复制失败仍返回旧成功字符串的行为 |
| 低成本安装版本元数据 | `internal/proc/version.go:23 Version`→`:80 npmVersion` 优先读所属 npm package.json；否则有界 `--version` | `version_test.go` 的 npm 不执行与失败／超时用例 | **移植纯文件读取 seam**，先验证确切 package 身份。仅证明 packageVersion，不证明 binary 可执行或 adapter 可用；已有可执行版本探针仍保留 |
| 子进程拥有权 | `internal/proc/probe.go:19 ProbeContext`、`:59 EndProbes`；`probe_other.go` 创建本次探测的进程组并处理取消 | `probe_test.go:73 TestProbeTimeoutEndsWhatItStarted` 等清理用例 | **借取消／收尸不变量和测试**，适配 CordCode 已有执行器；唯一 Wait、输出上限、runtime 退出取消。不能另建全局 supervisor 或按进程名杀官方工具 |
| 版本与更新 pill | `internal/gui/assets/app.js:2124 cliTag`、`:2176 paintCLIButton`、`:2186 paintCLI`；版本晚到只重绘该行版本区域 | `loadCLIs:2193`→GET agents/cli；`updateCLI:2211`→POST agents/cli/{id}→`cliupdate.go:895 UpdateCLI` | **复用交互规则**：先画列表、按稳定 ID 补结果、pill 紧跟版本、保持焦点与行位置。Swift 原生重写；本版点击提供准确命令／说明，不调用 Magpie 的自动升级 API |
| 安装名称＋命令同行 | `internal/agent/install.go:179 Installs`→catalog/CLIMissing；`app.js:2378 paintInstalls` 将名称与 command/copy 放同行 | `install_missing_test.go:17 TestCLIMissing`、`:152 TestCLIMissingSharedFolders` | **借布局与残留配置负例**。本页安装事实来自 executable/app locator；安装不自动启用；仅复制命令，无 Node 环境自动安装 |
| 手动关闭不被自动恢复 | `internal/agent/applied.go:514 Disconnected`、`:706 Disconnect` 持久记录显式关闭；Wired/ConnectHow 判配置是否仍由 Magpie 管理 | `Disconnected` 被自动同步／恢复路径检查，`.disconnected` 写入在 Disconnect 内 | **镜像不变量**：用户关闭后，重扫、启动、修复不改变 desiredEnabled；不搬 Magpie 的 stash/config 改写机制 |
| 连线随真实布局 | `internal/gui/assets/routing.js:1021 layout` 在同一容器坐标中测节点矩形，再生成底层 SVG 路径 | 实际 layout 生产调用，按节点在 hub 右侧或下方选择路径；非静态截图坐标 | **移植几何思路**到 Swift anchor preferences＋单 Canvas；设备→服务→Agent 与侧边 Relay 按主方案数据画，不能复用它的模型路由业务 |

Magpie 包是 Go `internal/agent`，不能从 CordCode 跨模块直接 import。推荐抽取上述只读小函数及必要的数据结构，依赖显式 resolver、文件读取、HTTP、进程接口；不能把整个 Magpie GUI/config/auth/proxy 模块当 CordCode 运行依赖。JavaScript/CSS 不适合直接塞进 SwiftUI。

## 3. 安装来源识别的价值：避免更新错副本

`/opt/homebrew/bin` 里的工具可能是 npm 安装的软链接，并不是 Homebrew formula。Magpie 正确优先识别“该 Agent 自己的 formula/cask”，否则再识别它是否属于精确 `node_modules/<package>/`。Homebrew Node 的 keg 内安装 npm Agent，也应归 npm 管理。

本轮仅做文件系统候选路径探测，结果见 [path probe](assets/2026-10-15-agent-management/magpie-reuse-path-probe.json)：Claude、OpenCode、dsh 的候选真实路径均在 `/opt/homebrew/lib/node_modules/<各自官方 package>/`；Grok 候选指向 `~/.grok/downloads/grok-1.0.52-macos-aarch64`。这证明本机存在此类路径案例，**不证明生产 runtime 使用这些候选**；实际使用路径仍按主方案 §4.3 的 owning resolver 冻结。

推荐的分类输出不是一段 shell 文本，而是 `InstallOrigin{method, packageID?, prefix?, managerPath?, nodeBinDir?, channel?, evidence}`，无法证明来源就 unsupportedSource。更新说明从此身份生成 `CommandRecipe{executable,args,environment}`；显示时完整 POSIX quoting，不能用裸 `npm` 或另一个 Node 默认 prefix。必要 Node PATH 环境与完整管理器路径也要反映到复制命令。

不直接复制 `updater.shown()`：它仅在参数有空格时使用 Go `strconv.Quote`，不是完整 shell 转义，`$`、反引号、引号等仍需可靠处理。不复制 `fromNodeModules` 找不到所属 npm 就再从 PATH 换一份的分支；本版无法证明对应管理器时给官方指引。

mise/asdf 的 shim 解析与测试可留作扩展证据（`cliupdate_shim.go`、`cliupdate_shim_test.go:51 TestMiseShimIsNodesNpm`）。本版不默认新增 Windows/WSL/bun/pnpm/Volta 或任意版本管理器兼容；所有新增解析必须正面证明目标与 scope，不采用失败后猜路径。

## 4. 必须改造或不采纳的行为

| 源码事实 | 为什么不能照搬 | 本方案规则 |
| --- | --- | --- |
| `Agent.Detected`（agent.go:307）接受配置文件／目录／应用／CLI 多种存在迹象；CLIMissing 又处理配置残留 | “配置过”不等于 executable 已安装；Wired 不等于在线 | installation、desired、applied、readiness 分轴；配置只作来源线索 |
| `memo.get` 失败保留旧字符串；`loadCLIs` catch 后直接 return | 网络失败可能继续呈现旧 current 或静默隐藏 | 失败显式显示；旧值仅作带时间的“上次检查”，不能授权有效更新 pill |
| `proc.Version` 先读 package.json，非零退出也可返回 stdout | npm 安装记录升版但可执行依赖坏了，会误认成功 | packageVersion 与 executableVersion 分开；运行失败保留 exit/error；不推导 readiness |
| `Newer/preNewer`（cliupdate.go:409/438）是手写宽松比较 | 不完全满足严格 SemVer | 使用成熟库的严格解析；补 numeric/non-numeric prerelease、build metadata 和非法 core 用例 |
| `UpdateCLI` 直接执行 `@latest`，并可补 `--include=optional` 自动修复 | 当前只授权设计／命令指引；latest 也可能在 UI 检查后变化；自动修复扩大环境变更 | 保持 OD4，不实现自动升级、repair 或管理员提权；指引 target 固定在已验证目标，执行后真实重查 |
| `npmInstall`（install.go:128）可能把 nvm＋Node LTS 安装拼到命令 | 会改用户环境，超出本任务 | 缺 Node 明确说明并给官方链接，不拼环境修复脚本 |
| Grok latest helper、brew metadata provider 有额外外部内容解析／备用源 | 源码不能代替目标版本真实响应证据；备用源违背当前 source 边界 | Grok 用已取证官方 update-check；native/brew 新 provider 仍过 E5，不复制镜像 fallback |
| `desktopapp.go:89 AppVersion` 用正则读取 XML plist | 不支持 binary plist；Magpie Codex 产品身份不同于 CordCode ChatGPT Desktop | 复用 CordCode 实际 bundle resolver 与 Foundation/plist 读取，不用 npm codex 替代 codex-remote |

SemVer 反例来自静态阅读，未执行测试：`Newer("1.0.0-2", "1.0.0-11a")` 会进入字符串比较并认为前者较新；SemVer 要求 numeric prerelease 小于 non-numeric。将此输入移入未来组件回归，不引用 Magpie 测试通过作为严格比较的证明。

可以借未来自动升级的 per-ID busy、执行后重新定位／验证、共享包管理器串行调度测试思路；不能因此把自动升级加入本版完成标准。Magpie 失败检查之后的自动修复路径不移植。

## 5. 对主方案 v2 的具体修订

| 意见 ID | 处置 | 修改位置与理由 | 遗留门 |
| --- | --- | --- | --- |
| MR1 安装／版本重复所有者 | 采纳 | §4.1/4.2/4.3、S1/S5：Go `toolinventory` 统一安装元数据、版本来源、去重和命令 recipe；取消拟新增 Swift AgentVersionService | S0 冻结 seam、依赖与 typed schema；不动产品代码 |
| MR2 来源识别与更新 scope | 采纳 | §7.1/7.2：适配 howInstalled 的正面证据／同 prefix 规则，可靠 shell quoting | 未验证原生／brew latest 仍 E5，不扩大源 |
| MR3 异步按行补数据 | 采纳 | §4.3/§7.1：snapshot 只读已有结果；有界异步初查／重查，同身份合并在飞；UI 不等待所有来源 | 旧 epoch/revision 结果拒绝提交 |
| MR4 错误与轻量元数据 | 部分采纳 | 复用指纹和纯文件读取，改 typed error；metadata 不证明可执行 | §11 定向负例，不能用缓存替代真实状态 |
| MR5 开关与拓扑 | 部分采纳 | 镜像手动关闭不自动恢复；几何算法转 Swift；不搬 wiring/config 或模型路由 | 保持 OD3 与设备/Relay 的实际拥有者 |
| MR6 自动安装／自动更新 | 不采纳当前范围 | 仅记录参考调用链，保留 OD4 的指引交互，不引入 Node bootstrap/repair | 未来明确扩大范围并过 E5 后单独设计 |

实施顺序：S1 先抽纯 classifier/metadata runner seam 与定向用例；S5 增加已取证来源、strict comparison、typed coalescing 和 recipe；S6 接 Swift presentation 与 anchor geometry。S2 的安全重启、S3 的零 Agent 跨仓修复、S4 的同源设备 presence 不能用 Magpie 接入替代。

`toolinventory` 仅接收业务传入的准确路径和 scope，不自行扫描全部代理／启动用户 shell／采集全部环境。读本机路径可供 `.help`，不写遥测；未知 terminal PATH 时不显示“你的终端找不到它”。

## 6. 许可证与交付验证

G `LICENSE` 是 MIT，版权所有 `Copyright (c) 2026 yetone`。允许复制／修改源码与测试；正式复制时为移植文件添加来源路径＋commit，并将完整原版权声明和 MIT 许可放到随产品分发的第三方声明，记录修改点。不能只写“参考 Magpie”替代许可。当前仅记录算法／调用链，尚未复制产品源码，不新增虚假依赖声明。

本轮 D0：静态核对来源、符号、引用与 SHA；`git diff --check` 和 staged secret scan。没有执行 Magpie 或 CordCode 的 build、unit/UI test、快照测试、安装和重启。所列测试是可迁移用例来源，不是本轮测试通过的报告；外部更新源以 v1 raw 样本审计为准，新增本地元数据证据如下。

## 7. 新增文件格式的真实样本核验

为防止只从 Magpie 源码推断 package.json 的字段形状，本轮从上文候选实际 package 路径逐字节复制三个完整文件。捕获时间、原路径、字节数、SHA 与字段类型见 [样本索引](assets/2026-10-15-agent-management/magpie-reuse-package-sample-index.json)。来源 M/I/G 的完整身份、dirty 与配对见 §1；这不是运行时路径选择或 executable health 证明。

| 内容类型／计划字段 | 实际全文件 | 样本结果 | 核验 |
| --- | --- | --- | --- |
| Claude npm 安装包，根 name/version string | [local-claude-package.json](assets/2026-10-15-agent-management/local-claude-package.json) | name=`@anthropic-ai/claude-code`；version=`2.1.296` | verified，非空 |
| OpenCode npm 安装包，同一根字段 | [local-opencode-package.json](assets/2026-10-15-agent-management/local-opencode-package.json) | name=`opencode-ai`；version=`1.19.0` | verified，非空 |
| dsh npm 安装包，同一根字段 | [local-dsh-package.json](assets/2026-10-15-agent-management/local-dsh-package.json) | name=`@deepseek-ai/dsh`；version=`0.2.0-rc.2` | verified，非空，保留 prerelease |

解析约束：只读取已正面定位的所属包，JSON 根必须对象、name 必须精确等于产品白名单 package、version 必须非空 string 并经过严格版本校验；文件上限256KiB。其他字段不解析、不执行 scripts、不沿 package metadata 扫描额外工具。不拿 npm latest 文档推断本地包形状。

未在真实环境制造 malformed/null/missing/name mismatch 或删除包负例；这些是未来纯组件 fixture 的拒绝用例，不宣称来自实测。三个样本仅证明当时元数据成功形状，不证明可执行输出等价，不声称 packageVersion 升级后 seat/App 自动变化。简单根字段读取一次 json.loads＋类型/精确 name 校验足够，无嵌套数组、跨记录去重或两类等价声明。源文件／归档 byte SHA 对账以索引为准。

未验证内容：native/brew latest、额外 npm 包和 Grok 新输出 generation 仍由 E5 阻断新增解析，不能借 Magpie 的支持表直接放行。没有把 v1 审计改写成它当时已核验此第三类文件格式。
