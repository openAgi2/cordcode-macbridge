# DSH 会话存储桥接（列表 + 历史 + file-backed 投影）完成情况

- 计划：`docs/2026-08-16-dsh-session-store-bridge-design.md`
- 日期：2026-08-16
- 触发：owner 裁决——「iOS 端无法显示 mac 端 DeepSeek harness 的 session 列表」「iOS 端 DeepSeek 模式可以新建 session……但是 session 列表始终为空」不做完不算完整功能，原 6 行验收矩阵挂起。
- 队列：17/17 done（exec-plan `plan-bf2e7697a934`，全 self-attested，关键测试附亲跑命令）

## 结论

owner 两点已实现并全部本地验证：iOS DeepSeek 模式现在显示**全局会话列表**（Mac 端 dsh web 会话 + 手机自建会话，标题/目录来自 harness 自身的 session/title 事件与头行）；点开任意已结束会话可查看**完整历史**（含思考与工具调用，file-backed SSV2 投影冷加载）。已结束会话内发消息得到诚实提示（SDK 无跨进程恢复），绝不覆写磁盘。正式版 App 已含全部改动（03:26 覆盖安装重启，runtime 新鲜构建核实）。

## 关键事实（源码取证，pin 47f9438 / npm rc.6）

1. SDK JSON-RPC 无 list/resume；`session/prompt` 对已知 id 懒创建新对（server 仅 `ctx.agents.create`）。
2. harness 持久化拒绝重复物化（"refusing to materialize: a log already exists on disk"）→ prompt-known-id「恢复」假设**否定**，且不会覆写磁盘（安全）。
3. headless/ACP 均 create-only；resume 仅存在于 host 内（tui/web）。
4. 存储布局 `$DSH_HOME/sessions/<projectKey>/<id>/session.jsonl[.zstd]`；projectKey 有损（读侧以头行 cwd 为准）；标题=日志内 `session/title` 事件折叠+人类首条 fallback；web 写 zstd、driver 写明文。

## 交付物

**MacBridge `e83e186`/`a3c13d1`（dsh/driver）**
- `agent/dsh/store.go`+`zstd_reader.go`：projectKey TS 对齐移植（UTF-16 码单元/`~XXXX`/分隔运行合并/≤251）、全局双后缀扫描、头行、subagent 过滤、标题扫描（512KB 预算）、id 解析（拒穿越）、klauspost zstd v1.18.0（纯 Go）。
- `agent/dsh/history.go`+`dsh.go`：`ListSessions`（替换 not_supported；mtime 倒序、Directory=头行 cwd）、`RichHistoryProvider`（双 pass tool/result 按 callId 关联；turn 累积 reasoning→tool step→text，grokbuild parts 约定；稳定 ID `<sid>:<seq>`；arguments 双重编码兼容）。
- 投影：deepseek 入 `backendSupportsProjectionHydrate`/pathless 全量重建分支（opencode/grokbuild 同款 machinery）；live-only admission 降级为「store 未落盘回退」；`projection.not_found` 收窄为 kernel+store 双未知；两处 forceCold 集合（下拉刷新对 dsh web 增长可见）；observation 剪枝换 `backendHasNoExternalEventSource`。
- 发送守卫：driver `ErrSessionNotResumable`（覆盖全部 StartSession 调用点）+ bridge preflight wire 错误 `session_resume_not_supported`（retryable=false，spawn 前快速失败）；canonical 入册。

**iOS `4b5eb69`/`9489a6f`（dsh/driver）**：解除全部列表门控（BackendModels 双属性删除、主列表+三隐藏入口+目录解析回通用路径）；SidebarView 通用空态；not_found 文案收窄；`session_resume_not_supported`→owner 向文案（不泄漏 SDK 版本）；mirror 协议同步。

## 证据（attestation 均为 self-attested，命令为亲跑）

