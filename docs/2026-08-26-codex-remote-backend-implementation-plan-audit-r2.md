# codex-remote 实施方案第二轮评审（audit-plan 复核）

- 日期：2026-08-26
- 评审对象：[2026-08-26-codex-remote-backend-implementation-plan.md](2026-08-26-codex-remote-backend-implementation-plan.md)（修订版，619 行，含 §14 处置记录）
- 评审依据：第一轮报告 [2026-08-26-codex-remote-backend-implementation-plan-audit.md](2026-08-26-codex-remote-backend-implementation-plan-audit.md)
- 结论：**通过，可进入 Phase 0**。第一轮全部 P1/P2 已按声明闭环，处置记录与实际修订一致，未发现修订引入的新未证实声明；本轮另以真实 fetch + diff 把 P1-2 的版本基线从"待办"升级为"已解决"。

## 0. 来源清单

```text
MacBridge=/Users/jacklee/Projects/cordcode-macbridge main 375f298ec120e19e90c979bd371cb804ea83880f（工作区仅评审对象未跟踪）
Codex 上游=/Users/jacklee/Projects/codex main 25a6e316c81fb7600d1d75f3e63ffe26be10b7c8（干净；本轮新增 fetch 了上游 tag refs/tags/rust-v0.150.0-alpha.8 → 仅写 FETCH_HEAD，未建本地 ref，工作树未动）
运行态 App=/Applications/ChatGPT.app 26.820.60940，内嵌 codex-cli 0.150.0-alpha.8
```

## 1. 第一轮意见闭环核验

| 第一轮意见 | 修订位置（行号） | 核验结果 |
|---|---|---|
| P1-1 envelope 移除 `environment_id`、绑定机制待 fixture | §6.1 图（L344-357）已删该字段并新增独立块"environment 选择/绑定 = 闭源 relay 契约"；L338-340 明示"host 侧字段全集不含 environment_id"；§6.2 L374-375、Phase 0 任务3（L435-436）、§9.1 新增 environment binding 行（L516） | ✅ 闭合，与 protocol.rs 字段全集 dump 一致 |
| P1-2 版本基线显式化 | §3.1（L173-188）+ Phase 0 任务1（L431-432）：fetch alpha.8、差异清单、不可定位时以 binary fixture 为准、版本四元组记录 | ✅ 闭合；且本轮已提前执行（见 §2） |
| P2-1 controller 主动刷新标 assumption | §6.2 L377-378："controller enroll/refresh 两步端点已证实；'到期前主动刷新'的具体调度仍是 assumption pending Phase 0 fixture，未取样前不得写死提前量或退避" | ✅ 闭合，措辞精确 |
| P2-2 精确常量进入断言 | §6.2 L368-373（cursor header、segment 五常量、idle 10min/30s）；§9.1 L527-530 完整清单 + host/controller 角色分离 | ✅ 闭合；"部分采纳"的实质是正确实现——24–36s 是 host 失败退避，只进上游基线测试，不类推 controller 产品时序，恰好避免了制造新的未证实声明（第一轮 P2-1 与之同向） |
| P2-3 双路径族索引 | §3 表行8（L168）`/codex/*`（enroll/refresh + expected-path 校验）与 `/wham/*`（pairing/list/MFA）分列用途；Phase 0 任务2（L433-434） | ✅ 闭合，与 asar 提取一致（expectedPath 见 `/codex/.../enroll/finish`、`.../refresh/finish`；wham 族为 client/pair、clients、mfa） |
| P2-4 Pong/ClientClosed 覆盖 | §5.2 L259、§6.2 L372、§9.1 L517 | ✅ 闭合，`Pong{status: Active\|Unknown}` 与 `ClientClosed` 均为 protocol.rs 已证实 variant |
| Gate 补强（environment 绑定 + cursor 实证） | Phase 0 任务3（L435-436） | ✅ 闭合 |

处置记录（§14）逐行与实际修订比对一致；唯一"不采纳"（host 退避不当 controller 合同）理由成立且与第一轮意图一致。

## 2. 本轮新增证据：版本基线从"待办"变"已解决"

第一轮只能确认本地无 alpha.8 tag。本轮执行了方案 Phase 0 任务 1 要求的动作：

1. `git ls-remote origin refs/tags/rust-v0.150.0-alpha.8` → **tag 存在于上游**（annotated `4111e744` → commit `fcbdb57`）；
2. fetch 后 `git rev-list --count <alpha.8>..HEAD` = **107 个提交**；
3. `git diff --stat <alpha.8> HEAD -- …/remote_control/ + processor + cli/remote_control_cmd.rs` → **仅 2 个文件各 +1 行，且全部是测试代码**（tests.rs 与 websocket.rs 测试模块的 fixture 各加一个 `bedrock_access_keys: None` 字段）。

结论：**remote_control 生产模块在出货 alpha.8 与 HEAD 之间逐字节一致**。§3 表引用的全部 host 侧源码事实（URL 族与校验、认证拒绝 API key、enroll/refresh/pair 响应形状、envelope、segment 五常量、idle 10min/30s、cursor header、client_tracker origin、processor 方法、CLI 子命令）对出货二进制的源码层面成立。剩余不可由源码证明的部分只在 controller 侧（app.asar/闭源 relay），方案已全部标记 fixture 待取证。

注意：源码一致 ≠ 二进制身份证明（出货二进制可能有构建差异/内部补丁），方案的"binary fixture 为当前兼容合同"规则仍然必要，本证据只是把漂移风险从"未知"降为"源码层已排除"。

## 3. 残留观察（P3，不阻断）

1. §2.1 拓扑图 L114 "controller WSS：client/environment/stream 多路复用" 是架构层描述（client/environment/stream 三个概念均各自有证据），非 wire 字段声明；wire 层已由 §6.1 修正。可在后续文档维护时加"概念示意"注脚，非必须。
2. §9.1 L529 "前三组 host 数值"的指代（segment/idle/refresh-backoff，不含 cursor header）依赖读者对照 L527-528 的枚举顺序；cursor 的 host/controller 区分已在 §6.2 L368-369 单独限定，语义无错，仅措辞可再显式一点。
3. 建议在 Phase 0 开工时把本轮 §2 的事实（tag 存在、107 提交、模块 diff 仅测试 2 行）回写进 §3.1，把"若 tag 仍不存在"分支标注为已排除（保留该分支作为未来版本的通用程序无害）。

## 4. 最终判定

- 15 类内容形状声明（第一轮 #1–#15）中 14 类维持 🟢，#13（environment 绑定）已由事实错误改为显式"闭源契约 + Phase 0 必取证"，评级从 🔴 升为 🟡（待证而非错误）。
- 未出现新的无样本声明；§6.2/§9.1 新增的全部"已证实"标注（cursor header、五常量、idle、两步端点）均能对应第一轮的源码 dump。
- 凭据红线、fail-closed、先探针后接线、复制白名单/黑名单结构未受修订影响。
- **Phase 0 可以开工**；其 Gate（1–12，特别是任务 3 的 environment 绑定与 cursor 实证）是本方案剩余不确定性的唯一收敛路径。
