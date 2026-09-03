# Changelog

本文件记录 CordCode MacBridge 的对外可见变更，按 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 惯例组织，最新在前。

技术细节与文件级证据见同目录下每轮的 `docs/YYYY-MM-DD-<主题>完成情况.md` 及对应审计报告；本 CHANGELOG 面向使用者/维护者，记录「改了什么 / 有何提升」，不重复罗列实现细节。

版本号对齐 MacBridge Release 构建的 `MARKETING_VERSION`（见 `MacBridge/project.yml`）。日期为协调世界时（UTC）。

## [Unreleased]
- **新功能：Grok Build 多选/单选问题卡可在 iPhone 上直接作答（follower 旁观应答）**：此前 Mac 端 grok 弹出的 ask_user_question 问题卡（如「双面人的故事讲完了，下一步？」）在 iPhone 上只是只读旁观，必须回 Mac 才能继续。现在 Leader 模式下 iPhone 可直接点选项作答、勾选多选（multi_select 选项照官方语义生效）、在「type your answer」输入框自由输入（按官方 wire 形状以 Other + notes 上行）；Mac 端先答时 iPhone 卡片静默收口、不再卡在待选；Leader 模式关闭则回退只读基线（权限类交互按裁决不开放，仅 question）。断线恢复完整：杀掉 iPhone App 重开后，挂起中的问题卡会随会话重新水合回来且仍可作答——修复链含 leader 断线自动 reclaim、chat_history 重进未答题、hydrate 提交门以会话订阅为活跃信号放行挂起回合，以及水合后首个实时问题事件触发的一个 nil map 崩溃（表现为无限「正在展开会话」）。多题一次提问时各题独立作答；抢答按先到先得收口。
- **修复：Grok 选模型后发消息报「Invalid params」的深层根因：官方两种模型 id 与 iOS 回显值不一致（2026-09-02 真机二轮反馈）**：grok 官方目录用「条目 id」（如 grok-4.5）接受切换，但持久化到会话的是「底层模型 id」（如 glm-5.3）；iPhone 显示并回发的恰是底层 id——它不在官方目录里，切换请求被以 Invalid params 拒绝、消息发不出去。现在这类「目录外模型 id」不再杀掉整个回合：会话本就在用户所选的模型上时，记录警告日志后按会话当前模型继续发送，消息不再被卡死；其余失败（如会话 id 无效）仍如实报错。iPhone 端后续应改为发送模型列表里的条目 id（另案）。
- **修复：Grok 切到不思考的模型后发消息报「Invalid params」（2026-09-02 真机反馈）**：iPhone 先选过 Grok 4.6 的思考力度再切到 glm 时，力度选择会残留——发送时把「glm + high」这个官方不支持的组合原样发给 grok，被以 Invalid params 拒绝、消息发不出去。现在 MacBridge 与官方客户端同规则把关：官方目录证明该模型不支持思考力度（或所选值不在该模型的档位菜单内）时，发送前就丢弃力度、只按模型正常发出（丢弃会记录日志），模型本身无效仍如实报错；没有目录真值时不拦截、由官方裁决。
- **新功能：Grok Build 模型与思考力度（effort）选择全链路接通**：Grok 会话此前不支持选模型（模型目录为空、切换只改内存不生效）。现在 initialize 时采纳官方 `_meta.modelState` 目录——iPhone 的模型选择器直接显示 grok 实际可用的模型（含用户在 grok 配置里自定义的 provider 条目，如 glm）；支持思考力度的模型（如 Grok 4.6）会显示官方档位菜单（xhigh/high/medium/low，默认值同官方），不支持的不显示、不猜测。新建会话携带显式选择的模型/力度；恢复已有会话或发送消息前发现选择与会话当前状态漂移时，经官方 `session/set_model` 切换（无效模型/力度按官方语义报错，不静默降级）；无显式选择时不发送任何切换请求，完全尊重会话现状。模型/力度由官方持久化（重启 grok 后仍生效）。iOS 零改动——目录与档位经既有模型接口下发。
- **改进：Grok 会话列表实时刷新（leader roster 广播消费）**：Grok Leader 模式开启时，官方 leader 每次会话集合变化都会向所有已连接客户端广播 roster 通知，此前 MacBridge 直接丢弃、iPhone 侧栏只能等最多 5 秒的轮询。现在该广播会立即触发一次权威目录重扫并推送 `sessions_changed`——Mac 端新建/结束/删除 grok 会话，iPhone 列表近实时同步。广播只作失效信号：不本地套用增量，目录指纹 diff 仍是唯一真值；轮询兜底保留，无观察会话时行为不变。
- **新功能：Grok Build 行新增「Leader 模式」开关**：开启时外科手术式写入 `~/.grok/config.toml` 的 `[cli].use_leader = true`（关闭则删除该键、保留节头），其余键、注释与换行风格逐字节保留；写入前自动备份（滚动保留最多 3 份）、原子落盘、写后校验失败自动受限回滚。MacBridge 保持只读共存——不 spawn leader 进程、不抢官方锁。开关下方副文案按九态状态机提示（已开启待重启 grok / 检测到 Leader socket / socket 运行痕迹 / 已显式关闭 / 自定义 socket 路径 / 读取失败等），配置异常时开关禁用并说明原因，切换失败弹窗提示且原配置不受影响。帮助与诊断新增 Grok Leader 状态行：user 层配置值区分「未设置 / 已显式关闭 / 已开启」、socket 路径与存在性、安装版 grok 版本（含发行身份）——grok 上游改名或移除该键时症状可见（开关打开但 socket 提示不转变），不加 fallback 伪装生效。
- **改进：Grok Leader 订阅更稳健、无人观看时主动下线**：leader socket 订阅建立失败（未转发任何事件即断开）时自动回退 `updates.jsonl` tailer 并记录日志，外部 turn 观察不中断；iOS 断开后持续无订阅者超过 60 秒的会话会主动取消 observer、下线 relay，避免无人观看的长会话持续占用资源；订阅者短暂闪断不清除 relay，重开 App/会话时目录游标失效即整页重建，不残留不可证实的「未知」执行态。
- **修复：iPhone 切走 Grok 会话后 60 秒自动下线真正生效**：此前的取消判据只增不减——切到另一个会话时旧会话的观察订阅不退订，且 iPhone 打开会话产生的订阅键带目录后缀、不等于守望判据使用的观察键，导致「无人观看自动下线」在正常使用中永不触发。现切换单会话时退订旧观察键，读路径订阅统一记观察语义键；自有会话（发过消息/恢复过）不受影响，切换后继续收流。真机复测取消精确 60 秒、无虚假「已中断」，重开会话即恢复。
- **修复：iPhone 在 Grok 会话里自己发的消息不再从对话中消失**：grok 上游对用户 prompt 回显按设计不携带回合标识，此前只有旁观路径补齐了身份——iPhone 自己发起的回合里这条回显没有身份、被投影跳过，乐观占位释放后发送的消息就看不见了（自 2026-08-05 引入，非本轮新开关造成）。现在回合身份补齐下沉到会话层统一覆盖两条路径：自己发的消息与 Mac 端 grok 里的消息在 iPhone 上都稳定显示。
- **改进：Codex Desktop 会话打开与历史明细改为官方懒加载路径**：打开会话不再一次拉取全量回合与全部 items，首屏走官方 `thread/turns/list` Summary 视图，点「加载详细过程」再按 `thread/items/list` 分页拉取该回合明细（`turn_detail_chunks_v1`：明细按页渐进加载，超长输出按 128KB chunk 二级懒加载，断线后按 chunk 续载）；滚动到顶加载更早历史复用既有窗口链，上游分页游标不暴露给客户端。长会话首屏可交互时间明显缩短（owner 真机矩阵确认），单个超大回合不再整段占用传输与内存。
- **修复：旧版 Codex Desktop 会话的「用时」详情行不再整段消失**：旧会话（legacy historyMode）点「用时xx分xx秒」此前会触发必然失败的 `session_turn_items` 分页请求，iOS 按不支持处理并把整个会话的详情入口全部移除、列表锚点上跳。现 legacy 全量读到的完成回合直接标记为本地已加载（投影 v13 `detailInline`），点击只做本地展开/收起、零分页请求；legacy 会话恢复后新完成的回合同样适用。旧会话展开内容按官方 legacy 视图如实显示（官方对 legacy 线程不下发思考/工具明细，属官方面有意限制，不做补造）。
- **修复：Codex Desktop 历史详情不再把过程叙述当最终正文**：详情分页保留官方 `commentary` / `final_answer` phase；旧运行时生成的错误详情缓存由 mapper 版本栅栏按回合自动失效并从官方分页重建，避免升级后继续复用错误分类。

