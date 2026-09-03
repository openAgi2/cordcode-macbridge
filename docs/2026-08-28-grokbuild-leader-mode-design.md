# Grok Build Leader 模式开关设计（B 路线：官方共享 leader 接入引导）

- 日期：2026-08-29（v10 终版：九轮必改已落实，**十轮机械复核 APPROVE**。四~九轮结论均为
  "修改后通过"，十轮结论为 APPROVE；处置见
  [§12](#12-一轮评审采纳记录2026-08-28) / [§13](#13-二轮评审采纳记录2026-08-28) /
  [§14](#14-三轮评审采纳记录2026-08-28) / [§16](#16-四轮评审采纳记录2026-08-28) /
  [§17](#17-五轮复审采纳记录2026-08-29) /
  [§18](#18-六轮复审采纳记录2026-08-29) /
  [§19](#19-七轮复审采纳记录2026-08-29) /
  [§20](#20-八轮复审采纳记录2026-08-29-有限闭合清单) /
  [§21](#21-九轮机械闭合复核采纳记录2026-08-29最终有限清单) /
  [§22](#22-十轮机械复核结论approve2026-08-29)。**§15 Owner 裁决门已于 2026-08-28 签署**：
  D-1 批准 D-G1、D-2 否决现状并采纳 D-G2、D-3 接受、D-4 接受。行数以文件现状为准。）
- 评审报告：[一轮](2026-08-28-grokbuild-leader-mode-design-review.md) /
  [二轮](2026-08-28-grokbuild-leader-mode-design-review-r2.md) /
  [三轮](2026-08-28-grokbuild-leader-mode-design-review-r3.md) /
  [四轮](2026-08-28-grokbuild-leader-mode-design-review-r4.md) /
  [五轮](2026-08-29-grokbuild-leader-mode-design-review-r5.md) /
  [六轮](2026-08-29-grokbuild-leader-mode-design-review-r6.md) /
  [七轮](2026-08-29-grokbuild-leader-mode-design-review-r7.md) /
  [八轮](2026-08-29-grokbuild-leader-mode-design-review-r8.md) /
  [九轮](2026-08-29-grokbuild-leader-mode-design-review-r9.md) /
  [十轮](2026-08-29-grokbuild-leader-mode-design-review-r10.md)
- 状态：**设计稿 v10（终版，2026-08-29 十轮机械复核 **APPROVE**）。R9 最终有限清单
  （fence 四类状态变化及 G7/G8 缓存回归、releasePassiveClaim typed outcome、T1–T33 与
  unknown 第四状态术语、连续负样本不重置锚点、T31–T33 manifest 登记）已逐行核对全部
  命中。本设计可交开发 agent；**下一步必须先执行 §0.2 Phase 0 十步现场拓扑证明**，
  通过前不得进入产品代码实施（本次批准的是设计可实施性，不代表 Phase 0、构建、测试
  或生产路径已验证）。**
  **实施规范 = §0–§11 正文；§12–§22 仅供审计追溯。**
  本设计覆盖
  "Mac 端开关 + 官方配置写入 + 状态可视化 + 观察通道自动升级 + 两处受控 Go 改动"；
  follower 交互升级是后续另案。
- **2026-09-02 漂移复核（re-pin）**：两仓 main 前进（MacBridge `2bb415b`，+123 commit
  codex-remote 线；grok-build `bc7f02e`，+1 monorepo sync）后逐项核对：**设计方向、
  D-G1/D-G2 语义、Phase 0 门全部不变，无需重新评审；本文仅重灌来源 pin 与漂移行号
  （§0–§11 语义与 §12–§22 评审记录未改动）**。上游同步后 grok 实际安装版与源码 pin 的
  版本差距可能进一步扩大——**Phase 0 第 1 步（实际安装版 grok 版本与 flag/config key
  核对，评审时实测 1.0.12 / 源码 pin 1.0.10）重要性上升**；有利变化：chat 互斥已抽为
  可单测函数（§2.1-5）。
- 背景裁决：2026-08-28 跨仓源码评审确认，Grok Build 官方架构下通往 codex-web 式
  "共享会话 server 实时接力"的唯一有效路径是 leader 模式；workspace daemon 不承载对话、
  code.grok.com 是与本地初衷无关的云端滞后镜像、`grok agent serve` 单活跃客户端。
- 结构参照：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)、
  [2026-08-18-opencode-web-backend-design.md](2026-08-18-opencode-web-backend-design.md)、
  [2026-08-16-dsh-web-backend-design.md](2026-08-16-dsh-web-backend-design.md)。
- 不变约束：CordCode 初衷 + source-first 真实证据纪律 + 构建测试成本纪律。定级：TOML
  依赖接入 **D4**（§3.6）；D-G1 / D-G2 Go 改动各 **D3**（§3.5，独立提交）；manager
  单测 **D2**；UI **D1/D2**（§7.4）。

### 来源清单（P0，本文全部断言的来源身份）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=2bb415b399e41fe688a4b898a8457d28cda0afd9（2026-09-02 漂移重灌时复核；
十轮评审 pin 为 032fdd8105ce41d4e41063922eb8eae39aaed0e5，经 codex-remote 线
123 commit 合并前进，设计核心资产零改动、少数锚点行号漂移，已逐项重锚）
未提交状态=复核时工作树干净；设计稿与 r1–r10 评审报告已随 d3980ef 提交入库，
本次漂移重灌编辑为唯一未提交变更
任务预期分支=main
配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=bc7f02eddd3d84085849dc19ed216f11c23b0571（漂移重灌时复核；十轮评审
pin 为 9684fa3cdbf2995e30ea8b9b637f1db008f144fc，前进 1 个 monorepo sync commit，
核心语义全部存活、仅行号漂移，已逐项重锚）
配套未提交状态=干净
预期产品特性=MacBridge 工作站 grok build 行内「Leader 模式」开关（当前未实现）
```

**版本身份警示**：评审机实际安装 `grok 1.0.12 (ece2b556c271)`，而配套源码
`xai-grok-version` crate 为 `1.0.10`。本文源码断言以 pin 提交为准；Phase 0 第 1 步必须
记录实际安装版本并核对 flag/config key 支持（§0.2）。

实施 agent 接手时必须在编辑前、构建前按 CLAUDE.md P0 规则重新生成并核对本清单；grok-build
侧若已前进到新提交，§2 的官方源码断言需先复核再开工。

---

## 0. 路线裁决（为什么是 leader 模式）

2026-08-28 对 grok-build 官方源码（pin `9684fa3`）的四条候选路径评估结论：

| 候选路径 | 结论 | 决定性证据 |
| --- | --- | --- |
| workspace daemon / hub | ❌ 不承载对话。daemon 是沙箱/本机 tool-server，主动出站连 xAI 云 hub，本地不监听；广播事件明确排除工具执行与对话内容 | `xai-grok-workspace-types/src/events/workspace.rs:20-22` |
| `code.grok.com` 后端 HTTP API | ❌ 与本地初衷无关。它是本地会话的可选云端 writeback 镜像（`StorageMode` 默认 `Local`，end-of-turn flush，best-effort），本地 JSONL 才是官方真相；绕云读本地是倒退 | `xai-grok-shell/src/remote/sync.rs`（"local JSONL files are the source of truth"）、`config/mod.rs:865-910` |
| `grok agent serve` | ❌ 单活跃客户端：每个新连接替换上一个转发目标，非多客户端共享；且是自持实例，与用户官方工作流零共享（等价 codex-web 禁止的 managed-loopback 形态） | `xai-grok-shell/src/agent/server.rs:501-555` |
| **leader 模式** | ✅ **官方协议级多客户端共享会话 server**：单 leader/机器，TUI、IDE 扩展、headless/web 多客户端经 `~/.grok/leader.sock` 共享同一 agent；会话订阅者模型、per-client 能力路由、mid-turn interjection 广播齐全 | `xai-grok-shell/src/leader/mod.rs:1-30`、`leader/protocol.rs:158-177` |

**默认用户的首要前置条件**（不是唯一条件；显式 `--no-leader`、requirements/MDM 覆盖、
confinement veto、stale socket、interaction 等边界见 §2.1/§6）：`[cli] use_leader` 默认
`false`（完整解析链见 §2.1-1），默认用户的 TUI 是 inline 内嵌 agent，`~/.grok/leader.sock`
不存在。本设计的产品动作就是把"用户在 user 层 config 写两行"变成 **MacBridge 工作站
grok build 行内的一个开关**（用户点击即授权），并在 UI 上诚实呈现配置/生效两级状态。

### 0.1 效果边界（本设计能拿到什么）

开关打开且用户以 leader 模式运行 grok 后：

- macbridge 的外部会话观察**自动**从 1s `updates.jsonl` 文件轮询升级为 leader 实时推送
  （`agent/grokbuild/grokbuild.go:156-198` 已有的路径选择逻辑，核心链路零改动；另含两处
  Owner 批准的受控 Go 改动：D-G1 建立失败回退 §3.5.1、D-G2 observer 随订阅取消 §3.5.2）；
- 摆脱 file tailer 的 90s grace / 30min hardCap 退出逻辑；
- iPhone 实时看到用户在 grok TUI（或经 relay 挂同一 leader 的 grok.com web）发起的 turn。

**拿不到的**（诚实声明，禁止 UI 暗示）：iOS 仍不能停外部 turn、不能替答外部 turn 的权限
请求——`LeaderSubscriber` 保持只读 observer（`leader_subscriber.go:319-322`），交互升级是
后续另案（§9）。

### 0.2 顺序不可倒置：先证宿主拓扑，再做开关（Phase 0 门）

**前置门：§15 Owner 裁决门已于 2026-08-28 签署（四项 decision 见 §15）**；v10 落实
九轮最终有限清单、且逐行机械核对全部命中（APPROVE）后即可执行 Phase 0——**该条件已
于 2026-08-29 由第十轮机械复核满足（结论 APPROVE，见 §22）；Phase 0 十步现场拓扑证明
现在可以执行，通过前不得进入产品代码实施**。**本句是 Phase 0 开工的唯一权威门；§15
仅为 Owner 产品裁决记录，不单独授权开工（r8-M8）。**

从 codex-web 迁移的第一原则同样适用：**先让用户的 grok 实际以 leader 模式运行、证明
leader.sock 出现且 macbridge 只读订阅能实时收到事件，再把开关做进产品 UI。** 不能先把
Toggle、文案、诊断做完，把"配置写入是否真的让官方 TUI 变 leader"留到交付末期验证。

宿主拓扑证明链（Phase 0，任何 Swift 代码之前，手工完成并留存日志）：

1. **版本与启动身份**：记录实际安装 `grok --version`（评审环境为 1.0.12，源码 pin 为
   1.0.10，属已知漂移）；核对 `--leader` / `--no-leader` / `--leader-socket` flag 与
   `[cli] use_leader` key 在该安装版上存在；**记录实际启动命令行，确认不含 `--no-leader`**
   （最高优先级 veto，r3-M4）与 `--chat`；记录 grok TUI、CordCode Link app 与 go runtime
   三方**实际继承**的 `GROK_HOME` / `GROK_LEADER_SOCKET`，确认解析后的 socket 路径一致
   （`leader_subscriber.go:63-79` 同链解析）。注意：TUI 自己的 `--leader-socket` flag 在
   其进程内转为 `GROK_LEADER_SOCKET` env（`pager-bin/src/main.rs:2007-2009`），CordCode
   Link 不会自动继承另一个进程的 flag——custom socket 场景按 §6-8 excluded 处理；
2. **user 层写入与 effective 证据**（r4 修订：日志隔离 + requirements 文件保护）：
   - **日志规则（r4-M2 + r5-M2）**：`GROK_LOG_FILE` 以 **append** 模式打开
     （`xai-grok-telemetry/src/appender.rs:13-25`）——**每次启动使用独立空文件，且路径
     必须先保存到变量并登记**：`LOG=$(mktemp)` →
     `echo "p0 <子例名> log: $LOG" >> p0-evidence.txt` → 以 `GROK_LOG_FILE="$LOG"` 启动
     → 事后从**同一路径**取证（禁止不保存路径的 `GROK_LOG_FILE=$(mktemp)` 内联写法——
     执行后无法可靠取回文件名，证明链不可执行）；禁止复用同一文件；若不得已复用，必须
     记录进程 PID 与启动时间，只取该进程最新一条解析行；
   a. 手工在**解析后的 `$GROK_HOME/config.toml`**（user 层，即开关将来做的事）写入
      `[cli] use_leader = true`，按上方日志规则以独立日志文件（路径已登记）启动 grok
      TUI，核对 INFO 行
      `pager TUI leader mode resolved` 的字段：`use_leader=true`、`policy_disable_reason`
      无、`leader_disabled_by_sandbox=false`（日志通道 `debug_log.rs:8,81-82,414-421`，
      默认 DEBUG 级；字段定义 `app/mod.rs:680-689`）；
   b. `grok inspect --json` **仅用于证明参与层路径**（config_sources 含 user 层；
      `inspect/mod.rs:55-82,265-290,1067-1175`——它只输出 config_sources 与 warnings，
      **不输出 effective 值**，不得当作生效证据）；
   c. **可执行负例一（requirements 覆盖；r4-M1 加保护）**：**首选独立临时 GROK_HOME**：
      ```bash
      TMPHOME=$(mktemp -d); LOG=$(mktemp); echo "p0 requirements log: $LOG" >> p0-evidence.txt
      printf '[cli]\nuse_leader = true\n'  > "$TMPHOME/config.toml"
      printf '[cli]\nuse_leader = false\n' > "$TMPHOME/requirements.toml"
      GROK_HOME="$TMPHOME" GROK_LOG_FILE="$LOG" grok   # 启动后即退出
      # 期望：$LOG 中 use_leader=false；同 env 运行 grok inspect --json 应含 requirements 层
      rm -rf "$TMPHOME"
      ```
      若该安装版在无凭据的临时 home 下无法输出启动解析行（TUI 提前退出），回退到真实
      home 执行，且必须：**先 `lstat` `$GROK_HOME/requirements.toml`——已存在则逐字节
      备份到独立位置（同时记录 inode/device），测试后做内容身份比较并原子恢复，恢复
      失败立即停线保留现场；不存在才允许创建，删除前同样复核 inode/device 与逐字节内容
      仍等于本轮写入——身份已变（并发期间被其他进程替换）则保留现场停线，不删**。
      任何情况下不得直接覆盖或无条件删除既有文件；
   d. **可执行负例二（confinement veto）**：以 confining sandbox profile 启动（如
      `GROK_SANDBOX=<非 off profile>` env 通道，`config_layers.rs:59-60` 官方注释确认），
      **使用另一个独立日志文件**，应显示 `leader_disabled_by_sandbox=true`；
3. 启动 grok TUI（项目 A）并正常发一条消息，确认解析后的 socket 路径出现文件、TUI 行为
   无退化；
4. iPhone 打开同一会话，核对 `go-bridge.log` 的**完整证据链**：`grokbuild: leader
   subscriber starting`（`grokbuild.go:192`）→ `leader subscriber connected`
   （`leader_subscriber.go:147`）→ `leader subscriber live`（`leader_subscriber.go:199`，
   该行证明 initialize/session/load 完成，仅 `connected` 不足以证明订阅成功）→ 真实
   turn 事件到达 iPhone；
5. 在**第二个项目 cwd / 另一个 session** 上重复第 4 步（多项目订阅验证）；
6. **TUI 关闭的两个子场景**（observer 不随 iOS 关闭会话取消——**D-G2 实施前的现状**，
   见 §2.1-3；D-G2 实施后该窗口收窄为"iOS 订阅 + grace"，§3.5.2）：
   a) 无 pending interaction 的 turn 进行中关闭 TUI——观察 turn 是否在 leader 上继续
   完成至终态、iPhone 是否看到完整收口（**待证假设**：interaction 是共享广播
   first-answer-wins，observer 收到但不答；无 interaction 时无人应答也可能完成——以实
   测为准）；b) permission/question/plan approval pending 时关闭 TUI——观察 turn 是否
   长期等待（预期风险，§6-6）。两个子场景都记录 leader 是否因 observer 存活；
7. **driver transfer 实测**：TUI 断开时 session driver 是否转移到 macbridge observer
   （`leader/server.rs` Disconnected 分支 "Transferred session driver after disconnect"）；
   归因注意：interaction 请求本来就是**共享广播**、与 driver 无关（`server.rs:491-500`），
   driver transfer 只影响 driver-only 消息路由——本步验证的是后者；
8. **stale socket 与 F-7（实施前基线）**：`kill -9` leader 留下残留 socket 文件 → 验证
   **第二次** session-open 仍选择同一失效 socket 并再次失败（**这是 D-G1 实施前的基线
   记录**；D-G1 实施后由 §7.3 第 11 行复验回退行为）；turn 进行中 crash leader，验证
   iPhone 收到 `turn_aborted(leader_disconnect)` + idle（F-7 兜底的真实触发场景）；随后
   人工清除 socket 文件，验证下一次订阅恢复；
   > **2026-09-02 实测修正**：本步前半的「第二次 session-open 仍失败 + 需人工清
   > socket」基线**不成立**。kill -9 后 owner TUI 在 0.1s 内触发重连、新 leader spawn
   > 并成功 bind（flock 随进程死亡释放，connect 级 ECONNREFUSED 驱动
   > connect_or_spawn，新 bind 替换 stale socket；TUI 无限重连 1s/30s backoff，
   > `leader/mod.rs` connect_or_spawn + `leader.ipc.reconnected`）。剩余有效语义：
   > **observer-only 场景（无任何 grok 客户端在场）leader 死亡后不自愈**——macbridge
   > 永不 spawn leader 的自然后果；iPhone 视角 = relay 断开后事件流停，直到任何 grok
   > 客户端把 leader 拉起且 iPhone 重开会话。D-G1 的「检测到 socket ≠ 已订阅生效」
   > 红线不变；「人工清 socket」步骤从验证计划中移除。F-7 部分不变。
9. **回退验证（D-G2 实施前基线：先退出 CordCode Link）**：退出 CordCode Link（**仅关闭
   iOS 会话不够——现状 observer 不随订阅者数量取消**，见 §2.1-3）→ 确认 leader 进程随后
   退出 → 删除 user 层键 → 重启 grok 确认回到 inline + tailer 路径（日志出现
   `falling back to updates.jsonl file tailer`）。D-G2 实施后由 §7.3 第 12 行复验"关闭
   会话 >grace 即取消"，无需退出 Link；
   > **2026-09-02 实测修正**：「确认 leader 进程随后退出」被证伪——**leader 生命周期
   > 完全由 grok 客户端侧维持，与 CordCode Link 无关**：退 Link 后 owner TUI 立即
   > respawn leader；TUI 全关后 leader（`--no-exit-on-disconnect`，PPID=1 孤儿）仍存活；
   > SIGTERM 退出后 socket 文件残留（与 kill -9 一致，进程死亡路径均不触发
   > cleanup_socket，需手动 rm 才能让 macbridge 的 os.Stat 走 tailer 分支）。
   > 实际执行序列：删键 → 退 Link → 新 TUI 验证 inline（GROK_LOG_FILE 193KB 零
   > leader 痕迹）→ Link 重启 → SIGTERM leader + rm socket → iPhone 重开会话 →
   > 15:09:44.231 fallback 行 + .241 tailer starting 行。回退闭环结论不变：开关
   > 回退只依赖 socket 存在性（os.Stat 判据），不依赖 Link 侧状态；D-G2 后
   > 「退 Link 不再把 leader 续命」的表述应理解为「取消 observer 订阅」，而非
   > 「导致 leader 退出」。
10. **场景边界声明**：sandbox/confinement veto（§2.1-1）、chat 模式互斥（§2.1-5）、
    custom socket（§6-8）三场景在本设计中为 **excluded**：不做验收承诺，帮助文案说明。
    2026-09-02 三锚点在 bc7f02ed 复核成立：`resolve_leader_mode` docstring
    （confinement 链后 veto、不回收共享 leader）、`chat_mode_conflicts_with_leader`
    （`app/mod.rs:692` + 官方单测 `:2188-2192`）、`--leader-socket` flag 进程内转 env
    （`xai-grok-pager-bin/src/main.rs:2039-2041`；设计原引 `:2007-2009` 行号漂移，
    实为 `crates/codegen/xai-grok-pager-bin/`）。

任一步失败 = 官方实际行为与 §2 源码推导存在分歧：停止产品代码，保留失败现场，回写本文
§2 后重新裁决。UI 状态呈现、诊断行、文案打磨全部属于"拓扑已证明后"的产品面；单元测试
通过、Toggle 演示可用、config 文件写入成功，均不能单独授权后续 Phase。

### 0.3 改动边界与代码来源纪律

本设计**不新建 backend、不修改 `agent/grokbuild` 的协议实现**（`leader_subscriber.go`、
relay 协议、capability 声明全程零改动）。与 codex-web"空目录原则"对应的边界约束：

- 新增实现分五块：`GrokLeaderModeManager`（配置读写）、agentRow 行内 UI、TOML 依赖接入
  （§3.6，D4）、以及 **Owner 批准的两处受控 Go 改动**：D-G1 建立失败回退（§3.5.1，仅
  `agent/grokbuild/grokbuild.go`）与 D-G2 subscriber-aware cancellation（§3.5.2，
  `go-bridge/handlers_relay.go` + `go-bridge/types.go` 的 sessionRegistry unknown 态与
  claim token/unknown 态（第四状态）消费者安全改造，r6-B1/r7-B1 授权扩展）。TOML 编辑器的职责边界是"单键外科手术"，**禁止**把它
  写成通用 config 重写器，也禁止顺手支持修改其他键；
- **禁止顺手改 go-bridge**：观察路径选择（`grokbuild.go:156-198`）现状、relay 协议、
  `RequiresExternalTurnPolling` 声明是本设计的地基；**唯二例外是 §15 D-1/D-2 批准的
  D-G1 与 D-G2**——二者必须各自单独提交、单独测试、独立可回退（D-G1 只碰
  `grokbuild.go` 的 leader 失败分支；D-G2 只碰 `handlers_relay.go`——grok relay loop
  及六处 `!isIdle` 消费点的 `isKnownActive` 等价替换——与 `types.go` 的 registry
  unknown 态/generation claim 扩展），
  不得混入 Swift 改动或互相混写。实施中发现 runtime 其他缺陷时另案处理并回写本文；
- **禁止删除或绕过 file tailer**：leader 不存在时它是唯一观察路径（§4.3）；D-G1 也只是
  在 leader 建立失败时**回到**它；
- **禁止把只读观察结论类推为可写**：未来 follower 升级案必须按 source-first 纪律，用
  leader 协议 request 方向的真实样本重新建立语义；不得以"observer 已能收事件"推论
  "发请求也行"。

### 0.4 既有实现资产与评审基线（本设计 ≠ 新 backend 实施，防止按错模板审查）

**给评审 agent 的第一句话：本设计不是 dsh-web / opencode-web / codex-web 那类"新建
backend adapter"实施。** 那三份设计的主体交付物——官方 API 客户端、事件泵、session
binding、协议翻译、capability 组织——在本设计中**不是待实施项，而是已存在于
`agent/grokbuild` 与 go-bridge 的既定基线**。如果评审过程中发现自己在找"API 调用实现"、
"SDK 封装"或"协议翻译层"的新代码，说明正在按错误的模板审查本设计。

已存在、本设计零改动的资产（评审作为既定基线核对，不作为交付物检查）：

| 层 | 已有实现 | 位置 |
| --- | --- | --- |
| leader socket 协议客户端（envelope 编解码 / register→initialize→session/load 握手 / 30s ping / isReplay 过滤 / 只读不答请求） | `leader_subscriber.go`（协议以 grok-build 官方 `leader/protocol.rs` 为验证依据，见文件头注释） |
| ACP 编解码（`session/update` → `core.Event`，两路数据源共用） | `acp_codec.go` |
| 观察路径选择（leader.sock 探测 + tailer fallback，本开关的激活点） | `grokbuild.go:156-198` |
| relay loop（turn_started 合成 / promptId 身份 / F-7 中断兜底 / idle 收口） | `go-bridge/handlers_relay.go` |
| 自有 turn 驱动（`--no-leader` stdio / cancel / permission 应答） | `session.go` |
| catalog（ACP `session/list` 单例 + 分页 + 标题回填） | `catalog_session_list.go` |
| rich history（`chat_history.jsonl` + 稳定行号 ID） | `session_catalog.go` |
| 文件 tailer 备胎（1s 轮询，当前生产主路径） | `updates_file_tailer.go` |

含金量区分（评审与验收都应知道）：**tailer 路径是生产主路径**，同一 codec 的翻译正确性
已被真机长期验证；**leader 客户端路径有实现与单测但生产暴露少**——Phase 0 兼任它的首次
真实链路检验。

本设计的**全部新增面**（评审只需检查这些）：

1. `GrokLeaderModeManager`（TOML 单键读写，§3.3 规则与 §3.6 依赖是评审重点）；
2. `agentRow` Toggle 与状态呈现（§3.4 状态机 + 诚实性红线）；
3. TOML 依赖接入与接线（§3.6，D4 门）；
4. D-G1 + D-G2 两处受控 Go 改动（§3.5，均经 §15 批准，独立提交）；
5. Phase 0 拓扑证明链的执行证据（§0.2）。

评审关注点对照（三份参照模板 → 本设计的对应物）：

| 参照模板的评审关注点 | 在本设计中的对应 |
| --- | --- |
| 官方 API / SDK 调用是否 source-first | 已由既有资产满足；本设计新增面无协议工作 |
| 事件泵 / session binding / codec 正确性 | 已存在且零改动；本设计评审核对的是"零改动"本身（diff 范围见 §11） |
| capability 声明与 fail-closed | 本设计不动能力声明；等价的 fail-visible 要求 = 提示不得伪装已生效（§11） |
| 真实样本纪律 | 体现为 Phase 0 拓扑实测（§0.2）+ §3.3-9 样本 manifest（真实/官方 fixture/synthetic 分级） |
| 新旧 backend 并存与退役门槛 | 不适用——不新建 backend，无并存问题 |

---

## 1. CordCode 初衷约束

- **本地官方工作流延伸**：用户工作流是本地 grok 进程 + 本地文件；本设计不引入任何云依赖
  （与 code.grok.com 路线划清界限）。
- **只动一个官方开关项**：修改 user 层 config 仅限 `[cli]` 节的 `use_leader` 单键；不碰
  auth、账号、provider、MCP、权限等其他任何键。
- **可逆**：关闭 = 删除该键（恢复默认语义），不硬写 `false`；写前留备份。
- **失败可见（fail-visible）**：写入失败回弹开关并明示；配置已写但未检测到 socket 必须
  以独立状态呈现；**"检测到 socket"也不得表述为"已订阅生效"**（§3.4）。stale socket 的
  持续阻断已由 §15 D-1 批准的 D-G1 消解（§3.5.1：回退即恢复观察 + INFO 日志）。
- **不越权**：MacBridge 不 spawn leader、不抢 flock、不驱动会话——保持
  `leader_subscriber.go:3-12` 建立的只读共存纪律。

仓库先例（"官方工具配置托管修改"模式）：OpenCode managed_local 同步写 OpenCode Desktop
配置（`OpenCodeManagedServer.swift`）；codex-web 由 `RuntimeManager.configureCodexDesktopSharedRuntime`
向用户 launchd 域写 Desktop attach 开关。共同特征 = 用户显式点击授权 + 单一开关项 + 可逆 +
诊断可见。本设计完全沿用该模式。

---

## 2. 前置核实结论（source-first 证据）

证据与实现优先级（低级证据不得覆盖高级事实）：**官方源码（pin `9684fa3`）> Phase 0 本机
拓扑实测 > macbridge 既有实现行为 > UI 状态呈现**。任一断言超出已证明范围时只能标
`unverified`，不得继续依赖它编码。

### 2.1 官方侧（grok-build `main@9684fa3`）

1. **`use_leader` 完整解析链**，按优先级：
   `--no-leader` flag（最高优先级，显式给出即关闭，r3-M4 纳入归因）→ `--leader` flag →
   eligibility → **effective 分层配置**（见第 2 条）→ cli-chat-proxy
   `RemoteSettings.leader_mode`（服务器推荐，仅本地未设置时生效）→ 默认 `false`；**随后**
   `requested_confinement`（sandbox/confinement profile）对已解析出的 leader 结果做**最终
   veto**（`xai-grok-pager/src/app/mod.rs:394-437`，precedence 注释 :394-398 +
   `requested_confinement` veto 含于同段）。
   confinement veto 不是 eligibility 的一部分——两者位置不同，诊断"开了开关但 leader
   不生效"时必须分别排查。
   - flag 定义与默认取配置的说明：`xai-grok-pager/src/app/cli.rs:290-294`
     （leader/no_leader 定义；`--no-leader` 引用点 :1187）；
   - `--leader-socket` 可覆盖默认 socket 路径（仅影响 grok 自身进程，且就是转为
     `GROK_LEADER_SOCKET` env，`pager-bin/src/main.rs:2007-2009`）：`cli.rs:433`
     （`leader_socket` 字段；flag 字符串引用点 :1265）；
   - config 解析函数：`xai-grok-shell/src/util/config/mcp.rs:1841-1861`（重构为
     `use_leader_from_toml_opt` / `use_leader_from_toml` 两函数，未设置默认
     false 不变；默认值测试在 `mcp.rs:2199-2223`，官方 fixture 含 canonical true/false
     形态 `mcp.rs:2177-2196`）；
   - 服务器推荐 fallback：`xai-grok-config-types/src/lib.rs:448-450`。
2. **配置分层事实**：官方 `ConfigLayers`（`xai-grok-config/src/config_layers.rs:20-75`）
   按"lowest→highest priority"逐层 deep-merge（`config_layers.rs:177-197`）：
   `system_managed → managed → user → env_overlay → user_requirements →
   system_requirements → mdm_requirements`。**后合并者覆盖前者：user 层覆盖
   managed/system_managed**；能在 user 之后覆盖 `[cli]` 的是 requirements/MDM 层
   （requirements 层为自由 TOML、无键白名单，可携带 `cli.use_leader`，`validation.rs:128-164`）；
   env overlay 的 allowlist **明确排除 `cli`**（`config_layers.rs:44-48`），不可能覆盖该键。
   **没有 project slot**——仓库内 `.grok/config.toml` 结构上无法参与该键的解析
   （`xai-grok-shell/src/config/tests.rs:3040-3043` 注释明示）。本开关写 **user 层**。
   - **"开关不生效"的完整归因（四因，r3-M4 补全）**：① 实际启动参数显式 `--no-leader`
     （最高优先级）；② requirements/MDM 层覆盖（可执行负例见 §0.2 第 2c 步）；③
     confinement veto（链后最终 veto，§0.2 第 2d 步负例）；④ 版本漂移（键/flag 不存在或
     语义变化）。managed 层**不是**原因（被 user 覆盖）。项目级 `.grok/config.toml`
     不影响该键。
   - TUI 实际使用 global effective config 而非 MCP 专用加载器：
     `xai-grok-pager/src/app/mod.rs:394-437,680-689`。
3. **leader 进程模型与 observer 生命周期（现状事实；D-G2 实施后行为改变，见 §3.5.2）**：
   `connect_or_spawn`——第一个 leader 模式客户端若无 live leader 则选出/拉起 leader
   （flock 单例），自己作为 follower 附着；leader 内 IPC server 做会话路由与 ownership
   跟踪（`leader/mod.rs:1-30`）。**退出条件是 `clients` 为空**：每个 register 的连接计入
   client count（`leader/server.rs:1620-1761`）；macbridge observer 会 register 并以 30s
   ping 保持连接，且 grok relay loop 用 `context.Background()` 订阅、**没有按 iOS session
   subscriber 数量取消 observer 的路径**（`go-bridge/handlers_relay.go:158-185,206-250`；
   对照：codex file relay 显式查询 `HasSessionSubscriber`，`handlers_relay.go:365-374`）。
   因此现状是：observer 一旦建立，会续命 leader 直到 leader 自身崩溃或 CordCode Link
   runtime 退出，且每个打开过的 session 各自累积一条 relay/连接/goroutine
   （`grok-leader:<sessionID>` key，`handlers_relay.go:154-186`）——**Owner 已在 §15 D-2
   否决该现状**，批准 D-G2 引入 subscriber-aware cancellation。socket 文件仅在 leader
   正常收尾时删除（`server.rs:2348-2351`），**异常崩溃可残留 stale socket 且不会被
   macbridge 清除**（§6-7；恢复由 D-G1 承担，§3.5.1）。
4. **多客户端共享与 interaction 语义**：`ClientCapabilities` 注释明确 "a TUI
   (`terminal: false`) and a web client (`terminal: true`) **sharing the same leader** get
   independent routing"、"other **subscribers of a shared session** receive the payload too"
   （`leader/protocol.rs:158-177`）。mid-turn interjection 由 leader 广播到每个 attached
   pane（`session/acp_session_impl/interjection.rs`、`xai-grok-pager/src/app/acp_handler/mod.rs:776`）。
   **interaction 请求（tool permission / ask_user_question / plan approval）是"共享"反向
   请求：广播给所有 subscriber，first-answer-wins**（`leader/server.rs:491-500` 官方注释）；
   macbridge observer 收到但明确不答（`leader_subscriber.go:319-334`）——若其他客户端全部
   关闭，interaction 可能长期无人应答、turn 长期等待（§6-6）。
5. **chat 模式冲突**：`--chat` 与 leader 模式互斥，grok 启动时直接报错并提示 `--no-leader`
   或关闭配置（`xai-grok-pager/src/app/session_startup.rs:280-286`；上游 monorepo 同步
   （bc7f02e）已将其抽为 `CHAT_MODE_LEADER_CONFLICT` 常量 + 可独立单测的
   `chat_mode_conflicts_with_leader` 函数——对该冲突的官方语义更有把握，Phase 0 负例
   可直接对照常量文案）。
6. **版本契约**：macbridge 现行门槛 grok ≥ 0.2.93（`agent/grokbuild/diagnostics.go:97-105`）；
   `--leader` / `--no-leader` / `--leader-socket` 三 flag 已记录于
   [2026-07-12-grok-cli-compatibility-evidence.md](2026-07-12-grok-cli-compatibility-evidence.md)；
   `[cli] use_leader` 键在 pin `9684fa3` 源码核实，**评审机实测安装版为 1.0.12（源码
   pin 为 1.0.10），发行身份差异由 Phase 0 第 1 步记录核对**。若未来 grok 改名/移除该键，
   症状 = 开关打开但"未检测到 socket"提示永不转变（fail visible，处置见 §3.4/§11），
   不得加 fallback 伪装生效。
7. **官方自身的配置写入串行化**：官方 `save_config` 持有**进程级** `SAVE_LOCK`
   （`xai-grok-shell/src/util/config/persist.rs:11,103,219`）串行化同进程内的读改写。
   它不能协调 CordCode 这个外部进程，但说明官方写入侧存在串行化竞争者——CordCode 的
   并发保护按 §3.3-7 的 best-effort 语义设计，不承诺绝对互斥。
8. **官方日志与 effective 证据通道（r3-B2 核实，r4 补 append 事实）**：grok 支持单文件
   日志 env `GROK_LOG_FILE=<path>`（RUST_LOG 过滤器、默认 DEBUG 级；写入为 **append 模式**
   ——`xai-grok-telemetry/src/debug_log.rs:8,81-82,365,414-421` 与
   `appender.rs:13-25`；`--debug-file` flag 等效设置 `GROK_DEBUG_LOG=<path>`，
   `pager-bin/src/main.rs:2010-2015`）。TUI 启动时输出 INFO 行 **`pager TUI leader mode
   resolved`**，字段含 `use_leader`、`policy_disable_reason`、`sandbox_profile`、
   `leader_disabled_by_sandbox`（`xai-grok-pager/src/app/mod.rs:680-689`）——这是 pin 源码
   中**可观察的最终解析结果**，是 Phase 0 第 2 步的 effective 值证据；因 append 语义，
   每次启动必须用独立文件或按 PID/时间窗取行（§0.2）。`grok inspect` 只输出
   `config_sources`（层 role/path）与 warnings（`inspect/mod.rs:55-82,265-290,1067-1175`），
   **不能证明 effective `use_leader`**，仅作层参与证据。
9. **requirements 层的可执行性（r3 核实）**：requirements 层文件为
   `<grok home>/requirements.toml`（user 层）与系统配置目录同名文件
   （`xai-grok-config/src/validation.rs:68,78-79,117-123`），内容是自由 TOML：normalize 仅
   剥离 `fail_closed` 键并应用 `[[version_overrides]]`，**无键白名单**
   （`validation.rs:128-164`）——因此 `[cli] use_leader` 可被 requirements 层覆盖，负例
   判据可执行（§0.2 第 2c 步，按 r4-M1 以隔离 GROK_HOME 执行）。
10. **用户 config 可含凭据（r3-M2 事实依据）**：官方文档允许在 user config 内直接存放
    `api_key` 与 MCP token（`xai-grok-pager/docs/user-guide/05-configuration.md:275,282-287`）。
    这决定了备份安全规则（§3.3-10）。

### 2.2 macbridge 侧（`main@032fdd8`）

1. **观察路径自动选择已存在，核心链路零 runtime 改动**：`SubscribeSessionEvents` 在每次
   订阅时 `os.Stat(socketPath)`，存在则走 `LeaderSubscriber`（push），否则 fallback 文件
   tailer：`agent/grokbuild/grokbuild.go:156-198`（路径选择主体 `:174-198`；事件转发闭包
   `forward` 定义于 `:163-172`——这是 D-G1 首事件分界的实现锚点）。socket 路径按
   `resolveLeaderSocket` 解析：**`GROK_LEADER_SOCKET` env → `$GROK_HOME/leader.sock` →
   `~/.grok/leader.sock`**（`leader_subscriber.go:63-79`）——Swift 侧状态展示必须使用同一
   条解析链，且只反映 **Link 进程实际继承的 env**（见 §6-8）。路径选择发生在**下一次**
   订阅（打开/恢复/投影打开）；已在 tail 的会话不热切换；订阅内 leader 连接失败**现状**
   不会自动切回 tailer——**D-G1（§3.5.1，已批准）将改变这一点：建立期失败（未转发任何
   事件即断开）回退 tailer**。**stale socket 特例**：D-G1 前，stale socket 存续期间每次
   session-open 都会再次选中同一失效 socket 并再次失败，观察持续阻断（§6-7）；D-G1 后
   该场景经回退恢复观察。
2. **自保护已成立**：macbridge 自己的会话与 catalog 子进程均显式 `--no-leader`
   （`session.go:79-82`、`catalog_session_list.go:119-124`），flag 优先级高于 config，
   开关写入不影响 macbridge 自有 turn 路径。
3. **只读纪律已成立**：LeaderSubscriber 不 spawn leader、不抢 flock、不答任何
   agent→client 请求（`grokbuild.go:152-153`、`leader_subscriber.go:319-322`）。
4. **验收用日志证据行**（生产路径判据，§7.3）：
   - fallback 路径（socket 缺失）：`grokbuild.go:177` `leader socket absent, falling back to updates.jsonl file tailer`；
   - D-G1 回退路径（socket 存在但建立失败，实施后新增）：INFO 行（文案以 G1 冻结为准，
     如 `leader subscribe failed, falling back to updates.jsonl tailer`）；
   - leader 路径证据链（三行缺一不可）：`grokbuild.go:192`
     `leader subscriber starting`；`leader_subscriber.go:147`
     `leader subscriber connected`；`leader_subscriber.go:199`
     `leader subscriber live`（证明 initialize/session/load 完成）。
5. **UI 先例（槽位复用）**：`agentRow`（`WorkspaceView.swift:496`）已有 kind 特定的行内
   控件与副文案槽位——codex-web 的"重启共享服务"按钮（`:528-545`）与
   `codexConfigChangedHint` 提示（`:506-513`）。grok build 行照此模式扩展。
6. **本地化**：L10n 定义于 `MacBridge/MacBridge/Services/Localization.swift`。
7. **【2026-09-02 Phase 0 步骤 4 实测回写】v2 projection 开启路径的 cwd 缺口（基线被证伪）**：
   三行链在生产止于 connected（37ms 退出，无 live）。根因链（全部独立取证）：
   iOS v2 客户端的 projection 开启走 `get_session_projection`，而 iOS
   `ProjectionStore.swift:688/:834` 两处 fetch **硬编码 `directory: nil`**——该路径从不
   携带 directory；Mac `handlers_projection.go:99` → `startProjectionLiveRelay(directory="")`
   → `startGrokLeaderSessionRelay(cwd="")` 回落 `GetWorkDir()` = runtime
   `-work-dir`（如 `/Users/jacklee`）；grok leader 对 `session/load` 校验 cwd 必须匹配
   session 所属项目目录，不符即拒（`session/load: Path not found.`——探针复现）。
   `sub.Run` 错误在 `grokbuild.go:194` slog.Debug 且生产日志硬编码 INFO（`main.go:125`），
   失败被静默吞掉。**含义**：只要 iPhone 以 v2 projection 开启 grok 会话（当前唯一
   开启路径），leader 订阅结构性无法达到 live；本节上文「核心链路零 runtime 改动」的
   前提（调用方总带正确 directory）不成立。legacy `get_session_messages`/`resume_session`
   路径 iOS 会带 directory（`CCCodeBridgeClient.swift` 参数链完整），不受影响。
   官方 grok 拓扑本身无分歧（正确 cwd 下 register/initialize/session/load/live/replay
   全通）。修复路径待 owner 重新裁决：A. `grokbuild.go` 内 sessionID→cwd 解析
   （`~/.grok/sessions/*/<sessionID>` 父目录解码，机制与 updates tailer 同源，落在
   D-G1 文件面，全调用路径修复）；B. iOS 补 directory（违背单仓任务边界，需真机重装）；
   C. `handlers_relay.go` catalog 反查（D-G2 文件面，仅修 relay 调用点）。附带建议：
   把 `grokbuild.go:194`（及同类 `handlers_relay.go:246`）的 Debug 错误行提升为 Warn，
   消除生产诊断黑洞。证据：`.exec-plan/artifacts/grokbuild-leader-p0/execution-log.md`
   步骤 4 停线裁决节。
8. **【2026-09-02 Phase 0 步骤 6 实测回写】leader live rail 的 turn 终态通知被订阅者
   method 门丢弃（基线被证伪 #2）**：内容流（`session/update` 直发 agent_message_chunk/
   user_message_chunk 等）正常到达 relay（owner 已确认 iPhone 流式同步），但 turn 终态
   **从不**作为 `session/update` 广播——live rail 的终态走 gateway ext 通知
   `x.ai/session_notification`（wire 上 `_` 前缀包裹形态 `_x.ai/session_notification`，
   `update.sessionUpdate="turn_completed"` + `prompt_id` + `stop_reason`），另有孪生
   fire-and-forget `x.ai/session/prompt_complete`（上游
   `session/turn_completion.rs:1-9`：TurnCompleted journal 行是 replay 用孪生）。
   `leader_subscriber.go` 的 `isSessionUpdateMethod` 白名单为
   `{"session/update", "_x.ai/session/update", "x.ai/session_notification"}`——第三个
   条目**缺 `_` 前缀**（真实 wire 恒为带前缀形态，见 grok-build `leader/server.rs`
   `method_of` 文档），终态帧在 method 门即被静默丢弃。该错误形态源自文件创建提交
   `0008f1d`（从未生效过）。**后果链**：relay `turnArmed` 一旦置位永不复位（无终态）→
   `markIdle`/`turn terminal` 永不触发 → iPhone live 视角 turn 永远「运行中」（冷拉
   才能看到完成态）；后续 turn 内容被并入上一个 stale armed turnId。实测证据：
   当日 3 个正常完成 turn（含 owner 确认的 12:36 轮）0 条 "turn terminal" 日志；
   原始帧转储探针（复刻 register→initialize→session/load 握手，转储全部下行帧）
   在一轮正常 turn 中收到 7 帧 `_x.ai/session_notification`（含
   `turn_completed stop=end_turn`）+ 289 帧直发 `session/update`。官方拓扑与 §2 推导
   一致（终态确实广播、形状与 codec 既有 `turn_completed` case 匹配），失败仍在
   macbridge 基线（与条目 7 同类）。证据：
   `.exec-plan/artifacts/grokbuild-leader-p0/execution-log.md` 步骤 6 停线裁决节 +
   `logs/step6-rawdump.*` 原始帧转储。
   **裁决与修复（2026-09-02）**：owner 选方案 A——白名单补
   `_x.ai/session_notification`（无前缀形态从白名单移除，wire 恒为包裹形态），实施
   为 D-G0b 独立提交 `6dc9353`（`leader_subscriber.go` + 回归测试：fake leader 发
   live 终态通知断言 `EventResult{Done,TurnID}` 转发 + replay 孪生仍丢弃 + wire
   形态表驱动断言）。

9. **【2026-09-02 Phase 0 步骤 7/8 实测回写】stale socket 自愈 + driver 与广播解耦**：
   (a) stale socket 基线被证伪 #3——kill -9 leader 后 owner TUI 0.1s 内触发重连，
   新 leader spawn 并成功 bind（socket inode 更新；flock 随进程死亡释放，
   connect 级 ECONNREFUSED 驱动 connect_or_spawn 路径，新 bind 替换 stale socket；
   TUI 无限重连 1s/30s backoff）。推论：observer-only 场景（无任何 grok 客户端）
   leader 死亡后不自愈——**macbridge 永不 spawn leader 红线的自然后果**，产品语义
   可接受（§0.2-8 的修正注记同步）。
   (b) driver transfer 的 debug 行（"Transferred session driver after disconnect"）
   在 unified.jsonl **不可见**（全天 0 次；上游按 target 的级别过滤，文案在 1.0.13
   二进制 strings 中确认存在、源码 server.rs:1728-1736 逻辑未变）。改用行为证据：
   TUI-A（driver 候选）SIGKILL 后 observer 连接不断；新客户端 TUI-B attach 发起
   turn，observer 收到完整 live 生命周期（user_message_chunk → agent_message_chunk
   → `_x.ai/session_notification turn_completed stop=end_turn` → prompt_complete，
   全 replay=false）——**广播与 driver 身份完全解耦，driver 变更对 observer 无感**，
   §2 的「transfer 只影响 driver-only 路由」假设成立。步骤 7 的日志证据预期从
   「unified.jsonl Transferred 行」修正为「行为级：driver 死亡前后 observer 事件流
   连续性」。证据：execution-log 步骤 7 节 + `logs/step7-rawdump.log`。
   (c) 步骤 6b 补记：pending interaction（ask_user_question）跨 TUI 死亡存活
   （56s 零可答客户端挂起、不超时不报错）+ replay-on-attach（新客户端 load 后
   立即收到 pending 弹窗）+ first-answer-wins 实证；observer 视角确认 §6-6 风险
   （REQUEST 帧在 method 门被弃，iPhone 看不到问题、turn 转圈至 terminal）。
10. **【2026-09-02 Phase 0 步骤 9/10 实测回写】回退闭环 + excluded 锚点**：
    (a) 关=回退完整闭环：删 `use_leader` 键 → 新 grok TUI inline（GROK_LOG_FILE
    193KB 零 leader 痕迹：leader_connect/connect_target/leader.client.registered
    均为 0）→ leader 下线 + socket 消失后 iPhone 重开会话，`go-bridge.log` 出现
    `grokbuild: leader socket absent, falling back to updates.jsonl file tailer`
    （`grokbuild.go` os.Stat 判据）+ `updates file tailer starting` 双行证据。
    (b) leader 生命周期修正：见 §0.2-9 实测注记（归 grok 客户端维持；SIGTERM
    亦不清理 socket 文件）。对 D-G2 语义的影响：「退 Link 不再把 leader 续命」
    的准确含义是取消 observer 订阅，不是 leader 退出——§3.5.2 相关表述在
    实现期按此口径复核。
    (c) excluded 三锚点 bc7f02ed 复核成立（见 §0.2-10）；唯一行号漂移：
    pager-bin flag→env 转换 `:2007-2009` → 实际 `:2039-2041`，文件路径根
    `crates/codegen/xai-grok-pager-bin/src/main.rs`。

11. **【2026-09-02 owner 验收矩阵实测回写】三个真实缺口 + 修复**：
    (a) **D-G2 永不触发（行 12 事故，两轮修复）**：设计假设订阅键随 scope 切换退订，
    实际 `handleSetObservationScope` 只加不减——App 连接期间 `HasSessionSubscriber`
    恒真（98ae793 补 `ReconcileObservationSubscriptions`）；修后仍不触发，第二根因是
    iOS `get_session` 携带 `currentSessionDirectory` 使读路径产生**带目录键**，幸存
    reconcile（05152aa 读路径统一记**空目录观察键**）。由此固化订阅键三分语义：空目录
    键=观察键（随 scope 切换退订）；带目录键=自有会话键（send_message/resume，切换后
    继续收流）；`Targets` 的 noDir 匹配保证空目录键是带目录事件的合法投递目标。
    实测取消延迟精确 60s（elapsed=1m0s），无虚假中断，重开恢复。
    (b) **iPhone 自有 turn 的 user echo 缺身份（行 6 事故）**：`user_message_chunk`
    按上游设计不带 promptId（§0.2 上游锚点已核），98f0e57 只在观察 loop
    （`grokLeaderSessionRelayLoop` pendingUserText）做了身份重建，自有 turn 的
    `relayEvents` 路径无补齐 → SSV2 reducer 跳过 identityless user_message →
    乐观占位释放后发送的消息从投影消失（39e29a8 在 `grokSession.emitTurnScoped`
    层补齐，覆盖两个消费路径）。
    (c) **F-7 / D-G1 实测值**：armed turn + leader kill -9 → turn_aborted
    (leader_disconnect) + idle 合成实测 17ms 内；D-G1 干净序列实测：退出 TUI →
    杀 leader（TUI 存活时 ~12s 内自动重生 leader 并重建 socket，无法保持 stale）
    → iPhone 重开会话触发全新订阅 → `leader subscribe failed, falling back to
    updates.jsonl tailer` → TUI 发消息按轮询节奏（~1s 批）恢复，终态干净。

12. **【2026-09-02 后续实施】§4.1 roster 通知消费落地（§9 未来改进首项）**：
    `Agent` 实现 `core.CatalogRefreshSignaler`（commit `c77bb80`）。`LeaderSubscriber`
    识别 machine-wide `x.ai/sessions/changed`（wire `_` 前缀 + 裸形态，官方
    server_tests.rs:4419/4523 两形态均有样本；上游 roster.rs `RosterChanged`
    payload camelCase）→ roster 回调 → buffered-1 通道合并多订阅连接重复广播 →
    discovery watcher `catalog-signal` 立即权威指纹重扫 → `sessions_changed`。
    **失效信号语义而非增量应用**：不本地折叠 roster upserted/removed（那是上游
    FleetView 的视图逻辑，复制易与 catalog 权威扫描漂移），指纹 diff 拥有
    fence/seen/publish 真值。5s grok fast poll 与 60s safety scan 保留——roster
    仅在「leader 存活且至少一条订阅连接」时可达，纯侧栏浏览（无打开会话、
    D-G2 已回收 relay）时 fast poll 仍是快速路径。go-bridge 零改动（类型断言
    自动接线），iOS 零改动（sessions_changed 既有事件）。

13. **【2026-09-02 后续实施】§4.7 model/provider/effort 缺口补齐（commit
    `c1dfa81`）**：SetModel 此前只改内存、session/new 不传、AvailableModels 空目录。
    实施前按 source-first 对目标二进制实证（**版本偏移声明**：本机 grok 1.0.13 /
    5e9a58528b76，不在本机 checkout bc7f02ed 历史中——checkout 只作语义参考，
    wire 细节以 1.0.13 真实协议探针为准，探针零 prompt、零 API 消耗、close 清理）：
    (a) initialize `_meta.modelState`（`_` 前缀；checkout 无前缀）= 官方目录，
    per-model `_meta.reasoningEfforts[]` 是档位菜单真值（grok-4.6 四档默认 high；
    owner 的 GLM provider 条目无 meta、诚实不显示档位）；(b) `session/set_model`
    是 **snake-case**（camelCase → -32601，checkout fixture 恰为 camelCase——
    照抄会发错），`modelId` 服务端必填，无效值 -32602 fail-closed；成功持久化到
    summary.json（current_model_id/reasoning_effort）；(c) `session/new` params
    `_meta.{modelId,reasoningEffort}` 均被消费（sessionConfig options flip
    selected:true，result 顶层 `models` 回显）；`session/load` 不接受模型参数。
    实现：`Agent.adoptModelCatalog`（catalog 单例 initialize / 会话 initialize 双
   入口）→ `AvailableModels` 目录优先 + `core.ModelEffortCatalog`
    （`EffortsForModel` per-model 档位，handleListModels 既有 wire 字段下发，iOS
    零改动）；newSession 仅显式选择携带 `_meta`；`grokSession.appliedModel/
    appliedEffort` 从 session/new|load 响应 `models` 真值播种，Send 前漂移检查
    （漂移→set_model；失败=硬失败 turn 不发出；loadSession 后失败=软 Warn 会话
    健康优先）；`New()` effort 空值短路（`normalizeReasoningEffort("")` 会折叠为
    medium，防空 config 值静默变显式选择）。测试
    `session_model_switch_test.go` 11 个，fixture 全部来自 1.0.13 真实样本形状。
    上游语义参考锚点：`xai-grok-pager/src/acp/model_state.rs`（ModelState 客户端
    视图/effort 门控）、`xai-grok-sampling-types/src/types.rs:856-893`（meta 键
    常量 + ReasoningEffortOption）、`xai-grok-shell/src/agent/config.rs:5444`
    （to_acp_model_info meta 键全表）。

14. **【2026-09-02 后续实施】条目 13 首轮真机修复：effort 客户端侧门（commit
    `da17648`）**：owner 矩阵行 3 复现 iOS 切 GLM 后 effort 残留 high，
    `set_model{glm,high}` 被官方 -32602 拒绝、turn 被杀。上游
    `model_state.rs` `resolve_effort_for_model` 本就是官方客户端的本地拒绝门
    （"so the TUI fails instead of sending a blocked effort to the API"），
    官方 TUI 从不把无效组合发给 API——第一轮只镜像了 server 接受面。修复
    `effectiveEffortForModel`：目录外模型 / 无 supports 标志 / 菜单外值 →
    丢弃 effort（log），model 照发；无目录真值透传官方裁决。接线两处
    （sessionNewMeta 构造、applyModelSelection 漂移判定）。iOS 侧切模型不清
    effort 状态属 UX 打磨另案，Mac 端修剪已兜住。

15. **【2026-09-02 后续实施】条目 14 修复后二轮真机：官方双模型 id 形态
    （commit `a0b0f11`）**：owner 主动选 glm+高仍 -32602。错误指纹矩阵探针
    （故意发坏参数）证明官方 set_model **只校验 modelId/sessionId**（effort
    传 bogus/xhigh-on-glm 均成功），-32602 必为 modelId 无效。根因：官方目录
    与 set_model 请求用**条目 id**（"grok-4.5"），持久化（summary.json
    `current_model_id`）与 set_model 应答用**底层 id**（"glm-5.3"，来自 owner
    config `[model."grok-4.5"] model=`）；iOS 从 transcript 读出底层 id 显示
    「glm 5.3」，选择后把它当 model id 回发 → 目录外 → unknown model id。
    旁证：无 effort 修剪 log（iOS 该次未发 effort 字段）、首次不选模型发送
    成功（无 model 即无漂移）。修复：`unknown model id` 时软化（WARN + 保持
    会话当前模型继续 turn——会话本就在用户所选模型上；其余错误硬失败）；
    callRPC 错误串附带官方 data；applyModelSelection 加参数日志。**iOS 侧
    根治另案**：send_message 的 model 应发 list_models 条目 id，transcript
    底层 id 仅用于显示。

---

## 3. 架构

### 3.1 拓扑（开关前 / 开关后）

```text
开关前（默认）：
  grok TUI（inline 内嵌 agent）──写──> ~/.grok/sessions/**/updates.jsonl
                                            ▲
  macbridge file tailer（1s 轮询，按需附着）─┘

开关后（用户重启 grok 生效）：
  grok TUI ──IPC──┐
  IDE 扩展 ────────┼── 同一 leader 进程（socket，flock 单例）
  headless/web ───┘         │
                     ACP 会话通知广播
                            ▼
  macbridge LeaderSubscriber（只读 push，实时）
  ├─ D-G1（§3.5.1）：订阅建立失败（未转发任何事件即断开）→ 回退 file tailer，观察继续
  └─ D-G2（§3.5.2）：session 无 iOS 订阅持续 > grace → 取消该 session 的 observer，
     不再无界累积、不再把 leader 续命到 Link 退出
```

### 3.2 模块与身份（改动清单）

**产品改动在 MacBridge Swift 侧 + 两处受控 Go 改动；iOS、protocol pack、SSV2 零改动。**
基础设施新增：一个已冻结的 TOML 解析依赖（§3.6，D4）。

| 组件 | 职责 | 位置 |
| --- | --- | --- |
| `GrokLeaderModeManager`（新增） | ① 路径解析：`GROK_HOME` env 优先否则 `~/.grok`；socket 路径按 `GROK_LEADER_SOCKET` env → `$GROK_HOME/leader.sock` → `~/.grok/leader.sock` 与 runtime 同链，只反映 Link 进程实际继承的 env；② 经"语义 parser + 仓内 canonical locator"（§3.3-3）读取 user 层 `[cli].use_leader`，状态区分 absent / explicit false / true / 解析失败；③ symlink 身份模型（§3.3-4）；④ 开 = 外科手术式写入 `true`，关 = 删除该键；⑤ 备份、原子落盘、best-effort 并发检测、写后校验与受限回滚（§3.3）；⑥ 暴露 `@Published` 状态（配置值 × socket 文件存在性） | `MacBridge/MacBridge/Services/` |
| `agentRow` grokbuild 分支（扩展） | 尾部 `Toggle`（`.toggleStyle(.switch)`、`controlSize(.small)`），槽位与 codex-web 行内按钮对齐；名字下方副文案槽位按 §3.4 状态机渲染 | `WorkspaceView.swift:496` 一带 |
| L10n 新键 | 开关 label、核心三态与失败态副文案（§3.4） | `Localization.swift` |
| DiagnosticsSheet grok 组（**Phase 4 必做**） | 一行 Leader 状态：user 层配置值（**区分 absent / explicit false / true**）、解析出的 socket 路径与存在性、安装版 grok 版本——版本漂移 fail-visible 依赖此行（§2.1-6） | `DiagnosticsSheet.swift` |
| TOML 依赖（§3.6 冻结） | **仅语义职责**：整文档合法性（parse 失败→F1）+ `cli.use_leader` 语义值；不承担原始写法识别（§3.3-3 分工） | `MacBridge/project.yml` + 重生成工程 |
| D-G1（§3.5.1，§15 D-1 已批准） | leader 订阅**建立失败**（未转发任何事件即断开，且非 ctx 取消）时回退 file tailer + INFO 日志 | `agent/grokbuild/grokbuild.go` |
| D-G2（§3.5.2，§15 D-2 已批准） | session 无 iOS 订阅持续超过有界 grace 时取消该 session 的 grok observer（subscriber-aware cancellation） | `go-bridge/handlers_relay.go` + `go-bridge/types.go`（registry unknown 态（第四状态）/claim，§3.5.2） |

接线方式参照 `restartSharedCodexDaemon` 的既有路径（viewModel/runtimeManager 作用域持有
manager，单实例，避免多窗口竞态）。

### 3.3 TOML 读写规范（十条规则，r3/r4 修订）

1. **写入禁止 parse→serialize 往返**（丢注释丢格式），实现为定向文本编辑：定位
   `[cli]` 节头（顶层 `use_leader` 键与 `[cli].use_leader` 必须区分）→ 节内查找
   `use_leader` → 存在则原位替换值；不存在则在节内末尾追加一行；`[cli]` 节不存在则在
   文件末尾追加节头 + 键。定位由 §3.3-3 的 canonical locator 承担。
2. **关闭 = 删键，不写 false**：explicit false 会屏蔽 xAI 服务器将来全量开启的推荐
   （§2.1-1 优先级链）；删键恢复默认语义。删键后**即使 `[cli]` 节变空也一律保留节头**
   （跨进程重启无法可靠识别"节头是否由我们追加"，持久标记引入额外状态；空节头对
   grok 无害，确定性优先）。
3. **读取/检测/校验 = 语义 parser（§3.6 冻结依赖）+ 仓内保守 canonical locator 分工
   （r3-B1 修正）**：
   - **parser 职责（仅两件）**：(a) 整文档 TOML 合法性——parse 抛错 → **F1**；(b) 语义
     oracle——按 TOML 语义把点键/quoted key/inline table 归一后回答"`cli.use_leader`
     是否存在、值是什么"（`TOMLDecoder` 解码到只含该键的 struct；多余键忽略）。
     **parser 不提供、也不被要求提供原始写法/源码区间**（§3.6 选定库是 value-tree API；
     不得声称从归一化结果反推原始写法）；
   - **locator 职责（仓内实现，保守词法扫描）**：在原文中定位 canonical 形态——顶层
     `[cli]` 节头 + 节内 canonical `use_leader = ...` 赋值行，供外科编辑与删键使用。词法
     跟踪多行字符串（`"""` / `'''`）与多行数组状态；任何无法完整分类的行 → 拒绝（F2），
     不猜测；
   - **交叉裁决矩阵**（编辑前必须四格判定）：

     | parser 语义值 | locator 结果 | 裁决 |
     | --- | --- | --- |
     | `cli.use_leader` 无 | 无 canonical 行 | 安全：节内追加（或新建节） |
     | 无 | 有 canonical 行 | 矛盾（语法异常）→ **F1** |
     | 有 | 恰一 canonical 行 | 原位编辑 |
     | 有 | 无 / 多个 / 歧义 | **F2**（等价形态，拒绝写入，不产生第二语义键） |

   - 不采用自研行级扫描单独承担检测（多行字符串/数组会破坏行级状态跟踪，r2-M3 维持）；
     也不要求 parser 提供 CST（r3-B1 修正后职责已拆分，价值树足够）；
   - locator 的多行结构保守性由 **T27–T30 专项测试冻结（r4-M3）**：三引号 basic/literal
     string 内伪节头/伪赋值、跨行数组（字符串元素含 `]`/`#`）、注释中的伪 token、未闭合
     结构（→ F1/F2）；
   - 写后校验（第 8 条）同样走该矩阵：写入后 parser 语义值必须等于目标值，且 locator
     定位到恰一 canonical 行。
   - **节边界语义（r8-M4 冻结）**："在 `[cli]` 节末尾追加"以**下一节头为界**——顶层表
     `[other]`、子表 `[cli.child]`、数组表 `[[x.items]]` 都终止 `[cli]` 节；locator 不得
     把子表后的行仍当作 `[cli]` 内容（否则 `use_leader` 会追加到错误语义路径，只能靠
     写后校验兜底回滚）。无尾随换行的文件，追加时只补必要换行，写后必须是合法 TOML。
   - **类型错误裁决（r8-M7）**：`use_leader = "true"` / 整数 / 数组等**合法 TOML 但非法
     Bool** 的形态 → **F1**（配置不可安全读取/类型非法，拒绝管理）——semantic parser
     的 `TOMLDecoder` 解码 Bool type mismatch 属 F1，**禁止误判为 absent 后追加第二
     语义键**（矩阵"语义值无"格仅指合法 Bool 的 true/false/absent）。
4. **symlink 身份模型（r3-M3 补全；best-effort + fail-visible）**：
   - 解析：从 config 路径 `readlink` 沿链最多 8 级；**相对链接按链接文件所在目录解析**
     （POSIX 语义）；最终目标必须是普通文件，否则（目录/FIFO/socket/悬空/循环/超深）→
     **F1**；
   - 身份钉扎：编辑前记录（解析后 canonical 目标路径、目标 inode/device、mode）；temp
     写入后、rename 前**双重复核**：①链接链仍解析到同一 canonical 路径（link swap
     防护），②最终目标 **inode/device 与初始记录一致**——逐字节内容比较识别不了
     "同路径、同内容、不同 inode"的目标替换（r8-M5）；目标不存在或任一不一致 → 放弃
     写入、失败可见（残余 TOCTOU 与第 7 条同级 best-effort）；
   - 落点：temp+rename 一律作用于**解析后的目标文件路径**（同目标目录），链接本身永不
     替换（兼容 dotfile 管理器）；
   - 写后校验与受限回滚绑定**初始 canonical 目标路径**（不重新解析链）；
   - 属性：仅保留 POSIX mode；**ACL / xattr / 所有者不保留**（temp+rename 新文件按目录
     默认继承）——已接受边界，T24 冻结；
   - 测试 T18/T21–T24；本机真实样本为普通文件（第 9 条 manifest），symlink 行为以合成
     测试冻结，不宣称现场已验证。
5. **原子与权限**：同目录临时文件 + `rename`；保留原文件 mode（无原文件时 `0644`，
   目录不存在时创建 `$GROK_HOME` 为 `0700`，见第 9 条裁决）；注释/键序/行尾原样保留。
6. **非常规等价写法 fail-visible**：即第 3 条矩阵的 F2 格——提示"配置含 CordCode 无法
   安全管理的 use_leader 写法，请手工处理"。
7. **并发保护 = best-effort 内容身份比较，不承诺绝对互斥**：写入流程为读原文件快照 →
   在快照上定向编辑 → rename 前重读磁盘逐字节比较快照，一致才 rename，不一致则放弃
   写入并失败可见。**残余竞态窗口客观存在**：第三方（官方 `save_config` 或用户编辑）
   可在比较完成后、rename 前写入，随后被本次 rename 覆盖——内容比较缩小窗口但**不是
   原子 compare-and-swap**；官方进程内有 `SAVE_LOCK`（§2.1-7）但无法协调外部进程。
   因此本文不使用"绝不覆盖/同等保护"表述；缓解 = 备份（第 10 条）+ 受限回滚（第 8 条）
   + 冲突失败用户重试。不引入跨进程文件锁：对用户配置文件过重，且与官方锁机制不互认。
8. **写后校验与受限回滚**：rename 后重新按第 3 条矩阵校验。校验失败时：**仅当磁盘内容
   仍逐字节等于本次写入内容**才用备份原子恢复；若内容已再被第三方修改，保留现场并
   报告并发冲突（避免回滚覆盖他人修改）。回弹开关 + alert（含原因与备份路径）。
9. **样本基线、裁决与 manifest（r3-M5 分级）**：
   - **裁决（维持 r2）**：目录/文件均不存在时创建 `$GROK_HOME`（0700）并原子创建
     config（0644）——grok 自身会创建 `~/.grok`，行为与其一致；不做"目录不存在即拒绝"。
   - **样本 manifest**（实施与评审的形态基线；来源/版本/分级逐项记录）：

     | 形态 | 样本 | 分级 |
     | --- | --- | --- |
     | 真实安装样本：普通 `[cli]`（installer/auto_update/channel）、无 `use_leader`、0644、LF、558B、SHA-256 `c78c74d2b63b801c6f591c291a5df2425fc83d7edd7c5d82f928b29b4d8ea464` | 本机 `~/.grok/config.toml`（r2/r3/r4 评审 + 设计方多轮核实一致） | **真实** |
     | canonical `true` / `false` / absent 解析行为 | 官方 fixture `mcp.rs:2177-2265` | **官方 fixture**（契约证据，非现场样本） |
     | CRLF、节头尾随注释、行内注释（T12–T14） | 自造输入 | **synthetic**：仅冻结实现行为，不宣称现场支持 |
     | dotted / quoted / inline table 等价形态（T15） | 自造输入（语义归一行为另有官方 deep-merge 源码佐证） | **synthetic** |
     | multiline string/array 内伪 token、未闭合结构（T27–T30，r4 新增） | 自造输入——验证的是仓内 locator 的保守行为，synthetic 在此正合适 | **synthetic** |
     | 表/子表/数组表节边界 + 无尾随换行（T31/T32，r8-M4 新增） | 自造输入——冻结 locator 的节结束判定与换行补齐 | **synthetic** |
     | 合法 TOML、非法 Bool 类型（T33，r8-M7 新增） | 自造输入——只冻结 fail-closed（F1）行为，不宣称外部现场存在该形态 | **synthetic** |
     | symlink（相对/多级/外部目标/悬空/循环，T18/T21–T24） | 自造输入 | **synthetic** |

   - **synthetic 的边界**：自造 fixture 只证明实现自洽，不证明外部形态现场支持。等价
     形态的**安全属性本身是保守拒绝（F2）**——即检测失败时拒绝写入，因此"无真实非常规
     样本"不构成风险敞口；若 Phase 1 期间能取得真实非常规样本（脱敏后加入 manifest），
     必须用于验证 locator 的识别覆盖。
10. **备份（r3-M2 + r4-M4 安全修订；r8-M6 碰撞防护）**：写入前把原文件**字节级完整复制**到专用子目录
    `~/Library/Application Support/CordCode Link/GrokConfigBackups/`（目录 `0700`、每份
    文件 `0600`、文件名 = **高分辨率 UTC 时间戳（纳秒）+ 随机 UUID 后缀**，并以
    **exclusive create**（已存在即换新名重试，**绝不 truncate/覆盖既有备份**）创建——
    防快速 ON/OFF、低分辨率格式或多窗口竞态生成同名路径覆盖旧备份，破坏"写前字节级
    备份"与 ≤3 硬不变量）。**备份可能包含完整用户配置及其中凭据**（官方
    允许 user config 内联 `api_key` / MCP token，§2.1-10），因此凭据留存面必须最小化：
    - **顺序与不变量（r4-M4 + r5-M3 裁决：crash-safe）**：**先把既有备份集合收敛到
      ≤2 份**（删最旧）→ 收敛失败 fail-closed（不动原文件）→ **创建本轮新备份
      （0600），集合恰为 3 份** → 写原文件。任何崩溃/中断点集合都 ≤3：收敛后崩溃 →
      ≤2；创建后崩溃 → 3——"最多 3 份"是 crash-safe 硬不变量（"先造第 4 份再轮转"
      存在崩溃后永久遗留第 4 份 secret 的窗口，禁止）。代价（已接受并诊断可见）：收敛
      后、创建前失败会使可用回滚点暂时降为 2 份，不恢复已删旧备份（硬上限优先于回滚
      深度）；
    - 内容不打印、不写入日志、不上报（日志仅记路径与字节数）；
    - 备份创建或权限收紧失败 → fail-closed：不动原文件（T26）；
    - 备份用于受限回滚（第 8 条），不做 redaction（会破坏精确回滚的逐字节等值前提）；
    - 原文件不存在时无备份，错误文案分支覆盖该情况（T17）。

### 3.4 UI 状态机与文案

开关 label 用名词（"Leader 模式"）。**术语约定**：开关只表达 **user 层配置维**；副文案
区分"核心三态"（配置 × socket 检测）与"失败态"；**"检测到 socket"≠"已订阅生效"**。
Swift 不做 socket 连接拨测——理由：官方 leader 本就是多客户端架构，拨测不是"与
LeaderSubscriber 竞争单活连接"，而是**新增一条会扰动 leader client 计数与生命周期的
连接**、并在 Swift 侧复制协议握手职责；leader 建立失败的可见性由 D-G1 在 runtime 层
承担（§3.5.1），实际订阅生效以 `go-bridge.log` 证据链为准（§2.2-4）。

核心三态：

| # | user 层 `use_leader` | socket 文件 | 开关 | 副文案 |
| --- | --- | --- | --- | --- |
| 1 | 无 / false | 未检测到 | OFF | 无（absent 与 explicit false 在 DiagnosticsSheet 中区分显示，行内不区分——两者对开关都是 OFF；explicit false 会在扩展观察态 #5 提示） |
| 2 | true | 未检测到 | ON | 橙色："已开启，重启 grok 后生效"（hover 全文：若 grok 正在运行请关闭并重新启动；尚未运行则直接启动。仅对此后启动的 grok 生效） |
| 3 | true | 检测到 | ON | 次级色："检测到 Leader socket，实时推送以运行日志为准"（不得表述为"已生效"） |

扩展观察态（不改变开关位置，仅信息，Phase 4 实现）：

| # | 条件 | 副文案 |
| --- | --- | --- |
| 4 | user 层无键或 false + 检测到 socket | 次级色："检测到 Leader socket 运行痕迹（原因无法判定：手动 `--leader` / 服务器推荐 / 配置变更后旧 leader 仍在运行）" |
| 5 | user 层 explicit false | 次级色："已显式关闭（会屏蔽服务器推荐开启）"——absent 与 false 的 remote fallback 语义不同，需可见 |
| 6 | 自定义 socket 路径（**Link 进程实际继承的 env** 解析结果非默认） | 中性提示 socket 路径；只反映 Link 继承的 env，不能发现任意 TUI 的 `--leader-socket`（§6-8） |

失败态（开关禁用 + 错误提示，fail-visible）：

| # | 条件 | 表现 |
| --- | --- | --- |
| F1 | config 读取失败 / TOML 解析失败 / symlink 悬空、循环或非普通目标 | 开关禁用，提示"无法安全读取 grok 配置（原因）" |
| F2 | 交叉裁决矩阵判等价形态（§3.3-3/6） | 开关禁用，提示"配置含 CordCode 无法安全管理的 use_leader 写法，请手工处理" |
| F3 | grok 未安装（`agent.isAvailable == false`） | 开关禁用 + `.help()`"未检测到 grok CLI" |

状态刷新时机：视图 appear、agents 刷新回调、开关操作完成后；socket 存在性仅做 `stat`，
**不引入常驻定时器**；UI 缓存可能滞后于 socket 实际消失（stale 残留属 §6-7 边界），
不追求实时一致。

**OFF 动作的 UI 可达路径说明**：explicit false 状态下开关已是 OFF，无"再次关闭"动作；
删键路径的 UI 入口是 **ON→OFF**。false→OFF 调用是幂等无操作（T16），不是删键的证明
路径。若用户希望移除自己的 explicit false，OFF 语义（删键）在 ON→OFF 之外不主动触达
用户既有选择；DiagnosticsSheet 的 absent/false 区分让状态可见。

### 3.5 Go 侧受控改动（均经 §15 Owner 批准；两块独立提交、独立测试、独立可回退）

#### 3.5.1 D-G1：leader 订阅建立失败回退 tailer（§15 D-1：accept，2026-08-28）

- **动机（r3-B3）**：零改动下 leader 异常崩溃残留 stale socket 后，每次 session-open
  重复失败、观察持续阻断；Swift UI 只能显示"检测到 socket"（§3.4 #3/#4），失败仅
  debug 日志（`grokbuild.go:193-195`）——与 §1/§11 fail-visible 红线直接冲突。
- **建立/live 分界（r4-B1 裁决：首事件规则）**：`grokbuild.go` 拿不到
  `leader_subscriber.go:190-199` 内部的 session/load 完成时点（无 callback/status
  channel），因此分界定义为——**"是否已向下游转发任何 leader event"**：
  - `SubscribeSessionEvents` 已在 `:163-172` 定义 `forward` 闭包并把它交给
    `sub.Run(ctx, forward)`；D-G1 在此处包一层**首事件标记**（如 `atomic.Bool`，
    `trackedForward` 先置位再转发），diff 完全限于 `grokbuild.go`；
  - **回退条件（三要素同时满足）**：订阅结束（`sub.Run` 返回，error 或 nil 一致处理）
    **且** 尚未转发任何事件 **且** ctx 未被取消（`errors.Is(err, context.Canceled)` 或
    ctx.Done → 不回退——这是与 D-G2 的互锁：D-G2 主动取消时 relay 已退出，不得再拉起
    无人消费的 tailer）；
  - 满足条件 → INFO 日志（含失败原因，文案以 G1 冻结为准，如 `leader subscribe failed,
    falling back to updates.jsonl tailer`）→ 在**同一 channel** 上启动 file tailer
    （复用 `grokbuild.go:176-185` 的 tailer 路径），`defer close(ch)` 语义不变；
  - **已转发过 ≥1 事件后断开 → 不回退**：channel 照常关闭，由 relay 层走现有 F-7 收口
    （`handlers_relay.go:225-243`）。注意"live 但尚未转发任何事件即断开"按本规则回退——
    这是安全的：`updates.jsonl` 是真相文件（grok 所有模式都写），tailer 可从文件补齐，
    事件不丢。
- **范围限定**：仅上述失败分支；**不删除 socket 文件**（可能是他人生成或正在重启的
  leader）；不做周期重试（下次 session-open 自然重选）；**不改 `leader_subscriber.go`**。
- **fail-visible 语义**：观察经回退继续（用户可见的恢复本身）+ `go-bridge.log` INFO 行
  可查；Swift 侧不需要新通道。
- **测试**：G1–G4（§7.1 Go 表）。
- **级别**：D3（恢复逻辑）；与 D-G2、Swift 改动分开提交。

#### 3.5.2 D-G2：subscriber-aware observer cancellation（§15 D-2：reject 现状，2026-08-28）

- **动机（r4-B2）**：现状 observer 以 `grok-leader:<sessionID>` 为 key（`handlers_relay.go:154-186`），
  每个打开过的 session 各自建立独立 relay，订阅用 `context.Background()`（`:244`），只有
  leader/channel 自身结束才清理——**长寿命 Link 进程中，已打开 session 的连接/goroutine/
  subscription 按会话无界累积，且 leader 被续命到 Link 退出**。Owner 否决该现状。
- **设计（对照 codex 先例）**：codex file relay 已有完整的订阅检查先例——
  `codexSessionHasSubscriber`（`handlers_relay.go:365-374`）查询
  `broadcaster.HasSessionSubscriber(backendID, sessionID)` 决定是否继续观察（push 模型：
  iOS 还开着就继续看）。D-G2 把同一模式用于 grok leader relay：
  1. `grokLeaderSessionRelayLoop` 把 `context.Background()` 换成
     `context.WithCancel`（ctx 本就会流入 `SubscribeSessionEvents` → `sub.Run`，取消即
     关闭订阅连接——`grokbuild.go:153` 注释确认这是两条数据源的设计行为）；
  2. 事件循环改为 `select`：`events` channel + 周期 ticker（**10s**）。每次 tick 查询
     `HasSessionSubscriber`（复用 codex 同款 helper，`backendID="grokbuild"`）；
  3. **取消时钟（r5-M1 立上界 + r6-M1 修正锚点）**：计时锚点是**首个连续负样本时间**
     `firstNegativeAt`——tick 采样为负（无订阅）且当前为零时记 `now`；采样转正（订阅
     重现）立即清零重计。在 tick 上判定 `firstNegativeAt ≠ 0 && now - firstNegativeAt ≥
     60s` → cancel。由此**从订阅者实际消失到取消的区间为 [60s, 70s)**（≥60s 判定阈值
     + 最多一个 10s 采样周期），保证"至少 60s grace"。v5/v6 的 last-positive 锚点已被
     r6-M1 证明与该区间不相容（设最后正样本 tick 为 L、实际消失 D∈(L,L+10]，取消在
     L+60，实际窗口仅 [50s,60s)），弃用；本文所有"grace 上界"统一指 **< 70s** 且一律
     从订阅者实际消失时刻起算（§4.3/§4.9/§6-4/§7.3 第 12 行/G5 同此口径；测试以注入
     假时钟覆盖边界 tick，满足"允许短 idle 抖动、但必须有确定上界"的 Owner 要求；
     **（r7-S1）[60s,70s) 为 ticker 正常调度下的名义窗口**——Go scheduler 长时间停顿/
     进程暂停可使实际取消晚于 70s，生产 wall-clock 非硬实时上界；假时钟单测仍严格
     断言该区间，验收不得把系统暂停/调度延迟误判为算法错误）。
     **（r8-M3 测试 seam，冻结；r9-M3 消歧）**：把 `firstNegativeAt` 判定抽成接收
     `(now, hasSubscriber)` 的**纯 helper**（无时钟、无 IO 的状态机：**负样本且
     `firstNegativeAt` 为零时才记 now；连续负样本保持原锚点，不重置**——否则计时永远
     达不到 60s；正样本→清零；返回是否应取消），ticker 只负责生产样本；G5 直接驱动
     该 helper 断言区间，另加一个短接线测试证明 ticker→helper→cancel/relay 清理的
     链路——不引入通用 fake-clock 基础设施，不跑 60 秒真实时间；
  4. iOS 之后重开该 session 时，现有 `startGrokLeaderSessionRelay` 路径自然重启 relay
     并重新选路（stat socket）——无需新增重启逻辑。
- **行为语义变化**：observer 生命周期从"到 Link 退出/leader 崩溃"收窄为 **"该 session
  存在 iOS 订阅期间 + <70s"**（首个负样本 +60s 判定 + ≤10s 采样，从实际消失起算）。
  leader 在最后一个客户端（官方
  客户端 + observer）离开后即可正常退出；不再按 session 无界累积。
- **主动取消与 source 断开分流（r5-B1，阻断级修正）**：现有 defer
  （`handlers_relay.go:225-243`）在 `turnArmed` 时无条件 `sendSessionEvent`，而该事件
  **并非只发给在线客户端**——`handlers.go:3035,3063` 走统一 `publishEvent`（`Offline:
  IsDurableMilestone`），`event_publisher.go:1059-1072` 中 timeline event 在解析目标连接
  **之前**先进入 Projection Kernel，`projection_reducer.go:1326-1328` 把 `turn_aborted`
  持久化为 aborted 终态。"当前无订阅者"只意味着没有在线接收者，**不意味着无副作用**。
  因此 D-G2 必须在 `handlers_relay.go` 内维护取消原因标志（如 `selfCancelled`，触发
  cancel 前置位）：**主动无订阅取消 → defer 不合成 `turn_aborted(leader_disconnect)`**
  （外部 turn 可能仍在 leader 上继续，结果未知不得猜终态——与 F-7 原则同源），只做
  `relayRunning` 清理 + registry claim 释放（下条）+ INFO 日志。**真正的 source channel
  断开（leader 崩溃/kill）仍走现有 F-7 合成，不变。**
- **主动取消的 sessionRegistry 收口（r6-B1，阻断级修正）**：v6 的"重开冷拉恢复真值"
  只对 projection/history 成立，**对独立的 sessionRegistry 不成立**。事实链（pin 源码
  已逐项核实，见 §10）：relay 在首个内容事件处 `h.sessions.markRunning`
  （`handlers_relay.go:266-280`，另 `:306-308` 对 turn_started 防御性 markRunning），
  正常终态 `:290-305` 与 source 断开 defer `:225-243` 各自 `markIdle`；v6 主动取消跳过
  两者后**没有任何路径收回 running**。而 grok catalog 链路
  `buildGrokEnrichedSessions`（`handlers_grok_catalog.go:75-81`）→
  `enrichSessionStatesForList` 明确 **never mutates the registry**
  （`handlers_opencode.go:238-245`），且 grokbuild 不实现 `RunningSessionLister` →
  `getRunningMap` 返回 nil（`:220-235`）→ `applyListRuntimeState`（`:278-297`）直接采用
  registry last-known running——**侧栏运行徽标会无限期 sticky running**；冷拉 /
  Projection hydrate 均不触碰 registry。修复设计：
  1. **授权 diff 扩展到 `go-bridge/types.go`**（r6/r7 允许"如实扩展"而非死守单文件）：
     registry 现有 idle/running/closing 三态（`types.go:226-230`，closing 无生产写点但
     在语义域内），无法表达"观察已结束、
     source 状态未知"；条目可能携带真实 `AgentSession`（put/putRaw），**删除真实会话行
     会丢句柄（禁止）**，但外部 relay 经 markRunning 新建的常见条目本来就是
     `session=nil` 的 **passive synthetic row**（`types.go:321-335`）。新增 unknown
     状态（既有 idle/running/closing 之外的**第四状态**）`sessionStateUnknown`；
  2. **ownership：generation token 取代局部 bool（r7-B1）**——v7 的 `claimedRunning`
     布尔只证明"过去调用过一次 markRunning"，不能证明 registry 当前 running 仍属于本
     relay（正常 terminal 后 grace 到期的 defer 会把已正确的 idle 覆盖成 unknown；
     其他路径的较新更新也会被过期 defer 覆盖）。改为仿现成先例 `agentRelayGen`
     （`handlers_relay.go:2687`，relayEvents `:2679` 起的所有权 token/CAS）：
     - `types.go`：registry 维护**全局单调 generation 计数器**，`put/putRaw/markRunning/
       markIdle/markUnknown` 每次变更都给条目盖上**全局唯一**的新 gen（全局而非每条目
       自增，保证任何替换/重建后旧 token 必然失配）；
     - 新方法 `claimRunning(sessionID) uint64`：= markRunning + 返回本次 gen（token）；
     - 新方法 `releasePassiveClaim(sessionID, gen) passiveClaimReleaseOutcome`（r9-M1：
       返回 **typed outcome 枚举** `Noop / Deleted / Unknown`，不是 bool——bool 无法
       无竞态区分三值，调用方也不得 release 后重读 registry 猜结果）：锁内取条目，
       若**不存在 / state ≠ running / gen 失配**（claim 已被正常 terminal、其他路径或
       条目重建取代）→ 返回 `Noop`，绝不覆盖较新状态；若匹配则按两类落点释放
       （下条，分别返回 `Deleted` / `Unknown`）。日志与 fence 判定统一以 outcome 为
       准：`claimReleased = outcome ≠ Noop`；
     - `handlers_relay.go`：relay loop 的两个 markRunning 调用点（`:269` 与 `:307`）改用
       `claimRunning` 并更新本地 `claimGen`；正常 terminal（turn_completed/error →
       markIdle，`:303`）与真 source 断开 defer（markIdle + F-7）**都清零 claimGen**；
       主动取消 defer 仅在 `claimGen ≠ 0` 时调用 `releasePassiveClaim`；
  3. **释放的两类落点（r7-B1 第 2 条）**：token 仍匹配时——
     - **synthetic 行（`session == nil`）→ CAS 删除条目**：catalog 无记录时
       `applyListRuntimeState` 本来就输出 `runtimeState="unknown"`（F-8），删除即达到
       "无徽标"且**不留下永久条目**（否则把 D-G2 要消除的按 session 无界累积从
       goroutine 转移成 registry map 累积——`cleanupIdleSessions` 只清 `state==idle`
       （`handlers.go:929`），unknown 条目永不回收）；
     - **真实会话行（`session != nil`）→ 转 `sessionStateUnknown`**，保留句柄；该行
       生命周期由 session open/close（deleteIfSame 等）管理，不属 D-G2 回收范围；
  4. **unknown 态消费者安全（r7-B1 第 3 条 + r8-B2 修正）**：registry **并非二态**——
     `types.go:226-230` 已声明 `idle` / `running` / **`closing`** 三种状态（当前 grep
     无生产写点，但已声明的状态在语义域内，不得当作不存在）。现有
     `isIdle = !ok || state==idle`（`types.go:373-377`）把 running **与 closing** 都判为
     active；若按 v8 用精确 `isRunning = state==running` 替换，会**静默改变 closing 的
     既有语义**（六处消费点横跨 Codex/Claude/通用 relay，非 Grok 私有路径）。修正：
     新增 **`isKnownActive(sessionID) = ok && (state == running || state == closing)`**
     ——idle/running/closing 既有域上与 `!isIdle` **逐点等价**，仅 unknown/不存在退出
     active；把 `!isIdle` 当 active 使用的全部六处消费点替换为 `isKnownActive`：
     `handlers_relay.go:324`（codex Live 判定）、`:449,457`（codex hardCap 的
     process_death abort 合成 + broadcastIdleState）、`:905`（claude TTL
     broadcastIdleState）、`:2728`（relayEvents channel-close 合成
     **`turn_completed(reason=events_channel_closed)`**，事件体 `:2737`——r8-M1 修正：
     v8 误写为 aborted）、`:2863-2864`（relayEvents idleTimer 自动合成
     **`turn_completed`**）。
     unknown 态下从"自动收口"降为"不动作"——unknown 不得触发任何 running-only
     自动终态或 idle 广播。六处全部位于 `handlers_relay.go`，**无需触碰 `handlers.go`**；
  5. **wire/持久化语义**：release（删除或转 unknown）只改 registry——不产生任何 wire
     event、不写 durable 终态；catalog 下一次 list 输出 unknown → 客户端不渲染徽标，
     即既有 **F-8"不知道就不亮灯"语义**（owner 2026-08-15 拍板，
     `handlers_opencode.go:273-277`）在 Grok 的自然延伸。`onStateChange` 消费方
     （`handlers.go:289-298`）对 unknown 态安全：只做 Claude running map 无条件失效；
     newState≠idle 不触发 `completeBridgeTurn`（主动取消本就不应结算 bridge turn）；
  6. **真值重建**：iOS 重开后新 relay 若见 turn 仍在跑（首个内容事件）→ claimRunning
     重建 running 徽标；若 turn 在无观察期间已完成，冷拉显示真实历史、registry 无该
     claim（synthetic 已删 / 真实行 unknown 无徽标）直至下一事件——两种结果都不撒谎。
     真 source 断开 defer 不变（markIdle + turn_aborted）："已证中断"与"观察结束状态
     未知"语义分流；
  7. **catalog 快照穿透（r8-B1，裁决：现成 fence 方案）**：v8 声称"catalog 下一次 list
     输出 unknown"**不成立**——Grok catalog 的 builder 在富化时把当时 registry 状态写进
     wire map（`handlers_grok_catalog.go:75-81`），缓存的是**已富化快照**，page-0 走
     `FetchOrReuse` 不会重新 overlay registry（`catalog_wire_snapshot.go:232-318`）；快照
     TTL **10 分钟**（`catalog_cursor_v2.go:31`）；runtime overlay 被
     `listSemanticFingerprint` 排除在语义指纹外（`catalog_native_membership.go:78-99`，
     指纹只含 id/updatedAt/title/dir/project）；registry `onStateChange` 只失效 Claude
     running map，不失效 Grok catalog wire cache（`handlers.go:289-298`）。只改 registry
     会双向陈旧（缓存 running → 取消后继续亮 ≤10min；缓存 unknown → reclaim 后继续无
     徽标 ≤10min），与第 12 行验收承诺冲突，**不接受"10 分钟内最终一致"**。裁决采用
     **最小范围方案**：relay 在 Grok registry 的**四类有效状态变化成功后**调用现成的
     `catalogWireSnapshotCache.FenceBackend("grokbuild")`（r9-B1 补全）——
     **① claim running；② 正常 terminal → idle；③ 真 source 断开 → idle（F-7 defer 的
     `markIdle`，`handlers_relay.go:225-243`——v9 遗漏该分支：armed turn 真断开同样改
     registry，无 fence 时已缓存的 running 快照可继续复用 ≤10 分钟）；④ self-cancel
     release → unknown/delete**（`catalog_wire_snapshot.go:202`：同锁推进 backend 级
     generation、删除已提交 scope、完成并丢弃 in-flight
     构建）→ 下一次 page-0 重建并按当前 registry 富化。**已披露代价（r8 要求）**：fence
     使该 backend 存量分页 cursor 的 page-N `Peek` 返回 nil → **`cursor_stale`**，客户端
     需回到 page-0 重取；fence 仅由 Grok 状态变化触发、频率 = 外部 turn 生命周期节奏，
     不构成高频抖动。本方案 diff 仍在 `handlers_relay.go`（调用现成函数），**不改
     catalog 代码**；被否决的替代方案（runtime overlay 移出富化快照、出站时重覆盖）
     会真实扩展 catalog 文件范围，不采用；
  8. **取消日志冻结结构化字段（r8-S2）**：self-cancel 的 INFO 行至少包含
     `backendID`、`sessionID`、`reason=no_subscribers`、`firstNegativeAt`/`elapsed`、
     `claimReleased`、`registryOutcome=deleted|unknown|noop`；**不得记录 cwd/config
     内容**。owner 与开发 agent 据此即可判定 release/CAS 是否真正发生，不依赖自然语言
     文本。
- **与 D-G1 的互锁**：D-G2 的 cancel 走 ctx 取消路径，D-G1 的回退条件显式排除 ctx 取消
  （§3.5.1），不会在 relay 已退出后拉起无人消费的 tailer。
- **范围限定**：diff = `go-bridge/handlers_relay.go`（grok relay loop、其启动处、§3.5.2
  第 4 条列出的六处 `!isIdle`→`isKnownActive` 替换，及第 7 条的 `FenceBackend` 调用——
  仅调用现成函数）+
  `go-bridge/types.go`（sessionRegistry unknown 态 + 全局 generation claim/释放，r6-B1/
  r7-B1 授权扩展，附 registry 定向测试）；不改 `grokbuild.go`（那是 D-G1 的领地）、
  不改 `leader_subscriber.go`、不改能力声明、**不改 catalog/enrich 代码**（unknown
  消费走 `applyListRuntimeState` 既有分支、fence 走现成 `FenceBackend`，均无需改动）、
  无需改 `handlers.go`（六处消费点均在 handlers_relay.go；cleanupIdleSessions 为
  `handlers.go:929`，不触碰）。
- **测试**：G5–G8（§7.1 Go 表，含 registry 定向测试）。
- **级别**：D3；与 D-G1 分开提交。二者均为本设计新增，但行为判据独立：D-G1 = 建立失败
  恢复；D-G2 = 无订阅回收。

### 3.6 TOML 依赖冻结与接线（D4；r3-B1 裁决，r4 上游核实一致）

**冻结选型**（设计期已定，r4 评审已从上游 tag 独立核实；实施不得漂移；变更需回写本节）：

| 项 | 值 |
| --- | --- |
| Package | `mattt/swift-toml`（https://github.com/mattt/swift-toml） |
| 版本 | **2.0.0 exact**（tag commit `827506c90475e82d5a7f191f950fb3025cbdc0d6`） |
| License | MIT（库）+ 内嵌 toml++ 单头文件（MIT，随包分发，上游版本由周检 CI 跟踪） |
| 平台 | macOS 10.15+（app deployment target macOS 14.0 ✓）；manifest `swift-tools-version:6.0` → 需工具链 Swift ≥ 6（本仓 Xcode 26.5 ✓；app target 语言模式不受影响） |
| 传递依赖 | 无（内部 `CTomlPlusPlus` C++17 target 为包内内嵌，无网络依赖；`Package.swift:1-40` r4 已核实） |
| 使用 API | `TOMLDecoder`（Codable）：整文档 parse + 解码只含 `cli.use_leader` 的 struct（多余键忽略）——即 §3.3-3 的语义 oracle 职责 |
| 不使用的 API | 编码器 / 格式化输出（本设计写入走外科文本编辑，永不 parse→serialize） |
| 备选（仅当构建不兼容时） | `LebJe/TOMLKit` 0.5.0+（MIT，同为 toml++ 封装，`TOMLTable(string:)` API）；换用须回写本节 + 重跑 D4 门 |

**接线（XcodeGen，全部入提交范围）**：

1. `MacBridge/project.yml` 顶层 `packages:` 增加 TOML（url + `exactVersion: 2.0.0`）；
   app target 与 test target 的 `dependencies:` 各加 `package: TOML`；
2. 运行 `xcodegen generate` 重生成**已跟踪**的
   `MacBridge/CordCodeLink.xcodeproj/project.pbxproj`（提交）；
3. `xcodebuild -resolvePackageDependencies` 生成
   `MacBridge/CordCodeLink.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved`
   （当前仓库无此文件，属新增，提交；确认锁定的 revision 即上表 tag commit）。

**D4 验证门（r3-M1）**：① XcodeGen 重生成一致性（再跑一次 `xcodegen generate` 无非预期
diff）；② locked resolution（Package.resolved 固定 2.0.0/827506c，无浮动）；③ app 与
test target 增量编译通过；④ unsigned Release package 构建通过（`scripts/build-unsigned-release.sh`）；
⑤ license / 传递依赖审计（MIT ×2、无网络传递依赖，记录于本节）。D4 不默认跑全量测试，
也不含 UI automation。

**审计留档（r4-S1）**：Phase 1 完成报告必须保存依赖的 **source identity**——tag commit
哈希、`Package.swift` 内容哈希、主 LICENSE 摘要、内嵌 `toml.hpp` 的 SPDX 标识与版本
（r4 自 tag 核实为 v3.4.0）——未来升级不必重新猜基线。

---

## 4. 功能面完整映射（开关 OFF × ON，对照 bridge 全功能面 × iOS 既有功能）

总原则（源码证据）：两条观察源在 `SubscribeSessionEvents` 汇入**同一 codec**（`convertSessionUpdate`）与**同一 relay loop**（`grokLeaderSessionRelayLoop`），turn 生命周期合成逻辑——首内容事件合成 `turn_started` + `_meta.promptId` 身份、`turn_completed` 主收口、源断开时 F-7 合成 `turn_aborted`——两条路径共享不变（`grokbuild.go:143-150` 注释、`go-bridge/handlers_relay.go:185-243`）。**D-G1 不触碰该层；D-G2 不改 codec/合成逻辑，但修改该 relay loop 的取消路径、registry claim/释放与 catalog 可见性穿透（§3.5.2）**。因此**开关的行为变化集中在 §4.1（D-G2 的运行徽标收口）、§4.3 流式同步与 §4.9 恢复/生命周期三处，其余功能面不变**（r8-M2 修正：原"两行"口径遗漏了 D-G2 对侧栏徽标的影响）；这正是 §3.2"核心链路零改动"主张的依据（codec/协议层零改动）。

### 4.1 会话列表与发现（iOS 侧栏：目录分组、标题、时间、运行徽标、置顶、下拉刷新）

**结论（r8-M2 修正）：catalog 链路结构不变，运行徽标有 D-G2 例外**：catalog 走 ACP
`session/list` 单例子进程（显式 `--no-leader`，
`catalog_session_list.go:119-124`），目录分组 / 标题回填 / 置顶 / B2 公平切片 / cursor
分页照旧；列表级"新会话出现"仍是 5s 指纹轮询 + 60s 兜底（`go-bridge/session_discovery.go:46`）。
**例外：运行徽标**——D-G2 的 registry claim/释放改变该行 `runtimeState`，且必须经
`FenceBackend` 穿透 10 分钟富化快照才能及时可见（§3.5.2 第 7 条，r8-B1）。
真实原因：**官方 leader 把 `x.ai/sessions/changed` 作为 machine-wide 通知广播给所有客户端**
（`leader/server.rs:390-428`，官方 Pager 也消费它，`xai-grok-pager/src/app/acp_handler/settings.rs:409-426`），
而 macbridge 的 `LeaderSubscriber` 当前只处理 session-update 方法并**丢弃该 roster 通知**
（`leader_subscriber.go:319-334` 的 `isSessionUpdateMethod` 过滤）。消费它需要 go-bridge
改动，属未来改进（§9），本设计不做（D-G1/D-G2 均不触碰该过滤器）。

### 4.2 消息页加载（历史冷拉、SSV2 投影水合）

不变。`chat_history.jsonl` rich history + 稳定行号 ID + pathless hydrate 照旧；观察路径只影响 live 增量，不影响冷基线。

### 4.3 流式同步与运行态（外部 turn 实时流——本设计唯一核心变化行）

| 维度 | OFF（现状：updates.jsonl file tailer） | ON（leader.sock push，开关生效后） |
| --- | --- | --- |
| 事件来源 | `~/.grok/sessions/<cwd>/<id>/updates.jsonl` 文件追加（grok 所有模式都写） | leader 进程经 socket 推送的 `session/update` 通知帧 |
| 延迟 | 1s 轮询 + 批处理 | 实时推送 |
| 附着模型 | 按需启动；session 目录迟绑定等待上限 2min；turn 终态后 90s grace 守下一轮；30min hardCap；文件 truncate 重置 | `register(client_type=cordcode-macbridge-observer)` → ACP `session/load` 完成订阅；30s ping 保活；`_meta.isReplay` 重放帧丢弃 |
| 建立失败行为 | 不适用 | **D-G1：未转发任何事件即断开（非 ctx 取消）→ 回退 tailer + INFO 日志，观察继续（§3.5.1）** |
| 事件语义 | 与右侧**完全一致**（同 codec 同 relay loop：turn_started 合成 / promptId turnId / F-7 中断兜底） | 同左 |
| 覆盖范围 | 仅覆盖 iOS 打开过（有订阅）的会话 | 同左；**D-G2：session 无 iOS 订阅（首个负样本起算 ≥60s，下一 tick 内取消，全程 <70s）→ observer 回收（§3.5.2）**，观察窗口 = iOS 订阅窗口 + <70s |
| 生效判据 | 日志 `falling back to updates.jsonl file tailer` | 日志三行链 starting → connected → **live** + 真实 turn 事件（§2.2-4）；**socket 文件存在本身不是生效证据**（§6-7） |
| iOS 体验 | 外部 turn 最多 ~1s 级延迟；超长 turn 可能被 hardCap 掐断后靠重开恢复 | 外部 turn 实时流式；不受 hardCap 约束 |

### 4.4 发送消息（iOS 自己发起的 turn）

不变。macbridge 自有 `grok agent --no-leader stdio` 子进程（`session.go:79-82`）；flag 优先级高于 config（§2.1-1），开关写入不改变此路径。

### 4.5 会话生命周期（新建 / 恢复 / 取消）

不变。`session/new` / `session/load`（含 capability 门禁）/ `session/cancel` 均为自有子进程 ACP RPC。外部 turn 的 abort 两路均不支持（只读纪律，§2.2-3）。

### 4.6 权限与问答

不变。自有 turn 权限照旧（`session/request_permission`，"always" 降级 allow）；外部 turn 的权限请求两路均不介入（observer 从不回答 agent→client 请求，`leader_subscriber.go:319-322`）；question 两路均不支持（ACP 无该协议）。**例外风险见 §6-6（interaction 无人应答时 turn 长期等待——D-G2 后等待窗口 = iOS 会话保持打开期间）。**

### 4.7 模型 / provider / reasoning effort

不变。现有缺口（`SetModel` 只改内存、spawn 参数不传、`AvailableModels` 空目录）与观察路径无关，属 macbridge 侧独立改进项，本设计不触碰。

### 4.8 用量 / 附件 / checkpoint

不变。context usage 仍来自 `signals.json` + `usage_update` 事件（两条观察源共用 codec 的映射分支）；附件仍 file-only；checkpoint 仍是 workspace 文件快照、非会话真相源。

### 4.9 恢复、重连与 leader 生命周期（v5 按 D-G1/D-G2 目标语义重写；现状差异已标注）

- OFF：tailer 生命周期自管理（90s grace / 30min hardCap 退出）；iOS 下次 poll 打开会话时重启 relay。
- ON（目标语义 = D-G1 + D-G2 实施后）：
  - **观察窗口 = 该 session 的 iOS 订阅窗口 + <70s**（首个负样本 +60s 判定 + ≤10s
    采样，从实际消失起算，D-G2）：iOS 关闭/切换会话后经 60s 判定 + 下一 tick（上界
    <70s）→ observer 取消、relay 退出并清理（**主动取消不合成 turn_aborted、registry
    收口为 unknown**，见 §3.5.2 分流）；重开会话时
    现有路径重启 relay 并重新选路（stat socket）。iOS 快速切页（<60s）不产生重建抖动；
  - **TUI 关闭后 turn 的命运（Phase 0 第 6a 步已实测证实，2026-09-02）**：无 pending
    interaction 的 turn 在 leader 上继续执行至完成——driver（TUI）mid-turn SIGKILL 后
    leader 无头跑完 turn（实测 kill 后约 108s `turn_completed`（end_turn）落盘
    updates.jsonl，与上游 `session_driver.entry().or_insert` 首个 session/load 者为
    driver、订阅者死亡仅清理的语义一致）；前提是 iPhone 会话保持打开（observer 在场）。
    interaction（permission / question / plan approval）是**共享广播、
    first-answer-wins**（`server.rs:491-500`），observer 收到但永不回答——若其他客户端
    均已关闭且 iPhone 会话也关闭（超过 §3.5.2 上界），observer 取消 → leader 若无其他
    客户端则退出，turn 命运交还 grok 自身（Phase 0 第 6a/6b 步实测定型，§6-6）；
  - **leader 真死（崩溃 / 被 kill）→ channel 关闭 → relay 退出**：已转发过事件的订阅，
    turn 未收口时按 F-7 合成 `turn_aborted(leader_disconnect)` + idle
    （`handlers_relay.go:225-243`）；**尚未转发任何事件的订阅按 D-G1 回退 tailer**
    （§3.5.1），观察经文件继续，不触发 F-7（无事件即无 armed turn）；
  - **stale socket**：崩溃残留 socket 不会被 macbridge 清除，但 **D-G1 使每次 session-open
    的建立失败都回退 tailer**——观察不中断（以 1s 轮询节奏继续），INFO 日志可查；该路径
    被新 leader 正常接管后，下次订阅自动回到 push。与"正常退出删 socket、下次订阅回退
    tailer"统一为同一恢复语义；
- iOS 下次打开会话时重启 relay 并重新选择路径（`grokbuild.go:174-198`）；订阅运行期间
  不做自动热切换（D-G1 仅覆盖"未转发任何事件"的建立失败，不覆盖 live 中断）。
- driver transfer（Phase 0 第 7 步）：TUI 断开时 session driver 可转移给剩余订阅者（含
  observer）；其影响范围是 **driver-only 消息路由**，interaction 请求本来就是共享广播、
  与 driver 无关。
- **现状对照（D-G1/D-G2 实施前，Phase 0 基线）**：observer 不随 iOS 关闭会话取消、按
  session 无界累积、leader 续命到 Link 退出（§2.1-3）；stale socket 持续阻断观察
  （§6-7）。两处差异正是 §15 D-1/D-2 批准的改动对象。

### 4.10 诊断与能力声明

能力声明不变：diagnostics 仍为 cli 探测 + 版本门槛；wire descriptor 的
`RequiresExternalTurnPolling: true` 与 `StaticCapabilities` 保持不动——`external_turn_streaming`
capability 的广告属于 follower 升级案（§9）。D-G1/D-G2 不改变能力声明（回退与取消都是
runtime 内部行为，对外事件语义不变）。诊断**可见性增强为 Phase 4 必做**（§3.2）：
DiagnosticsSheet 增加 Leader 状态行（user 层配置值**区分 absent / explicit false / true** /
socket 路径与存在性 / 安装版本），它是 §2.1-6 版本漂移 fail-visible 的落地通道。

---

## 5. 安全与风险

- 修改的是用户官方工具的配置文件：授权来自用户显式拨动开关；影响面限单键（user 层）；
  可逆（关 = 删键 + 备份）；诊断/日志可见。与 OpenCode managed、codex-web attach 先例同级。
- **并发安全是 best-effort**：内容身份比较存在 compare→rename 残余窗口，极端情况下可能
  覆盖官方同期写入（§3.3-7）；缓解 = 冲突检测失败可见 + 备份 + 受限回滚 + 官方侧进程内
  SAVE_LOCK 降低同进程交叠概率。**不得在任何验收或报告里宣称"绝不覆盖"。**
- **备份与凭据（r3-M2 + r4-M4 + r5-M3）**：manager 不解析、不记录、不外发任何配置内容；唯一读
  用途 = 字节级备份与外科编辑。**备份副本可能包含完整用户配置及其中凭据**（官方允许
  user config 内联 `api_key` / MCP token，§2.1-10），以 `0700` 目录 + `0600` 文件保护；
  **留存上限 3 份是 crash-safe 硬不变量**：先收敛旧集合（≤2）再创建新备份，任何崩溃/
  中断点集合 ≤3（§3.3-10）——凭据留存面不因轮转失败或崩溃扩大。不触碰
  `~/.grok/auth.json`。
- 不改变 macbridge 自有 turn 路径（`--no-leader` 显式优先，§2.2-2）。
- observer 生命周期（D-G2 目标语义 = 订阅窗口 + <70s 取消上界，即首个负样本 +60s 判定
  + ≤10s 采样）与 interaction 等待边界是**Owner 已签署的产品边界**（§15，2026-08-28）；
  帮助文案如实披露（§6-4/§6-6）。

---

## 6. 已知边界与待实测项

1. **只对未来启动的 grok 生效**：正在运行的 inline TUI 不会因此变 leader；UI 以状态 #2
   的橙色提示如实表达。开关打开 ≠ 已生效，"检测到 socket" ≠ 已订阅生效（§3.4）。
2. **观察通道升级 ≠ 交互能力升级**：iOS 对外部 turn 仍是只读（不能停、不能答权限）。
3. **已在 tail 的会话不热切换**：路径选择发生在下一次 `SubscribeSessionEvents`；表现为
   用户重启 grok 后，iPhone 需重新打开/切换会话才走上 push。可接受；D-G1 亦不改变
   （仅"未转发任何事件"的建立失败回退，§3.5.1）。
4. **observer 生命周期（v7 = D-G2 目标语义）**：observer 随 iOS 订阅存续（上界 <70s =
   首个负样本 +60s 判定 + ≤10s 采样，从实际消失起算），不再无界累积、不再把 leader
   续命到 Link 退出（§3.5.2）；主动取消**不合成 turn_aborted、registry claim 释放**
   （§3.5.2——synthetic 行删除 / 真实行 unknown：侧栏不 sticky running、也不谎报 idle、
   不留永久条目），重开后冷拉显示历史
   真值、后续事件重建 running/idle 徽标。**副作用（帮助文案披露）**：iPhone 长期不看
   的会话，其外部 turn 的实时流也会停——重开会话即恢复（tailer/leader 重选 + 冷拉
   补齐）；取消后至重开前，侧栏该会话无运行徽标（unknown，F-8"不知道就不亮灯"）。
   数值以实现测试冻结（G5 边界 tick 断言）。**实施回写（2026-09-02）**：D-G2 已落地
   （`grok_leader_relay_dg2_test.go` 13 用例：G5 锚点状态机/59.9s 不取消/[60s,70s)
   取消区间/正样本清零、G6 闪断保持、G7 synthetic CAS-deleted 与真实行 unknown、
   G8 claim/终态/idleTimer/codex 硬上限/claude TTL 五路 unknown 不冒充），生产路径级
   观感归 §7.3 owner 矩阵。
5. **effective config 的真实不生效原因（四因，r3-M4 补全）**：① 显式 `--no-leader`
   （最高优先级，启动参数可观察）；② requirements/MDM 层覆盖；③ confinement veto（链后
   最终 veto）；④ 版本漂移。managed 层被 user 覆盖、不是原因；项目级 config 不影响该键。
   本开关只写只读 user 层；此类环境症状 = 状态 #2 橙色提示持续，由 DiagnosticsSheet 与
   帮助文案说明（帮助文案应列出四因，含"检查启动命令是否带 `--no-leader`"）。
6. **interaction 无人应答风险（v5 窗口更新）**：interaction 请求是共享广播、
   first-answer-wins（§2.1-4）；TUI 关闭后若有 pending permission/question/plan approval，
   observer 收到但不答，turn 可能长期等待——**等待窗口 = iPhone 会话保持打开期间**
   （D-G2 后，iPhone 关闭会话超过上界即取消 observer，leader 可能随之退出）。帮助文案
   （Owner 已批 D-3）："权限/提问等待期间不要关闭唯一可应答的客户端（grok TUI）"。
   follower 应答能力仍另案。"TUI 关闭后 turn 继续完成"仅对无 interaction 的 turn 成立
   （**Phase 0 第 6a 步已证实**，见 §4.9），**不得作为无条件收益表述**。
   **第 6b 步实测结论回写（2026-09-02，session 01a06048）**：① pending interaction
   跨 TUI 死亡存活——发问 TUI 死于提问前 1s，问题广播后在零可答客户端状态挂起 56s
   （上游默认 30 分钟超时，`[toolset.ask_user_question] timeout_enabled/timeout_secs`
   可配；56s 为实测窗口，机制上无差别——无客户端→无回答→挂到超时）；②
   **replay-on-attach**——新客户端 `session/load` 后立即收到 pending interaction
   （attach→回答注册 2.5s）；③ first-answer-wins、回车确认默认高亮项 options[0]；
   ④ observer（iPhone）视角确认 §6-6 风险——`ask_user_question` REQUEST 帧在
   macbridge method 门被弃（iPhone 看不到问题内容），turn 全程运行态、回答后 terminal
   正常到达，iPhone 观感即「转圈直到突然结束」。
   **（2026-09-03 已修复：follower question-only 起步交付后 method 门按 request kind
   分发，iPhone 可直接作答；本条 ④ 保留为修复前实测基线，进度见 think.md 总账
   「Grok follower 交互升级」行与
   `docs/2026-09-02-grokbuild-follower-question-implementation-plan完成情况.md`。）**
7. **stale socket（D-G1 已批，语义更新）**：leader 异常崩溃残留 socket 文件后，每次
   session-open 的建立失败都会**回退 tailer**（§3.5.1）——观察以 1s 轮询节奏继续、INFO
   日志可查，不再持续阻断；socket 文件本身仍不被清除，该路径被新 leader 接管后自动回到
   push。Phase 0 第 8 步记录的是 D-G1 实施前基线（持续失败），实施后以 §7.3 第 11 行
   复验。
8. **custom socket（excluded）**：`GROK_LEADER_SOCKET` / `--leader-socket` 自定义路径
   场景：Swift 只按 **Link 进程实际继承的 env** 解析并展示（扩展观察态 #6），不能发现
   任意 TUI 进程的 `--leader-socket` flag（该 flag 在 TUI 进程内转为 env，
   `pager-bin/main.rs:2007-2009`，不跨进程）；不承诺完整诊断与验收。
9. **chat 模式互斥（excluded）**：用户使用 `grok --chat` 时 grok 自身报错引导——grok
   官方行为，不由本开关处理，帮助文案说明。
10. **状态 #4（OFF + socket 存在）**：原因不可判定（手动 `--leader` / 服务器推荐 /
    配置变更后残留），文案保持中性（§3.4），不做单义归因。

### 6.1 历史坑与结构性消除对照

对照 codex-web §2.5 的方式，把与本设计直接相关的已发生/已识别问题转成结构约束与验证落点：

| 历史问题 | 与本设计的关系 | 结构性消除机制 | 验证落点 |
| --- | --- | --- | --- |
| F-7（2026-08-15 登记簿）：leader 断开时未收口的 turn 曾被静默猜成"完成" | 开关 ON 后 F-7 的真实触发场景 = 已转发事件后 leader 真死（崩溃/kill）；正常 TUI 关闭在 iPhone 会话打开时不触发 | relay loop defer 兜底：未收口 turn 合成 `turn_aborted(leader_disconnect)` + idle（`handlers_relay.go:225-243`）；D-G1 不触碰该路径 | Phase 0 第 8 步 + §7.3 第 10 行 |
| file tailer 30min hardCap 掐断长 turn 观察，靠 iOS 重开恢复 | 开关 ON 后该路径退场；OFF 用户仍依赖；D-G1 后它兼任建立失败回退位 | tailer 原样保留（§0.3 禁止删除） | §7.3 第 9 行 OFF 回归（及第 11 行 D-G1 回退） |
| macbridge 自有 turn 与官方进程的 writer 竞争（grok-driver 设计期确立的 `--no-leader` 纪律） | 写入 config 后竞争面必须不变 | spawn 显式 `--no-leader`（`session.go:79`）优先于 config；写入器绝不触碰 spawn 逻辑 | §7.3 第 6 行 |
| per-session 后台 observer 无界累积 + leader 被续命到 Link 退出（r4-B2 识别，现状缺陷） | 开关 ON 后每个打开过的 session 都会挂 observer | D-G2 subscriber-aware cancellation（§3.5.2，Owner 已批）：无订阅 ≥60s 后下一 tick 取消（上界 <70s）；主动取消不合成 turn_aborted（r5-B1）、registry claim 释放不 sticky running/不积累（r6-B1+r7-B1） | G5–G8 + §7.3 第 12 行 |
| "部署成功"实为旧 runtime 在跑（2026-08-25 事故） | Phase 4 Release 覆盖安装后的开关验证 | 进程代际 + 8777 监听者核对（CLAUDE.md 部署后运行态验证） | §7.4 构建纪律 |
| 跨仓排障读了错误工作树（2026-08-24 P0 事故） | 本设计引用 grok-build 源码断言 | 头部来源清单 + 接手时重新生成核对 | 本文头部约定 |

---

## 7. 测试与验收

### 7.1 单元测试

**Swift（`GrokLeaderModeManager` / TOML 编辑器）**：测试文件
`MacBridge/MacBridgeTests/GrokLeaderModeTests.swift`（测试 target 名为 `CordCodeLinkTests`，
源码目录为 `MacBridgeTests`，命名惯例 `<Subject>Tests.swift`——勿把 target 名当目录名找）。

| # | 用例 | 期望 |
| --- | --- | --- |
| T1 | 无 config 文件 → 开 | 创建含 `[cli]` + `use_leader = true` 的最小文件，mode 0644 |
| T2 | 已有 `[cli]` 含其他键与注释 → 开 | 仅增/改 `use_leader` 行；其他键、注释、顺序、缩进原样 |
| T3 | `use_leader = false` → 开 | 原位改 `true`，行内注释（若有）保留 |
| T4 | `use_leader = true` → 关 | 删键；`[cli]` 其他键保留 |
| T5 | 删键后 `[cli]` 节为空 → 关 | **节头保留**（§3.3-2） |
| T6 | 其他节存在同名风格键（如 `[agent]` 下） | 不误读不误改；顶层裸 `use_leader` 与 `[cli].use_leader` 正确区分 |
| T7 | 写入前磁盘内容被并发修改（内容身份比较失败） | 放弃写入、失败可见，磁盘内容不被覆盖（best-effort 语义内） |
| T8 | 写入失败（目录只读等） | 原文件不变，错误上抛，开关回弹 |
| T9 | `GROK_HOME` / `GROK_LEADER_SOCKET` 解析 | home 与 socket 路径解析链与 `leader_subscriber.go:63-79` 逐分支一致；仅反映 Link 继承的 env |
| T10 | 备份轮转（crash-safe 顺序） | 初始已有 3 份 → 先收敛到 2 再创建新份（任意观察时刻 ≤3，§3.3-10）；备份内容 = 写前原文件；无原文件时无备份；**同一时刻两次写入的备份名碰撞 → exclusive create 换名重试、不覆盖（r8-M6）** |
| T11 | config 文件与 `$GROK_HOME` 目录均不存在 → 开 | 创建目录（0700）+ 原子创建 config（0644） |
| T12 | `[cli] # trailing comment` 节头（synthetic） | 节头识别正确；追加/删键不破坏注释 |
| T13 | CRLF 行尾（synthetic） | 全程保持 CRLF，不产生混合行尾 |
| T14 | 前导空格与行内注释（`use_leader = true # x`，synthetic） | 识别与原位替换正确 |
| T15 | 点键 `cli.use_leader` / quoted key / inline table 等价形态（synthetic） | 交叉裁决矩阵命中 F2：拒绝写入，不产生第二语义键（§3.3-3） |
| T16 | 已存在 false / absent 时执行 OFF | false → 删键；absent → 幂等无操作成功返回（并注明 UI 可达路径是 ON→OFF，§3.4） |
| T17 | 无原文件时执行写后校验失败分支 | 错误文案覆盖"无备份可恢复"路径 |
| T18 | symlink config（synthetic） | 链接到外部普通文件 → 写目标文件、链接不变、temp+rename 在目标目录；悬空/循环链接 → F1 |
| T19 | TOML 解析失败（非法语法） | **F1**：拒绝管理，不猜测（§3.3-3） |
| T20 | absent / explicit false / true 状态上报 | manager 状态三者区分，DiagnosticsSheet 可显示 |
| T21 | 相对 symlink 与多级链（≤8 级，synthetic） | 相对链接按链接所在目录解析；写最终 canonical 目标 |
| T22 | symlink 最终目标非普通文件（目录/FIFO/socket，synthetic） | **F1** |
| T23 | 身份钉扎复核失败：①resolve 后、rename 前链接链被换（link swap）；②**链接文本不变但最终目标文件被替换（同路径、同内容、不同 inode）**（synthetic，r8-M5） | 两种都放弃写入、失败可见（§3.3-4 双重复核） |
| T24 | symlink 目标 mode 保留（synthetic） | POSIX mode 保留；ACL/xattr 不保留（已声明边界，附注释说明） |
| T25 | 备份目录与文件权限 | 子目录 0700、每份 0600、位于 app support；日志仅含路径与字节数 |
| T26 | 备份各中断点（r5-M3）：收敛失败 / 收敛成功后创建失败 / 创建后写原文件前崩溃（模拟） | 全部 fail-closed 或崩溃后集合仍 ≤3；收敛后创建失败 → 保留 2 份、不恢复已删旧份（诊断可见）；原文件字节不变 |
| T27 | 多行 basic string（`"""…"""`）内含伪 `[cli]` 节头与 `use_leader = …` 行（synthetic，r4-M3） | locator 不计伪行为 canonical；真实键 absent → 安全追加不落进字符串；真实键存在 + 字符串诱饵 → 恰一 canonical、原位编辑正确 |
| T28 | 多行 literal string（`'''…'''`）同上诱饵（synthetic） | 同 T27 |
| T29 | 跨行数组（字符串元素含 `]` 与 `#`）+ 注释行含伪 `[cli]` / `use_leader =` token（synthetic） | 数组/注释状态跟踪正确；伪 token 不误判 |
| T30 | 未闭合多行字符串 / 未闭合数组（synthetic） | parser 判非法 → **F1**（矩阵"矛盾"格同样 F1），拒绝写入 |
| T31 | locator 节边界（synthetic，r8-M4）：`[cli]` 后紧跟 `[other]` / `[cli.child]` / `[[other.items]]` 三类 | 追加发生在下一节头之前、落在 `[cli]` 语义路径；其他表（含子表/数组表）完全不动 |
| T32 | config 无尾随换行（synthetic，r8-M4） | 追加时只补必要换行；写后仍是合法 TOML、无混合行尾 |
| T33 | `use_leader = "true"` / 整数 / 数组（合法 TOML、非法 Bool，r8-M7） | **F1** 拒绝管理；**不误判 absent、不追加第二语义键** |

synthetic 用例的边界见 §3.3-9：冻结实现行为，不宣称现场支持。

**Go（D-G1 与 D-G2；`agent/grokbuild` 与 `go-bridge` 测试文件，随各自提交）**：

| # | 用例 | 期望 |
| --- | --- | --- |
| G1（D-G1） | 预置 stale socket 文件（存在、无 listener）→ `SubscribeSessionEvents` | dial 失败且未转发任何事件 → **回退 file tailer**：updates.jsonl 事件照常流出；输出建立失败 INFO 日志（含原因）；不删除 socket 文件 |
| G2（D-G1） | listener 接受连接后在握手阶段断开（未转发任何事件） | 同样回退 tailer + INFO 日志（"未转发任何事件"规则涵盖 dial/register/initialize/load 全阶段与 nil-error 结束） |
| G3（D-G1） | 已转发 ≥1 事件后连接断开 | **不回退**：channel 照常关闭（F-7 由 relay 层负责，本层只关 channel） |
| G4（D-G1×D-G2 互锁） | ctx 被取消（模拟 D-G2 取消）时订阅尚未转发任何事件 | **不回退 tailer**（`context.Canceled` 不算建立失败），channel 正常关闭，不拉起无人消费的 tailer |
| G5（D-G2·r6-M1） | session 无 iOS 订阅：**直接驱动 §3.5.2 第 3 条冻结的纯 helper（`(now, hasSubscriber)` 状态机，r8-M3 seam）+ 一个短接线测试（ticker→helper→cancel/清理）**，断言一律以**订阅者实际消失时刻**起算（非采样时刻），不跑 60s 真实时间 | 取消区间恰为 **[60s, 70s)**（假时钟下的名义窗口；生产另计进程暂停/调度延迟，r7-S1）：实际消失后 <60s 不取消（含 59.9s 观察点）；首个负样本 ≥60s 后的下一 tick 内取消、全程 <70s；**连续第二/第三个负样本不移动 `firstNegativeAt` 锚点（r9-M3）**；订阅转正即清零负样本计时（与 G6 呼应）；订阅连接关闭、relay 退出、`relayRunning` 清理、日志可见取消原因（结构化字段，§3.5.2 第 8 条） |
| G6（D-G2） | 订阅在 60s 内消失又重现（快速切页抖动） | observer **不**取消、relay 不重建（无抖动） |
| G7（D-G2·r5-B1 + r6-B1 + r7-B1 + r8-B1） | **armed turn 进行中**被 D-G2 主动取消（无订阅） | **负断言：不合成、不持久化 `turn_aborted(leader_disconnect)`**——Projection Kernel / offline 路由无 aborted 终态写入；**registry 收口断言：synthetic 条目（session==nil）被 CAS 删除；real-session 条目转 unknown、句柄保留、条目未被删除；不触发 `completeBridgeTurn`**；**catalog 穿透断言（r8-B1）：预热 running 快照 → release + fence → page-0 重建输出 unknown（无徽标、不 sticky）；预热 unknown 快照 → reclaim + fence → page-0 重建输出 running；fence 后存量 cursor 的 page-N → `cursor_stale`**；只做 relay 清理 + INFO（§3.5.2 第 8 条结构化字段）；**typed outcome 断言（r9-M1）：`releasePassiveClaim` 分别返回 `Deleted`（synthetic 行）/ `Unknown`（真实行）/ `Noop`（gen 失配），日志 `registryOutcome` 与返回值一致，无二次读 registry 猜结果**；随后模拟 iOS 重开：冷拉 + 新 relay 恢复该 turn 真实状态——turn 仍在跑则 claimRunning 重建徽标、已完成则历史真值 + 无徽标；对照用例：同 armed 状态下**真 source 断开**仍走 F-7 合成（registry markIdle + claim 清零，回归保护），且**预热 running 快照 → 断开 + fence → page-0 重建输出 idle、旧 cursor stale（r9-B1）** |
| G8（D-G2·r7-B1+r8-B2 claim 所有权与消费者谓词） | ① armed turn 正常 `turn_completed` 收口（claim 清零）后 grace 到期 self-cancel（**含预热 running 快照**，r9-B1）；② claim 后模拟其他路径对同一 session 较新的 markRunning/markIdle（gen 取代）再 self-cancel；③ 同一外部 session 反复订阅/取消 N 轮；④ registry 置 unknown 后触发各自动收口路径（relayEvents idleTimer 到期 / channel close / codex hardCap / claude TTL）；⑤ **closing 回归（r8-B2）**：六处消费点在 idle/running/closing 既有域上逐一驱动 | ① **不把 idle 覆盖成 unknown**（claimGen 已清零 → defer no-op）；**预热 running 快照 → terminal + fence → page-0 重建输出 idle、旧 cursor stale（r9-B1）**；② 过期 cancel no-op（gen 失配，不覆盖较新 running/idle）；③ registry map 大小有界（synthetic 行释放即删，不随轮数增长）；④ **unknown 不触发任何自动终态**：不合成 `turn_completed`（channel-close 与 idleTimer 两处均为此事件，r8-M1 修正）、不 broadcastIdleState；⑤ **idle/running/closing 域上行为与替换前逐点一致**（closing 仍按 active 处理，`isKnownActive` 等价性回归），仅 unknown/不存在改变 |

### 7.2 UI 验证（静态 + 增量编译，一次）

- 核心三态 + 扩展观察态（#4/#5/#6）+ 失败态按 §3.4 表渲染；开关不触发行级其他手势。
- 不做 UI automation / snapshot test（owner 授权边界，且 D1/D2 级不需要）。

### 7.3 owner 手动验收矩阵（真机 + 生产 App）

诊断分级声明：**第 1–3 行为组件级（本机文件与 UI）；第 4–12 行为生产路径级**（引用生产
runtime 日志 `~/Library/Application Support/CordCode Link/logs/go-bridge.log`）。第 11/12
行依赖 D-G1/D-G2 已实施。

| # | 前提 | 动作 | 应看到 |
| --- | --- | --- | --- |
| 1 | grok 已装、config 无键 | 打开开关 | config.toml 出现 `use_leader = true`（user 层），其他内容原样；行内橙色"已开启，重启 grok 后生效" |
| 2 | 开关已开 | 关闭开关 | 键被删除（非 false；空 `[cli]` 节头保留）；备份存在（0700/0600，≤3 份） |
| 3 | grok 未安装（或 PATH 移除） | 查看行 | 开关置灰，悬浮说明"未检测到 grok CLI" |
| 4 | 开关已开 | 启动 grok TUI 并发消息 | socket 出现；iPhone 打开同一会话，日志出现**完整三行链** `leader subscriber starting` → `connected` → **`live`** 且无 `falling back` 行；TUI 内 turn 在 iPhone 实时可见 |
| 5 | 第 4 行之后 | TUI 内发长消息 | iPhone 实时流式显示（push 延迟，非 1s 轮询节奏）；turn 唯一终态 |
| 6 | 第 4 行之后 | iPhone 对自己发起的会话发消息 | 正常（macbridge 自有 `--no-leader` stdio 路径不受影响） |
| 7 | 开关开启、grok 未重启 | — | 提示保持橙色"重启后生效"，不得显示检测到 socket |
| 8 | 开关已开、TUI 内无 interaction 的 turn 进行中、**iPhone 会话保持打开** | 关闭 TUI | Phase 0 第 6a 步已实测拓扑（TUI mid-turn SIGKILL 后 leader 无头跑完 turn）；本行作为 D-G2 后回归复验：turn 在 leader 上继续完成，iPhone 看到完整终态；observer 因 iPhone 订阅在场而保留 |
| 9 | 开关关闭（或删键后）、grok 正常 inline 运行 | TUI 发消息，iPhone 打开同一会话 | 观察照旧走 tailer 路径（日志出现 `falling back to updates.jsonl file tailer`），流式正常——证明开关未破坏 OFF 基线 |
| 10 | 开关已开、TUI 内 turn 进行中、iPhone 会话打开 | `kill -9` leader 进程（模拟崩溃） | 已转发事件的 turn：iPhone 收到明确"已中断"终态（`turn_aborted(leader_disconnect)`）并回 idle，不残留执行中（F-7 生产验证） |
| 11（D-G1） | D-G1 已实现、stale socket 存在 | `kill -9` leader 留 stale socket，iPhone 重开会话 | 观察经回退**继续工作**（事件以轮询节奏恢复流动）；`go-bridge.log` 出现建立失败回退 INFO 行（文案以 G1 冻结为准）；不再持续阻断 |
| 12（D-G2） | D-G2 已实现、某会话 observer 在场（最好有 turn 进行中） | iPhone 关闭该会话并等待 > 70s | `go-bridge.log` 可见 observer 取消与 relay 退出（无订阅，**主动取消非崩溃语义**）；若有 turn 进行中：**不得出现虚假"已中断"终态**（r5-B1 负断言），重开后按真实历史/后续事件呈现；**侧栏该会话不得继续显示"运行中"——取消后无状态徽标（unknown，r6-B1；registry 变化经 `FenceBackend` 穿透快照后由下一次 list 反映，非等 10 分钟 TTL，r8-B1），重开后徽标按真实状态重建**；leader 若无其他客户端则退出；随后 iPhone 重开该会话，观察恢复（socket 在则 push、否则 tailer + 冷拉补齐） |

### 7.4 构建纪律（按 CLAUDE.md 成本分级，r3-M1/r4 修正）

| 子任务 | 级别 | 验证 |
| --- | --- | --- |
| TOML 依赖接入 | **D4** | §3.6 的五项 D4 门（重生成一致性 / locked resolution / 双 target 编译 / Release package / license 审计 + source identity 留档）；不默认全量测试、无 UI automation |
| D-G1 | **D3** | G1–G4 定向 Go 测试 + `go build ./go-bridge`；独立提交 |
| D-G2 | **D3** | G5–G8 定向 Go 测试 + `go build ./go-bridge`；独立提交（与 D-G1 分开） |
| manager + 单测 | **D2** | T1–T33 定向单测 + 一次增量编译 |
| UI | **D1/D2** | 静态核对 + 一次增量编译 |
| Release 覆盖安装 | 交付前集中一次 | `killall CordCodeLink` → 覆盖 → `open`，核对 8777 监听者为 `/Applications` 内嵌 runtime；§7.3 矩阵执行 |

禁止 clean、禁止临时构建产物直接启动、禁止为依赖接入以外的理由重装依赖。

---

## 8. 实施拆分

### Phase 0：宿主拓扑证明门（任何产品代码之前；§15 已签署）

1. 手工执行 §0.2 第 1–10 步并留存独立日志文件（每启动一个 `$(mktemp)`）/ `go-bridge.log`
   与进程/socket 证据；任一步失败即停线，回写本文 §2 后重新裁决。该门不可被"开关 UI 已
   完成"或"单元测试通过"替代。

### Phase 1：TOML 依赖接入（D4 门）+ GrokLeaderModeManager + 单测

1. §3.6 接线与五项 D4 验证门 + source identity 留档；
2. 新建 manager（§3.2/§3.3 全部十条规则 + §3.3-3 交叉裁决矩阵 + §3.3-4 symlink 身份
   模型）；T1–T33 单测；
3. 一次增量编译通过。

### Phase 2：Go 受控改动（两个独立提交）

1. **2a D-G1**（§3.5.1）：`grokbuild.go` 首事件分界 + 建立失败回退 + INFO 日志；G1–G4；
2. **2b D-G2**（§3.5.2）：`handlers_relay.go` cancellable ctx + 订阅检查 ticker +
   首个负样本锚点的 [60s,70s) 取消上界 + 主动取消/真断开分流（不合成 turn_aborted）+
   `types.go` registry unknown 态/generation claim 释放（r6-B1+r7-B1）+ 六处 `!isIdle`→
   `isKnownActive` 等价替换 + Grok 状态变化后 `FenceBackend` 穿透（r8-B1）；G5–G8（附 registry 定向测试）；
3. 各自 `go build ./go-bridge` + 定向测试；互锁行为（G4）在 2b 后复跑。

### Phase 3：行内开关 UI（D1/D2）

1. `agentRow` grokbuild 分支：Toggle + 核心三态副文案 + 失败态禁用 + 失败回弹 alert；
2. L10n 新键（`grokLeaderMode` / `grokLeaderModePendingRestart` / `grokLeaderModeSocketDetected`
   / `grokLeaderModeDisabledHelp` / 失败态文案等）；
3. 一次增量编译 + 静态核对。

### Phase 4：状态可见性收尾 + owner 验收

1. DiagnosticsSheet grok 组 Leader 状态行（**必做**：user 层配置值区分 absent /
   explicit false / true、socket 路径与存在性、安装版本）；扩展观察态 #4/#5/#6 副文案；
2. §6-6（interaction 等待）与 §6-4（observer 生命周期）实测结论回写本文；帮助文案含
   D-3 批准的 interaction 提示；
3. Release 覆盖安装（一次）+ §7.3 owner 矩阵执行；
4. 完成情况文档 + CHANGELOG 条目（对齐既有格式："新功能：Grok Build 行新增 Leader 模式开关…"）。

---

## 9. 非目标与后续

- **不 spawn / 托管 leader 进程**：`grok agent leader --no-exit-on-disconnect` 是否常驻由
  用户自行决定；帮助文案可提示。MacBridge 保持只读共存纪律。
- **不做 follower 交互升级**（iOS 停外部 turn / 答外部权限）：B 路线的后续另案，需先按
  source-first 纪律冻结 leader 协议的 request 方向真实样本（interjection / cancel /
  permission response 的 follower 可用性），并处理 writer 仲裁；本文不预支其结论
  （§15 D-3 维持另案）。
  - **为什么另案而非并入本文（2026-09-02 Owner 问询补记，四条流程红线）**：
    1. **证据未冻结（source-first 红线）**：本文十轮评审通过的根基是**观察方向**
       （leader → 订阅者推送）的源码证据已逐条 pin 死；follower 要走的 **request
       方向**（follower 能不能发 interjection / cancel / 答权限、发什么形状、writer
       仲裁怎么定）在真实样本里未证明。把未证明的协议假设塞进已 APPROVE 的文档，
       等于让评审结论作废重来，总进度反而变慢。
    2. **读写风险不对称，且拆分是 Owner 已签裁决**：只读 observer 最坏是"看漏了"；
       写路径最坏是打断 / 答错用户真实工作里的 turn。§15 D-3（2026-08-28）明确签了
       "保持只读边界，follower 应答另案"——合并等于推翻已签署的裁决门。
    3. **交付节奏**：观察侧改进（实时流、不掐长任务、徽标准确）独立有价值、低风险，
       可先落地先用上；follower 是高风险写路径开发，捆在一起会让前者陪跑等后者。
    4. **Phase 0 的产出正好是 follower 设计的输入**：十步拓扑证明会拿到 leader 实际
       交互广播的真实形状，直接喂给 follower 另案——顺序不浪费。
    另案跟踪：Mac 仓 `think.md` 顶部「后续计划索引」follower 行（2026-09-02 建）。
- **不消费 `x.ai/sessions/changed` roster 通知**（§4.1）：官方 leader 已广播、官方 Pager
  已消费，macbridge 消费需 go-bridge 改动——列为未来改进另案（D-G1/D-G2 均不触碰
  `leader_subscriber.go` 的方法过滤器）。
- **Go 改动严格限于 D-G1 + D-G2**：Link 退出前 interaction 提醒、live 中断热切、stale
  socket 主动清除、周期重试等均不做（§3.5 范围限定）；发现新 runtime 需求另案处理。
- 不动 iOS、protocol pack、SSV2、`RequiresExternalTurnPolling` 声明（外部 turn streaming
  capability 的广告调整属于 follower 升级案）。
- 不做 code.grok.com、workspace hub、`agent serve` 任何接入（§0 已裁决）。
- TOML 等价形态（点键 / quoted key / inline table）不做安全改写，fail-visible（§3.3-3/6）。
- 文件 tailer 路径**必须保留**：leader 不存在时它是唯一观察路径；D-G1 也只是回到它。

---

## 10. 权威证据入口

- 官方 leader 架构与生命周期：`/Users/jacklee/Projects/grok-build/crates/codegen/xai-grok-shell/src/leader/`
  （`mod.rs` 架构图、`protocol.rs:158-177` 能力枚举、`server.rs:1620-1761` client 计数与
  断开/退出、`server.rs:390-428` machine-wide 广播、`server.rs:491-500` interaction 共享
  广播 first-answer-wins、`server.rs:2348-2351` 正常收尾删 socket、`lock.rs` flock 选举）
- 官方配置分层与解析链：`xai-grok-config/src/config_layers.rs:20-75,177-197`（层定义与
  merge 方向：user 覆盖 managed；requirements/MDM 覆盖 user；env overlay allowlist 排除
  `cli`，`:44-48`；无 project slot）、`xai-grok-shell/src/config/tests.rs:3040-3043`、
  `xai-grok-pager/src/app/mod.rs:394-437`（precedence 注释 + `requested_confinement`
  最终 veto + TUI 走 global effective config 的解析链）
- 官方 leader 解析日志（effective 值证据）：`xai-grok-pager/src/app/mod.rs:680-689`
  （INFO `pager TUI leader mode resolved`：`use_leader` / `policy_disable_reason` /
  `sandbox_profile` / `leader_disabled_by_sandbox`）；日志通道
  `xai-grok-telemetry/src/debug_log.rs:8,81-82,365,414-421`（`GROK_LOG_FILE` 单文件、
  RUST_LOG 过滤、默认 DEBUG）与 `appender.rs:13-25`（**append 模式**）；`--debug-file`
  flag → `GROK_DEBUG_LOG`：`pager-bin/src/main.rs:2010-2015`
- `grok inspect` 的边界（只列层来源，不输出 effective 值）：`xai-grok-shell/src/inspect/mod.rs:55-82,265-290,1067-1175`
- requirements 层（自由 TOML、可覆盖 `[cli]`、负例文件）：`xai-grok-config/src/validation.rs:68,78-79,117-164`
- 官方配置写入串行化：`xai-grok-shell/src/util/config/persist.rs:11,103,219`（进程级 SAVE_LOCK）
- 官方 user config 可含凭据：`xai-grok-pager/docs/user-guide/05-configuration.md:275,282-287`
- `--leader-socket` flag → env 转换：`pager-bin/src/main.rs:2007-2009`
- 官方 flag / config：`xai-grok-pager/src/app/cli.rs:290-294,433`（leader/no_leader 定义
  与 leader_socket 字段；`--no-leader` 引用点 :1187、`--leader-socket` :1265）、
  `xai-grok-shell/src/util/config/mcp.rs:1841-1861,2177-2265`
- 官方服务器推荐：`xai-grok-config-types/src/lib.rs:448-450`
- confinement veto 判定：`xai-grok-sandbox/src/lib.rs:113-133`（requested_confinement_profile）
- macbridge 观察路径与只读纪律：`agent/grokbuild/grokbuild.go:143-198`（`forward` 闭包
  `:163-172` = D-G1 首事件锚点）、`agent/grokbuild/leader_subscriber.go:3-12,63-79,319-334`
- macbridge relay 生命周期：`go-bridge/handlers_relay.go:154-186`（`grok-leader:<id>` key
  与 relayRunning 门）、`:206-250`（grok observer 无 subscriber 取消路径，D-G2 改动点）、
  `:225-243`（F-7 defer）、`:365-374`（codex `HasSessionSubscriber` 先例，D-G2 参照）
- D-G2 取消分流的事实依据（r5-B1）：`go-bridge/handlers.go:3035,3063`（sendSessionEvent →
  统一 publishEvent，`Offline: IsDurableMilestone`）、`go-bridge/event_publisher.go:1059-1072`
  （timeline event 先进 Projection Kernel 再解析目标连接）、
  `go-bridge/projection_reducer.go:1326-1328`（turn_aborted 持久化为 aborted 终态）
- D-G2 registry 收口的事实依据（r6-B1，逐项独立核实）：`go-bridge/types.go:226-230,243-377`
  （registry 实为 idle/running/closing 三态；条目可携带真实 AgentSession，直接删除会丢句柄）、
  `go-bridge/handlers_relay.go:266-280,290-307`（relay markRunning 与正常终态/defer
  markIdle 调用点）、`go-bridge/handlers_grok_catalog.go:75-81`（grok catalog 富化链）、
  `go-bridge/handlers_opencode.go:220-235`（getRunningMap 对非 RunningSessionLister 返回
  nil——grokbuild 未实现该接口）、`:238-297`（enrichSessionStatesForList never mutates
  registry；applyListRuntimeState 的 F-8"不知道就不亮灯"：无记录 → `runtimeState=
  "unknown"`，客户端不渲染徽标）、`go-bridge/handlers.go:289-298`（onStateChange 消费方
  对 unknown 态安全：仅无条件失效 Claude running map，newState=idle 才 completeBridgeTurn）
- D-G2 claim 所有权与 unknown 态消费者的事实依据（r7-B1；r8-M1/B2 修正后）：
  `go-bridge/types.go:226-230`（registry 实为 idle/running/**closing** 三态——closing
  无生产写点但在语义域内）、`types.go:373-377`（isIdle = !ok || state==idle →
  unknown/closing 会被判 "active"）、`go-bridge/types.go:321-335`（markRunning 对无条目
  session 创建 session=nil 的 passive synthetic row）、`go-bridge/handlers.go:929`
  （cleanupIdleSessions 只清 state==idle → unknown 条目永不回收）、
  `go-bridge/handlers_relay.go:2687`（agentRelayGen 所有权 token/CAS 先例；relayEvents
  定义 :2679）、
  `!isIdle` 全部六处消费点 `handlers_relay.go:324,449,457,905,2728,2864`
  （relayEvents 两处均合成 durable `turn_completed`——channel-close 带
  reason=events_channel_closed；v8 曾误写为 aborted，r8-M1 修正）
- D-G2 catalog 快照穿透的事实依据（r8-B1，逐项独立核实）：
  `go-bridge/handlers_grok_catalog.go:75-81`（富化发生在 builder 内，registry 状态被
  写进 wire map）、`go-bridge/catalog_wire_snapshot.go:232-318`（FetchOrReuse 缓存已
  富化 wire maps，page-0 不重新 overlay registry）、`:202`（**FenceBackend**：
  同锁推进 backend 级 generation、删除已提交 scope、完成并丢弃 in-flight 构建）、
  `go-bridge/catalog_cursor_v2.go:31`（快照 TTL = 10 分钟）、
  `go-bridge/catalog_native_membership.go:78-99`（listSemanticFingerprint 只含
  id/updatedAt/title/dir/project，runtime overlay 被排除在语义指纹外）、
  `go-bridge/handlers.go:289-298`（onStateChange 只失效 Claude running map，不失效
  Grok catalog wire cache）
- macbridge UI 槽位先例：`MacBridge/MacBridge/Views/WorkspaceView.swift:496,506-513,528-545`
- TOML 依赖：https://github.com/mattt/swift-toml（tag 2.0.0 = commit `827506c90475e82d5a7f191f950fb3025cbdc0d6`；
  r4 已自 tag 核实 Package.swift/license/toml.hpp v3.4.0）
- 历史 CLI 兼容证据：[2026-07-12-grok-cli-compatibility-evidence.md](2026-07-12-grok-cli-compatibility-evidence.md)、
  [2026-07-12-grok-driver-design.md](2026-07-12-grok-driver-design.md)
- 路线裁决背景（四路径对比）：2026-08-28 跨仓源码评审（本文 §0 摘要）
- 评审记录：[一轮](2026-08-28-grokbuild-leader-mode-design-review.md)（§12）、
  [二轮](2026-08-28-grokbuild-leader-mode-design-review-r2.md)（§13）、
  [三轮](2026-08-28-grokbuild-leader-mode-design-review-r3.md)（§14）、
  [四轮](2026-08-28-grokbuild-leader-mode-design-review-r4.md)（§16）

---

## 11. 防偏航清单

实施 agent 接手时先逐条回答；任一条不符即停止并回写本文：

| 问题 | 合格证据 | 不合格替代品 |
| --- | --- | --- |
| Phase 0 拓扑证明是否先于产品代码完成？ | §0.2 十步 + 独立日志文件 / `go-bridge.log` 证据留存 | Toggle 演示可用 / 单元测试通过 |
| 开关状态、socket 检测、订阅生效三级是否分离呈现？ | 状态 #2 / #3 措辞 + 日志三行链判据 | 把"配置已写"或"socket 文件存在"显示为"已生效" |
| go-bridge diff 是否严格限于 D-G1（`grokbuild.go`）+ D-G2（`handlers_relay.go` + `types.go` unknown 态）+ 各自测试？ | git diff 逐文件核对；两个独立提交 | 顺手改 runtime、tailer、协议实现、能力声明或 catalog 代码；D-G1/D-G2 混写一个提交 |
| D-G1 分界是否为"未转发任何事件 + 非 ctx 取消"？ | G1–G4 断言（含互锁 G4） | 以 `connected`/`live` 日志时点当分界（`grokbuild.go` 拿不到）；error 才回退而 nil 结束不回退 |
| D-G2 上界是否为 [60s,70s)（名义窗口）且从实际消失起算（首个负样本锚点），主动取消不合成 turn_aborted，registry claim 为 generation token/CAS（synthetic 行可回收、unknown 不进 running-only 分支）？ | G5–G8 断言（含 r5-B1 负断言、r6-B1 catalog 断言、r7-B1 所有权/回收/消费者断言与真断开 F-7 对照）；假时钟注入 | 无上限等待；last-positive 锚点；局部 bool 冒充所有权；主动取消复用 F-7 defer；取消后 sticky running；unknown 条目永久积累或被 `!isIdle` 当 active；或把取消逻辑写进 D-G1 的提交 |
| 依赖是否与 §3.6 冻结一致？ | project.yml packages + 重生成 pbxproj + Package.resolved 锁定 2.0.0/827506c + source identity 留档 | 引入未冻结的其他 TOML 库或浮动版本 |
| 是否把既有 grokbuild 资产误当本设计交付物？ | §0.4 资产清单为既定基线 | 评审要求"补 API 客户端/codec/catalog 实现"，或实施时重新实现它们 |
| 关闭是否删键而非写 false？ | T4/T5/T16 断言 | 写 `use_leader = false`（屏蔽服务器推荐） |
| config.toml 是否零重排？ | T2/T12–T14 逐字 diff（注释/顺序/缩进/行尾不变） | parse→serialize 往返 |
| 交叉裁决矩阵是否按 §3.3-3 实现？ | T15/T19/T27–T30 断言（F1/F2 分支与多行诱饵） | 自称 parser 能识别原始写法；或正则单独判等价键 |
| 并发保护是否按 best-effort 如实实现与表述？ | T7 + 备份 + §5 措辞 | 宣称"绝不覆盖/原子 CAS"；或直接覆盖不检测 |
| 备份是否按 §3.3-10 安全规则？ | T25/T26（0700/0600、fail-closed、≤3 份硬不变量、不记录内容） | 备份进 grok home / 权限默认 / 日志打印内容 / 轮转失败仍保留第 4 份 |
| 是否保持了只读共存纪律？ | leader 路径日志只有 subscribe/observe | spawn leader、抢 flock、向 leader 发驱动请求 |
| 文件 tailer 是否保留？ | leader.sock 删除后观察仍工作（回归）；D-G1 只是回到它 | 删除或绕过 fallback 路径 |
| 生产路径验证是否有完整日志证据链？ | §7.3 第 4–12 行引用 starting → connected → live | 只看 UI 开关变绿 / 只看 connected 一行 |
| grok 版本漂移时是否 fail visible？ | 橙色提示持续 + DiagnosticsSheet 版本行 | 加 fallback 伪装生效 |

强制停线信号：owner 报告"开关已开但 iPhone 观察仍是轮询节奏"→ 先核对 leader.sock 与
`go-bridge.log` 路径选择日志（§2.2-4），再核对用户 grok 是否真的以 leader 模式启动
（`GROK_LOG_FILE` 独立日志的 `pager TUI leader mode resolved` 字段；启动参数是否带
`--no-leader`；是否被 requirements/MDM 覆盖或 confinement veto，§6-5 四因）；禁止在未
证明运行层之前改 runtime 逻辑。

修复纪律（对照 codex-web §3.4，开关类 bug 固定排查顺序）：

1. 先证运行层：socket 文件是否存在、`go-bridge.log` 走的是 leader 还是 fallback 行、
   用户 grok 进程是否以 leader 模式启动（`GROK_LOG_FILE` 日志字段 / user 层 config 实际
   内容 / 四因逐项排除）；
2. 再查写入器：config.toml 实际内容、备份、写入返回；
3. 最后查 UI 绑定与状态刷新时机。

一次定向修复未改变现象，必须停止并在同一假设上停止叠加补丁，回到第 1 步重新定位；
"修了没效果"优先怀疑"没跑到/没生效"，而不是"修不好"。

---

## 12. 一轮评审采纳记录（2026-08-28）

> **规范性边界（r8-S1）**：实施只以 §0–§11 当前正文为规范；§12–§20 仅供审计追溯，
> 其中含已被后续轮次废弃的方案（多数附 vN 注指向后续修正），**不得作为实现要求**，
> 也不得从历史表格拾取旧实现。

对应评审报告：[2026-08-28-grokbuild-leader-mode-design-review.md](2026-08-28-grokbuild-leader-mode-design-review.md)。
四项 B 级断言均经设计方在 pin 提交上独立复核确认后采纳。

| 评审项 | 处理 | 设计修订 / 未采纳理由 |
| --- | --- | --- |
| B1 配置覆盖模型错误 | 全部采纳 | 撤销"项目级 config 覆盖该键"主张（MCP 专用 walk）；补全 ConfigLayers 层模型。**v3 注**：v2 对 merge 方向的转述仍有误（managed 覆盖 user），r2 已再修正，见 §13 R2-B1 |
| B2 observer 续命已可裁决 | 全部采纳 | 写入源码裁决并接受续命为产品边界。**v3 注**：v2 把续命窗口写成"到 iOS 关闭会话"有误，r2 已再修正为"到 Link runtime 退出或 leader 崩溃"，见 §13 R2-B2。**v5 注**：r4/Owner 已否决该现状，改为 D-G2（§3.5.2） |
| B3 stat 不足证活 | 全部采纳 | 措辞降级"检测到 socket"；socket 路径同链解析；stale socket 入边界。**v3 注**：v2 的"下次 session-open 恢复"说法有误，r2 已再修正为"持续阻断"，见 §13 R2-B3。**v5 注**：stale 阻断已由 D-G1 消解（§3.5.1） |
| B4 roster 通知归因错误 | 全部采纳 | §4.1 重写归因；消费动作列入 §9 未来改进另案 |
| M1 TOML 并发/回滚不闭合 | 部分采纳 | 内容身份比较 + 冲突即失败 + 受限回滚 + 空节头保留；未采纳锁文件方案。**后续**：绝对化表述由 r2 降级为 best-effort（§13 R2-B4）、备份安全由 r3/r4 补全（§14 R3-M2、§16 R4-M4） |
| M2 TOML 语法与测试不足 | 部分采纳 | 等价形态 fail-visible + 边界用例。**后续**：解析机制/symlink/样本分级/multiline 由 r2/r3/r4 补齐（§13 R2-M3、§14 R3-B1/M3/M5、§16 R4-M3） |
| M3 状态机不闭合 | 部分采纳 | 失败态 F1–F3、扩展观察态、术语分离；未采纳 Swift 拨测（理由 r2 已修正措辞，见 §13 R2-S1） |
| M4 Phase 0 不完整 | 全部采纳 | 扩为十步。**后续**：effective 证据与负例由 r3 重写（§14 R3-B2）、文件保护与日志隔离由 r4 补（§16 R4-M1/M2） |
| M5 行号漂移 | 全部采纳 | 全文行号修正 |
| M6 验收分级与证据链 | 全部采纳 | 生产路径级统一、三行日志链、DiagnosticsSheet 必做 |
| M7 内部引用与矛盾 | 全部采纳 | 引用修复、矛盾消除（后续轮新增错误见对应章节注记） |
| S1 区分启动与重启 | 全部采纳 | 状态 #2 文案区分 |
| S2 统一态术语 | 全部采纳 | 核心三态 / 扩展观察态 / 失败态 |

---

## 13. 二轮评审采纳记录（2026-08-28）

对应评审报告：[2026-08-28-grokbuild-leader-mode-design-review-r2.md](2026-08-28-grokbuild-leader-mode-design-review-r2.md)
（总结论：退回）。四项 R2-B 断言均经设计方在 pin 提交上独立复核确认后采纳；r3 已确认
四项事实阻断闭合。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R2-B1 merge 方向与 confinement 位置错误 | 全部采纳 | §2.1-1/2、§6-5 重写：merge 顺序 `system_managed → managed → user → env_overlay → user_requirements → system_requirements → mdm_requirements`，**user 覆盖 managed**，requirements/MDM 可在 user 后覆盖 `[cli]`，env overlay allowlist 排除 `cli`；confinement 是整条 policy chain 之后的**最终 veto**，与 eligibility 分开表述 |
| R2-B2 observer 生命周期与 turn 继续写错 | 全部采纳 | §2.1-3/§4.9 重写：observer 无 subscriber 取消路径（对照 codex `HasSessionSubscriber`），**续命到 Link runtime 退出或 leader 崩溃**；"TUI 关闭后 turn 继续"降级为待证假设（Phase 0 第 6a 步）；新增 interaction 无人应答等待风险（§6-6，第 6b 步）；driver transfer 归因修正为 driver-only 消息（§4.9）；回退验证改为"退出 Link"（Phase 0 第 9 步、§7.3 第 8 行）。**v5 注**：现状已被 Owner 否决，目标语义 = D-G2（§3.5.2），Phase 0 第 6/9 步保留为实施前基线 |
| R2-B3 stale socket 不会自动恢复 | 全部采纳 | §2.2-1/§4.9/§6-7 重写：stale 存续期间**每次 session-open 重复失败**，恢复条件 = 新 leader 接管或人工清除；Phase 0 第 8 步要求验证第二次打开仍失败；撤销"与 leader 真死同边界"表述。**v4 注**：r3 指出该代价与 fail-visible 红线冲突；**v5 注**：D-1 已批 D-G1，建立失败回退 tailer（§3.5.1） |
| R2-B4 内容比较仍有 TOCTOU | 全部采纳 | §3.3-7/§5/§11 措辞降级为 **best-effort**（compare→rename 残余窗口，非原子 CAS）；修正"官方不加锁"的错误表述（官方有进程级 SAVE_LOCK，§2.1-7）；保留备份 + 受限回滚作为缓解 |
| R2-M1 Phase 0 路径与 effective 核查不可执行 | 部分采纳（当时） | 改为写解析后的 `$GROK_HOME/config.toml` + `grok inspect` 证据入口。**v4 注**：r3 证实 `grok inspect` 不输出 effective 值，证据已替换为 `GROK_LOG_FILE` 日志字段（§14 R3-B2）；**v5 注**：append 语义下的日志隔离由 r4 补（§16 R4-M2） |
| R2-M2 T11 未裁决 | 全部采纳 | 裁决：目录不存在时创建 `$GROK_HOME`（0700）+ 原子创建 config（0644）（§3.3-5/9、T11） |
| R2-M3 缺解析机制与 symlink | 方向采纳（当时） | v3 提出"受控 TOML 依赖"方向。**v4 注**：r3 指出未冻结且职责矛盾，已按 §14 R3-B1/M3 落实（§3.3-3/4、§3.6） |
| R2-M4 absent 与 explicit false 混同 | 全部采纳 | DiagnosticsSheet 区分 absent / explicit false / true；扩展观察态 #5 显式 false 提示；§3.4 说明 OFF 删键的 UI 可达路径是 ON→OFF，T16 补注 |
| R2-M5 F-7 落点错位 | 全部采纳 | F-7 验证改到 Phase 0 第 8 步（crash/kill 场景）+ 新增 §7.3 第 10 行生产验证；§6.1 表同步修正。**v5 注**：分界精确化为"已转发事件后断开"（§16 R4-B1） |
| R2-M6 编号与来源清单新错误 | 全部采纳 | "第 0 步"→第 1 步；"七条规则"→十条（§0.4/§3.3 对齐）；来源清单逐项列出未跟踪路径；"唯一拦路条件"→"默认用户的首要前置条件" |
| R2-S1 不拨测理由不准确 | 全部采纳 | §3.4 理由改为：多客户端架构下拨测会新增扰动 client 计数/生命周期的连接并复制协议握手职责（非"竞争单活连接"） |

**§13.1（v3 的零 Go 重新裁决）由 §15 Owner 裁决门承接**：v3 的"维持零 go-bridge 改动"
是基于当时认知的设计方单方裁决；r3/r4 要求 owner 基于真实代价逐项独立签署，v4 起以
§15 为准（v5 已签署）。

---

## 14. 三轮评审采纳记录（2026-08-28）

对应评审报告：[2026-08-28-grokbuild-leader-mode-design-review-r3.md](2026-08-28-grokbuild-leader-mode-design-review-r3.md)
（总结论：退回）。R3-B1/B2 的替代证据与 R3-B1 的依赖选型均经设计方在 pin 源码与上游
仓库独立核实后采纳；r4 已确认全部实质闭合。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R3-B1 TOML 依赖未冻结、职责矛盾、接线缺失 | 全部采纳 | §3.3-3 拆分为"语义 parser + 仓内 canonical locator"并给出四格交叉裁决矩阵；§3.6 冻结 `mattt/swift-toml` **2.0.0 exact**；接线含 project.yml packages + target dependency + 重生成 pbxproj + Package.resolved；§11 diff 范围同步更新 |
| R3-B2 `grok inspect` 不能证明 effective 值 | 全部采纳 | Phase 0 第 2 步重写：effective 证据 = `GROK_LOG_FILE` 日志的 `pager TUI leader mode resolved` 字段；`grok inspect --json` 降级为层参与证据；补 requirements 与 confinement 两条可执行负例 |
| R3-B3 零 Go 缺 owner 逐项裁决；stale 阻断违反 fail-visible | 全部采纳 | §15 Owner 裁决门；设计方推荐 D-1 批准 D-G1。**v5 注**：Owner 已签署（D-1 accept D-G1、D-2 reject 现状 + D-G2、D-3/D-4 accept），见 §15/§16 |
| R3-M1 依赖属 D4 非 D2 | 全部采纳 | 头部/§7.4/§8 定级：依赖接入 D4、D-G1/D-G2 D3、manager D2、UI D1/D2 |
| R3-M2 完整备份复制凭据 | 全部采纳 | §3.3-10/§5：0700/0600、不打印内容、创建失败 fail-closed、不做 redaction；T25/T26。**v5 注**：轮转语义由 r4 收紧（§16 R4-M4） |
| R3-M3 symlink 身份规则不足 | 全部采纳 | §3.3-4 补全 + T21–T24 |
| R3-M4 遗漏 `--no-leader` 归因 | 全部采纳 | 不生效归因四因（§2.1-2/§6-5/§11）；Phase 0 第 1 步核对启动命令行 |
| R3-M5 样本门未满足 | 全部采纳 | §3.3-9 样本 manifest 三级分级；synthetic 用例只冻结实现行为。**v5 注**：multiline 诱饵入 manifest（§16 R4-M3） |
| R3-M6 行号漂移 | 全部采纳 | §4.9 `grokbuild.go:176-199` → `:174-198` |
| R3-S1 送审行数过时 | 采纳 | 头部不写死行数 |
| R3-S2 owner 决策独立可签署 | 采纳 | §15 四行独立 `Owner decision` |

---

## 15. Owner 裁决门（已于 2026-08-28 签署）

四项零-Go 代价逐项独立裁决（r3 要求；不接受打包式默认）。签署来源：owner 于 2026-08-28
对话明示 + 四轮评审报告 §4 记录。

| # | 代价（真实语义） | 设计方推荐 | Owner decision（2026-08-28） |
| --- | --- | --- | --- |
| D-1 | stale socket：leader 异常崩溃残留 socket 后，零改动下每次 session-open 重复失败、观察持续阻断，UI 只能显示"检测到 socket"、失败仅 debug 日志（§6-7） | 批准 D-G1 | **accept D-G1**（接受 Go 侧建立期失败回退 tailer；按 r4-B1 明确分界 = "未转发任何事件 + 非 ctx 取消"；不清 socket、不热切、不周期重试） |
| D-2 | observer 常驻续命：iOS 关闭会话不取消 observer，leader 最长续命到 Link 退出；且 per-session 连接/goroutine/subscription **无界累积**（r4-B2 补充事实） | 接受现状 | **reject 现状；替代 = Go 侧 subscriber-aware cancellation**（D-G2，§3.5.2：无订阅 > 有界 grace 即取消，避免无界累积与长期续命；独立测试、独立判据，不与 D-G1 混写） |
| D-3 | interaction 无人应答：TUI 关闭后 pending permission/question 可能使 turn 长期等待（§6-6） | 接受 + 帮助文案 | **accept + 帮助文案**（保持只读边界；文案明确 pending 权限/提问时不要关闭唯一可应答客户端；follower 应答仍另案） |
| D-4 | config 并发仅 best-effort：compare→rename 残余窗口，极端时覆盖官方同期写入（§3.3-7） | 接受 | **accept**（内容身份比较 + 0600 备份 + 受限回滚；保留残余 TOCTOU 诚实披露，不引入不互认的锁） |

签署效力：四项 Owner 产品裁决均已完成；**Phase 0 开工门以 §0.2 为唯一权威，本节不
单独授权开工（r8-M8）**。D-2 的 reject 产生 D-G2 需求并纳入本设计
（§3.5.2、G5–G8、§7.3 第 12 行、§7.4/§8 定级）；这不是 follower 可写方向。

---

## 16. 四轮评审采纳记录（2026-08-28）

对应评审报告：[2026-08-28-grokbuild-leader-mode-design-review-r4.md](2026-08-28-grokbuild-leader-mode-design-review-r4.md)
（总结论：**修改后通过**）。五项必改全部采纳；B1/B2 的源码事实（`grokbuild.go` 拿不到
session/load 时点、relay per-session key 与 `context.Background()`、`codexSessionHasSubscriber`
先例、`GROK_LOG_FILE` append 模式）均经设计方在 pin 提交独立复核确认。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R4-B1 D-G1 无法区分建立失败/live 断开；G3 分界与正文不一致 | 全部采纳（方案一） | §3.5.1 分界统一定义为**"是否已向下游转发任何 leader event"**：`grokbuild.go:163-172` 的 `forward` 闭包加首事件标记（diff 仍限 `grokbuild.go`）；回退条件三要素 = 订阅结束（error/nil 一致）+ 未转发任何事件 + 非 ctx 取消；G1–G4 与正文同界；"live 但无事件即断开按建立失败处理"显式声明（安全：updates.jsonl 可补齐） |
| R4-B2 Owner D-2 reject：per-session observer 无界累积 | 全部采纳 | 新增 §3.5.2 D-G2（subscriber-aware cancellation）：`handlers_relay.go` 改 cancellable ctx + 10s ticker 查询 `HasSessionSubscriber`（复用 codex 先例 `:365-374`）+ **60s 有界 grace** → 取消该 session observer；行为语义、与 D-G1 的 ctx 互锁（G4）、G5–G7、§7.3 第 12 行、§7.4/§8 定级（D3 独立提交）全部纳入；§2.1-3/§2.2-1/§4.3/§4.9/§6-4/§6.1 按"D-G2 目标语义 + 现状基线标注"重写。**v6 注**：r5 发现 v5 的取消语义仍有阻断缺陷（defer 误判 + 计时不闭合），已再修正（§17 R5-B1/M1） |
| R4-M1 requirements 负例可能覆盖/删除用户策略文件 | 全部采纳 | §0.2 第 2c 步重写：首选**独立临时 GROK_HOME** 执行层负例（完整命令块）；回退路径必须 `lstat` + 逐字节备份 + 内容身份比较 + 原子恢复（恢复失败停线）；任何情况下不得覆盖或无条件删除既有文件 |
| R4-M2 `GROK_LOG_FILE` append 导致旧 effective 行误读 | 全部采纳 | §0.2 日志规则：每次启动独立 `$(mktemp)` 文件；复用时必须 PID/时间窗取行；§2.1-8 补 append 事实（`appender.rs:13-25`）；Phase 0 留存要求同步（§8） |
| R4-M3 locator 多行结构无测试 | 全部采纳 | §3.3-3 补 T27–T30：三引号 basic/literal string 内伪节头/伪赋值、跨行数组（元素含 `]`/`#`）、注释伪 token、未闭合结构 → F1/F2；入 §3.3-9 manifest（synthetic，验证仓内 locator 保守行为） |
| R4-M4 "轮转失败无害"与凭据留存/三份承诺冲突 | 全部采纳 | §3.3-10/§5 改为：**先完成轮转，失败则删除本轮新备份并 fail-closed**；"最多 3 份"升级为硬不变量；T10/T26 断言更新。**v6 注**：r5-M3 发现该顺序（先创建第 4 份再轮转）仍非 crash-safe，v6 已改为"先收敛旧集合 ≤2 再创建"（§17 R5-M3，勿按本行旧顺序实施） |
| R4-M1/M2 补充（r5） | — | v5 的 §0.2 命令把 `mktemp` 内联写入 env、路径不可取回，且 real-home 新建文件删除前缺身份比较；v6 已修正（§17 R5-M2） |
| R4-S1 依赖审计留 source identity | 采纳 | §3.6 增加完成报告留档要求：tag commit、`Package.swift` 哈希、主 LICENSE、内嵌 `toml.hpp` SPDX/版本（v3.4.0，r4 自 tag 核实） |

---

## 17. 五轮复审采纳记录（2026-08-29）

对应评审报告：[2026-08-29-grokbuild-leader-mode-design-review-r5.md](2026-08-29-grokbuild-leader-mode-design-review-r5.md)
（总结论：**修改后通过**；定向复核范围 = R4 五项必改 + §15 回填）。四项必改全部采纳；
R5-B1 的持久化链路经设计方在 pin 源码独立复核确认（`handlers.go:2923-2935` 的
`Offline: IsDurableMilestone`、`event_publisher.go:782-831` 的"先进 Projection Kernel
再解析目标连接"、`projection_reducer.go:1222-1245` 的 aborted 终态持久化）。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R5-B1 D-G2 主动取消被现有 defer 误判为 leader 崩溃，持久化虚假 durable turn_aborted | 全部采纳 | 撤销 v5"空订阅者集合广播无害"的错误表述；§3.5.2 新增"**主动取消与 source 断开分流**"：`handlers_relay.go` 内取消原因标志（`selfCancelled`），主动取消 defer **不合成** `turn_aborted`（只清理 + INFO），session 真值由重开冷拉 + 新 relay 重建；真 source 断开保留 F-7；G7 重写为 armed-turn 负断言（Projection Kernel / offline 无 aborted 写入）+ 真断开对照；§7.3 第 12 行加负断言；§10 补持久化链路证据入口。**v7 注**：r6-B1 发现 v6 只解决了 durable 虚假终态，registry 的 running 仍会 sticky；v7 补 unknown 收口（§18 R6-B1） |
| R5-M1 10s 轮询与"≤60s grace"计时不一致 | 全部采纳（诚实窗口方案） | §3.5.2 取消时钟精确定义：tick 上判定 `now - 最后正样本 ≥ 60s` → 取消区间 **[60s, 70s)**；§4.3/§4.9/§6-4/§7.3 第 12 行/§8/§11 统一为上界 <70s；G5 改为假时钟边界 tick 区间断言（含 59s 不取消边界）。**v7 注**：r6-M1 证明 last-positive 锚点与 [60s,70s) 数学上不相容（实际约 [50s,60s)），v7 已改锚点为首个连续负样本（§18 R6-M1） |
| R5-M2 mktemp 路径未保存、real-home 新 requirements 删除缺身份复核 | 全部采纳 | §0.2 日志规则改为 `LOG=$(mktemp)` + 登记 `p0-evidence.txt` + 同路径取证，禁止内联 `$(mktemp)` 写法；requirements 负例命令块更新；real-home 新建文件删除前 inode/device + 逐字节身份复核，身份已变保留现场停线 |
| R5-M3 先造第 4 份再轮转非 crash-safe | 全部采纳（收敛优先方案） | §3.3-10 顺序改为"先收敛旧集合 ≤2（失败 fail-closed）→ 创建新份（恰 3）→ 写原文件"，任何崩溃/中断点集合 ≤3；披露"收敛后创建失败回滚点降为 2"代价；T10/T26 覆盖初始 3 份与各中断点；§5 措辞同步（crash-safe 硬不变量） |

---

## 18. 六轮复审采纳记录（2026-08-29）

对应评审报告：[2026-08-29-grokbuild-leader-mode-design-review-r6.md](2026-08-29-grokbuild-leader-mode-design-review-r6.md)
（总结论：**修改后通过**；定向复核范围 = R5 四项必改）。两项必改 + 一项建议全部采纳；
R6-B1 的 sticky-running 链路与 R6-M1 的区间推导均经设计方在 pin 源码/数学上独立复核
确认（registry 两态 + catalog 无 runningMap 回退 + enrich never mutates registry +
冷拉不触碰 registry；last-positive 锚点下实际窗口 = L+60−D ∈ [50s,60s)）。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R6-B1 D-G2 主动取消遗留 sessionRegistry=running，侧栏可能永久显示运行中；冷拉不触碰 registry | 全部采纳 | §3.5.2 新增"**主动取消的 sessionRegistry 收口**"：授权 diff 如实扩展到 `go-bridge/types.go`（第三态 `sessionStateUnknown` + `markUnknown`，从不删除条目）；relay 维护 `claimedRunning`，主动取消在 claim 在场时 `markUnknown`——收回 passive 观察 claim，不猜 idle/running、不产生 wire/durable 副作用；catalog 经 `applyListRuntimeState` 既有分支输出 `runtimeState="unknown"`（F-8"不知道就不亮灯"自然延伸）；`onStateChange` 消费方对第三态安全且不触发 completeBridgeTurn；G7 增 registry/catalog 断言，§7.3 第 12 行增侧栏不 sticky 检查；§0.3/§8/§11 授权范围同步；§10 补六项源码证据。**v8 注**：r7-B1 发现 v7 的局部 bool claim 不构成所有权、unknown 会被 `!isIdle` 当 active、synthetic 行永不回收；v8 已升级为 generation token/CAS + isRunning 消费者替换 + synthetic 行可回收（§19 R7-B1） |
| R6-M1 last-positive 锚点得不到从实际消失起算的 [60s,70s)（实际约 [50s,60s)） | 全部采纳（选项 1：首负样本锚点） | §3.5.2 取消时钟锚点改为**首个连续负样本时间**（转正即清零），`now - firstNegativeAt ≥ 60s` 判定 → 从实际消失到取消恰为 **[60s, 70s)**，保证"至少 60s grace"；弃用 last-positive 并留推导说明；G5 重写为按实际消失时刻断言（含 59.9s 不取消、转正清零）；§4.3/§4.9/§5/§6-4/§6.1/§8/§11 全部改口径 |
| R6-S1 §0.2 残留"v5 落实四轮必改后即可执行 Phase 0"旧门槛 | 采纳 | §0.2 前置门改为"v7 落实六轮必改、且第六轮对 R6-B1/M1 的定向复核通过后即可执行 Phase 0" |

按六轮报告约定：R6-M2/M3 已闭合的日志/requirements/备份项无需重审；下一轮只需定向
复核 R6-B1（unknown 收口 + G7/§7.3 断言）与 R6-M1（锚点与区间一致）。

---

## 19. 七轮复审采纳记录（2026-08-29）

对应评审报告：[2026-08-29-grokbuild-leader-mode-design-review-r7.md](2026-08-29-grokbuild-leader-mode-design-review-r7.md)
（总结论：**修改后通过**；定向复核范围 = R6-B1 unknown 收口 + R6-M1 计时锚点）。
R6-M1 计时项已通过复核、算法不再改动；唯一阻断项 R7-B1 与建议项 R7-S1 全部采纳。
R7-B1 的四条子论据（局部 bool 非所有权、正常终态后 defer 覆盖、`!isIdle` 把 unknown
当 active、unknown 条目永不清理）均经设计方在 pin 源码独立核实：`types.go:373-377`
的 isIdle 定义、`!isIdle` 全部六处消费点（grep 全量核实，均在 `handlers_relay.go`）、
`handlers.go:856-877` 的 cleanupIdleSessions、`handlers_relay.go:2684-2707` 的
agentRelayGen 先例、`types.go:321-335` 的 session=nil synthetic 行。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R7-B1 `claimedRunning` bool 不能证明共享状态所有权；正常终态后可被覆盖为 unknown；`!isIdle` 把 unknown 当 active；unknown 条目永不清理造成新的无界积累 | 全部采纳（升级为 ownership-aware + 第三态安全 + 可回收） | §3.5.2 registry 收口重写为 6 条：`types.go` 增加全局单调 generation 盖章 + `claimRunning` 返回 token + `releasePassiveClaim(sessionID, gen)` CAS 释放（失配即 no-op，绝不覆盖较新状态）；正常 terminal 与真断开 defer 都清零本地 claimGen；释放两类落点——synthetic 行（session==nil）CAS **删除**（catalog 无记录自然 unknown，且不积累），真实会话行仅 gen 未被取代时转 unknown、句柄保留；新增显式 `isRunning` 并把六处 `!isIdle` 消费点（`:324,449,457,905,2728,2864`）**等价替换**（二态行为不变为回归前提，第三态下 unknown 不触发任何自动终态合成/idle 广播）；G7 断言更新（synthetic 删除/真实行保留）+ 新增 **G8**（terminal 后不覆盖、过期 cancel no-op、map 有界、消费者安全）；授权 diff 仍限 `handlers_relay.go` + `types.go`（六处消费点均在 handlers_relay.go，无需改 handlers.go）；§0.3/§8/§11/§10 同步。**v9 注**：r8-B2 发现 registry 实有第三态 closing，`isRunning` 替换并非等价；v9 已改为 `isKnownActive`（§20 R8-B2） |
| R7-S1 [60s,70s) 应注明为 ticker 正常调度下的名义窗口 | 采纳 | §3.5.2 取消时钟与 G5 注明：假时钟单测严格断言 [60s,70s)；生产 wall-clock 非硬实时上界，另计进程暂停/调度延迟，验收不得把系统暂停误判为算法错误 |

下一轮只需定向复核 R7-B1 registry 阻断项（generation claim/CAS、`isRunning` 六处替换、
synthetic 行回收）；R6-M1 计时项及此前已闭合项无需重审。
（该定向复核已由第八轮执行：closing 语义与 catalog 缓存两项新阻断被发现并处置，见 §20。）

---

## 20. 八轮复审采纳记录（2026-08-29，有限闭合清单）

对应评审报告：[2026-08-29-grokbuild-leader-mode-design-review-r8.md](2026-08-29-grokbuild-leader-mode-design-review-r8.md)
（总结论：**修改后通过；v8 暂不能直接交开发 agent**）。2B + 8M + 2S 共 12 项全部采纳；
两项阻断的事实链（catalog 10 分钟富化快照、`closing` 已声明状态）与 M1 的终态事实/
行号修正均经设计方在 pin 源码独立核实（`types.go:226-230` 三态、
`catalog_wire_snapshot.go:200-221,232-305`、`catalog_cursor_v2.go:31`、
`catalog_native_membership.go:78-99`、`handlers_relay.go:2717-2742,2859-2872` 两处
均为 turn_completed、`handlers.go:857-883`）。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R8-B1 registry 的 unknown/删除不会穿透 Grok catalog 10 分钟富化快照，G7/验收 12 不可成立 | 全部采纳（裁决：现成 fence 方案） | §3.5.2 新增第 7 条：relay 在 claim/release/terminal 状态变化成功后调用现成 `FenceBackend("grokbuild")` → 下一次 page-0 按当前 registry 重建；**披露 cursor_stale 代价**（存量分页 page-N Peek nil → 回 page-0）；否决"10 分钟最终一致"与"出站 overlay"（后者需扩 catalog 代码范围）；G7 增预热缓存双向断言（running→unknown、unknown→running）+ cursor stale；§4.1/§7.3 第 12 行标注"经 fence 穿透，非等 TTL"；diff 仍在 handlers_relay.go（仅调用现成函数）；§10 补六项证据 |
| R8-B2 registry 实有 `closing` 第三态，`!isIdle`→`isRunning` 并非等价替换 | 全部采纳 | §3.5.2 第 4 条重写：新增 **`isKnownActive = running || closing`**（idle/running/closing 既有域上与 `!isIdle` 逐点等价，仅 unknown/不存在退出 active），六处消费点替换为 `isKnownActive`；G8 增 ⑤ closing 回归（既有域逐点一致）；§3.5.2-1"两态"表述、§10、§19 v9 注同步修正 |
| R8-M1 relayEvents 两处终态均合成 turn_completed（v8 误写 aborted）；cleanupIdleSessions 行号漂移 | 全部采纳 | §3.5.2 第 4 条与 §10 修正：`:2717-2742` channel-close → `turn_completed(reason=events_channel_closed)`、`:2859-2872` idleTimer → `turn_completed`；`handlers.go:857-883` |
| R8-M2 范围与功能映射不一致（§3.2 漏 types.go、§4 "均不触碰/两行"、§4.1"结论不变"、§15 G5–G7） | 全部采纳 | §3.2 D-G2 行补 types.go；§4 总原则改"D-G1 不触碰；D-G2 不改 codec/合成但改取消/registry/可见性，行为变化集中在 §4.1/§4.3/§4.9 三处"；§4.1 改"链路结构不变 + 运行徽标 D-G2 例外（fence 穿透）"；§15 G5–G8 |
| R8-M3 假时钟缺可注入 seam | 全部采纳 | §3.5.2 第 3 条冻结 seam：`(now, hasSubscriber)` 纯 helper 状态机 + ticker 只产样本；G5 改为直接驱动 helper + 一个短接线测试；不引入通用 fake-clock、不跑 60s 真实时间 |
| R8-M4 locator 缺节边界关键用例 | 全部采纳 | §3.3-3 冻结节边界语义（顶层表/子表/数组表都终止 `[cli]` 节；无尾随换行只补必要换行）；新增 T31（三类边界）/T32（无尾随换行） |
| R8-M5 symlink 身份钉扎缺 rename 前 inode/device 复核 | 全部采纳 | §3.3-4 改为双重复核（链接链 canonical 路径 + 最终目标 inode/device 与初始一致）；T23 扩为含"链接文本不变但目标 inode 被替换"用例 |
| R8-M6 备份文件名碰撞/覆盖语义未冻结 | 全部采纳 | §3.3-10：文件名 = 纳秒级 UTC 时间戳 + UUID 后缀，exclusive create（已存在换名重试，绝不 truncate）；T10 增同一时刻两次写入碰撞用例 |
| R8-M7 合法 TOML 但非法 Bool 类型无裁决 | 全部采纳 | §3.3-3 裁决为 **F1**（拒绝管理，禁止误判 absent 后追加第二键）；新增 T33 |
| R8-M8 Phase 0 开工门口径冲突 | 全部采纳 | §0.2 为唯一权威门（"v9 落实八轮必改 + 机械闭合复核通过后即可执行"）；§15 明确"不单独授权开工" |
| R8-S1 历史采纳记录应降级为非规范性附录 | 采纳 | §12 前加规范性边界声明（§0–§11 为规范，§12–§20 仅审计追溯）；各轮"下一轮只需…"句标注后续轮处置 |
| R8-S2 D-G2 成功日志应冻结结构化字段 | 采纳 | §3.5.2 新增第 8 条：backendID/sessionID/reason=no_subscribers/firstNegativeAt/elapsed/claimReleased/registryOutcome，不记录 cwd/config 内容；G5 断言引用 |

**交接状态（取代各轮"下一轮只需……"口径）**：按八轮报告约定，本修订版不再接受开放式
全量评审；下一次审查为**机械闭合复核**——只对照本表 12 项 + 源码行（缓存命中测试、
closing 回归、章节范围与测试编号逐项对照）即可判定 APPROVE；APPROVE 后按 §0.2 开工
Phase 0。（该机械复核已由第九轮执行：12 项中 7 项闭合，fence 分支覆盖、typed outcome
等 1B+4M 收敛为最终有限清单，处置见 §21。）

---

## 21. 九轮机械闭合复核采纳记录（2026-08-29，最终有限清单）

对应评审报告：[2026-08-29-grokbuild-leader-mode-design-review-r9.md](2026-08-29-grokbuild-leader-mode-design-review-r9.md)
（总结论：**修改后通过；暂不交开发 agent、不启动 Phase 0**）。1B + 4M 全部采纳；R9-B1
的事实依据（真 source 断开的 F-7 defer 同样执行 registry `markIdle`，
`handlers_relay.go:225-243`）沿用本设计此前各轮在 pin 源码的核实结论。

| 评审项 | 处理 | 设计修订 |
| --- | --- | --- |
| R9-B1 fence 漏掉真 source 断开分支；正常 terminal 缺预热缓存回归 | 全部采纳 | §3.5.2-7 fence 规则改为**四类 Grok registry 有效状态变化**（claim running / 正常 terminal→idle / 真 source 断开→idle（F-7 `markIdle`，v9 遗漏）/ self-cancel release→unknown-delete）；G8-① 增预热 running 快照 → terminal + fence → page-0=idle + cursor stale；G7 对照用例增真断开 + fence → page-0=idle + cursor stale（F-7 事件面不变）；仍只调用现成函数，不扩大文件范围 |
| R9-M1 `releasePassiveClaim` bool 无法可靠产出三值 registryOutcome | 全部采纳 | §3.5.2-2 冻结 typed outcome 枚举 `Noop / Deleted / Unknown`；`claimReleased = outcome ≠ Noop`，日志与 fence 判定统一以 outcome 为准，禁止二次读 registry 猜结果；G7 增三态返回值断言 |
| R9-M2 §7.4/Phase 1 仍写 T1–T30；"第三态"术语与 §10 行号 | 全部采纳 | §7.4 与 Phase 1 改 **T1–T33**；§0.3/§3.2/§3.5.2/§8 规范正文的"第三态"改为"unknown 态（既有 idle/running/closing 之外的第四状态）"；§10 改 `types.go:226-230,243-377` |
| R9-M3 helper 文案可被误读为每次负样本重置计时 | 全部采纳 | §3.5.2-3 seam 改写："负样本且 `firstNegativeAt` 为零时才记 now；**连续负样本保持原锚点，不重置**"；G5 增连续第二/三个负样本不移动锚点断言 |
| R9-M4 manifest 未登记 T31–T33 | 全部采纳 | §3.3-9 manifest 增两行：表/子表/数组表节边界 + 无尾随换行（T31/T32，synthetic）；合法 TOML、非法 Bool 类型（T33，synthetic，只冻结 fail-closed 行为） |
| R9-S1 历史章节残留旧口径 | 采纳（不再逐行改历史表） | 规范正文（§0–§11）已清掉 stale 术语；§12–§21 保持非规范性审计记录，历史原文不再逐行修订 |

**交接状态（最终）**：下一次审查为逐行对照本表 + R9 报告最终清单的机械核对，全部命中
即 **APPROVE**，无需再扩展评审范围；APPROVE 后按 §0.2 开工 Phase 0。
（该机械核对已由第十轮执行，结论 **APPROVE**，见 §22 与 r10 报告。）

---

## 22. 十轮机械复核结论（APPROVE，2026-08-29）

对应评审报告：[2026-08-29-grokbuild-leader-mode-design-review-r10.md](2026-08-29-grokbuild-leader-mode-design-review-r10.md)
（评审对象：本设计 v10 规范正文，SHA-256
`886fc4e76a7c361942cc7b45f5bbeb99277abeb9e8e82ef02ef4626e79ac1e17`；两仓 HEAD 与冻结
来源一致，无来源漂移）。

**结论：APPROVE。无 B / 无 M / 无新增 S。** R9 最终有限清单（1B + 4M）逐项核对全部
闭合：fence 四类状态变化与 G7/G8 预热缓存回归、`releasePassiveClaim` typed outcome、
T1–T33 与 unknown 第四状态术语及源码行号、连续负样本不重置计时锚点、T31–T33 manifest
登记（r10 报告 §3 逐项给出文档行号与源码证据）。

**批准范围与边界（r10 报告 §5 原文归纳）**：本次批准的是**设计可实施性**——v10 可交
开发 agent；**不代表 Phase 0、构建、测试或生产路径已经验证**。下一步必须先执行 §0.2
的 Phase 0 十步现场拓扑证明，通过前不得进入产品代码实施。

本节及头部状态更新仅记录复核结论，不改变 §0–§11 规范正文；规范正文的变更（若实施期
发现必要）须按 §0.3 纪律回写本文并重新评审受影响条目。

---

## 23. Session rename / delete / archive 实施方案（2026-09-03 追补；owner 指令：只补方案，不写代码）

> 背景：owner 在 iOS Grok Build 模式某 session 的「更多设置」菜单依次试了重命名 / 归档 /
> 删除，三项全部报错。owner 裁定先读 grok-build 官方源码、把实施方案补进本文档，
> **本轮不写任何产品代码**。本节按 §0.3 追补纪律新增，不改动 §0–§22 的既有结论。

### 23.1 问题定位：既没设计也没实现

owner 看到的三条报错全部来自 **go-bridge 通用 fallback**，不是 grokbuild 自己的错误路径：

| iOS 文案 | 来源 | 触发条件 |
| --- | --- | --- |
| 重命名失败：session rename not yet supported | `go-bridge/handlers.go:2309`（`handleRenameSession`） | agent 未实现 `core.SessionRenamer` |
| 归档失败：session archive not yet supported | `go-bridge/handlers.go:2344`（`handleArchiveSession`） | 未实现 `core.SessionArchiver` |
| 删除失败：backend does not support session deletion | `go-bridge/handlers.go:4283`（delete handler） | 未实现 `core.SessionDeleter` |

- `agent/grokbuild/` 对三接口 `grep` 零命中——**三项能力全部未实现**。
- 本文档 §4 功能面映射未覆盖 session 增删改三项——**也未设计**（leader 模式九轮评审
  的范围是观察/应答面，不含 session 管理）。
- capability 推导（`go-bridge/backend_capabilities.go:47-51/56-58`）：`session_mutation`
  需 **Renamer + Archiver 双实现**才宣告（AND 语义）；`session_delete` 由 Deleter 独立
  宣告。grokbuild 目前两者都不宣告。
- iOS 入口结构（iOS 仓 `ContentView.swift:873-879` + `ChatUIKitContainerView.swift:892-921`）：
  「更多设置」header 菜单的**重命名/归档/删除三个回调恒提供、无 capability 门**（只有
  「分享」gate 在 `supportsSessionSharing`），点击后靠后端 RPC 报错兜底——这就是 owner
  在 grokbuild 上仍能看到三个入口、点了才报错的原因。侧边栏（`SidebarView.swift:37`）
  三项 gate 在同一 `session_mutation`，grokbuild 未宣告故隐藏（现状正确）。

### 23.2 官方源码证据（上游 `/Users/jacklee/Projects/grok-build` @ `72a61251`，clean）

`x.ai/` 命名空间全量枚举（`grep -rhon '"x\.ai/[a-z_/]*"' | sort | uniq -c`）：

- **`x.ai/session/rename` 存在**（5 处引用）；**`x.ai/session/delete` 存在**（3 处引用）；
- **无 `x.ai/session/archive`**——官方 ACP 面没有 archive 概念，零命中。

#### 23.2.1 分派层：agent-level，无 resident session 门

`crates/codegen/xai-grok-shell/src/agent/mvp_agent/acp_agent.rs:2320-2324`：

```rust
"x.ai/session/rename" | "x.ai/session/delete"
| "x.ai/session/update_mcp_servers" | "x.ai/session/fork"
| "x.ai/plugins/reload" | "x.ai/commands/list" => {
    crate::extensions::session_admin::handle(self, &args).await
}
```

ext 分派在 `ext_method`（`acp_agent.rs:2268`），是 **agent-level**——不要求 resident
session，**常驻 leader 进程与 `--no-leader stdio` 子进程（MvpAgent）都能响应**。CordCode
现有的进程级单例 catalog 子进程（`grok agent --no-leader stdio`，§5.4）天然具备响应能力。

#### 23.2.2 rename 语义（`extensions/session_admin.rs:88-221`）

- **wire params**（camelCase，`#[serde(rename_all = "camelCase")]`）：
  `{sessionId, title, cwd?, kind?, resetToAuto?}`；`kind` 缺省 `Build`（`unified_list/envelope.rs:7-11`，
  serde lowercase `"build"`）；`resetToAuto` 缺省 false。官方测试
  `agent/mvp_agent/tests/session_rename_tests.rs:31-42` 直接以
  `{"sessionId", "title", "cwd"}` JSON 调 `ExtRequest::new("x.ai/session/rename", …)`。
- **wire 帧（2026-09-03 探针实测修正）**：ext 方法在 stdio JSON-RPC 上**必须带 `_`
  前缀**——wire method 字段为 `"_x.ai/session/rename"`（agent-client-protocol crate 的
  wire 约定，与 leader socket 半包装形态一致；`_` 剥离发生在 acp crate wire 层，grok
  源码内无对应逻辑）。**裸 `"x.ai/session/rename"` 在 grok 1.0.13 上返回 -32601
  Method not found**——本节初稿据 checkout 源码写的「裸方法名」结论已被探针推翻。
  帧的其余部分（params 内联 camelCase、数字 id、单行 JSON-RPC 2.0）与 catalog rail
  现有 `callRPCWithCtx` 同构。官方客户端侧证据：`xai-grok-pager/src/app/effects/
  mod.rs:4734-4765` `session_rename_rpc`（`acp::ExtRequest::new` → `acp_send` → 响应取
  `error` 字段）；`actions.rs:2096-2134` `RenameSessionRequest`（`resetToAuto` false 时
  skip 序列化）。**错误形状**：官方错误（`session not found: {id}`、title 校验文案）
  在 JSON-RPC error 的 **data 字段**（message 是泛化 "Invalid request"）——透传必须带
  data。
- **校验链**（官方已在边界做，CordCode 无需复刻）：
  1. `title.len() > MAX_TITLE_BYTES`（=464 字节，`session/persistence.rs:59`）→ 报错；
  2. `sanitize_rename_title`：剥 C0/C1 控制字符 + bidi/format overrides（U+200E/200F/
     202A-202E/2066-2069），trim——单一权威点；
  3. 空 title → 报错（`title must not be blank`）；
  4. 标量数 > `MAX_TITLE_SCALARS`（=100 字符，`persistence.rs:54`）→ 报错。
- **执行链**：`list_summaries(cwd?)` 按 sessionId 精确匹配（找不到 → `session not
  found: {id}`；`cwd=None` 列全部 cwd，跨目录 session 也能找到）→ `JsonlStorageAdapter
  .update_session_title` 落盘 → 常驻 session 走 persistence actor 冻结 auto-title /
  休眠 session 直接落 watermark → 搜索 index 更新 → `SessionSummaryGenerated` 通知
  （`x.ai/session_notification` ext notification，`_meta.x.ai/titleIsManual`，**不是**
  `x.ai/sessions/changed` roster）→ writeback（非 ZDR）时同步远端
  `save_session_data`（ExportedMetadata{title, title_is_manual:true}）→ fire-and-forget
  registry replica 更新 → 响应 `{"success": true}`。
- **官方已知限制**（doc comment，session_admin.rs:108-113）：relay-registered sessions 的
  sidebar title 权威在 x.ai relay REST endpoint，此 ACP 方法不写 `relay_sync`——未达
  relay 的 rename 会在下次 sidebar refetch 回滚。这是官方自己的 gap，照抄语义、不做补偿。

#### 23.2.3 delete 语义（`extensions/session_admin.rs:441-482`）

- **wire params**：`{sessionId, cwd?, kind?}`（camelCase，kind 缺省 Build）。
- **执行链**：writeback + 非 ZDR 时 **remote-first**（远端删除失败 → 本地不动、RPC 报错
  ——fail-closed 语义直接传播给 iOS）→ `teardown_live_session_before_delete`（先排空常驻
  session 与 coordinator 子进程）→ `delete_session_history`（本地磁盘删除 + FTS 搜索
  index 驱逐）→ 响应 `{"success": true}`。CLI 路径 `grok sessions delete <id>` 镜像同一
  逻辑。
- delete 同样**不发 roster 广播**（官方 pager 靠自身操作成功后本地移除行）。
- Chat kind（conversations lane，OIDC）走 `soft_delete_conversation`——CordCode
  grokbuild driver 只管 build session，不触及。

### 23.3 裁决：rename + delete 复用官方 ext 方法；archive 不做

| 能力 | 裁决 | 依据 |
| --- | --- | --- |
| rename | **实现**：`SessionRenamer` + catalog rail 发 `x.ai/session/rename` | 官方方法存在、语义完整、与 catalog rail 同构 |
| delete | **实现**：`SessionDeleter` + 同 rail 发 `x.ai/session/delete` | 同上；remote-first fail-closed 与 CordCode 纪律同向 |
| archive | **不实现**（维持 `not_supported` fallback） | 官方 ACP 面**无** `x.ai/session/archive`；自建 bridge-owned archive 状态会把 session 在 iOS 隐藏而 Mac 端 grok TUI 仍显示（跨端不一致），违反「不得为凑 capability 自造语义」红线与上游源码优先门第 5 条 |

archive 的行为后果（可接受、诚实）：
- `session_mutation` 因 AND 语义（Renamer+Archiver 双实现才宣告）**维持不宣告**——
  与 **dsh-web 先例一致**（dsh-web 只实现 `SessionRenamer`，见 `agent/dsh-web/sessions.go:357`，
  同样不宣告 `session_mutation`）。
- iOS「更多设置」header 三项恒显示：rename/delete 变为**真实可用**；archive 点击仍报
  `session archive not yet supported`（诚实状态，不是假成功）。
- iOS 侧边栏三项继续隐藏（gate 未满足）——现状不变，iOS 零改动。
- 后续若想把 header 三个入口按能力分别显隐（拆 `session_mutation` 为 rename/archive
  独立 capability），属于协议 + iOS 变更，**列为另案**，不在本方案内。

### 23.4 CordCode 实施方案（MacBridge 单仓，iOS / protocol pack 零改动）

#### Rail：复用进程级单例 catalog 子进程

rename/delete 走 `grokCatalogClient`（`catalog_session_list.go`，`grok agent --no-leader
stdio`）：已握手（initialize → authenticate）、进程级单例、按需重建、`callRPCWithCtx`
支持任意 JSON-RPC 请求。**不选**另外两条 rail 的理由：
- per-turn driver 子进程（`session.go`）：rename/delete 是列表级操作，不应依赖/干扰
  活跃 turn 子进程；
- leader socket（`leader_subscriber.go`）：CordCode 在该 rail 是订阅者/应答者角色，
  不发管理请求（§3 架构边界维持）。

#### 23.4.1 `RenameSession`（新文件 `agent/grokbuild/session_admin.go`）

```
func (a *Agent) RenameSession(ctx, sessionID, title) (*core.AgentSessionInfo, error)
```

1. 预检：trim sessionId/title 非空（与 `handlers.go:2302-2308` 的 missing_param 门
   互补，agent 层不重复官方校验——464 字节/100 字符/控制字符清洗由官方边界做，错误
   原文透传给 iOS）；
2. `catalogClientInstance(ctx)` → `sessionAdminCall(ctx, "_x.ai/session/rename",
   {"sessionId": …, "title": …})`（60s 超时；独立于 `callRPCWithCtx` 的 raw-call helper，
   因为通用路径的错误格式化会丢 `error.data`——官方细节文案全在 data）——**不发
   cwd**（`list_summaries(None)` 跨目录匹配，实现最简且行为正确；带 cwd 列为可选
   优化）、不发 kind（缺省 Build 即目标语义）、不发 resetToAuto（bridge-v1 rename
   协议只有 title，iOS 无 unpin UI）；
3. 响应 `{"success": true}` → 构造 `AgentSessionInfo{ID, Summary: title, ModifiedAt:
   time.Now()}`（**dsh-web 模式**：官方响应不含完整 Session.Info，本地构造返回值，
   参考 `agent/dsh-web/sessions.go:290-305`）；
4. 成功后 `a.signalCatalogRefresh()`——复用既有 roster 信号通道（`grokbuild.go:193`）：
   `session_discovery.go:169-172` 收到信号立即 `authoritativeRefresh` → fingerprint
   diff（磁盘 summary title 已变）→ `sessions_changed` 广播 → iOS 列表自动换新标题。
   **无需改 session_discovery / 新增轮询**。
5. JSON-RPC error（含 `session not found: {id}`、`title must not be blank`、
   `title too long…`、writeback 远端同步失败）原文包装返回——iOS 已有错误 toast 渲染。

#### 23.4.2 `DeleteSession`

```
func (a *Agent) DeleteSession(ctx, sessionID) error
```

1. 预检 trim 非空；
2. 同 rail 发 `x.ai/session/delete`，params `{"sessionId": …}`（不发 cwd，理由同上）；
3. 官方 remote-first：writeback 用户远端删除失败时本地不动且 RPC 报错——直接透传，
   iOS 显示失败、session 仍在（**fail-closed，不造本地假成功**）；
4. 成功后 `a.signalCatalogRefresh()`（磁盘目录已删 → fingerprint 少一行 →
   `sessions_changed`）。iOS 正在查看被删 session 的窗口由列表刷新自然收口（与
   Claude/OpenCode 删除路径同语义，无特判）。
5. 不做额外收敛校验：官方 pager 自身信任 `{"success":true}`；且探针实测 delete 对
   不存在 id **幂等成功**（§23.5）——陈旧列表上的重复删除不报错，无需 absence 校验。

#### 23.4.3 capability 变化（零手写，接口断言自动推导）

| capability | 变化 | iOS 影响 |
| --- | --- | --- |
| `session_delete` | **新增宣告**（Deleter 实现即自动，`backend_capabilities.go:56-58`） | 无（iOS 全仓 grep `session_delete` 零消费，纯真值诚实） |
| `session_mutation` | **维持不宣告**（缺 Archiver，AND 语义） | 侧边栏三项继续隐藏（现状）；header 恒显示不受 capability 影响 |

不新增任何手写能力真值表；`hello_ack.backends[]` 由推导自动携带。

#### 23.4.4 边界与不做清单

- **不动 iOS、不动 protocol pack、不动 SSV2**：bridge-v1 `rename_session` /
  `delete_session` RPC 形状已存在且 iOS 已在用（对 Claude/OpenCode），grokbuild 只是
  从 fallback 变为真实实现。
- **不做 resetToAuto / unpin**：bridge-v1 rename 协议无此概念。
- **不触碰 leader 订阅 / follower 应答链路**（§3 架构边界）：rename/delete 全部走
  catalog rail，与 leader_subscriber.go 零交集。
- **不做 Chat conversation rename/delete**（conversations lane，OIDC）：grokbuild
  driver 只管 build session；`kind` 缺省 Build 即官方语义。
- **不为 grok TUI 端做额外同步**：官方 rename/delete 内部已通知 leader gateway
  （`x.ai/session_notification`），Mac 端 grok TUI 自己收口。

### 23.5 版本偏移与 Phase 0 探针（已执行，2026-09-03）

- 本机目标二进制 **grok 1.0.13**（`5e9a58528b76`）与 checkout `72a61251` 存在版本
  偏移（§2 既有口径）；rename/delete 的 wire 形态在 1.0.13 上**必须先探针实证**，
  不得只凭 checkout 结论编码。
- **零成本探针**（沿用 follower-question 调研方法论：零 prompt、零 API 消耗、用完即删；
  临时测试文件 `probe_rename_delete_ext_test.go`，已删）：catalog 子进程握手后对同一个
  不存在 sessionId 试了五种 wire 形态。**实测结果**：

  | wire method | 结果 |
  | --- | --- |
  | `_x.ai/session/rename` | `-32600 Invalid request`，**data: `"session not found: cordcode-probe-nonexistent-0000"`** —— 分派存在，穿透到 rename handler 的 session 匹配 |
  | `_x.ai/session/delete` | **`{"success":true}`** —— 分派存在；不存在 id **幂等成功**（与官方 `delete_session_history` 语义一致） |
  | `_x.ai/session/archive` | `-32601`，data `"unknown ACP extension method: x.ai/session/archive"` —— 1.0.13 同样无 archive |
  | 裸 `x.ai/session/rename` / `x.ai/session/delete` | `-32601 Method not found` —— 裸名不可用 |
  | `ext_method` 包装 / `session/request` 包装 | `-32601 Method not found` |

  三个实施级结论：① wire 方法名必须 `_` 前缀（§23.2.2 已修正）；② 错误细节在
  `error.data`；③ delete 幂等——重复删除陈旧列表里的 session 不报错，CordCode 无需
  额外收敛校验。

### 23.6 测试与验收

- **单测**（`agent/grokbuild/session_admin_test.go`）：
  - rename 成功：fake catalog 子进程返回 `{"success":true}` → 返回 info（ID/title/
    ModifiedAt 非零）+ `signalCatalogRefresh` 被触发；
  - rename 失败透传：`session not found` / `title must not be blank` → 错误原文包装；
  - delete 成功 / remote-first 失败透传（fake error）；
  - 空 sessionId/title 预检报错。
- **验收矩阵**（owner 真机）：
  1. iOS header 重命名 → grok TUI 同 session 标题同步变化 + iOS 列表刷新出新标题；
  2. iOS header 删除 → session 从 iOS 列表消失 + `~/.grok/sessions/…/<id>/` 磁盘目录
     消失（Mac 侧只读核对）；
  3. archive 点击仍报 `session archive not yet supported`（诚实不支持）；
  4. 重命名超长/空白标题 → iOS 显示官方错误文案。
- 验证级别按 D3（状态/协议类）：相关测试类 + 一次增量 build，不跑全仓。

### 23.7 来源清单（P0 门）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader
分支=codex/grokbuild-leader-mode
提交=dc30edc（HEAD，本节写入前）
未提交状态=docs/2026-09-02-same-name-ios-device-eviction-fix-design.md（untracked，另案，未触碰）
任务预期分支=codex/grokbuild-leader-mode（本工作树即任务工作树）

配套仓库（上游，只读）=/Users/jacklee/Projects/grok-build
分支/提交=72a61251（detached 快照，clean）
引用符号=session_admin.rs handle_session_rename/handle_session_delete、
  acp_agent.rs:2320-2324 ext 分派、persistence.rs MAX_TITLE_*/sanitize_rename_title、
  unified_list/envelope.rs SessionKind、effects/mod.rs:4734 session_rename_rpc、
  actions.rs:2096 RenameSessionRequest、tests/session_rename_tests.rs（官方测试锚点）

配套仓库（iOS，只读参考）=/Users/jacklee/Projects/cordcode-ios
分支=main，提交=de264a2c
引用符号=ContentView.swift:234/873-879、ChatUIKitContainerView.swift:892-921（header
  菜单恒显示证据）、SidebarView.swift:33-39（侧边栏 session_mutation gate）、
  CCCodeBridgeBackendClient.swift:183（capability 消费）；OpenCodeiOS 全仓 grep
  "session_delete" 零消费（2026-09-03 核对）

版本偏移=目标二进制 grok 1.0.13（5e9a58528b76）≠ checkout 72a61251；
  rename/delete wire 实施前需 §23.5 探针实证
```