| 项 | 证据 |
|---|---|
| store 读取层 | `go test ./agent/dsh/ -run 'TestProjectKey|TestScanSessions|TestSessionTitle|TestFallbackTitle|TestResolveSession|TestOpenDshSessionStore'` 全绿；**真实 store 冒烟**（`DSH_STORE_SMOKE=1`）：7 个真实会话全中、真实标题正确提取 |
| history 映射 | 黄金样本双压缩格式全绿；**真实 17.6k 行会话冒烟**：单 turn 52 工具步全映射（与日志 52 条 tool/call 一致） |
| 投影 file-backed | `LiveOnly|DeepSeekProjection` 8/8（含 file 基线达 Ready、无 agent 诚实失败、T1-T5/T8 语义保留）；`go test ./go-bridge/ -count=1` 全量 48s 绿 |
| 发送守卫 | 三态 4 用例全绿（错误码/retryable/零 StartSession 调用/pending 清空/unknown 透传/driver 层） |
| 全模块回归 | `go test ./go-bridge/... ./agent/... ./core/... ./transcriptindex/... -count=1` 14 包全绿 |
| iOS | Debug build SUCCEEDED；LiveOnlySessionListTests 5/5（store-bridge 契约）、LiveOnlyProjectionStateTests 3/3、ChatViewModelSessionSyncV2Tests 52/52、SessionListColdCache 三类+BridgeModelsTests 55/55 |
| 部署 | Release 构建成功（runtime=HEAD 新鲜）；覆盖安装+重启核对：8777 归属 `/Applications` runtime、无残留进程、dsh route② 探测正常、iOS 客户端自动重连 |

**已知边界（如实）**：① 死会话续聊 SDK 封死（诚实错误+协议入册），待官方 resume 后升级；② dsh web 正在跑的会话 v1 无实时 tail——iOS 下拉刷新触发全量重建可见增量；③ 全量 CCCodeTests 套件在 85 分钟后 0% CPU 挂起（既有隐患、非本次改动面——改动类 115/115 通过），建议另行立项排查；④ 列表 MessageCount 恒 0（标题扫描预算内不做全量计数，诚实置空）。

## 真机修复轮（2026-08-16 owner 复测反馈）

owner 复测：列表/历史/跨端同步 ✅（含 Mac 新建目录及时同步）；死会话续聊被诚实拒绝（设计内，SDK 0.1.0-rc.6 无跨进程 resume）。**缺陷**：iOS 新建会话发送后长时间「执行中」无回复、Mac web 看不到该会话。

根因（日志 + 裸探针双重取证）：`624c6a4` 将 `DSH_SESSION_ROOT` 指向用户 store 后，web 写入的 zstd 工件与 driver `compression: none` 冲突——harness `checkRootEncoding` 扫整个 root，materialize 时抛 `…uses .jsonl.zstd, but this backend is configured for compression "none"`（真机日志：turn_started 后 1ms turn_error）。私有目录时代 root 为空纯明文故未暴露。

修复（`c522e73`）：cordis.yml `compression: zstd`（与 web 默认一致；读侧双后缀本就兼容）。验证：`DSH_TURN_REPRO` 真实 spawn 复现测试——修复前探针捕获 encodingMismatch 原文，修复后编码错误消失、turn 到达 LLM 层（mock key 401 预期终态）、会话成功物化进 store；`agent/dsh` 全套回归绿；Release 重装核对（8777=/Applications runtime、route② 探测正常）。

## 验收（owner 真机，替代原矩阵）

| # | 动作 | 应看到 |
|---|---|---|
| 1 | iPhone 切 DeepSeek 模式 | 列表显示 Mac 端会话（含 dsh web 会话，标题/目录正确） |
| 2 | iPhone 新建会话发消息 | 正常聊天，会话出现在列表 |
| 3 | 点开任一已结束会话 | 完整历史渲染（含思考/工具调用） |
| 4 | 在已结束会话内发消息 | 诚实提示「此 DeepSeek 会话已结束……发起新会话」，不卡死、不覆写 |
| 5 | 活会话续聊（流式/停止/附件拒绝/重启后历史） | 原行为不变 |

全绿后两仓 `dsh/driver` 合回 main + 推远端（与前置计划收尾合并执行）。