- **继续降低 Codex Desktop runtime 热路径 CPU**：Grok 会话标题优先按目录直接定位，避免每次目录刷新递归扫描整个 sessions 树；失效的直连 socket 不再继续作为广播目标，离线期间的重绑定尝试按会话节流；Codex Web 已接入官方目录生命周期信号并保留低频安全扫描；投影诊断改为 Debug 才计算，大回合已发布的 turn shell 后续只发送工具增量。
- **降低无客户端长回合的 runtime CPU**：当没有可接收 `session_sync_v2` patch 的观察者时，仍保留 reducer 的权威投影，但不再为超过阈值的整回合工具/问答增量深拷贝和记录大 patch；后续连接通过现有完整投影快照恢复。没有任何 live connection 时跳过逐 token 的设备重绑定扫描；无在线目标日志降为 Debug，避免流式回合产生高频日志 I/O。
- **进一步降低 Codex Desktop runtime 空闲 CPU**：Remote 不再按固定 15 秒发送 `thread/list` head 请求；改由官方 `thread/started`、线程名称/归档/删除以及 `turn/started`/`turn/completed` 通知触发权威目录刷新，并保留 60 秒安全扫描。这样目录即时性来自官方事件，空闲连接不再持续解析目录 JSON。
- **进一步降低 Codex Desktop runtime CPU**：head probe 超时现在进入独立指数退避；Remote 目录指纹忽略流式回合不断变化的 recency 时间戳，仅由会改变会话行的标题、目录、项目和成员变化触发目录事件，避免后台探测和流式事件互相放大。
- **修复：Codex Desktop 会话操作即时可见、标题一致，并消除目录轮询高 CPU**：Remote 目录发现改用官方 thread 生命周期通知触发，失败的全量刷新按独立指数退避，不再被同一旧 head 自激；安全探测降频并按官方最大页长拉取。iPhone 新建会话的标题会通过官方 thread/name/set 持久化并以 thread/read 回读，Mac/iPhone 不再分别显示 cwd 与 basename；重命名/归档响应直接携带权威 session，客户端无需再发一次慢回拉。
- **修复：Codex Desktop 配对一次即可跨重启使用**：此前配对成功时还没把 Desktop 环境写进状态，本地文件被静默跳过，重启后又变「未配置」。现配对成功即写入 Link 数据目录（0600）；下次启动刷新官方 controller token 并自动接回 Desktop，不必再填电脑配对码。恢复完成后管理状态会刷新成「就绪」；数据面断开后自动重连。Desktop 没开时保持已配对、等它上线。只有官方判定撤销才要求重新配对。
- **修复：iPhone 打开 Codex Desktop 会话显示「还没有消息」**：会话投影把该后端当成空源，1ms 内提交了 0 条 turn。现按官方 `thread/read` 的 turn 身份冷拉历史。
- **修复：iPhone 在 Codex Desktop 发消息能到 Mac App，但手机停在「正在生成」**：该后端未加入会话投影，iPhone 看不到 Desktop 的实时回复。现 Mac 用官方 `thread/read` 冷基线并广告 `session_sync_v2`，live 文本进入投影。模型目录仍未广告，输入条不弹出空模型面板。
- **改进：Codex Desktop 补齐 Mac 会话目录、历史和中断**：目录走官方 `thread/list`（分页、按工作区 cwd），历史走 `thread/read(includeTurns)`，停止走 `turn/interrupt`（含观察会话）。这条路径不套用 Codex Web 的 workspace-root 缓存过滤。iPhone 接线仍等这层在 Mac 上可测后再做。
- **修复：Codex Desktop 打开会话不再报不支持历史**：配对后的 Remote Control 数据面补上官方 `thread/read(includeTurns)` 冷基线（用户/助手正文；未取样的工具类型跳过），iPhone 点开会话可加载已有对话。模型目录仍未广告，输入条不再弹出空的「后端未提供」模型面板。
- **新功能：AI 工具新增「Codex Desktop」配对入口**：在 CordCode Link 的 AI 工具列表中出现独立的 Codex Desktop 行；未配对时显示「未配置」和「配对」。点配对后走 ChatGPT 浏览器授权，再在本机窗口填入 Desktop「控制这台 Mac → 电脑」配对码（不要把码发到聊天里）。配对成功后该行变为就绪。iPhone 产品面尚未接线。
- **安全：依赖与工具链漏洞清零（govulncheck 全绿）**：web push 依赖 webpush-go v1.3.0 间接引入的 golang-jwt v3.2.2 存在头解析内存放大漏洞（GO-2025-3553），升级到 v1.4.0（改用 golang-jwt/v5 v5.2.1，推送行为不变）；Go toolchain 升至 go1.26.6，修复 go-bridge 与 relay-server 共 13 处可达的标准库 CVE（crypto/tls、encoding/asn1、net/http、net/url 等）。升级后两模块 `govulncheck` 均 0 受影响漏洞。
- **修复：iPhone 打开正在执行的 Claude Code 会话时不再显示内部 `<task-notification>` XML**：Claude Code 的后台命令完成后会写入带 `origin.kind=task-notification` 的 synthetic user row；此前 projection/history 把它当作用户消息同步，导致任务 ID、临时输出路径和内部标签出现在聊天页。现按结构化 origin 将其作为 control-plane row 消费：source cursor 与后续执行连续性保持不变，但不生成可见用户气泡；普通用户输入即使包含相似 XML 文本也不受影响。
- **修复：Claude 任务在跑时「重启共享 Codex 服务」按钮被误禁用（2026-08-28 真机反馈）**：管理状态里的活跃 turn 计数是全局（所有 backend 合计），Mac 端却直接当作 codex 专属计数——任一 backend（如 Claude）有任务就禁用 codex 重启按钮、预检也拒绝。现 status 增加 per-backend 活跃 turn / pending 交互明细（可选新增键，旧版 runtime 仍可解码并保守回退全局计数），重启门控只数 codex / codex-web 自己的活动；全局计数继续服务于跨 backend 的 quiesce 排空语义，不受影响。
- **修复：空闲的 Claude 会话从手机续聊被误报「该会话记录的进程仍在运行」（2026-08-28 真机反馈）**：发送前检查此前把「Claude 桌面端进程开着该会话」当作占用；现区分为「进程活着」与「transcript 证明在跑任务」两级——只有后者才拦截（错误文案相应改为「该会话正在另一个客户端中执行任务」），Claude 桌面开着但空闲的会话可正常串行追加新 turn。会话列表的运行中判定同源收紧为 transcript 证明。
- **改进：DeepSeek Harness 执行中显示具体命令行（2026-08-28 真机反馈）**：工具事件此前不带人类可读摘要，iOS 运行状态条与活动行只能显示笼统的「正在执行工具」。现工具事件摘要与冷启动 hydration 同源（bash/git 命令行、读写文件路径、搜索 pattern 等，按 rune 安全截断 80 字），执行中与历史渲染一致。
- **降低 DeepSeek Harness 长回复的 projection patch 负载**：投影 reducer 此前在每个几十字节的 `text_delta` 上同时重发不断增长的完整 assistant turn，令累计 Relay/JSON/iOS apply 成本呈二次方增长。现首个内容 patch 仍携带可挂载的 turn shell，后续正文增量只发 `partOps`，单帧大小不再随累计正文增长，权威完整 projection 保持不变。真机复测证明该优化不是“时间线中途清空”的完整根因；最终定位为 iOS message-web 在流式正文跨 6000 字时热切换 Virtuoso unit 拓扑，修复归属 iOS 配套分支。
- **修复：停止生效后 iOS 不再卡「执行中」，且第一次点停止即生效**：此前停止共享 daemon 上运行中的 turn 时，若第一次请求被官方以过期 turnID 拒绝（同一会话又被另一客户端发起新 turn，本地记录的 turn 身份没随事件流更新），bridge 会删除本地会话并关闭事件监听——agent relay 从此读不到官方帧但永不退出（运行标记残留并挡住被动观察泵），官方 `turn/completed`（interrupted）无人摄入 Kernel，iOS 按钮永久「执行中」、Mac 列表只剩「待执行」占位。现共享 daemon 上中断后的会话与 relay 保留至官方收口；turn 身份改以中央泵观测流为准（事件驱动更新）；会话关闭时 relay 正常退出、官方后续帧交给被动泵，并对共享 daemon 不再合成「通道关闭」伪终态。
- **修复：iOS 发送长任务时流式正文逐段重复（如「第二个笑话」显示成「第二第二个个笑话笑话」）**：同一官方 text_delta 被两条管线各投递一次（session route 中继 + 被动观察泵），两拷贝在批处理器内合并成一份双倍增量写入 Projection Kernel，iOS 按投影逐段叠加后出现字词级重复。现按审计-008 单一摄入所有者收敛：中继会话由 relayEvents 单点摄入，被动泵只补「无中继但有观察兴趣」的会话（codex-web 外部 turn 仍需它兜底，判据是中继运行状态而非「是否有订阅者」）。Mac 端 kernel 文本从此为严格增量，iOS 无需改动。
- **修复：iOS 停止共享 daemon 上的长任务时「停止 1 秒后又变回执行中」**：注册表路径（iOS 发起的 turn）在官方 `turn/interrupt` 失败时仍合成 `turn_completed{aborted}` + `session_state_changed: idle`，投影被抢先改写为 idle；但 daemon 回合实际还在跑，继续到达的官方事件又把投影挂回 running——即「1 秒已停止闪变」。现共享 daemon 后端（codex-web / app_server 模式 codex）的 abort 只回执接受，不再合成终态，执行态等待官方 `turn/completed` 权威收口；`CancelTurn` 失败此时可见记录。私有进程后端（关闭即真实终止）保持原有合成行为。
- **修复：Mac 新建 Codex 会话 iPhone 列表不再等重启才出现（codex-web 目录同源）**：codex-web 此前因 `agent.Name()=="codex"` 的字符串分派被排除在 list_sessions / discovery fingerprint / 3s hint 的 thread/list 富管线之外，目录指纹与 Mac 侧不同源；Mac 新建会话时 `sessions_changed` 触发面脱节，iOS 只能靠重启刷新。现 codex-web 实现 `FetchThreadList`/`FetchThreadListHead` 与 codex 共用同一 catalog seam，三处分派改为能力断言，3s 头部提示对 codex-web 同样生效，发现日志同时打印过滤前后计数（raw/filter）便于核对。
- **修复：codex-web 长任务待办可见，停止真正生效**：codex-web 此前不实现 todo 查询接口，iOS 的 fetch_todos 恒返回 `not_supported`，task dock 只在恰好有投影文本解析的瞬间出现。现 Mac 端缓存官方 `turn/plan/updated`（EventPlan）并应答真实待办（会话删除时清缓存）；iOS 停止 Mac 发起的 turn 时，注册表缺失的观察会话按 threadID 直达官方 `turn/interrupt`（turnID 取自观测流），不再静默 Ok。中断 ACK 只表示请求被接受，不再合成完成/idle；执行态等待共享 daemon 的官方 `turn/completed` 权威收口。
- **新功能：Codex Web 行新增「重启共享 Codex 服务」按钮**：cc-switch 等工具改完 `~/.codex/config.toml` 后，运行中的共享 daemon 不会重读配置（进程内副本），切换 provider 需要让 daemon 重启才是生效杠杆。按钮执行官方 `app-server daemon restart` 并在控制 socket 恢复后提示；有任务正在执行时禁用；重启后 Codex 桌面若未自动恢复会提示完全退出并重新打开。检测到 `config.toml` 变更时，该行会先行提示「重启后生效」。
- **改进：工作站主操作按钮不再做扫光/呼吸**：配对主按钮的持续动画（1.5s 扫光 + 1.8s 呼吸循环）长期占用 CPU，改为静态样式，仅保留悬停反馈。
- **修复：codex-web 会话目录（thread/list）不再因 `section` 字段类型变化整体解码失败**：官方 0.149.0-alpha.4 的 `section` 实测是 `{id,name,appearance}` 对象（部分线程为 `null`），旧解析器只接受 string，导致会话发现轮询每次解码失败（`no broadcast`）、iOS 会话目录长期无法实时刷新。现已按真实 wire 样本兼容对象/字符串/null 三种形状。
- **修复：重启 Link 后 iPhone 仍开着的 Codex 会话应立刻旁观**：`set_observation_scope` 不再无条件返回成功。观察连接未就绪或 Desktop 持有私有 writer 时会失败可见；成功则表示已 Subscribe 并 attach。零目标窗口内仍观察的会话会继续进入投影，`turn_completed` 会进入 LAN 回放缓冲，长回合收尾后不应只剩正文、输入框一直执行中。
- **修复：退出 CordCode Link 不应要求重启 Codex Desktop**：官方 daemon 改为登录级座位（LaunchAgent 以 KeepAlive 循环跑幂等的 `daemon start`，0.25s 补位），随登录存在、不随 Link 退出。Desktop 已附着后，停掉 MacBridge 不再把 daemon 带走。官方 Desktop 一旦 `daemon version` 探测失败会把 transport 锁死成私有 stdio，之后再也不会试 websocket——那是异常恢复，不是退出 Link 的正常步骤。Desktop 与 standalone 的 CLI 小版本不一致不再挡住座位安装。
- **修复：Codex Web 在 iOS 发完一轮后收不到 Mac Desktop 的实时流**：iOS 自己的转发协程在 `turn_completed` 后立即退出，而观察连接又没有在 iOS 打开会话时 resume 该 thread。现让 `codex-web` 的 live relay 跨回合常驻，并在 `set_observation_scope` / `thread/started` 上把观察连接 attach 到同一 thread。
- **修复：MacBridge 重启后 iOS resume 报 active writer、Mac 看不到新 session**：`-32600 already has an active writer` 有两种真实成因——Desktop 已脱离共享 daemon 进入私有 stdio（拓扑分裂，需完全退出并重开 Desktop），或 Desktop 仍在共享 daemon 上但**打开着该会话**（writer 座位按进程独占，属官方预期，见架构文档 §12.2）。冲突文案与日志已按实测口径修正，不再把后者误判为前者。
- **修复：点击「重启共享 Codex 服务」后 iOS 显示「无法加载会话投影」且 Mac 新消息不同步**：daemon restart 把 go-bridge 的既有 RPC 连接变成「死而不通知」（写出即 `broken pipe`），而 `withClient` 的断线判定漏认 `broken pipe`，导致 projection 水化、会话目录、发现指纹、模型清单全部停在死连接上，直到重启 MacBridge。现已按 `broken pipe`/`connection reset`/errno 级别识别连接丢失并在下一次调用时重新探测；重启共享服务后的短暂空窗内 MacBridge 自动恢复，iOS 不必再依赖「重新打开会话」。
- **修复：重启 MacBridge 会弹出 DeepSeek Harness 3080 网页**：官方 `dsh web` 默认打开浏览器。托管启动现带 `--no-open`，只在本机 3080 补座位，不再抢前台。
- **修复：Codex Web 会话重命名误报不支持**：官方 `thread/name/set` 与 `thread/read` 确认链路已经存在，但 driver 漏接 Bridge 的 `SessionRenamer` 能力，请求在进入 app-server 前就被拒绝。现重命名直达官方写面，并以随后读取到的官方 thread 元数据作为唯一返回真相，同时即时刷新会话列表。
- **修复：Codex Web 在项目目录新建的会话跑到用户根目录**：`create_session`/`send_message` 已携带项目目录，但 codex-web driver 漏实现 Bridge 既有的工作目录切换接口，官方 `thread/start.cwd` 因而恒为 Link 启动目录 `/Users/<用户名>`。现请求目录会原样进入官方 `thread/start`，会话列表继续按官方 `thread.cwd` 分组；Chat 中新建的会话会留在 Chat，不依赖客户端重分组或缓存纠偏。
- **修复：Codex Web 连续发送时第二条用户消息不显示**：官方 `item/started` 已携带每轮 `userMessage` 的完整正文和 `threadId/turnId/itemId`，但 live codec 此前只转发 assistant delta，导致首轮靠冷水合偶然补回、后续轮只见回复。现按官方事件语义在 `item/started` 唯一投影用户消息，`item/completed` 保持静默防止双发；连续多轮均由同一 Mac Projection Kernel 实时归并。
- **新功能：Codex Web 独立并存 backend**：CordCode Link 默认同时注册旧 `codex` 与新 `codex-web`。新入口只读官方 app-server lifecycle/catalog/history/live 事件，模型与 reasoning effort 来自官方目录；审批与批量用户输入经官方应答帧收口。backend/wire/config/cache identity 全部独立，旧 Codex rollout 路径和行为不变。
- **修复：OpenCode Web 重启后历史没有思考过程和工具调用**：官方消息里工具 part 是 `tool: "read"|"edit"` 加旁边的 `state`，跟 Desktop 时间线同一份 `GET /session/:id/message`。冷拉却按旧 adapter 把 `tool` 当成嵌套对象，断言失败就把全部工具卡丢掉——直播走 SSE 所以当场看得到，杀 App 再进只剩正文。现按官方 ToolPart 映射（含文件路径、edit diff），思考块继续进历史；`todowrite` 仍只走任务卡、不进时间线。
- **修复：OpenCode Web iPhone 会话列表目录远多于 Desktop**：官方首页侧栏不是 `GET /project`（那是服务端注册表，本机 31 行），而是 Desktop 本地打开的 tab（`Persist.global("server").projects[serveURL]`）。iPhone 误把注册表当首页，所以 17 个目录对不上 Desktop 的 6 个。现只列出该 4096 服务在 Desktop 里打开的 worktree，每个目录仍走官方 `session.list({directory, roots:true, limit})`。
- **修复：OpenCode Web 工程注册表变更要等发现轮询才进 iPhone 列表**：官方 SSE 的 `project.updated` / `catalog.updated`（Desktop 打开目录时先发这两类，会话可能还没有）此前被忽略，目录缓存要等到下一轮 discovery 才失效。现与 `session.created/deleted` 一样立刻重扫 catalog。
- **修复：OpenCode Web 空闲后再发消息整条实时流静默断裂（2026-08-21 真机）**：会话被空闲回收后，转发协程仍堵在从未关闭的事件通道上；下一次发送会再建一条 SSE，同时把旧会话再 Close 一次。第二次 Close 把刚建好的新流引用减到 0 当场拆掉——iPhone 只看到「正在生成」，Mac 上同一回合其实跑完了。现改为 Close 只释放一次，并在关闭时结束事件通道，空闲后再发能继续流式。
- **修复：OpenCode Web 已开会话发消息卡死、正文只同步半截（2026-08-21 真机回归）**：推理模型每轮都输出思考内容，而 live 流把「已识别的 populated reasoning」按 E2 判定发成错误事件；go-bridge 对该错误一律按终态处理——回合被判失败、实时转发链路当场拆除。结果：Mac 端正常流式收尾，iPhone 端正文只到一半、会话一直卡「执行中」。现改为：live 思考内容保持不翻译（等有同版本 SSE 取证再决定是否上屏），但绝不再中断回合——正文完整流式、回合正常收口；历史里的思考块仍按 directive-014 正常显示。
- **新功能：OpenCode Web 官方 parity 集中实施（指令 8 号批次）**：以 canonical 收敛设计为唯一权威，一次批次补齐 C2–C7：
  - **发送即带官方选项**：`send_message` 现在逐请求携带 agent、`{providerId,id}` 模型和模型专属 `variant`（不是 reasoningEffort）。Mac 在请求内生成一次稳定 `messageID`；不在目录里的模型/agent/variant 在 POST 前就明确失败（零网络写入）。图片/文件附件按官方 file part（data URL）随 prompt 上送。
  - **官方模型选择链**：current → agent 模型 → provider default（优先于 legacy `/config.model`，E5b 钉死）→ config → 最近会话模型 → 首个已连接 provider 默认/首模型；`list_models` 模型行带 live `variants` 键（E1b），选择器只在有键时出现。
  - **单一全局 SSE 订阅**：每个 backend 实例只开一条 `/global/event` 连接，事件按 sessionID 路由到已打开会话；外部网页 turn（E3）经同一条流实时旁观；嵌套 `sync` 帧在归一化前精确跳过一次（官方 server-sdk 同规则）；断线重连后同一条订阅/路由继续，armed 回合经 `GET /session/status` 治愈，绝不伪造健康终态。
  - **reasoning live 不翻译且不毒化回合**：E2 取证证明 1.18.18 无已验证的 populated reasoning 形状——live 流遇到即跳过（不折叠进正文、不上屏思考），历史 hydrate 按 E2b 正常映射；绝不再因它中断回合。
  - **问答/待办/改名**：官方 `question.asked` 一次性翻译为结构化用户输入卡（回答走 `POST /question/:id/reply {answers:[[label]]}` / reject，首个服务器裁决生效）；`GET /session/:id/todo` 顺序/字段原样（不造 ID）；`PATCH /session/:id {title}` 改名收敛到返回元数据。
  - **严格 project 解码**：`GET /project` 只接受已验证的裸数组形状，畸形行整体失败（不再静默裁剪）。
  - **能力如实点亮**：todos、structured_user_input_v1、session_mutation、session_delete、permission_resolve、image/file 附件、external_turn_streaming 随实现广告；E2 reasoning 与 OD-3 十四项未来面保持不广告。
