# Agent 管理 S0：开工来源、契约与范围

2026-10-15（Asia/Taipei）。本记录是已通过 v3 的实施冻结，不改写历史评审／样本，不等于 S1–S7 完成。

## 开工来源

完整两次来源门、全部 dirty 路径和 worktree 元数据见 [.exec-plan source manifest](../.exec-plan/evidence/d7891a2d7f31/s0-source-manifest.json)。产品是 `claude,codex-remote,grokbuild,dsh-web,opencode-web`；M/I 配对来自已评审方案的明确路径，并经本轮 worktree 重新核对。

| 别名 | 绝对工作树 | 分支 | 开工完整提交 | 未提交状态 |
| --- | --- | --- | --- | --- |
| M | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` | `feat/ios-native-message-timeline` | `0b2a5aa105da0eaa538d2339755aa1bbd09f8321` | 干净 |
| I | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` | `feat/ios-native-message-timeline` | `f9546ffe7904edbd448ec4f7a31d8e1dce1ade2b` | 274 项已有 docs/.exec-plan 修改／未跟踪，逐项见 manifest；不编辑／提交 |
| G | `/Users/jacklee/Projects/magpie` | `main` | `a36319d607d97e4754e025b82070a32d1d1a58ac` | 7 个未跟踪 .DS_Store；逐项见 manifest；只读 |
| U-Codex | `/Users/jacklee/Projects/codex` | `main` | `cc7ba33601286a751477832b01de7b6f8de0dd9c` | 干净，只读 |
| U-Grok | `/Users/jacklee/Projects/grok-build` | `main` | `2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8` | 干净，只读 |
| U-DSH | `/Users/jacklee/Projects/deepseek-harness` | `master` | `d743267388641bc76f17c45ce8b4c231aed1d32c` | 干净，只读 |
| U-OpenCode | `/Users/jacklee/Projects/opencode` | `dev` | `0d61729b3fa005cec9aa754c075a809663c736a4` | 干净，只读 |

方案 SHA256=`e49e2d5d00e6216bd70d0127c02949351be96474aef1c8b6cd0b14f91d74121a`，与 r2／元审核一致。I 相对 v1 `e479378f…` 的六个锚点文件（BridgeProvider、OpenCodeiOSApp、ServerViewModel、web AppController/backend-selection/RemoteWebViewModel）逐文件 diff 均为空；当前源码没有重叠 dirty。未来跨仓首次编辑和 build/install 门仍重新捕获，不把此清单复用成当时身份。

评审建议勘误（不修改已批准文档字节）：F5 的 dsh v3“复测”没有归档，仅沿用 v1 probe＋v2 package.json；F6 作者自检块 `open_gates: []` 不作放行依据。正确门：E5 阻挡未取证 latest provider／所有自动执行更新；E3/E6 是实施完成门。本任务不做这些 E5 扩展。已有 npm/Grok 取证范围可实现只读检查和准确更新指引；不执行更新命令。

## 最小内部契约冻结

以下均为新内部接口，待 S3/S4 codec 测试证明，不冒充已存在协议。

- `GET /internal/workspace/snapshot`：Management bearer 鉴权，返回 schemaVersion=1、runtimeIdentity(pid/bridgeEpoch)、selectionRevision、requestedDrivers、activeDrivers、sampledAt、agents、devicePresence、relay(enabled/configured/connected)。空集合全部序列化为数组，不省略为 null。
- Agent 行：id、installation(state/reason/artifactKind/installMethod)、version/versionSource、versionCheck(state/checkedAt/freshUntil/source/channel/installed/target/error)、readiness(沿用活动 descriptor)。installation 和 versionCheck 各自失败，不把 packageVersion 当 executable health。无 driver 不伪造 readiness。
- `POST /internal/workspace/rescan`：body 上限16KiB、拒绝未知字段／尾随 JSON／未知产品ID；`{agentIDs:[...],checkVersions:Bool}`，空列表代表5产品；异步返回202与operationId。在飞任务按同一 runtime/安装身份合并；读取 snapshot 不启动探测。
- `-agent-selection-revision`：UInt64；`-opencode-installed-cli`：由既有 Swift resolver 选定的准确非 secret 路径；`-opencode-source`：区分 external_http 无需本地 CLI。App bundle 版本由 Foundation 读取真实 bundle 后给本机 inventory seam，带身份和采集时间。
- 显式 `-drivers ""` 合法；未提供 flag 的 CLI 历史默认保持。非空请求全部构造失败仍失败。成功 hello ACK 必带 `backends:[]`；缺失／null／非空未知 kind 不当作 none。客户端先交付兼容，Mac 全关默认最后启用。
- 选择唯一 writer：Swift AgentSelectionStore；进程唯一 supervisor：RuntimeManager；安装/版本观察：Go toolinventory；设备在线：现有 DeviceConnRegistry；timeline/SSV2 不增加写者。

## 来源与依赖冻结

| 行为 | 权威依据／有意差异 |
| --- | --- |
| npm 安装命令 | v3 raw 本地 package.json name 白名单，npm latest 三包样本；OpenCode 当前安装器的 prefix 模式。更新 recipe 固定实际 npm、Node、prefix 和已验证 target；未知 scope 只给官方指引 |
| Claude native | v3 `claude-install-help.txt` 的 `install [options] [target]`；未安装指引采用官方文档链接，不新增未经归档的 curl bootstrap |
| Grok 安装 | U-Grok README.md:46 官方 `curl -fsSL https://x.ai/cli/install.sh \| bash`；只供复制，不执行 |
| Grok 检查 | U-Grok auto_update.rs:167 UpdateStatus＋v3 help／v1 raw；只执行 `update --check --json`，未知 generation fail closed，不改 channel/policy |
| Codex Desktop | v3 Info.plist 归档标明 ChatGPT bundle；U-Codex desired_state.rs 的愿望态分离与 disable 持久修改；CordCode 不调用该官方 disable，不用 npm codex CLI 版本替代 |
| 安装 classifier | G cliupdate.go:199/258/303 及 TestHowInstalled/TestGrokBrewCask；最小 macOS 白名单移植，无未知来源 fallback，正式复制保留 MIT |
| 严格 SemVer | `github.com/Masterminds/semver/v3 v3.4.0`，module sum 在 manifest；已读该发布包 version.go:StrictNewVersion/Compare、version_test.go:TestStrictNewVersion/TestCompare。严格解析，不用宽松 NewVersion；MIT 声明随第三方资源分发 |

本机 go 命令初次失败原因为环境 `GOSUMDB=off` 禁止 toolchain 校验；使用单次命令 `GOSUMDB=sum.golang.org` 后正常下载、校验依赖。未修改用户全局 Go 环境，未绕过校验。

## 验证边界

S0 仅静态：方案哈希、来源／漂移／dirty、样本 SHA、catalog 锚点和新契约与 v3 范围一致性。无需 build、unit/UI test、安装或 runtime 重启。正式实现是 D3；集中完成 coherent edit 后定向验证，按客户端先行顺序交付，再提供 U1–U10 人工矩阵。外部真实业务和视觉由 owner 验收，不能用组件测试或 synthetic fixture 冒充。
