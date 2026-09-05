# 开工指令：Claude Code backend 官方能力收敛升级实施（2026-09-04）

## 任务

实施 `docs/2026-09-04-claudecode-official-capability-upgrade-design.md`（**v2.2**，
已经三轮评审通过，采纳记录见文档 §11.1–11.7）。设计文档是唯一权威来源；本指令只
定执行方式与红线，与设计文档冲突时以设计文档为准并停下来报告。

## 专用工作树（已创建，一切实施只发生在这两个工作树）

| 仓 | 工作树 | 分支 | 基于 |
|---|---|---|---|
| Mac (cordcode-macbridge) | `/Users/jacklee/Projects/cordcode-macbridge-claudecode-official` | `claudecode/official-capability` | `plan/approval-layer` @ bdc2cda |
| iOS (cordcode-ios) | `/Users/jacklee/Projects/cordcode-ios-claudecode-official` | `claudecode/official-capability-ios` | `plan/approval-layer-ios` @ bf9a8d9 |

不得在其他工作树（含两仓 main checkout 与 plan-approval 工作树）读取源码形成结论、
编辑、构建或安装。

## 执行方式（必须遵守）

1. **用 exec-plan skill 分段实施**：以设计文档为 plan 的权威来源做 todo 拆分，
   拆分顺序 = 设计 §6 的 Phase 0 → 1 → 2 → 3 → 4，不得重排、不得跳过。exec-plan
   的状态文件（plan json）由 skill 按其自身流程生成；本开工指令不预置 plan json。
2. 开工先读：两仓 CLAUDE.md、`GO_BRIDGE_ARCHITECTURE.md`、设计文档全文——重点
   §3 官方事实基线、§6 分阶段硬门、§7.1 红线清单、§8 风险、§11 评审采纳记录。
3. **Phase 0 证据包是全案硬门**：七个探针（§6 Phase 0.1–0.7）未全部执行并归档
   dump 之前，禁止编写任何 Phase 1 代码。探针 spawn 必须逐字复用
   `baseClaudeInnerArgs`（agent/claudecode/session.go:108-118——stream-json 双向、
   `--permission-prompt-tool stdio`、无 `-p`、stdin 保持打开）+ 与生产相同的 env
   注入；纯 `-p` 对照组不作为放行判据。
4. **§7.1 红线**：`list_models` / `initialize` / `set_model` / `set_permission_mode`
   / `interrupt` / `rename_session` 的成功体、各 hooks POST 原文、effective hooks
   ——**未 dump 到真实样本前一律当未核实**，先写解析器即违规。控制信封是
   `{"type":"control_request","request_id":…,"request":{…}}`（`request` 嵌套，§3.1）。
5. **fail closed 全程**：不支持的 subtype、缺失的 caps、无法解析的成功体 →
   能力位如实降级，禁止伪装支持、禁止 mock/fallback 路径掩盖失败。
6. **S8 裁决（interrupt 后留进程 vs 仍 Close）是 owner 产品裁决**：推进到 Phase 2
   编码前必须先向 owner 呈现两案并等待拍板，不得自行选择（设计 §6 Phase 2.4）。
7. 跨仓修改已在授权范围内（双仓 coherent change 直接做）；UI automation、真机
   UI 操作、生产 VPS/Relay 部署仍需 owner 明确授权。
8. 每次进入编辑/构建/安装门之前，按 CLAUDE.md P0 规则重新生成双仓来源清单，
   锚定上表两个专用工作树；实施中分支/提交变化要重新核验。
9. 验证纪律按 CLAUDE.md D 级分级（本计划为协议面 D3：相关测试组 + 定向构建，
   不默认全量）；MacBridge 验证只走「Release 构建 → 覆盖安装 /Applications →
   killall + open 重启 → 运行态核验（进程代际 + 新版本特征输出）」路径，禁止用
   临时构建产物测试。
10. 每轮对外可见改动完成在 `[Unreleased]` 追加 CHANGELOG 条目；涉及 wire 变化
    （模型行 `resolved`/观测键、能力位）同步 `docs/protocol/` canonical pack。
11. 完成定义 = 设计 §6 各 Phase 验收 + §7 测试与验收；收尾交付说明必须单列：
    探针 dump 归档路径、双仓来源清单、诚实列出的未实施项与原因。

## 第一步

进入 Mac 专用工作树，读设计文档 §6 Phase 0，用 exec-plan 建立本计划并开始
Phase 0.1 控制面探针。