- **新功能：OpenCode Web 会话归档与删除**：消息页「更多设置」里的「归档」「删除」此前报 `session archive not yet supported` / `backend does not support session deletion`（设计期两项挂起：delete 需活体钉死 HTTP、archive 列为二期）。现已在真 1.18.18 沙盒活体钉死官方路由并实现：归档 = `PATCH /session/{id}` body `{"time":{"archived":<epoch ms>}}`（200 回显 Session.Info）；删除 = `DELETE /session/{id}`（200 `true`，删后 404）。v2 同路由走 `/api` 前缀。列表行带 `time.archived` → `archivedAtMillis`，客户端隐藏已归档会话。
- **修复：OpenCode Web 新会话首回合输入框闪「完成」**：冷投影把还在跑的第一回合写成 `execution.phase=idle`。已拆掉「历史 0 条就 200ms×6 再拉」的等待（违反 SSV2 护栏：空投影先等 history、用条数猜 ready）。活会话未收口 user turn 现在提交 `phase=running`；hydrate 不得用冷 idle 盖掉已经 live 的 running。pending→real 早 rebind 保留。
- **新功能：OpenCode Web backend（并存入口）**：新增 `opencode-web` backend——官方 `opencode serve` 的纯 HTTP/SSE 客户端。iPhone 切换框出现「OpenCode Web」：列表/历史/占用/发送/模型/活跃态/审批全部来自官方 HTTP。占用圈按官方网页公式（最后一条 assistant ÷ 模型窗口）出数、发送必带目录内模型（坏模型明确失败不再 81ms 空转）、外部网页 turn 实时旁观、工具审批 Allow/Deny 走官方折叠（绝不自动批）。旧「OpenCode」入口原样并存供对照，成熟验收后再摘（详见 `docs/2026-08-18-opencode-web-backend-design完成情况.md`）。
- **改进：Claude / Grok Build / OpenCode 上下文圈能出数**：点 ⭕ 不再三家都是「暂无」。Grok 读会话 `signals.json` 的占用和 500K 窗口；OpenCode 读 `GET /session` 的 tokens + 模型 `limit.context`；Claude 用最后一条 assistant 的 prompt occupancy（input+cache）加模型窗口（200K / 1M）。不是 DeepSeek 那种拆分表，只做已用/窗口。
- **修复：长 DeepSeek Harness 会话 iPhone 打不开投影**：官方 history 按「消息数」分页，一条消息里可能有几千条 chunk。以前一页要 2000 条消息，Exec plan 那种会话单页 55MB，超过 32MB 读取上限后 JSON 被截断，iPhone 显示「无法加载会话投影」。现按官方默认每页 50 条消息，超大页会自动缩小再拉。
- **改进：DeepSeek Harness 已开会话显示当前预设**：列表和打开会话时带上官方 `agentPreset`，iPhone 标题旁能看到「标准模式 / PTC模式 / 极简模式 / 创造模式」，和官方 web 标题左那枚芯片一样。
- **新功能：DeepSeek Harness 新建会话可选官方 Agent 预设**：iPhone 空白页列出标准 / PTC / 极简 / 创造，创建时传给官方 `session.create`。开聊后锁定。
- **改进：DeepSeek Harness 会话统计进上下文表**：点 ⭕ 除了已用百分比和拆分，还能看到官方 StatsLine 的轮次/步数、LLM/工具耗时、首 token、吞吐、缓存命中和计费 in/out。数据来自官方 `sessionStats`/`tokenUsage`，不在 iPhone 上自己加。
- **改进：DeepSeek Harness 用官方鲸鱼标**：工作站工具行图标换成官方 `dsh web` favicon，尺寸和现有 Claude/Codex/Grok/OpenCode 标记一致。
- **改进：DeepSeek Web 对外显示为 DeepSeek Harness**：hello_ack 显示名改了，wire kind 仍是 `deepseek-web`，driver 仍是 `dsh-web`。
- **改进：产品入口不再提供旧 DeepSeek（SDK）模式**：默认只留 DeepSeek Web。旧 `agent/dsh` 源码还在，需要时仍可显式挂上，但 iPhone 上不再出现「DeepSeek」和「DeepSeek Web」两个入口。
- **修复：DeepSeek Web 点「查看更多」会列出所有工作区**：深挖只该看当前目录。此前列表接口没按目录过滤，cordcode-ios 的「查看更多」里会混进 Chat 的会话。
- **修复：DeepSeek Web 在 Chat 目录新建会话却进「未分组」**：官方只有带工作区 id 的创建才会写入该文件夹名单，只传目录路径不会归组。iPhone 在 Chat 点新建现在会带上对应工作区 id。
- **修复：DeepSeek Web 会话列表分组与官方 web 不一致**：官方按工作区名单归组（不在名单里的进「未分组」，归档的不显示），iPhone 之前按目录路径归组，所以 Chat 目录下的会话会被并进 Chat，归档的也会露出来。现跟官方同一套名单。
- **修复：DeepSeek Web 打开旧会话 iPhone 看不到上下文圆环**：用量是打开后再拉的，圆环不能等数据才出现。`get_session` 现在带上官方上下文用量，iPhone 打开会话和点圆环都会刷新。
- **改进：DeepSeek Web 上下文窗口对齐官方 web**：点输入条上下文钮不再是「暂无用量」。现读官方 `contextPressure`/`contextBreakdown`，显示已用百分比、`~41.2K / 1M` 以及系统提示词 / 工具 / 对话消息拆分。
- **修复：DeepSeek Web 选权限不再往消息页发 `/permission`**：官方齿轮走宿主斜杠命令通道（不进模型、不出现用户气泡）。此前误走了发消息接口，模型会回「我无法更改沙箱/权限策略」。现与官方 web 同一条写面，并记住默认。
- **修复：iPhone 多选卡点选项没反应**：DeepSeek Web 明确广告结构化问答能力，iPhone 提交/跳过才能真正回给 Mac。
- **修复：Mac 弹出的多选问答框 iPhone 看不到**：DeepSeek Web 的多选题（可同时勾几项）只在官方 web 出现。根因是只发了旧的 `question_asked`，投影内核按规定不吃这个事件，iPhone 消息页没有问答卡。现改为走权威的结构化问答，并带上「多选」标记。
- **修复：Mac 选完覆盖后 iPhone 不出现权限条**：文件已存在的选择题在 Mac 上选「覆盖」后，Mac 会弹出写文件/提权框，iPhone 没有。根因是 codec 重置后的新 turn 只在 Mac 内存里记一笔、不推 turn 壳，iPhone 收到 `upsert_tool` 却对不上 turn，直接丢掉。现把所属 turn 和权限工具一起推出去。
- **修复：DeepSeek Web 选择题只出现在 Mac**：文件已存在时的「您现在想让我做什么？」只在官方 web 弹出。现同样送到 iPhone（输入框上方问答卡），任一侧提交或跳过后两边都收。
- **改进：DeepSeek Web 审批只留 iPhone 原生条，Mac 发起也能批**：消息页不再叠一张前端权限卡；原生「需要权限」条加高到两行以展示 escalate sandbox 原因。Mac web 发起的 turn 同样把审批送到正在看的 iPhone，任一侧点允许/拒绝后两边都收。
- **修复：DeepSeek Web 第二轮审批（删文件/提权）iPhone 不再弹出**：第一轮写文件结束后转发协程就退出了，空闲回收又关不掉旧事件通道，下一轮审批发到新会话却没人转发。现 turn 结束后继续听、Close 会关掉通道，删文件这类沙箱提权也会上屏。
- **修复：DeepSeek Web 审批在消息页看不见、点允许后任务验收不收**：权限卡被折进过程组只剩「等待授权」摘要；允许之后投影仍是 pending，任务验收权限页留着。现消息页在输入框上方弹出允许/拒绝；允许或拒绝后投影清掉待批准状态，卡片收起。
- **修复：DeepSeek Web 审批二次仍不上 iPhone**：权限事件即使送到手机，SSV2 也会用投影重画消息页，而投影里没有权限标记，卡片立刻被盖掉。现把 `permission_request` 写入投影（pending tool + `requiresPermissionConfirmation`），iPhone 从投影画审批卡。
- **修复：DeepSeek Web 写文件审批只出现在 Mac web、iPhone 没有**：官方 `approval/asked` 审计事件被码器当未知类型重置；SSV2 把 `permission_request` 误判成「已进投影」而丢掉（reducer 其实不吃它）；审批等待期间 `relayEvents` 60 秒空闲超时又把 turn 自动收口。现改为：审计事件忽略、权限/问答仍走 raw 投递、dsh-web 关闭空闲超时。
- **修复：DeepSeek Web 复测仍不流式（Mac web 在出字、iPhone 不渲染）**：Mac 已推完整 projection_patch，但 iOS 投影渲染开关漏了 DeepSeek Web（与当初 DeepSeek 同一类漏列），补丁被泵丢掉、界面仍等 raw live event；Mac web 旁观只有 passive/patch，iOS 发送后也不渲染。另：刚 StartSession、kernel 仍空时旧条件把 live 会话收成空基线（首张 snapshot 只有 1 个 turn）。修复：iOS 把 DeepSeek Web 列入 SSV2 投影族；Mac 仅在已有 kernel 时走 live-only，空 kernel 先用官方 history 播种。
- **修复：DeepSeek Web 真机首测 live 流断裂（执行态卡住、切会话冷重建才见回复）**：根因是 iOS 在 live turn 期间冷拉投影时，pathless 历史重建产出落后基线（在飞 turn 未入 history 快照、turn 身份与 live 流不同源），fence 把 kernel 版本回退后 live 补丁身份脱节不渲染（并诱发重建循环）；另 web profile 的控制面事件（命令执行标记、标题生成请求）触发码器重置。修复：live/kernel 会话一律以 kernel 状态为投影基线（与旧 DeepSeek 收口同构），重建只服务首次打开与脱活会话；三类控制面事件入已知忽略清单。


- **新功能：DeepSeek Web backend（dsh-web，官方 Web API 转发 + bridge-v1 翻译）**：MacBridge 新增 `deepseek-web` backend——探测复用用户自启的 `dsh web`（默认 127.0.0.1:3080），未命中时托管拉起 loopback 实例（3096–3196，随 Link 启动保活，凭据零记录）。所有数据格式转换在 MacBridge 端完成：官方 `session.list/history/prompt/cancel/rename/models/providers/selectModel` 映射为 bridge-v1 成熟格式，mux/host 双 WebSocket 流全量推送（用户在 Mac web 发起的外部 turn 在 iPhone 实时可见，无需轮询），web 新建会话/外部 turn 秒级同步到 iPhone 列表；审批/问答经既有权限 UI 应答（官方 ask 策略下不接会无限挂起——一期必接）；投影冷加载走 pathless 家族（官方 history 重建基线），旧 `deepseek` backend 完整保留。iOS 端为纯枚举增量（「DeepSeek Web」模式入口），零新渲染逻辑。


- **修复：iOS 新建 DeepSeek 会话 turn 失败于模型名（真机第二轮）**：rc.6 官方路由只支持 `deepseek-v4-pro/flash`，driver 旧默认 `deepseek-chat` 越界（报错原文「The supported API model names are deepseek-v4-pro or deepseek-v4-flash」）。默认与模型列表对齐 v4 两档，旧模型名自动归一（iOS 缓存防御）。
- **修复：失败 turn 后消息页投影卡「执行中」（hydrating 循环）**：活会话被误推向文件重建与 live 流竞争、且失败 turn（仅用户消息无回复）的尾部未答 turn 永不封口。基线选择改为 live/kernel 优先（验证过的 admission 路径）、文件重建只服务死会话；driver 实现活会话探测（死会话尾部未答 turn 如实封口）。

- **修复：iOS 新建的 DeepSeek 会话发送后立即失败（执行中无回复、Mac 端 dsh web 看不到）**：driver 的会话存储编码（明文）与 dsh web 写入用户 store 的 zstd 工件冲突——harness 在物化会话时拒绝混用编码的存储根。driver 改用与 web 一致的 zstd，新建会话正常写入 `~/.dsh/sessions`（Mac/手机双向可见恢复）。

- **新功能：iOS 端 DeepSeek 会话列表与历史（store 桥接）**：Mac 读取用户自己的 `~/.dsh/sessions`（dsh web 与手机自建的会话同列，明文与 zstd 双格式、标题取自 harness 的 session/title 事件）——iPhone 上 DeepSeek 模式现在有完整会话列表；点开任意已结束会话可查看完整历史（含思考过程与工具调用），file-backed 投影冷加载。已结束会话内发消息得到诚实提示（当前 DSH SDK 不支持跨进程续聊，可发起新会话），绝不覆写磁盘会话。

- **改进：CordCode 起的 DeepSeek 会话写入用户 harness 默认存储（双向接力前半）**：`DSH_SESSION_ROOT` 从 MacBridge 私有目录改为 `$DSH_HOME/sessions`（默认 `~/.dsh/sessions`）——手机上发起的 DeepSeek 会话直接出现在 Mac 端 `dsh web` 的会话列表里、可在 Mac 端续聊。不再造隔离的私有 session 目录；仅 HOME 解析失败时防御性回退。

- **修复：DeepSeek 消息页死寂（turn 完成但 iPhone 永久「执行中」无回复）**：live-only 会话正式迁移 SSV2——投影基线 = kernel 中 live 注入累积的权威状态，经独立 live-only admission 事务原子就绪（复用既有 hydrate 事务/fence 串行化，无磁盘历史源、无并行写路径）。三层缺口同修：投影 admission 路径（此前恒回 not_migrated 导致 iPhone 拿不到基线、补丁无 ownership 不渲染）、per-backend session_sync_v2 capability 广告、iOS 投影渲染开关。会话已死且 kernel 无痕（如 Mac 重启后重开）→ 诚实新错误码 `projection.not_found`（协议已入册）；kernel 有状态的死进程会话照常服务最后已知状态。顺带：观察心跳不再为 live-only 死会话每次续租空转 relayEvents。

- **修复：切到 DeepSeek 模式会话列表直出错误文案**：live-only 后端（无历史列表）此前把 wire 的 not_supported 原样渲染成「会话加载失败」横幅。现在列表直接呈现空态与一句提示（实时模式：直接发消息开始新会话，无历史列表），且不再对该后端发起任何会话列表请求；三处隐藏入口（会话目录补全扫描、目录加载 list_projects、查看更多分页）一并门控；同时新增通用兜底——任何后端的列表请求返回 not_supported 都显示空态而非错误（其他真实错误仍正常报错，不掩盖故障）。

- **装了 DeepSeek Harness 即零配置可用（探测-复用，永不代装）**：CordCode Link 只探测用户已装的 DeepSeek Harness——`npm i -g @deepseek-ai/dsh` 是第一公民（SDK stdio 层由 MacBridge 内置 vendor，运行时经影子 node_modules 复用你全局安装的 runtime 全家桶，不安装/不下载/不碰你的全局目录），PATH 上的 `dsh-jsonrpc-agent`、pip wheel、nvm 同样支持；DeepSeek key 直接沿用 `dsh` Web UI 存好的凭据（`~/.dsh/.credentials.yaml`，或 `~/.dsh/.env`），MacBridge 显式 provider 配置仍最优先。未装任何形态时 backend 如实显示未启动。诊断面板显示 runtime 与凭据来源。
- **新增 DeepSeek Harness（DSH）backend（设计 v13 / round12 APPROVE 全量落地）**：CordCode Link 新增 `deepseek` backend，桥接 `dsh-jsonrpc-agent` stdio JSON-RPC runtime（每 session 一进程、进程组回收）。事件面完整映射 turn/step/user/assistant chunk/tool/todo/usage；`user/message` 按 `source.kind` 分流（权限运行时上下文不再覆盖真实 prompt）；TurnID 携带每进程 128-bit nonce，进程重启永不串轴；seq 完整性 fail-closed（首帧必须 0、gap/倒退/冲突重复即终止）；subagent/foreign session 通知按 lineage tombstone 路由（迟到 child 事件不误杀）；错误二分——模型/turn 级错误只收口 turn 保留进程，协议损坏先发可见 terminal 再淘汰进程；at-most-once 交付（只有可证明未送达的 pre-write 允许重建后发送一次，其余 fail visibly 不重放）。live-only：无 session 列表/历史（list 返回 `not_supported`）；一期 text-only。要求 Mac 安装 `dsh-jsonrpc-agent`（缺失时该 backend 如实不出现）。
- **附件发送全面收紧为两级 pre-StartSession 校验**：`send_message` 附件现按「raw 结构（kind 词表 / 裸 `type/subtype` MIME / 非空可解码 base64 / 混合整条拒）→ `invalid_params`」和「effectiveKind（kind∨mime 单一分类规则）× backend 正向能力声明 → `unsupported_attachment`」在任何 session 副作用之前整条校验。各 backend 能力同步为语义真相：Claude/Codex image+file；OpenCode CLI 声明 image、managed server 不声明（该路径图像本就静默丢失，拒绝即现状语义化）；Grok Build file-only；DSH text-only。此前畸形 MIME/空 base64 会被静默丢弃流入 driver，现在诚实拒绝。协议章节已入 canonical pack 并同步 iOS mirror。

- **会话列表不再把「查不到状态」谎报成「空闲」（登记簿 F-8）**：session 列表与 claude 详情的状态富化此前把「确实查不到」（进程探测不可用且无 registry 记录、claude transcript 尾部无可判定条目或文件找不到）一律强转为 `idle`。现在这些情况产出 `runtimeState="unknown"`——客户端不渲染状态徽标（不知道就不亮灯）。已知状态（running/idle/requiresAction 等）显示不变；「registry 说 running 且无法复核」的既有回退维持现状。协议值域已入册（unified-bridge-protocol.md §6.1）；remote-web 零消费、零改动。

- **Grok 断线不再把结果未知的 turn 猜成「已完成」（登记簿 F-7）**：Grok 任务的 leader 观察通道异常断开且 turn 未收到完成信号时，此前直接补发 `idle` 状态，客户端据此把 turn 收口成完成——断线重连后任务可能仍在跑或结果已丢失，用户却看到「已完成」。现在断开时先合成 `turn_aborted(leader_disconnect)` 中断事件（复用协议既有 turn 中止语义，与 codex 死进程处理对齐），再补 `idle` 收口执行态；客户端（iOS 已同步支持）以「中断」收口、不假装完成。正常完成/错误路径不受影响。

- **Codex/Grok session 目录与原生客户端保持同源并主动刷新**：CordCode Link 的已声明 v2 列表、旧客户端 v1 列表和后台 `sessions_changed` 探测现在共用各 backend 的原生可见成员集；目录清空、标题/排序/工作区变化会先使旧分页快照失效，再通知 iPhone 刷新。Codex/Grok 不再用磁盘扫描补齐原生列表，旧客户端仍保留 v1 cursor 成功契约；OpenCode/Claude compatibility 未删除。控制面通知不进入 session timeline，原生读取错误会保留上次成功状态并等待恢复，不伪造空列表。

- **文件读取协议硬切为 `read_file_v2`**：CordCode Link 删除旧 `read_file` 的 dispatch、scope、capability、Relay 分类、codec 与 fixture，只保留带 workspace/session owner 的严格授权读取。旧客户端调用旧方法会收到统一 `method_not_found`；Relay 仅将 v2 归入 bulk，并继续支持分块、公平调度与取消。iOS/Web 同步移除 fallback 与旧 API，最低兼容版本随此次变更抬升。

- **RPC 按方法鉴权 scope（§6.3）**：每个后端 RPC 映射到 7 个 scope 之一（`session.read`/`session.write`/`config.read`/`config.write`/`workspace.read`/`workspace.mutate`/`delivery.manage`），`go-bridge/rpc_scopes.go` 为单一真相源。校验落在 `CapabilityPolicy.AuthorizeRPC`（HandleRPC 单一漏斗，先于 dispatchRPC 与所有 switch 外方法路由）。配对设备默认拥有全部 scope（向后兼容，不改变现有授权语义）；受限调用返回稳定错误码 `forbidden`。新增 RPC 必须声明 scope，否则 CI guard `TestEveryDispatchedRPCHasScope` 编译期失败。
- **hello/hello_ack 携带 scope（§6.3 additive）**：`hello` 增可选 `requestedScopes`，`hello_ack` 增可选 `grantedScopes`（回显设备实际拥有的 scope），供客户端 UI gating；旧 client 不受影响。`TrustedDeviceRecord` 增 `grantedScopes` 字段，nil/空视作默认全集。
- **Driver 自描述 wire 属性（§6.2 零跨层抽象）**：claudecode/codex/opencode/grokbuild 各自通过 `WireDescriptor()` 自报 Kind/DisplayName/LiveEventModel/Polling/StaticCapabilities，`BuildAgentDescriptor` 优先读自描述、仅未迁移 driver 才回退 id-keyed switch。新增 agent 不再需要改 wire 层 switch。
- **修正 id drift 致 claude 漏报能力**：迁移前 `backend_capabilities.go` 的 `id=="claudecode"` 判等在生产 backend id（`claude`）下从未命中，claude 实际未广告 `content_chunking` 与 `question_reply`。迁入 driver `StaticCapabilities` 后 claude 按设计恢复广告这两项（与既有注释意图一致），生产 descriptor 与设计/测试假设对齐。
- MacBridge 自有 Claude session 的 AskUserQuestion 已接通生产 responder：iPhone 可回答选项、Other 自定义答案或跳过；同一 interaction 由单一 claim/投影链路收口，多端竞态不会重复写入。
- `resolve_user_input` 现在返回完整 interaction/status/revision acknowledgement，客户端会等待权威 projection 决定终态；Claude 外部进程仍持有 session 或归属检查失败时，续接会在启动第二个 worker 前明确拒绝并允许人工重试。

