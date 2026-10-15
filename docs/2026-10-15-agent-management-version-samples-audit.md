# Agent 管理方案：外部版本样本核验

日期：2026-10-15（Asia/Taipei）。范围：主方案 §7 的版本解析内容形状；不是整个方案的独立评审，不宣称实现或升级成功。

主方案：[设备—服务—Agent 拓扑与状态管理](2026-10-15-agent-management-topology-and-state-machines.md)。样本目录：[assets/2026-10-15-agent-management](assets/2026-10-15-agent-management/version-sample-index.json)。v1 样本为本轮实际捕获；索引保存精确UTC时间、请求/命令、SHA-256、HTTP状态或退出码，UTC的10月14日16时对应台北10月15日00时。**v3 批注（2026-10-15 02:36–02:38 UTC 补采，r1 评审 F1）**：同索引追加四项命令语义观察（`claude-install-help.txt`、`opencode-upgrade-help.stdout/stderr.txt`、`grok-update-help.txt`、`chatgpt-infoplist-observed.txt`），本报告下文"两类外部版本数据"的结论范围不变——这四项是命令／本地读取形状证据，不是新增 latest provider。

## 结论

主方案仅解析两类外部版本数据，已有本轮真实成功样本和npm真实404负例。npm只取顶层 `version`；Grok只取本机1.0.52只检查更新命令的显式字段。其他发行源、Desktop最新版本查询与自动更新均未取证、没有被放入本版自动解析范围。方案提出的内部Management schema不是外部事实，不以本报告冒充已经存在的API。

## 逐类型证据

| 类型/声明 | 本轮真实样本 | 核验 |
| --- | --- | --- |
| `@anthropic-ai/claude-code/latest` 根对象，顶层version非空string | `npm-claude-latest.json`：`version="2.1.299"`，HTTP200 | verified；只证明npm发行线，不证明native/brew渠道 |
| `@deepseek-ai/dsh/latest` 同一顶层取值规则 | `npm-dsh-latest.json`：`version="0.2.0-rc.2"`，HTTP200 | verified；不能丢弃预发布标记或把CLI版当seat/App版 |
| `opencode-ai/latest` 同一顶层取值规则 | `npm-opencode-latest.json`：`version="1.19.0"`，HTTP200 | verified；图中1.20.0是示意，不能硬编码 |
| 不存在包的registry响应不是成功版本文档 | `npm-not-found.json`，请求不存在的测试包，HTTP404，JSON根是字符串 `"Not Found"`，不是带error键的对象 | verified；解析入口先检查HTTP状态，不在错误体里找版本 |
| Grok只检查更新JSON | `grok-update-check.stdout.txt`，`grok update --check --json`，exit0；字段见下 | verified，仅适用本次实测binary输出generation；不是实施目标checkout与binary一致证明 |

Grok完整原始stdout：

```json
{"currentVersion":"1.0.52","latestVersion":"1.0.52","updateAvailable":false,"installer":"internal","channel":"stable","autoUpdate":true,"error":null}
```

已核实current/latest非空string、updateAvailable为bool（false）、channel/installer为string、error为null。不要在 `updateAvailable=false` 时凭另一源/字符串排序创造更新。`autoUpdate`存在，但本版不写这个官方设置；不把该字段当CordCode接入开关。

## 来源、上游与运行版

本轮证据捕获所在M工作树：`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`，`feat/ios-native-message-timeline`，源码基线 `f7dee495ccf722ce66e4cd470bf795ec5332893c`；第一次文档修改前干净，本报告写入时仅本任务新文档/证据未跟踪。完整dirty列表和双仓配对见主方案§2及source-manifest，本报告不形成iOS源码结论。

U-Grok：`/Users/jacklee/Projects/grok-build`，main，`2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8`，干净；源码 `crates/codegen/xai-grok-update/src/auto_update.rs:167` 的 `UpdateStatus`、`:177` 的JSON序列化与 `xai-grok-pager/src/app/cli.rs:89` 的check参数可与样本对照。实测binary是 `grok 1.0.52 (0920c8169e27)`，该短hash在本机checkout不可解析；因此**shape证明来自真实stdout**，不能把新版上游其它功能视作安装版能力。`grok update --help` 确认 `--check` 的“不安装”语义（v3 归档：[grok-update-help.txt](assets/2026-10-15-agent-management/grok-update-help.txt)，raw 文本 “Check for updates without installing”），未执行更新；v1 轮此句曾无归档，是 r1 评审 F1 的一项。

npm请求来自公共registry，无账号、无认证头；只读取公开发布元数据。没有捕获token、设备身份或用户会话。版本样本不能证明目标工具已安装、已登录、当前runtime在运行或更新兼容。

## 边界与负例

- npm字段null、缺失、错误类型、非法SemVer与HTTP429/5xx在本轮未作为活体异常捕获；它们是解析器必须拒绝的内部组件用例，不宣称这些形状由registry稳定提供。
- Grok `error!=null`、`latestVersion=null`、`updateAvailable=true`、其它installer/channel组合未取得本轮真实样本。本版只消费稳定字段类型，错误/缺项fail closed；Grok实际可更新或失败的端到端交互须在实施后真实证据里补，合成单测只能证明内部决策。改变输出generation或新增自动执行受主方案E5阻断。
- 未解析tarball/dist、作者、dependencies、嵌套metadata；不根据这些字段推导安装方式。没有跨记录身份、去重或类型等价声明。
- 本版没有ChatGPT Desktop最新发布源、native/brew Claude最新源、OpenCode非npm渠道latest样本；范围明确为unsupportedSource＋准确官方说明，不在失败时换源冒充已最新。

## 提取脚本与对账

npm使用一次顶层 `json.loads(raw)["version"]` 类型/非空核对即可；三个package独立请求，不能以一个包的结果代替另外两个。Grok为平面对象，没有嵌套、多态数组或跨记录合并；逐键类型核对与原始单行JSON对照一致，没有记录/元素计数换算。静态核验重新计算全部raw的SHA并与索引比较，查看错误样本不包含可用version。

优先级：P0禁止从示意版本、其它渠道或失效样本制造目标版本；P1实施补字段错误/HTTP失败组件测试及真实升级后重新检测；P2新增发行源先补目标版本source＋样本，不扩展宽松解析器。