### 2026-08-02 — Claude Desktop AskUserQuestion transcript projection

- Claude Desktop 外部会话的 `AskUserQuestion` transcript 现在按 `structured_user_input_v1` 投影为 `observe_only` 的 `user_input_requested`，不再退化为普通 `tool_started`。
- 外部会话保留 Claude 原生的 Other 自定义答案能力，但明确为只读列表；待回答期间 execution 保持 `requires_action`，iOS 输入区不会再错误显示为已完成。
- Claude transcript 的 `toolUseResult.questions/answers` 只用于识别原地收口为 `user_input_resolved`，答案正文不进入 projection。
- checkpoint schema 升级后会丢弃旧的错误投影并从 transcript 重建；同时补齐 canonical Bridge v1 schema，修正 Codex 文本片段拼接和完整 Go 回归暴露的租约测试时序。

### 2026-07-31 — 修复 iOS/web 端 Claude 已完成 session 重开时消息重复出现两次

- Claude session 经 cold hydrate 重建投影时，不再把上一份投影（live 的 row-UUID turn）作为 baseline 叠加在 rich-history builder 重放之上。此前两套 turn-id 方案（live row-UUID 与 builder `user-line-N`）无法归并，同一份内容在两个 id 下各落一个 turn 并写进 checkpoint，重开经 AlreadyReady 直接返回这份陈旧重复，表现为「切走再切回仍重复两次」。Mac 端渲染本就不消费该投影，故一直正常。
- pathless rich-history 后端（Claude/OpenCode）现在恒从空 reducer 开始，builder 重放是唯一 baseline；Codex 文件型 pathless 维持原有内存 carry 不变。
- bump checkpoint schema 4→5，所有已污染 checkpoint 下次重开自动作废重建，历史重复自愈。

### 2026-07-22 — Codex 外部任务保留稳定 turn 身份与真实完成边界

- Codex rollout 的 `task_started.turn_id` 现在贯穿 `turn_started`、内容增量、`turn_completed` 与 rich history；客户端可按同一身份增量归并一轮任务。
- Codex rollout 写入外部用户问题时会发送 `user_message`，携带 response-item 的稳定消息 ID 与当前 turn ID；客户端不再需要等 history reconcile 才能显示 Mac 端输入。
- 读取仍在写入的 rollout 到达 EOF 时不再伪造完成时间或把最后一段过程提升为最终回答；只有源文件出现 `task_complete` 才建立完成边界。
- transcript 分页索引把 `task_started` / `task_complete` 纳入同一 span，分页 replay 不会丢失身份与终态证据。

### 2026-07-15 — 修复首页安全连接状态停留在暂不可用

- 首页首次读取 Relay 状态遇到启动期短暂失败时，会在三秒后自动重新读取一次；读取成功后立即显示真实状态。
- 避免 iPhone 已通过蜂窝网络正常使用加密远程连接时，Mac 端仍长期错误显示「Relay 状态暂不可用」。

### 2026-07-14 — 工作站状态操作重排

- 工作站首页改为聚焦的单列状态面板：连接结论、配对、设备和工具状态均采用统一对齐节奏。
- AI 工具改为全宽状态行，直接展示状态并为异常工具提供行内“重新检测”入口；不再重复显示独立的“需要留意”区。
- Relay 未就绪时，在“安全连接”行直接提供“连接状态”入口；正常状态保持安静、紧凑。
- 首页采用独立的 1100pt 聚焦内容列，不随全屏窗口横向拉散；“重启 / 停止”改为带图标的描边次级操作，配对保留唯一蓝色主操作。
- 工具状态固定为图标、名称、居中状态与操作四段，状态不再漂到窗口最右侧；配对按钮收回到设计稿的紧凑尺寸。
- 恢复原生标题栏文字，移除会被系统包成胶囊的居中标题；背景调整为低饱和的深蓝灰与暖灰弱光，不再出现明显的双色分区。
- 进一步对齐设计稿的标题栏与信息层级：标题改为无胶囊背景的居中标识、隐藏顶部快捷按钮；连接结论采用空心状态图标的纵向布局，设备元信息使用中点分隔。
- AI 工具行改为品牌化矢量标记、名称、真实状态与行内操作四列；可用/不可用状态分别以绿色对勾与橙色感叹号表达，操作仍调用实际检测接口。

### 2026-07-14 — 配对二维码可读性

- 将配对弹窗中的二维码放大 50%，便于从稍远距离快速扫码。
- 移除二维码下方重复的“返回”按钮，统一由弹窗右上角“完成”结束配对。

### 2026-07-14 — 工作站首页整合设备配对与管理

- **改了什么**：将「设备」页面整合进工作站首页，并移除已无目的地价值的侧栏。首页在连接状态行直接提供加大、带扫码图标的「配对新设备」主按钮，并在其后展示已授权设备列表、刷新与撤销授权；AI 工具改为紧凑状态行。首页内容列固定为 820pt 并在窗口中居中，重启/停止保留为可见的次级按钮；现有配对状态机和撤销二次确认保持不变。设备列表改为仅在 Management API 的地址与令牌齐备后加载，并在两者更新时自动重载。工作站背景改用约 40% 透出的暖灰材质，放大正文、标题、行距和工具列间距；撤销授权由设备行直接触发；配对按钮增加蓝色呼吸光晕和扫光动效，并遵循「减少动态效果」系统设置。配对流程改由固定尺寸 sheet 呈现，保留二维码、iOS/Web 切换、步骤、手动码、倒计时、复制与连接详情。
- **有何提升**：用户打开 CordCode Link 后即可确认连接状态、管理已授权设备并开始配对，无需在工作站与设备页之间切换；宽窗口不再把内容拉散，信息密度更克制，主操作清晰，同时不会因启动阶段 API 尚未就绪而先显示可通过手动重试恢复的错误。Relay、工具与设备状态会在 API 就绪后一起读取；连接状态工作表具有稳定完整的尺寸和可点击的「完成」关闭按钮。较亮的半透明底色和更宽松的文字节奏使首屏更接近设计稿，配对入口有可感知但不喧宾夺主的动感；配对任务不再撑开首页，而在专注窗口中完整完成。

### 2026-07-14 — Grok Build rich history 填充 Parts/Steps

- **改了什么**：`readRichSessionHistory` 从只产 ID/Role/Content 空壳升级为填充 Parts/Steps。扩展 `grokHistoryLine` 解析 tool_calls 的 `arguments`/`id`/`name` 和 tool_result 的 `content`/`tool_call_id`。两遍扫描设计：先收集所有 tool_result，再用稳定 call ID 关联到对应 tool step。step status 始终为 `unknown`（Grok 历史无状态字段），output 仅在 tool_call_id 匹配时填充（>500 rune 丢弃），title 从 proven arguments 派生（command/target_file/pattern 等）。entry 准入 guard 改为 role 空或 content/parts/thinking/steps/files 全空才跳过——空内容工具行不再被跳过或合成 `Tool:` 文本。step ID 用 `tool-<lineNum>-<hash8>` 派生，不受过滤/解析变化影响。不归并、不重做 ID 方案、不改 wire 契约。
- **有何提升**：iOS 消费侧拿到结构化 Parts/Steps，已完成 Grok session 的工具调用显示为 ProcessGroup 卡片而非平铺文本。无 proven result 状态不标 `completed`，无 proven 关联不填 output。稳定 ID/顺序/limit 不漂移。

### 2026-07-13 — 第二轮界面升级容器宽度复核

- **改了什么**：复核 r4 的 SwiftUI 宽屏实现路径，发现并明确 `PageContainer.maxContentWidth` 包含水平 padding、而内部 GeometryReader 测量内容宽度的层级关系；规定容器宽度必须在双列内容预算之外包含两侧 padding，且在创建容器时固定传入最大宽度。
- **有何提升**：避免宽屏判断被 880/1164pt 外层上限锁死，确保临界宽度可稳定进入双列且窄窗口仍自然退回单列。

### 2026-07-13 — 第二轮界面升级尺寸契约复核

- **改了什么**：复核 r3 后修订的视觉升级规格；确认日志脱敏边界、辅助栏内容、主 CTA 状态表和连接目的地已满足要求，并定位宽屏阈值测量坐标与推荐列宽未完全自洽的问题。
- **有何提升**：在编码前锁定实际内容宽度、列预算和容器上限的同一契约，避免临界宽度下出现双列溢出、主列压缩或实现者各自解释布局规则。

### 2026-07-13 — 第二轮界面升级最终修订复核

- **改了什么**：复核第二轮界面升级的最终修订规格，确认主 CTA、辅助栏内容与连接/endpoint 边界已对齐；发现并明确阻止了 1180pt 窗口阈值与双列内容预算的算术冲突，以及把原始日志混入脱敏支持信息的安全契约错误。
- **有何提升**：P0 不会在阈值点压缩或溢出主列，P2 仍能同时提供原样日志和安全的支持摘要，避免视觉重构引入布局与诊断信息泄露回归。

### 2026-07-13 — 第二轮界面升级修订版评审

- **改了什么**：复核第二轮视觉升级设计的评审后修订版。确认其已采纳克制分组、单一连接目的地、日志证据完整性等关键约束；补充宽屏阈值的真实尺寸预算、辅助栏 P0 内容归属、按 Bridge 运行状态确定唯一主 CTA 的实施前条件。
- **有何提升**：实现 agent 可在不压缩主列、不生成空辅助栏、也不误降级“启动 / 重启工作站”主操作的前提下完成 P0 宽屏布局。

### 2026-07-13 — CordCode Link 桌面体验重设计基线

- **改了什么**：新增并完成三轮评审修订的 Mac App UX 重设计报告，基于现有运行、设备配对、远程连接、设置与诊断界面，确立以“让 iPhone / iPad 安全接入这台 Mac 的 AI 工具”为中心的工作站信息架构。方案将主导航收敛为“工作站 / 设备”，把连接状态、设置与诊断移至一跳可达的按需界面；同时定义配对、Relay、高级连接、故障反馈、视觉层级与实施验收标准。评审修订已校正配对/撤销/诊断现状、补入状态迁移和容器宽度前置约束、补全 Grok Build 活文档命令示例，并明确连接入口与工具配置入口的唯一归属；第三轮评审的实施期注意事项已作为设计规范内化进报告 §1/§2。
- **有何提升**：后续界面迭代有统一的产品语言和可验证的交互目标，避免继续按运行模块堆叠页面，让首次用户能快速理解产品、已使用用户能快速确认可用性。

### 2026-07-12 — Grok Build CLI driver (`grokbuild`)

- **改了什么**：新增 `agent/grokbuild` ACP driver；默认 drivers 注册 `grokbuild`；本地 `~/.grok/sessions` catalog 实现 ListSessions/HistoryProvider（ACP 无 session/list）；`loadSession` 兼容 JSON bool；`list_projects` 不对 Grok 返回 Claude 工程树。
- **验证**：`go test ./agent/grokbuild/...`；Release 安装；与 iOS 真机联调列表与续聊通过。

### 2026-07-12 — 修复 Codex 会话列表时间被迁移时间覆盖

- **改了什么**：Codex 会话列表不再把 JSONL 文件的系统修改时间直接当作会话更新时间；改为从会话记录末尾的真实事件时间戳推导，只有记录中没有有效时间戳时才回退到文件时间。
- **有何提升**：新版 Codex/ChatGPT 迁移批量触碰历史记录后，各会话仍显示并按各自真实的最后活动时间排序，不会全部停在同一个迁移时刻。

### 2026-07-12 — 兼容 ChatGPT App 内嵌的 Codex runtime

- **改了什么**：CordCode Link 启动 go-bridge 时补入新版 ChatGPT App 的 Codex CLI 目录（`/Applications/ChatGPT.app/Contents/Resources`）。OpenAI 将独立 Codex App 合并为 ChatGPT App 后，该目录中的可执行文件仍名为 `codex`，但旧的 `/Applications/Codex.app/...` 路径已不存在。
- **有何提升**：使用新版 ChatGPT App 的用户不再被误判为“未安装 Codex”；Bridge 可正常以 app-server stdio 模式启动 Codex、iOS 可恢复加载 Codex session。原独立 Codex App 路径继续保留，兼容未升级用户。

### 2026-07-12 — 修复新版 Codex 会话打开时断连

- **改了什么**：Codex 新版 transcript 可能先写入 `patch_apply_end`、后写入对应的工具调用；rich-history 解析器此前假定已有可挂载的 tool step，错误索引空 steps 并触发 Go panic。现在只忽略无法关联的孤立 patch 完成事件，继续返回该会话的真实消息历史。
- **有何提升**：iOS 打开新版 Codex 创建且包含文件修改的 session 时，不再因 Bridge 连接被 panic 中断而无限“加载中”；其余消息、已关联 patch 与旧版会话解析保持不变。

### 2026-07-12 — 打开会话时不再显示冗余完成通知

- **改了什么**：iOS 打开某个会话时会撤销该会话已排队或已送达的「任务已完成」本地通知；完成回调也会在当前正在显示该会话时跳过投递。
- **有何提升**：用户已在消息页阅读该会话时，不会再被同一会话的完成横幅遮挡；切到其他会话或离开 App 后的后台完成通知仍会保留。

### 2026-07-06 — 修复 iOS OpenCode 模式无流式输出（active turn 改走 managed server + SSE）

- **改了什么**：OpenCode 模式下 iOS 发消息时，go-bridge 的 active turn 从批处理 `opencode run --format json`（一轮 turn 只在结束时发 1 帧 `text_delta`，iOS 表现为「等整段答完才一次性出现」）改为走 Swift 已托管的 `opencode serve` HTTP API + `/global/event` SSE。新建 `opencodeServerSession`（实现 `core.AgentSession`）：`Send` 时 `POST /session/:id/prompt_async`（204 非阻塞），消费一条 dedicated、按 sessionID client-side 过滤的 SSE，`message.part.delta` → 增量 `EventText`、`session.status idle` → `EventResult`。复用 `sseSubscriber` 全套事件解析 + dedup + 生命周期翻译，只新增 `sessionFilter`（atomic；pending 态全丢，避免 chatID 未定前把别的 session 事件串到 iOS）。`StartSession` 按 `httpBaseURL` 分流（server 在 → 流式；否则回退原 CLI 批处理，保留兜底）。模型经 `resolveOpencodeModelLocked` 解析，建 session 时用 `{model:{id,providerID}}` 绑定（prompt body 的 `providerID/modelID` 实测不生效，模型必须 session 级设定）。`providers.go` 加 POST-capable `doRequest`，`fetchJSON` 复用。绝不 kill `opencode serve`（全局共享，归 Swift `OpenCodeManagedServer.swift` 管）。
- **有何提升**：iOS OpenCode 模式（owner 真机实测 opencode/mimo-v2.5-free）发「讲一个1000字的故事」，消息页从「等整段答完才一次性出现」变为**逐字流式增长**，与 Mac opencode App 一致。live 集成测试（`server_session_live_test.go`，env-gated）对着托管 server 实测一轮 turn **80 帧 EventText**（对比批处理 CLI 的 1 帧）+ EventResult 正常收口。Claude/Codex 路径不动。注：Codex 模式经 cligate 供应商的非流式是 **cligate 上游问题**（`responses-route.js` 硬编码 `stream:false`，攒满整段再假装流式），不在本次范围；codex 经流式供应商（官方/cliproxyapi）iOS 本就流式正常。
- 改动限于 MacBridge `agent/opencode`：新增 `server_session.go` + `server_session_test.go` + `server_session_live_test.go`，改 `opencode.go`（`StartSession` 分流 + `resolveOpencodeModelLocked`）、`providers.go`（`doRequest`）、`sse_subscriber.go`（`sessionFilter`）、`session.go`（`stageImages` 抽成自由函数 `stageOpencodeImages` 供 CLI 和 server session 共用）、`session_test.go`。不动 wire protocol（对 iOS 仍是 `text_delta`，只是帧数从 1 变多）、不动 iOS、不动 relay。

### 2026-07-05 — 修复 Claude session PID 复用导致 stale session 误判 running

- **改了什么**：`agent/claudecode` 判定 Claude session 是否 running 的 PID 活性检查从纯 `kill(pid, 0)` 升级为 PID 身份校验。新增可注入 seam `procIdentityAlive(pid, expectCwd)`：在 PID 存活之上，再用 `ps` 校验可执行名包含 `claude`、用 `/proc/<pid>/cwd`（Linux）或 `lsof`（macOS）校验 cwd 与 stub 记录一致；任一强不匹配（PID 被复用为非 claude 进程，或 cwd 不同）即判非 live。`LiveSessionProcess` 和 `GetRunningSessionIDs` 改用该 seam，`IsProcessAlive`（file-relay 每 tick 的 cached PID 复查）保留纯活性语义不动。身份校验对平台探测失败 fail-open（不因 ps/lsof 暂时不可用而误判 idle），PID-reuse 防线只依赖强不匹配分支。Windows 仅为编辑器/CI 可移植性构建，fail-open 占位。
- **有何提升**：某个 Claude session 的 stub 残留（claude 异常退出未清理）且该 PID 被 OS 复用给无关进程时，不再被误判为 running——避免 iOS 因此进入 phantom executing（输入框锁"执行中"、status strip 不消失）。本次 07-05 external-turn 复现因 stub 正确缺失未触发，但这是真实 latent bug。新增 `TestGetRunningSessionIDs_PIDReuseNotRunning` 回归（active transcript + 复用 PID → 非 running；身份恢复后 → running 作对照）。
- 改动限于 MacBridge：`agent/claudecode/proc_seam.go`（新 seam）、`agent/claudecode/proc_unix.go` + `proc_windows.go`（身份校验）、`agent/claudecode/claudecode.go`（两处调用点改用 seam）、`core/interfaces.go`（`LiveSessionLister` 文档说明 `Live` 现为身份校验）。不动 wire protocol、iOS、relay，不改 `IsProcessAlive` 公共契约。

### 2026-07-05 — 修复 Claude 外部 turn 在 iOS 上滞后一轮显示

- **改了什么**：Claude Code 会话在 Mac 端外部进程（Claude App / Terminal）里继续对话时，MacBridge 的 transcript file relay 不再因为初始 snapshot 看起来 idle 就立即退出。现在 relay 会用 live-only PID 活性判断区分「已完成死进程」和「仍活着但 transcript 暂未增长」：死进程仍立即广播 idle 并退出；活进程进入轮询，看到新 user 行会发 `turn_started`，看到 final assistant 会发 `turn_completed` + idle 并退出。初始扫描改为 reader-based classifier，能在 relay 重启后识别已经写入的 user 行并补发 `turn_started`，避免 live-idle TTL 空窗吞掉 per-turn anchor；中断标记会完成当前 turn 但继续 watch；meta-only 增长不会重复发事件；进程死亡会有界收口。
- **有何提升**：iOS 旁观 Mac 端发起的 Claude 外部 turn 时，不再只到下一轮才显示上一轮回复的执行锚点；已完成或崩溃残留的 transcript 也不会被误报为 running。同步修复了 2026-07-05 CPU 修复中暴露的生产注册问题：`runningMap` cache 现在能在 `-drivers claude`（agent 注册名为 `"claude"`、`agent.Name()=="claudecode"`）下正确调用 `GetRunningSessionIDs`，不再只在测试里的 `"claudecode"` 注册名下生效。
- 改动范围限于 MacBridge Go runtime：新增 `core.LiveSessionLister` / `LiveSessionProcess`，`agent/claudecode` 复用 Claude session stub 扫描并暴露 live-only PID 检查；`go-bridge` file relay 增加 live gate、cached PID tick recheck、two-tier idle lifecycle 和定向回归测试。不改 wire protocol、不新增 `hello_ack` capability、不伪造 file-relay `text_delta`；外部 turn 内容仍由 iOS 历史同步渲染。

### 2026-07-05 — 修复 Claude list_sessions 高 CPU：list 路径不再 per-row 解析 transcript

- **改了什么**：iOS 反复刷新 Claude 会话列表时，`cordcode-bridge-runtime` 会因 `handleListSessions → enrichSessionStateWithAgent → GetRunningSessionIDs` 对每个列出的 session 重复解析 transcript（事件中 `wire_mapping_ms` 一度达 9.5–11.8s、144 session × ~116MB）而逼近单核 100%。现在 list 路径改为 list-safe 批量 enrichment：`getRunningMap(ctx, agent)` 每请求一次性算出 running 集，`enrichSessionStatesForList` 只用 registry + running map 给行打 `runtimeState`，**不再对任何行打开/解析 transcript、不再 `markIdle`、不再写 `/tmp/bridge-sessions.json`**。`GetRunningSessionIDs` 的结果用 2s TTL 缓存（burst 只算一次），MacBridge 拥有的 turn 状态迁移立即失效缓存；外部启动的 Claude turn 在 ≤1 个 TTL 窗口后探测到（仍由 live-PID 有界的 `GetRunningSessionIDs` 负责，未引入后台扫描器）；`isSessionExecuting` 结果按 `sessionID+path+size+mtime`（size 与 mtime 同时比较）缓存，把冷缓存代价收敛到「变化的 live transcript 数」。3 个 list 调用点（Claude / 非 Claude / OpenCode）全部迁移到批量 API；`reasoningEffort` 注入保留；single-session detail 路径（`get_session`/`get_session_messages`）仍保留更深的 transcript 检视。
- **有何提升**：list_sessions 在 catalog + running-map 缓存命中时不再花数秒在 `wire_mapping_ms`（144 session 的 fixture 实测 cold≈56ms / cache-hit≈42ms / per-row transcript 打开数=0），runtime 不再因 iOS 刷新而逼近单核；completed session 不再因 list 路径的 stale-running 校验被误判；外部 Claude turn 仍能在 TTL 内被发现。新增定向测试覆盖零 per-row transcript 打开、running map 每请求一次、TTL/失效、外部 turn（可注入 PID 活性 seam）、transcript 缓存指纹与 large-K 冷缓存护栏。
- 改动限于 MacBridge（`go-bridge/handlers.go`、`go-bridge/handlers_opencode.go`、`go-bridge/handlers_relay.go`、`go-bridge/types.go`、新增 `go-bridge/running_map_cache.go` + `go-bridge/transcript_probe.go`、`agent/claudecode/claudecode.go`、新增 `agent/claudecode/transcript_exec_cache.go` + `agent/claudecode/proc_seam.go`），不动 wire protocol、iOS 或 relay。

### 2026-07-04 — 清洗 Claude Code 斜杠命令在 iOS 消息页的协议标签泄漏

- **改了什么**：Claude Code 模式下，用户在 Mac 端执行 `/handoff-doc`、`/takeover`、`/model`、`/compact` 等斜杠命令时，Claude CLI 注入的内部协议标签（`<command-message>`、`<command-name>`、`<command-args>`、`<local-command-stdout>`、`<local-command-caveat>`）和 skill 文档全文（`Base directory for this skill: ... # Mission Takeover ...`）原本会原样作为用户消息出现在 iOS 消息页。现在 `agent/claudecode` 的 rich history 与会话列表预览统一清洗：斜杠命令收敛为简洁的 `/cmd args摘要`（args 按 rune 计数截断到 120，不切断多字节字符），`<local-command-stdout|stderr|caveat>` 等纯协议回显整条过滤，skill 文档注入按内容特征可靠过滤。
- **有何提升**：iOS 消息页和会话列表不再显示 CLI 内部 XML 标签噪声和 skill 全文，斜杠命令以可读的命令名形式呈现；普通文本（含合法的 `<`/`>` 字符）不受影响。已对本机全部 141 个真实 Claude transcript 回归验证 0 泄漏。
- 关键修正：skill 文档注入的过滤从"launch 状态机驱动"改为"内容特征驱动"。真实 transcript 中 skill 文档注入（`isMeta=true`、以 `Base directory for this skill:` 开头）不总是紧跟在 `Launching skill` tool_result 后面，原状态机会漏掉这种独立出现的注入；现按内容直接判定。
- 改动范围限于 MacBridge 源头（`agent/claudecode/claudecode.go` 的 user 文本分支与 `extractTextContent`），不动 iOS、wire protocol 或 shared-message-renderer；新增定向测试覆盖五类标签清洗、多字节截断与 skill 文档独立注入场景。

### 2026-07-04 — 记录 Claude 冷启动 spurious idle 跨仓结论

- 跨仓联调定位：冷启动既有 Claude session 时，transcript file relay 抢先基于上一轮已完成 transcript 广播 `session_state_changed(idle)`（早于真实 agent stdout relay 报 `running`），是 iOS 侧「首轮流式从头重播」的上游诱因。本轮 Mac 代码未改（relay-kind 拆分 `7c1d97d` 已修 file relay 占位问题但未覆盖 spurious idle 广播），iOS 侧已兜底（忽略 Claude local turn 首 token 前的 idle）。Mac 侧 file-relay/agent-relay 状态收敛为后续独立清债。详见 `think.md` 同节。

### 2026-07-04 — 强化 agent 自主诊断规则

- `CLAUDE.md` 新增“Autonomous diagnosis and evidence collection”规则，明确 bug 排查时 agent 必须先自行读取源码、日志、进程、端口、配置、Management API 和定向测试证据；不得默认让 owner 手动跑命令、复制日志或替 agent 选择实现路径。
- 明确连接真机时的边界：只读设备探测与日志采集应由 agent 自行完成；点击、输入、滑动、截图、视觉验收和 UI automation 仍需 owner 当前任务明确授权。
- 把 `think.md` 和相邻 iOS 仓 `think.md` 提升为 session/history/live-stream/执行态等问题的排障入口，要求 agent 先复用既有复盘结论，避免重复调查。

### 2026-07-04 — 修复 Claude Code 冷启动既有 session 首轮流式重复

> **根因口径修订（2026-07-04 架构健康第四轮）**：本条目原描述把 Mac 侧
> `relayRunningKind` 拆分当作冷启动重复从头输出的主因。经日志与 `think.md`
> 复核，Mac runtime 没有重复 `send_message`；**症状主因在 iOS 侧**——iOS 在本地
> live stream 中途执行普通 `loadMessages` / running polling / todo refresh，把权威
> 历史覆盖到了本地正在流式增长的 timeline（已由 iOS `e018cb5f Fix Claude local
> stream history overwrite` 单点修复）。Mac 侧 `relayRunningKind` 拆分属于 **latent
> bug / 独立 hardening**（transcript file relay 与真实 CLI stdout relay 不应混用为
> 同一布尔占位），保留为独立加固，不再标记为症状主因。第四轮把 iOS 侧的结构性硬化
> 完成为 backend-agnostic turn sync policy（见下条）。

- （Mac 侧 latent bug / 独立 hardening）冷启动打开既有 Claude Code session 后，首个本地提问可能被 transcript file relay 抢占真实 CLI stdout relay；现在 `send_message` 会让真实 AgentSession relay 接管。这不是冷启动重复从头输出的主因（主因在 iOS，见上），但两类 relay 混用是 latent bug，独立修复。
- 新增 go-bridge 回归测试，覆盖“已有 Claude file relay 标记 + 立刻本地发送”的接管路径，防止后续重构再次把两类 relay 混用成同一个布尔占位。

### 2026-07-04 — 架构健康第四轮（最终轮）：Chat turn sync state-model hardening

第四轮（本次专项收口轮）把 iOS `ChatViewModel` 的 local send / live event / history sync / running-session polling / session switch 互斥与优先级规则，从散落在多个 extension 的 Claude-only ad-hoc 条件（`isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend`）重构为 backend-agnostic 的显式 policy/coordinator，并用定向测试 + strict net-growth gate 防回涨。

- **新增 policy 类型（iOS 仓 `../cordcode-ios`）**：`ChatTurnSyncPolicy`（纯函数 enum：`Ownership` `.none`/`.localSend`/`.remoteLive`/`.reconciling`、`LoadTrigger` 8 case、`LoadDecision` 5 case）+ `ChatTurnSyncState`（`@MainActor` state holder：`decideLoad`/`beginLoadIfAllowed`/`canApply`/`finishLoad`，MainActor 原子读写 + apply 前复核）。policy 不访问 `ChatViewModel`/全局状态/网络。
- **接入生产调用点（iOS 仓）**：`loadMessages` 入口统一经 `turnSyncState.decideLoad` → `beginLoadIfAllowed`（`.defer*`/`.reject*` 在网络请求前短路）→ fetch → `canApply`（apply 前复核 ownership/session/initializationID/token）→ `finishLoad`；`sendMessage`/`beginGenerationTurn` 设置 ownership，`completeGenerationCycle` 转入 `.reconciling`，final reconcile apply 后清回 `.none`；`switchSession` 清理旧 session ownership。
- **Backend-aware 差异**：Claude Code 在 `.localSend` 时 defer 普通 load（CLI 无跨 session live bus）；Codex/OpenCode 走 merge-only（app-server/SSE live 权威、merge 幂等）。这是能力判断，不是「Claude 就跳过」粗规则。
- **定向测试（iOS 仓）**：`ChatTurnSyncPolicyTests` 25 条纯函数单测；`RemoteRunningSessionTests` 新增 `testClaudeCodeInterleave_inFlightHistoryLoadDoesNotOverwriteLivePartialAfterLocalSend`（证明 apply 前复核真实存在）+ `testClaudeCodeSecondTurn_finalReconcileClearsOwnershipForNextTurn`（证明 ownership 清回 .none 不阻塞下一轮）；既有 Claude/Codex/OpenCode/session-switch 相关测试全绿。
- **strict net-growth gate（本仓）**：`scripts/hygiene-baseline.json` 新增 `chatviewmodel_generation`（2336/56）+ `chatviewmodel_messagesync`（1577/46）两条 baseline；`check-architecture-hygiene.sh` 泛化为遍历所有 baseline 条目；`CORDCODE_HYGIENE_STRICT=1` 下净增即 fail。第三轮 BridgeProvider gate 保留。
- **文档同步**：`../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md` 新增「Turn ownership / history sync gate / final reconcile」小节；本仓 `GO_BRIDGE_ARCHITECTURE.md` 新增「iOS live event vs history polling 消费边界」小节；本条目修订既有 07-04 Claude 冷启动条目根因口径。
- **专项收口声明**：本次架构健康专项到第四轮结束（closed）。剩余大文件（`ChatUIKitContainerView`、`claudecode.go`、`appserver_session.go`、`handlers.go`、`BridgeProvider` 下一子域）作为普通维护债进入日常 backlog，不派生「第五轮架构健康」。未来若出现新系统性 gap，需另立专项。完整完成报告见 `docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md`。

### 2026-07-04 — 架构健康第三轮：BridgeProvider transport creation 子域提取（BridgeTransportConnector）

第三轮按 brief 执行 P0 → P3，目标是让 iOS god-object `BridgeProvider.swift` 实际变薄：把 transport creation 子域（构造 / direct+relay attempt / 多候选 direct race / 未采纳 transport 清理）从 `BridgeProvider` 拆到独立 `BridgeTransportConnector.swift`，不改 protocol、pairing、Relay crypto、路径选择语义或 recovery ownership。12 个 exec-plan 任务全部 proven done。

- **P0 测试保护（iOS 仓 `../cordcode-ios`）**：在未拆代码上确认 `BridgeLANFirstFallbackTests` / `BridgePathSwitchTests` / `GodObjectCharacterizationTests` 全绿（46 用例）；补 brief T1 指明的最小缺口——direct + relay 双失败时暴露 relay 链末端真实错误（`relay.connect_failed`）并记录 `relay-fallback-after-direct-fail` trace，不构造假成功。锁定提取前基线 lines=1967 / funcs=88 / forTesting=36。
- **P1 提取 BridgeTransportConnector（iOS 仓）**：新增独立 `BridgeTransportConnector.swift`（`@MainActor final class`），迁出 `connectTransport` / `relayCredentials(for:)` / `runDirectSingle` / `runRelay` / `runDirectPhase` / `attemptDirectPhase` / `attemptRelay` / `runDirectRace` 与 `RaceTransportCollector` / `RaceResult` / `RaceCompletion` 及三组测试 factory 注入。`BridgeProvider.connectBridge` 保留策略选择、generation/recovery 协调、adopt；通过注入式 `configure(generationGuard:probeRoundNotifier:taskCountLogger:)` 把 connection coordinator 上下文单向交给 connector。设计约束严格落地：connector 不写 `activeBridge` / `cachedClients` / `connectionStatus` / `activeConnectionKind`，不持 `RecoveryCoordinator`，不持 UI 状态，仅持 `SavedBridgeStore` 作 relayStore；`runDirectRace` 提取边界止于 `applyHelloAckLocalURLRefresh` 之前（后者保留在 BridgeProvider）。`BridgeProvider` 仅新增 1 个窄 forward `transportConnectorForTesting()`，未超过 brief 的 ≤2 上限。
- **P1 测试（iOS 仓）**：现有黑盒测试 factory 注入调用点改写到 `provider.transportConnectorForTesting().setXxxFactoryForTesting(...)`，断言与对外行为不变；新增 `BridgeTransportConnectorTests.swift` 6 条 connector 级定向测试，覆盖 `connectTransport` 非 CCCodeBridgeError 失败清理、`runDirectRace` 全候选失败聚合错误与 cleanup、relay factory 抛真实错误传播、direct factory 注入返回真实结果、generation superseded 拒绝 attempt。iOS 仓定向 52 用例全绿。
- **P2 baseline 下调 + strict gate（本仓）**：`scripts/hygiene-baseline.json` 下调为 lines=1629 / funcs=71 / forTesting=27（均低于 brief 目标 ≤1700 / ≤78 / ≤30）；`CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh` 通过（STRICT passed — no BridgeProvider net growth）。
- **P3 build / 真机安装启动（iOS 仓）**：定向 `xcodebuild test`（52 用例）+ Debug 构建在已连接物理设备 iPhone 16 Pro（UDID BFC431AC…）安装并启动成功（`Launched application with org.openagi.cordcode`）；未执行 UI automation / snapshot / 自动点击。

诚实口径：iOS 代码改动与定向测试发生在 `../cordcode-ios` 仓，baseline 下调与 strict gate 发生在 MacBridge 仓；两仓提交边界清晰（iOS 一条提交 + MacBridge 文档/gate baseline 一条提交）。connector 级测试覆盖了 T2/T3 的不变量（清理 + 不写 active state）；真实 socket 握手 / 真机肉眼连接路径核对仍按 brief 第 6 节归到 owner 人工验收清单。完整完成报告见 `docs/2026-07-04-architecture-health-third-round-development-brief完成情况.md`。

### 2026-07-04 — 架构健康第三轮开发 brief

- 新增第三轮开发 brief，明确第三轮主轴为 iOS `BridgeProvider` 的 `transport creation` 子域 extract-and-test，而不是继续扩大范围或直接拆 ChatViewModel。
- 规定先补 direct/relay attempt、未采纳 transport 清理、adoption 边界三类不变量测试，再提取 `BridgeTransportConnector.swift`；不改 protocol、pairing、Relay crypto、路径选择语义或 recovery ownership。
- 明确完成标准：`BridgeProvider.swift` 指标必须下降、MacBridge `hygiene-baseline.json` 必须下调并通过 strict gate，iOS 代码改动后按真机连接状态执行构建/安装/启动。
- 按独立评审修订 brief：修正不存在的 `attemptRelayConnection` 符号，补入 `runDirectRace` / `RaceTransportCollector` / `RaceResult` / `RaceCompletion` 等 transport-creation 层真实切片，定调本轮采用独立 `BridgeTransportConnector` 类型并纳入 direct race，量化目标为 `BridgeProvider.swift` lines ≤1700 / funcs ≤78 / ForTesting ≤30。
- 按第二轮评审补齐发车前澄清：P1 允许并预期改写现有测试的 factory 注入调用点到 connector 测试入口；`runDirectRace` 边界止于 `applyHelloAckLocalURLRefresh` 前；若 race 迁出阻塞，则 lines 目标挂起并暂停升级 owner，不接受降级提交。

### 2026-07-04 — 架构健康第二轮：web 共享包收口 5/5 + BridgeProvider 净增长 gate + handlers.go 物理分发

第二轮按 brief 推荐顺序 P0 → P2 → P1 执行，目标是止住恶化、降低第三轮拆分摩擦，不动 iOS god-object 本体。16 个 exec-plan 任务全部 proven done。

- **P0 web shared renderer 收口 5/5（代码在相邻 iOS 仓 `../cordcode-ios`）**：把剩余 3 个重复组件迁入 `shared-message-renderer`，共享包 exports 覆盖 DiffViewer/ToolBlock/ReasoningBlock/ProcessGroup/NarrativeBlock。
  - `ReasoningBlock`：2 行文案差异（中/英）通过 `host.labels` 注入；迁移后两 app 剩余 diff 实测仅 labels 值。
  - `ProcessGroup`：43 行真实差异（摘要文案 + 分类粒度 + 复数语义）通过 `summarizers` 注入保留，共享包首次引入 `components/turns/`。
  - `NarrativeBlock`：68 行差异（message-web 独有的 git directive summary）通过 `transformContent` 注入；共享包新增 react-markdown peer `>=9 <11` / remark-gfm peer `>=4 <5`，**9.x 与 10.x 跨大版本兼容经三包 typecheck/build 实测确认**。
  - 共享包新增 12 条定向 vitest（labels 注入 / summarizers / transformContent / DOM 契约）；三包 typecheck + build 全绿。message-web 视觉回归 owner 真机目测通过（2026-07-04，iPhone 16 Pro）；remote-web 靠对称薄 wrapper + typecheck/build。
- **P2 BridgeProvider 净增长 strict gate（本仓）**：新增 `scripts/hygiene-baseline.json`（冻结基线 lines=1967/funcs=88/forTesting=36）；`check-architecture-hygiene.sh` 增加 `CORDCODE_HYGIENE_STRICT=1` 分支——任一指标净增即 exit 1、允许减少、iOS 仓缺失时 graceful skip（不破坏 CI）；CI macbridge job 接入 best-effort 跨仓 checkout `openAgi2/cordcode-ios` + strict hygiene step。既有 5 个 inventory 段仍 warning-only，未被提升为 fail。
- **P1 handlers.go 物理分发（本仓）**：4559 行 `handlers.go` 拆出 `handlers_opencode.go`（488 行，OpenCode proxy 簇）+ `handlers_relay.go`（829 行，relay 簇含 brief 指定的 4 个 transcript 探测 helper 整组搬迁），`handlers.go` 降至 3269 行（-1290，-28%）。纯物理 move，不改函数体 / RPC 行为 / session registry / agent driver / protocol 字面契约；`go build` + 定向过滤 + 全量 `go test ./go-bridge/...` 全绿。

诚实口径：P0 三组件迁移代码与验证发生在 iOS 仓（typecheck/build/vitest），P2/P1 发生在 MacBridge 仓；react-markdown 跨版本兼容经三包实测而非穷尽 runtime 验证；`cordcode-ios` 已确认公开（无 auth 可读），CI strict gate 在每个 PR/push 实际执法。完整完成报告见 `docs/2026-07-04-architecture-health-second-round-development-brief完成情况.md`。

- **独立完成审计**：新增 `docs/2026-07-04-architecture-health-second-round-completion-audit.md`，复跑 exec-plan 结构核查、iOS 三包 typecheck/build/vitest、P2 strict gate（含模拟增长 exit 1）和 P1 Go build/tests。审计结论为通过，仅指出完成报告中 `required:true ×16` 应理解为 `verification.required=true ×16` 的低优先级口径修正。

### 2026-07-04 — 架构健康第二轮开发交接文档

- 新增第二轮开发 brief，基于第一轮 gap analysis 和讨论结论，把下一轮范围收敛为 web shared renderer 剩余组件迁移、`handlers.go` 物理分发、BridgeProvider 净增长 gate 试点。
- 明确第二轮不做 iOS god-object 大手术，第三轮再按子域 extract-and-test 启动本体拆分，避免“测试保护还不够”变成永久延期。
- 按独立评审修订 brief：移除不可复现的“漂移扩大”论据，补齐 `ProcessGroup` 路径、OpenCode handler 拆分、hygiene strict gate/CI 接入边界，并记录评审意见全部采纳。
- 按 r2 复核继续修正 P2 论据：移除不可复现的 `BridgeProvider` “78→88 func 增长”说法，改为静态 god-object、历史下沉点、无净增长门禁的可复现依据。
- 按 r3 清理复核微调 P1 拆分说明：列出 relay 簇内 4 个不带 relay 之名的 transcript 探测 helper（`detectClaudeTranscriptState` / `detectCodexTranscriptTaskState` / `scanCodexTranscriptTaskEvents` / `codexEventPayloadType`），要求整组搬迁以防反向依赖。r3 同时确认 brief 全部量化论据可在当前 `main` 复现，评审循环结束。

### 2026-07-04 — 架构健康第一轮整体完成（28/28 proven done）

本轮在 B2 删除 legacy config 包后收口，28 个 exec-plan 任务全部 done 且 proven。各批次交付：

- **A 能力单源化**：`backend_capabilities.go` 成为 BackendList 与 agent descriptor 的唯一能力推导源；Codex app_server 一致宣告 compression + question_reply；session_pagination 保持关闭。
- **B1 provider seed 解耦**：`provider_seed_config.go` 最小 TOML reader，`provider_switch.go` 切断对 legacy config 的生产依赖。
- **B2-predelete**：新增 `agent/providerseedtest/` 测试专用 loader，把 Claude/Codex 的 provider 测试从 legacy config 迁走。
- **B2（主项）**：删除 `config/` 包（4 文件，含 Weixin/Feishu/Web 后台等旧业务结构），中性化 `claudecode_test.go` 残留 Feishu fixture。
- **C web renderer 共享包**：iOS 仓 `shared-message-renderer/`，迁移 DiffViewer + ToolBlock，用 `host.post` 触发 openDetail/permissionAction/questionAction。
- **D god-object characterization**：iOS 仓加 `GodObjectCharacterizationTests.swift`（连接策略 + 生成周期边界），不拆 god object、只锁现状行为。
- **E 工程宪法**：`engineering-constitution.md` + `check-architecture-hygiene.sh`，warning-only 存量报告，不阻断 CI。

验证强度：

- **B2 验收**：Weixin/Feishu/业务符号扫描 0 命中、生产 config-import 0 命中、`go build`/`go vet`/全量 `go test ./...`（runtime 等价 PATH）全绿、relay-server 独立 module 绿、活文档无残留引用。
- **追加验证**（删除后更强确认）：Mac Release 重建+装机成功（commit `ea20d1ab4e0b`，启动 0 ERROR/WARN，8777 是 `/Applications` 内嵌 runtime）；iOS 从 `codex/web-renderer-shared-c1` 分支装到 iPhone 16 Pro；owner 真机功能冒烟通过。
- **诚实口径**：impl 类标 self-attested，命令类标 re-verified，功能性 UI 标 owner-verified（冒烟级，非穷尽）；Batch D 的 xcodebuild test 未本轮重跑，保留前序 re-verified。完整完成报告见 `docs/2026-07-03-architecture-health-execution-plan完成情况.md`。

### 2026-07-03 — 删除 legacy config 包（架构健康第一轮收口）

- **删除 `config/` 死重包**：移除约 6418 行的 legacy `config` 包（`config.go` / `config_test.go` / `config_repository.go` / `config_repository_test.go`）。删除前已确认它是孤儿包 — 生产代码、测试、cmd 入口均 0 处 import，`config.X()` 调用方为 0；仓内唯一引用是 `go-bridge/provider_switch_test.go` 的静态反回归守卫字符串（非真实 import）。
- **不再携带与 CordCode Link 无关的历史业务写入能力**：随包移除 Weixin/Feishu 平台凭据写入、Web 管理后台、Cron/Webhook/TTS/Hook/Speech 等旧一体仓（cc-connect）时代的结构，缩小运行路径维护面。owner 已确认这些能力不再维护。
- **删除后验收全绿**：`go build ./...` / `go vet ./...` / 根 module `go test ./... -count=1`（runtime 等价 PATH）全通过；Weixin/Feishu/业务符号与生产 config-import 扫描零命中；`go.mod` 的 `BurntSushi/toml` 保留（B1 的 provider seed reader 仍在用）。架构健康执行计划第一轮 28/28 todo 全部 proven done，正向完成报告见 `docs/2026-07-03-architecture-health-execution-plan完成情况.md`。

### 2026-07-03 — B2 删除前解除 provider 测试对 legacy config 的依赖

- **provider 集成测试不再 import legacy config 包**：Claude Code / Codex 的 provider 相关测试改为通过轻量 provider seed test helper 读取 `.cc-connect/config.toml`，保留真实配置存在才运行、缺失则 skip 的行为。
- **删除前证据更清晰**：新增 provider seed test helper 覆盖 provider refs、agent type 过滤、agent-specific endpoint/model、Codex headers 与 `${ENV}` 展开；静态防回归测试扩展到 agent provider 测试文件，避免删除 `config/` 前又引入 test-only 依赖。
- **仍未删除 `config/` 包**：B2 删除本体继续等待删除前审计和 owner 对旧业务写入能力不再维护的确认。

### 2026-07-03 — 架构健康治理第一轮：能力单源化与 provider seed 瘦身

- **后端 capability 宣告改为单一来源**：Management API 与 `hello_ack.backends[]` 现在共用同一套能力推导，Codex `app_server` 模式会一致宣告 `compression` 与 `question_reply`，避免客户端从不同入口看到不一致能力。
- **保持风险能力关闭**：`session_pagination` 仍不宣告；OpenCode/Codex 仍不宣告未实现的 `permission_resolve`，避免 UI 误启用不可用路径。
- **切断 go-bridge 对 legacy config 包的生产依赖**：provider seed 读取改为 go-bridge 内部最小 TOML 结构，保留 `.cc-connect/config.toml` 的 provider refs、work_dir/base_dir 匹配、active provider、models/env/Codex headers 映射，降低旧 Weixin/Feishu 等历史业务结构对运行路径的维护压力。
- **新增 warning-only 工程卫生检查**：新增工程宪法与 `scripts/check-architecture-hygiene.sh`，把日志、本地化、`ForTesting`、长文件和 protocol 同步规则变成可见的存量报告；当前只提示不阻塞 CI，避免在债务清零前制造硬失败。
- **补齐 web renderer 共享包施工设计**：新增 batch C 设计实施文档，限定第一轮只迁移 `DiffViewer` / `ToolBlock` 与稳定类型，要求用 host adapter 隔离 iOS WebKit 与 remote-web 宿主差异。

### 2026-07-03 — 活文档对齐当前 CordCode Link 架构

- **修正文档中的旧品牌与命令**：根活文档、安装说明、release checklist 和 README 统一使用 `CordCodeLink.app`、`cordcode-bridge-runtime`、`cordcode-relay` module 与当前 `/opt/cordcode-relay/bin/relay-server` 部署路径，避免照抄旧命令找不到 runtime 或 Relay 备份。
- **补齐当前运行态说明**：OpenCode 默认 `managed_local`、Codex transcript relay、Claude streaming partial、`transcriptindex` 分页索引、runtime 自愈规则、`hello_ack.currentURLs.locals`、Web QR `/web/` 静态部署和 capability 来源差异已写回活文档。
- **提升维护可恢复性**：CI、runtime.json `bridgeEpoch`、Relay nginx 要求和 OpenCode 排障路径更新为当前实现，后续 agent 不需要从过程文档反推最新架构。

### 2026-07-03 — 修复 OpenCode 连续 turn 流式收口抖动

- **OpenCode 连续问答的完成事件按 turn 复位**：OpenCode SSE 订阅在同一 session 进入新 user/running 状态时清除上一轮 completion 去重，避免第二轮开始后 completion 被 session 级状态吞掉。
- **避免历史轮询后伪造完成造成状态条闪烁**：OpenCode 事件 relay 不再使用空闲超时自动补 `turn_completed`，减少生成结束后的 runtime status strip 重复亮灭和布局抖动。

### 2026-07-03 — OpenCode Automatic managed local server 实现

- **OpenCode 新装默认改为 Automatic（managed_local）**：CordCode Link 会自动启动并管理一个只绑定 `127.0.0.1` 的 `opencode serve`，选择并持久化 `4096...4196` 范围内的端口和随机 Basic Auth 凭据；iOS 仍只连接 MacBridge，不直连 OpenCode。
- **Desktop 与 iOS 自动对齐同一 OpenCode scope**：MacBridge 写入 OpenCode Desktop 默认 server、`currentSidecarUrl` 与 `projects[managedURL]`，优先合并 Desktop `local` 项目集合；本机实测确认 Desktop 运行中不热重载配置，因此实现保留 Cocoa graceful quit + reopen fallback，且冷启动会服从写入的 managedURL。
- **失败保持真实可见**：managed server 启动失败不会阻塞 Bridge，Claude/Codex 继续可用；OpenCode 则保持未配置/不可用诊断。`opencode-managed-server.err.log` 独立脱敏滚动，password 不进入 argv。
- **验证**：新增 `OpenCodeManagedServerTests`，更新 OpenCode source 迁移与 Go managedURL scope 回归测试；Swift OpenCode 定向测试与 Go OpenCode list_projects/list_sessions 定向测试通过。

### 2026-07-03 — OpenCode 无缝接入 managed local server 方案

- 产出本地 managed local server 开发规格，把 OpenCode 最终目标从手动 `external_http` 配置细化为 CordCode Link 自动托管本机 OpenCode shared server、自动同步 Desktop 默认 server 与项目 scope、iOS 扫码后直接看到 Mac 端 OpenCode Desktop 项目/session 的实现路径。

### 2026-07-02 — 修复 OpenCode Desktop 切到 external_http 后项目列表为空

- **修复重启 OpenCode Desktop 后项目/session 看起来消失**：CordCode 同步 Desktop 默认 server 到 `external_http` endpoint 时，现在会优先把 Desktop `local` scope 下的完整项目集合迁移到新 endpoint key，并用旧 active server / legacy `64667` 只补充缺项，避免 Desktop 重启后进入一个没有项目历史的新 server scope。
- **保留并合并已有 external_http 项目状态**：如果目标 endpoint 已经有项目列表，会按 worktree 去重后合并 `local`/旧 server 的缺项；`lastProject` 已存在时不覆盖，避免用户手动整理过的 Desktop 状态被回滚。

### 2026-07-02 — OpenCode 项目列表跟随 Desktop 打开的 workspace

- **修复 iOS OpenCode 模式显示大量已关闭项目目录**：OpenCode `/project` 是历史 catalog,会包含 Desktop 里已经手动关闭的项目；Desktop 侧栏真正打开的项目保存在本机 `opencode.global.dat` 的 `server.projects[scope]` 数组中。MacBridge `list_projects` 现在按 Desktop 源码语义读取该数组,只向 iOS 返回仍在数组里的 opened projects 并保留 Desktop 顺序；`expanded=false` 仅代表 Desktop 侧栏折叠状态,不再被误判为关闭。读不到 Desktop 状态时才保留原 `/project` catalog 作为诊断事实。
- **项目名跟随 Desktop 元数据**：返回项目时优先使用 `/project` metadata 里的 `name`,没有元数据时退回目录 basename,对齐 Desktop 的 `displayName(project) = project.name || basename(worktree)`。
- **OpenCode session 列表对齐 Desktop 加载方式**：目录级 session list 允许 `rootsOnly + limit`,MacBridge 转发为 `x-opencode-directory + ?limit=N`,并按 Desktop 的保守策略在 array response `len == limit` 时标记“可能还有更多”；仍拒绝 `rootsOnly + cursor`,因为 Desktop 当前并不使用 session cursor 做 sidebar 加载。

### 2026-07-02 — OpenCode list_sessions 提高 limit 上限以支持完整项目拉取

- **OpenCode `list_sessions` 的 limit 上限从 50 提高到 1000**：OpenCode server 是 array-only(无 cursor/无 total),一次性返回 `min(limit, total)`,唯一能控制取回量的就是单次 `limit`。原 50 上限会让真实项目(观测到 459 条)被截断且无法翻页。提高到 1000 后客户端可一次拉满整个项目;对小项目无副作用(服务端只返回实际总数)。

### 2026-07-02 — OpenCode project-first session 列表分页协议

- **OpenCode `list_sessions` 改为目录级 page**：go-bridge 的 OpenCode proxy path 现在接收 `directory + limit + cursor`，向 upstream 发送 `x-opencode-directory` 和非空 query 参数，并继续返回既有 `{ sessions, nextCursor, hasMore }` envelope；array-only OpenCode 1.17.13 轨道不伪造 `hasMore`。
- **避免 global dump 误当全项目 catalog**：bare `/session` 仍只代表 `global`，项目桶必须走目录 scoped 请求；当前实现已按 Desktop 方式允许 `rootsOnly + limit`,但仍拒绝 `rootsOnly + cursor`,避免 cursor page 后再 client-side 过滤造成漏页。
- **诊断更清楚**：OpenCode list 日志新增 directory、limit、cursor-present、result-count、next-cursor-present、duration，便于从 `go-bridge.log` 验证冷启动请求预算。
- **协议文档同步**：`list_sessions` 分页与 `get_session_messages` 的 `session_pagination` capability 明确拆开；列表 cursor 是 backend/project/directory scoped opaque 值，客户端不得解析或跨 scope 复用。

### 2026-07-02 — OpenCode 共享本地服务接入（Phase A 显式 external_http）实现

- **移除对固定 `127.0.0.1:64667` 的隐式默认依赖**：OpenCode backend 改为显式 **Server Source** 模型（External HTTP / Legacy 64667 / Service discovery (future) / Disabled）。`go-bridge` 的 `-opencode-url` 默认改为空，`agent/opencode` 不再 fallback `http://localhost:64667`；endpoint 未解析时 descriptor 报 `not_configured`，绝不 dial `64667`。
- **External HTTP：bring-your-own-server**：CordCode 连接用户/运维启动的 stable `opencode serve`（loopback + Basic Auth），不启动也不保活它。Swift 端 `OpenCodeEndpointResolver` 规范化 URL（`localhost`→`127.0.0.1`，拒绝非 loopback/https），`OpenCodeHealthValidator` 先以 no-auth `/global/health` 证明 server 要求认证（必须 401）再做 authed 校验；无密码 server（no-auth 200）默认拒绝为 `server_unauthenticated`。
- **RuntimeManager 显式传 URL、凭据走 env**：argv 增加 `-opencode-url <url>`（URL 非 secret），password 仍经 `OPENCODE_SERVER_PASSWORD` 环境变量传递，不进 argv / 日志；argv/env 构造提取为可测试 static。Desktop 默认 server 配置同步到 resolved endpoint URL，不再固定写 `64667`，且去重保留用户其它 server。
- **升级连续性与新装默认**：存量 `credentials.json` + 无显式 source 自动一次性迁移到 `legacy_64667` 并提示改配 external_http；全新安装默认 Disabled。`legacy_64667` 是唯一允许带 `legacy_insecure_unverified` 警告继续运行的兼容例外。
- **iOS/Desktop 共享同一 OpenCode server**：消除过去 Desktop `vlocal` 与 iOS 固定 `64667` 分裂成两个项目/session scope 的问题（不修改 OpenCode 源码；不抓取 Desktop sidecar 密码）。
- **验证**：新增 `OpenCodeEndpointResolverTests`（18 例，URL 规范化/解析/迁移）、`OpenCodeHealthValidatorTests`（10 例，no-auth/authed 区分、schema、timeout、legacy 例外）、`MacBridgeBehaviorTests`（argv/env/Desktop 配置 6 例）、Go `TestDetectAgentStatus_OpenCode*` + `TestShouldStartPassiveSubscription`。Debug 构建 + 全部定向单测通过；既有 9 个 codex pagination 测试因本机未装 `codex` CLI 的环境原因失败（已确认在 clean main 同样失败，非本次回归）。

### 2026-07-02 — OpenCode 共享本地服务接入方案文档

- 新增 `docs/2026-07-02-opencode-shared-service-discovery-plan.md`，明确“不修改 OpenCode 源码”前提下不能直接发现 Desktop `vlocal` sidecar 密码；当前 stable 可开发路线改为显式 `external_http` 共享 server，并保留未来 `opencode service` discovery 的能力门控入口。
- 方案给出开发任务、失败模式、安全约束和验证清单，目标是移除 CordCode 对固定 `64667` 的默认依赖，避免 OpenCode Desktop 与 iOS 项目/session 分裂；经评审后修订为 stable-compatible 分阶段方案：当前可开发的是显式 `external_http` 共享 server，`opencode service` discovery 因 stable 1.17.13 未暴露 `service`/`--register` 被降级为 future-gated。第二轮补充 Phase A 认证实测、bring-your-own-server 持久化边界、存量用户升级默认迁移到 `legacy_64667` 的连续性规则；第三轮补强 passwordless guard，要求 no-auth `/global/health` 返回 200 的 OpenCode server 默认拒绝，并把 `legacy_64667` 明确为带警告的兼容例外。
- 修复 OpenCode backend 状态检测仍探测旧 `/health` 的问题；现在 descriptor 与方案一致使用 `/global/health` 并校验 `healthy/version` body，避免 shared server 已可用但 iOS/MacBridge 仍显示 OpenCode 未启动。
- 修复 OpenCode 模式项目与目录选择回归：`list_projects` 兼容 OpenCode 1.17.13 的 `worktree` 字段并映射为 iOS 需要的 `directory`；`list_directory` 在 OpenCode RPC 分支中重新走通用目录浏览 handler，恢复 iOS 手动添加项目目录能力。

### 2026-07-01 — Codex 外部 session 结束后 iOS 执行态快速收口

- **修复 Mac 端 Codex 任务完成后 iOS 输入框十几秒才恢复**：当 iOS 旁观 Mac 端 Codex session 时，go-bridge 现在会监视 Codex JSONL transcript 中真实的 `task_started` / `task_complete` 事件；`task_complete` 到达后立即广播 `turn_completed` + `session_state_changed: idle`，让 iOS 走 500ms 终态 debounce，而不是等待 history probe 的多轮 unchanged 兜底。Codex transcript relay 与标准 `AgentSession` relay 使用独立 lifecycle key，可覆盖 registry 里已有 session 但标准事件流收不到外部最终事件的情况。
- **保持长工具执行不闪断**：该 relay 只使用 Codex transcript 的真实任务生命周期事件，不把工具静默或历史无变化当成结束；长时间 `sleep` / verify / build 期间仍保持 running。
- **验证**：新增 go-bridge 单测覆盖 Codex transcript task state 解析。

### 2026-07-01 — Claude Code 模式支持流式打字机输出

- **修复 Claude Code 后端回复整段出现**：MacBridge 启动 Claude Code CLI 时启用 `--include-partial-messages`，并消费 `stream_event/content_block_delta`，将 token 级文本转成统一 `text_delta` 事件下发给 iOS。
- **避免 checkpoint 重复与多段文本丢失**：Claude CLI 会在 token delta 后再发完整 assistant checkpoint；driver 现在按 content block index 去重，正常路径不重复，尾部差量会补齐，异常非前缀 checkpoint 通过 `message_updated` 发送完整文本真值。
- **工具调用与历史重放边界保持真实**：`input_json_delta` 不作为文本重复下发，工具仍由最终 `tool_use` block 产出；`--resume` 历史重放期继续抑制 live 事件，避免把历史内容当作实时流灌给 iOS。
- **验证**：新增真实形状 JSONL fixture 与 claudecode driver 单测，覆盖首个 checkpoint 不清 partial、多 text block、尾差量、非前缀 reconcile、message id 切换和 historyDraining。Sonnet/GLM-5-Turbo 路径已用真实 Claude CLI 证明 `--include-partial-messages` 会产出 `stream_event`；Opus/GLM-5.2 路径仍返回本地 gateway `529 overloaded`，作为 provider route 问题单独处理。

### 2026-06-30 — iOS 新建的 Claude Code session 出现在 Mac IDE/桌面会话列表（修复 entrypoint 过滤）

- **现象**：iOS Claude Code 模式**新建** session 发消息，iOS 正常收到回复且消息持续可见；但 Mac 端 VSCode Claude Code 扩展（及 Claude 桌面 App）的会话列表里**找不到这条 session**，重启 Mac App 仍找不到。
- **根因（已用磁盘 + claude 二进制证据坐实，非数据丢失）**：claude 给 stream-json 方式 spawn 出来的 session 打标 `entrypoint=sdk-cli`（这是 MacBridge 不设 `CLAUDE_CODE_ENTRYPOINT` 时的默认）。Anthropic 的 IDE/桌面会话列表**按 entrypoint 过滤，只显示各自创建的**：VSCode/JetBrains 扩展 = `claude-desktop-3p`，桌面 App = `claude-desktop`。于是 `sdk-cli`（MacBridge 创建）的 session 即便 JSONL 完整落盘在 `~/.claude/projects/<cwd-hash>/<uuid>.jsonl`、内容正确、可被 `claude --resume <id>` 打开，也被这些列表排除——重启无效，因为是 entrypoint 过滤而非缓存。取证：owner 的 Chat 项目下 30 条 session 干净二分为 `claude-desktop-3p`（24，VSCode 可见）+ `sdk-cli`（6，MacBridge 创建不可见），其余字段完全一致；claude 二进制含 `CLAUDE_CODE_ENTRYPOINT` 环境变量与各 tag 值。
- **修复**：[`runtimeEnvLocked`](agent/claudecode/claudecode.go) 给 claude spawn env 注入 `CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p`（用 `core.MergeEnv` 覆盖+去重，确保始终生效）。MacBridge 本身就是第三方 host（"3p"），打这个标签语义正确，且与 IDE 扩展自创 session 同标签 → 出现在 **VSCode 扩展**的会话列表。实测设该 env var 后，新 session transcript 的 `entrypoint` 字段确为 `claude-desktop-3p`。
- **范围与边界**：仅影响**新建** session；已存在的 6 条 `sdk-cli` 旧 session 仍不在列表（未原地改写 transcript）。本修复只决定 transcript 的展示标签，不影响 iOS 端可见性、消息收发或持久化。
- **已知不可修 surface**：独立 **Claude 桌面 App**（`/Applications/Claude.app`）即便 session 同标签也不显示 iOS/MacBridge 创建的 session。取证：桌面 App 自带独立 Claude Code runtime（`~/Library/Application Support/Claude-3p/claude-code/2.1.187/`），其可见 session 与 iOS session 的 `entrypoint` 都是 `claude-desktop-3p`（仅 `version` 2.1.187 vs 2.1.185 之差），说明桌面 App 不按 tag/字段过滤，而是用自有 Electron 会话索引（IndexedDB）只收录经它自己创建的 session，不扫 `~/.claude/projects/`。这是桌面 App 的产品设计，非 MacBridge 可修；iOS 创建的 session 仍可经 **iOS / VSCode 扩展 / 终端 `claude --resume <id>`** 访问。
- **验证**：定向 Go 单测 `TestRuntimeEnvTagsClaudeEntrypointForIDEVisibility` / `TestRuntimeEnvClaudeEntrypointOverridesAndDedupes` 守护；Release 重建 + 覆盖 `/Applications` 安装 + 重启完成，新 runtime 已起（`cordcode-bridge-runtime` 二进制含该 env var）。**端到端：owner 已验收 iOS 新建 Claude session 出现在 VSCode 扩展列表；Claude 桌面 App 不显示（见上，已知不可修）。**

### 2026-06-30 — Claude Code 模型列表以 `~/.claude/settings.json` 为权威源（修复网关场景下选 GLM 仍 529）

- **现象**：iOS Claude Code 模式选 GLM-4.7 发消息，claude 回报 `Repeated 529 Overloaded … inference gateway (127.0.0.1:15721)`，即使 GLM 无速率限制。
- **根因**：`AvailableModels`（[`agent/claudecode/claudecode.go`](agent/claudecode/claudecode.go)）从 provider/网关 `/v1/models`/硬编码取模型，不读 owner 的 `~/.claude/settings.json`。该文件的 `env` 块是一张别名表：`ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL`（真实 id，如 `claude-haiku-4-5`）+ `*_MODEL_NAME`（显示名，如 `glm-4.7`）。网关收到 `claude-haiku-4-5` 才路由到 GLM-4.7，"glm-4.7" 只是显示名。旧逻辑下 iOS 拿到的模型 `providerID="default"`，被 iOS 的 `providerID=="claude"` 过滤丢弃 → 不带 model 发送 → claude 无 `--model` → 网关默认路由 → 529。
- **修复**：
  1. 新增 [`agent/claudecode/settings_models.go`](agent/claudecode/settings_models.go)：读 `~/.claude/settings.json`（`CLAUDE_CONFIG_DIR` 优先），把三对别名映射成 `ModelOption{Name=claude 别名(haiku/sonnet/opus), Desc=*_MODEL_NAME}`；mtime 懒重载（不引入 fsnotify、不启后台 goroutine）；缺失/无映射返回 nil 走 fallback。`AvailableModels` 优先用它。只读 `*_MODEL`/`*_MODEL_NAME`，不把 `ANTHROPIC_API_KEY`/`AUTH_TOKEN` 等密钥反序列化进程序变量。
  2. [`modelProviderForAgent`](go-bridge/handlers.go) 对 `claudecode` 显式返回 `providerID="claude"`（语义正确，且让 iOS 的过滤通过）。
- **映射契约**：`Name`（送 `claude --model`）= 别名，claude 按 settings.json 解析成真实 id 送网关——与 owner 顶层 `"model":"opus"` 同机制。iOS 显示 `*_MODEL_NAME`（glm-4.7）、发送别名（haiku）。iOS 侧无需改动。
- **验证**：定向单测 `TestSettingsModels_*`（5 项）通过；go-bridge model/provider 测试无回归；Release 重建 + 重启完成，新 runtime `runtime_ready`。端到端（iOS 选 GLM-4.7 不再 529、收到回复）由 owner 真机验收。详见 `../cordcode-ios/docs/2026-06-30-claudecode-models-from-settings-json.md`。

### 2026-06-30 — 修复 iOS 向已存在 Claude Code 会话发消息时 go-bridge 崩溃（消息被吞）

- **现象**：iOS 打开一个 Mac 端已存在的 Claude Code session 并发送消息，iOS 收不到回复、Mac 端刷新也看不到这条消息（重启后 iOS 也丢失）。
- **根因（已用 `go-bridge.log` 坐实）**：iOS 打开会话时，`claudeSessionFileRelay`/session-state 事件会先对该 sessionID 调 `markRunning`，在 `sessionRegistry` 留下 `session==nil` 的占位 `trackedSession`（用于状态追踪）。`getSession` 对它返回 `(nil, true)`；`handleSendMessage`（[handlers.go:1817](go-bridge/handlers.go)）只判 `ok` 就调用 `sess.Send`（[:1887](go-bridge/handlers.go)），对 nil 接口派发 → **panic**，HTTP 连接被 net/http 回收，`send_message` RPC 永不返回结果，消息也没送达 agent。
- **修复**：`handleSendMessage` 的首次 session 查找改为 `if !ok || sess == nil`（与同函数二次检查 `existingOk && existing != nil` 一致），把 nil 占位当"未持有真实会话"，回落到 `StartSession(ctx, realID)` 即 `claude --resume` 正确续接。`getSession`/`markRunning` 的状态追踪契约不变（`ok` 仍表示 trackedSession 存在），避免影响 `resume_session` 的 runtimeState 等既有行为。
- **验证**：定向 Go 单测 `TestClaudeSendMessageWithNilStubDoesNotPanic` 复现并守护；go-bridge 全量测试通过（除 4 个因本机未装 `codex` CLI 的环境性失败）。Release 构建 + 覆盖 `/Applications` 安装 + 重启 MacBridge 完成，新 runtime 已 `runtime_ready`。真机端到端（发消息收到回复、Mac 能看到）由 owner 验收。

### 2026-06-30 — Codex 历史 session 下发用户图片附件

- **修复 Codex 历史消息丢失 `input_image`**：Codex JSONL 的 rich history 解析现在会把用户消息里的 `input_image.image_url` 转成协议 `files/parts`，iOS 打开历史 session 时可拿到 Mac 端 Codex 已显示的真实图片。
- **相同 prompt 文本的多图消息保持区分**：图片 file id 由图片 URL 稳定派生；多条同文案用户消息各自携带不同图片时，下发结果不会把后续图片压成前一张。

### 2026-06-30 — send_message 转发图片/文件附件到 agent（跨仓联动）

- **修复 iOS 发来的图片/文件附件被 go-bridge 丢弃**：`SendMessageParams` 原无 attachments 字段，`handleSendMessage`（主路径）与 `ocHandleSendMessage`（opencode 路径）都硬编码 `sess.Send(content, nil, nil)`，导致 agent 永远收不到图。现新增 `AttachmentInput` + `splitAttachments`（`go-bridge/attachments.go`）：按 unified-bridge-protocol 的 `AttachmentInput{kind,mime,filename,base64}` 解码 base64，按 kind/mime 拆成 `core.ImageAttachment`/`core.FileAttachment` 传给 `sess.Send`。三个 driver（claudecode/codex/opencode）都已消费图片+文件，故对所有 backend 生效；非法/空 base64 附件被丢弃不伪造。
- 与 iOS 侧联动：iOS 端 bridge client 现按协议上送 attachments（见 cordcode-ios CHANGELOG）。协议 spec（`docs/protocol/unified-bridge-protocol.md`）早已定义 attachments，本轮补齐传输层实现。

### 2026-06-29 — Codex prompt 模板发送不再依赖共享 4141

- 修复 iOS 在 Codex 模式点击「继续任务 / 总结当前状态 / 只跑相关测试 / 解释失败原因」后报 `codex app-server ws dial ws://127.0.0.1:4141 ... connection refused` 的问题：MacBridge 产品默认不再强制注入共享 Codex app-server URL，未显式配置 URL 时改走 go-bridge 已有的 stdio app-server session 路径。
- 当未配置共享 app-server URL 时，Codex descriptor 会标记为需要历史轮询，避免继续假定存在进程级 passive websocket 事件流；显式配置共享 URL 的高级用法仍保留原 websocket/broadcast 行为。

### 2026-06-28 — Claude Code effort 真值源 + iOS 覆盖持久化

- **修正 Claude Code session effort 同步此前实际不生效**：上一轮虽已把「当前 Claude runtime effort」回填进历史 session，但 MacBridge 的 Claude runtime effort 此前没有任何来源（macOS App 不配置、iOS 仅在发消息时回传），恒为空，导致 iOS 仍显示「自动」。现已改为启动时从 `~/.claude/settings.json` 的 `effortLevel`（Mac 端 Claude Code 的真实全局 effort 偏好；回退到同文件 env 的 `CLAUDE_CODE_EFFORT_LEVEL`）读取并注入 Claude runtime，因此打开任意 Claude Code session 都能显示与 Mac 端一致的智能等级（如 `Extra High`）。
- **iOS 显式改动的 effort 现在跨重启持久化**：当 iOS 发消息时显式改变 effort 且与当前值不同，MacBridge 会把该选择原子写入数据目录的 `claude-effort.json`，重启后优先于 `settings.json` 生效；未显式改动时仍以 `settings.json` 为准。
- 背景澄清：Claude Code 的 transcript 不记录 per-session effort（已抽样确认），故「某历史 session 当时的 effort」不可恢复；MacBridge 能忠实反映的是「Mac 端 Claude Code 当前的全局 effort」及其在 iOS 上的最近一次选择。

### 2026-06-28 — Claude Code session 同步模型与智能等级

- Claude Code 历史 session 列表和单 session 查询现在会把 MacBridge 当前 Claude runtime 的 reasoning effort 补入缺少 effort 元数据的旧 transcript，iOS 打开这些 session 时可显示与 Mac 端一致的模型和智能等级（如 `glm-5.2` + `Ultra`）。
- Claude Code effort 枚举补齐 `low`、`medium`、`high`、`xhigh`、`max`、`ultra`，并兼容旧的 `ultracode` 输入为 `ultra`。

### 2026-06-22 — 修复外部进程会话状态同步缺陷 (运行态同步)

- **修复被动订阅事件流下的会话运行状态更新缺失**：在 `go-bridge` 的 `startPassiveSubscription` 事件监听中，当监听到外部 Agent 独立进程产生的 `turn_started`、`turn_completed`、`session_state_changed`、`session_status_changed` 等代表运行态变化的事件时，实时将状态同步更新到 `h.sessions` 缓存中，并增强 `sessionRegistry` 中的状态标记，允许外部独立进程在缓存尚未注册时自动补齐临时状态。由此解决 iOS 客户端连接并切换到新开运行中会话时，由于 go-bridge 返回的 runtimeState 为空而导致 iOS 侧输入框保持简易模式且思考不展开的问题。

### 2026-06-19 — 恢复拆仓时遗失的维护文档

- 从原一体仓库迁回 MacBridge 构建安装、runtime/端口诊断、go-bridge backend 进程模型和 Relay 部署资料，并作为仓库根目录活文档维护。
- 按当前内嵌 runtime、持久日志、Management API、HPKE Relay、TLS pin 与独立 Go module 状态重写，删除旧 Node Bridge、外部 cc-connect replace、Copilot sidecar 和 FRP 默认路径等过时说明。
- 将共享 `CLAUDE.md` 中的 VPS 主机与用户示例改为占位符，避免项目指南携带机器专属部署值。
- 补齐新 session 冷启动规则、Release 覆盖安装条件，以及 Claude 独立进程、Codex 共享 app-server `4141`、OpenCode HTTP/SSE `64667` 的运行与排障模型；修正失效的 Relay 首装链接和已迁出钥匙串的 identity 描述。
- 对原一体仓库五份关键根文档做逐节覆盖审计，补回 runtime flags/env、完整故障树、事件管线、registry/rebind、离线通知、OpenCode hybrid 路由矩阵、架构约束与旧 VPS/FRP 自定义路径的现行边界。
- 在 `CLAUDE.md` 建立维护入口，并链接 iOS 侧端到端交互文档，减少后续排障只看单仓库导致的误判。

### 2026-06-19 — 修复撤销授权对 relay 连接不生效（安全）

**问题**：在管理 UI 撤销某台 iPhone 的设备授权后，若该 iPhone 走 relay 加密通道连接（默认推荐的远程路径），撤销不会即时生效——iOS 仍能继续访问 Bridge、拉取会话内容，只有杀 App 重启后才进入扫码页。

**根因**：`DeviceConnRegistry`（撤销时负责下发 `device_revoked` 事件并断开连接的注册表）此前只在 direct 直连路径（`server.go`）注册连接，relay 路径的 `RelayDeviceConn` 从未注册进去。撤销授权调用 `DisconnectDevice(deviceID)` 时在注册表里找不到 relay 连接，既不发事件也不断开。

**改动**：
- `DeviceConnRegistry` 的连接存储从 `[]*Conn` 改为 `[]Connection`（接口），让 direct 与 relay 两种连接类型都能注册。
- relay 连接在认证成功后注册到注册表；在 stale 清理、心跳半开清理（`pruneDeadDevice`）、`closeConn`、`Close` 四处连接移除点同步注销，避免撤销时对已关闭连接发事件。
- `DisconnectDevice` 在下发 `device_revoked` 事件后补 `conn.Close()`，确保即使客户端未及时处理事件，连接也被强制断开（此前 direct 路径仅发事件不 Close）。

**提升**：撤销授权对 direct 与 relay 两条路径行为一致、即时生效。新增 `device_conn_registry_test.go` 覆盖接口化存储、多连接撤销、注销隔离（相关测试 3/3 通过）。

### 2026-06-19 — Relay 凭据迁出钥匙串，消除重装后授权弹窗

**问题**：每次重装 MacBridge 后打开 App，macOS 弹出「CCCode Bridge 想要使用你储存在钥匙串的机密信息」并要求输入登录密码。根因是 App 走 ad-hoc 签名，钥匙串按代码签名 / Team ID 授权访问，重装后 Team ID 变化即判定为陌生应用、触发授权弹窗。这对「下载即用」的普通用户是不可接受的体验。

**改动**：Relay 的三份密钥（route credential、activation install id、activation 签名私钥）从钥匙串迁出，改用文件存储，与 OpenCode `credentials.json` 同目录（`~/Library/Application Support/CCCode Bridge/relay-secrets/`）、同样 `0600` 权限。

- **提升**：重装 / 升级后不再弹钥匙串授权窗口；无需开发者证书或稳定 Team 签名。文件存储的 0600 保护对「丢了可重新 provisioning」的 relay route credential 安全性足够。
- **一次性迁移**：存量用户首次启动新版时，若文件不存在且旧版钥匙串条目还在，自动读取旧值、写入文件、删除钥匙串条目——凭证无缝继承，不会因迁移丢值而触发重新 provisioning（后者曾导致 iOS 配对的端到端凭证与 Mac 端不一致、显示离线）。迁移为尽力而为：钥匙串读取失败（含用户拒绝授权）时不阻塞，直接新生成凭证。
- **全新安装无影响**：没有旧钥匙串条目的用户从头走文件存储，行为与旧版等价。

### 2026-06-19 — 深度运行期 Code Review 修复（commit `a85adf1f613e`）

本轮按 `docs/2026-06-19-deep-runtime-implementation-plan.md` 完成 11 项运行期修复（T01–T11），经独立审计（`docs/2026-06-19-implementation-完成情况-审计报告.md`）逐行反查源码 + 独立复跑全部测试通过。覆盖安全、进程治理、Relay 背压、跨进程契约、稳定性五类。

#### 安全
- **控制面凭据不再泄漏进 agent 子进程（T01）**：agent（Claude/Codex/OpenCode）及其工具子进程的环境从「全量继承 `os.Environ()`」改为 deny-list（`CCCODE_*`/`OPENCODE_SERVER_*`/`CLAUDECODE`）+ 运行时 allowlist 双保险。stderr 在进入日志/错误帧前统一脱敏。
  - **提升**：远程设备无法再通过 agent 工具（如 shell）读取到 go-bridge 的管理 token、relay 凭据，消除了从 data-plane 向 loopback 控制面横向移动的风险。
- **配对限流 bucket 无界增长治理（T08）**：新增惰性 TTL 清理 + 全局容量上限（4096），超限对新 key fail-closed。
  - **提升**：任意 pairingId/IP 无法制造无界内存增长。

#### 稳定性 / 进程治理
- **runtime shutdown 不再泄漏 agent 子进程（T02）**：新增幂等、deadline 约束的 `Handlers.Shutdown`，并发关闭所有活跃 session；main 的关停顺序修正为 HTTP Server → handlers.Shutdown → CloseAllConnections → relay/tls/mgmt。
  - **提升**：SIGTERM / 睡眠唤醒 / 长跑后不再残留 agent 子进程。
- **连接 Close 超时不再触发 events channel panic（T03）**：四条订阅路径（opencode/codex/sse/appserver）对齐范本——超时分支绝不直接 `close` channel，改由延迟 goroutine 等 producer 退出后再关。
  - **提升**：消除「连接 Close 超时后 producer 仍发事件 → closed-channel send panic 崩溃」。
- **Claude 改为进程组回收（T02）**：新增 `Setpgid` + 进程组 kill，对齐 codex。
  - **提升**：Claude 的 sudo/shell/插件子进程不再在 shutdown 后残留。
- **修复 Codex 重连测试自身数据竞争（T11）**：`closeCount` 改 `atomic.Int32`。
  - **提升**：`-race -count=20` 稳定通过，修掉实跑复现的 DATA RACE。

#### Relay 背压（独立 module，已部署生产 `wss://relay.byteseek.uk:8443`）
- **per-device 有界发送队列，消除跨 device 队头阻塞（T04）**：每个 device 一个有界队列（256 帧 / 8 MiB）+ 专用 writer goroutine；bridge 的投递从同步 write 改为非阻塞 enqueue，队列满则断开慢 device 并把当前帧落入 mailbox（不丢）。
  - **提升**：原先一个卡住的 device（满 TCP 窗口）会阻塞**同 route 所有 device** 的投递；现在慢 device 只断开自己，正常 device 照常收帧。

#### 跨进程契约（Swift）
- **连续 restart 收敛为单次启动（T05）**：`launchGeneration` + 可取消 `restartTask` + `applyConfigAndRestart`；100 ms 内连续多次 restart（配置变更 + Relay provisioning 回调）收敛为单次进程启动。
  - **提升**：不再端口反复接管 / ready frame 抖动。
- **runtime.json / management-token 写失败 fail-fast（T06）**：`WriteReadyFrame` 返回 error，写失败时发布 `bootstrap_persist_failed` + exit，绝不发布 ready。
  - **提升**：磁盘满 / 权限错误时不再进入「网络已开放但 UI 永远未就绪」的假死态（原先每 60 s 重启）。
- **management API 客户端短超时 + 轮询解耦（T07）**：专用 ephemeral `URLSession`（request 2 s / resource 5 s）替代 `URLSession.shared`；status 决定存活状态、agents 刷新改为独立低优先级任务。
  - **提升**：management 半开（accept 连接不响应）时 supervisor ≤ 5 s 进入恢复，而非卡死数十秒。

#### 架构债务（非用户可见，为后续铺路）
- **Handler 生命周期组件可整体关闭（T09）**：`ObservationManager` 的 lease loop 从构造函数移到显式 `Start(ctx)`；`StartCleanupLoop` 改可停 ticker。测试不再泄漏 goroutine。
- **god-object 最小治理（T10）**：新增实例级 `ConfigRepository`，旧包级全局标注 `Deprecated`。为后续拆分 `handlers.go` / `config.go` 铺路，本轮不拆大文件。

#### 前后对比

| 维度 | 之前 | 之后 |
| --- | --- | --- |
| 安全边界 | agent 可继承控制面密钥 | deny-list + allowlist 双保险，stderr 脱敏 |
| 进程生命周期 | shutdown 泄漏子进程、events panic | 统一 shutdown + 进程组回收 + 安全 close |
| Relay 可用性 | 单慢 device 拖垮整条 route | per-device 隔离背压 |
| Swift 状态机 | 双 restart、假就绪、mgmt 卡死 | generation 收敛 + fail-fast + 短超时 |
| 可测试性 | 全局状态、race、goroutine 泄漏 | 实例注入、atomic、显式 Start |

#### 验证
- `go build` / `go vet ./...` + relay-server build/test/race + Swift `xcodebuild build` 全绿
- 11 项定向测试全通过，无新增回归（pre-existing 失败：未装 codex CLI、`AvailableModels` 时序 flaky，已 baseline 确认与本轮无关）
- 完成：`docs/2026-06-19-deep-runtime-implementation-plan完成情况.md`；审计：`docs/2026-06-19-implementation-完成情况-审计报告.md`

---

> **维护说明**：后续每轮工作请在最上方（`[Unreleased]` 下）按相同结构追加一节，标题为「日期 — 主题（commit）」。发布正式版时把 `[Unreleased]` 改为对应版本号与日期，再新开一个 `[Unreleased]`。
