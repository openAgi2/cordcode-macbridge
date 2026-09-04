# Claude Code backend 官方能力收敛升级设计 第二轮评审报告

- 评审对象：`docs/2026-09-04-claudecode-official-capability-upgrade-design.md` **v2**（2026-09-04）
- 对照基线：`docs/2026-09-04-claudecode-official-capability-upgrade-design-review.md`（v1，修改后通过）
- 评审日期：2026-09-04
- 评审方法：逐项对照第一轮 B1–B3 / M1–M6 / S1–S10 与两项裁决，**不采信 §11 采纳记录自述**；对 v2 新增断言做源码/文档/本机只读复核。未跑 Phase 0 探针（硬门仍属实施前置）。
- 评审边界：纯文档评审，未改设计稿、未改代码。

---

## 0. 本次评审来源清单（P0）

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=390ed6efe18b793ef5ccd886c007c93ba010e964
未提交状态=修改 .exec-plan/state/plan-dfb27dce3681.json（与本方案无关）；
  未跟踪 v2 方案、r1 评审、本 r2 评审
任务预期分支=plan/approval-layer 续作（v2 已冻结）
配套 Mac main=/Users/jacklee/Projects/cordcode-macbridge  main  a2200cf4771b7ded4a09577bdcf9599d145d93c1
配套 iOS 实施=/Users/jacklee/Projects/cordcode-ios-plan-approval  plan/approval-layer-ios  dbf0c048359ef3fec5106ed31102847c4f311eb3
iOS main（v2 仅作选择器/abort 现状核验引用）=/Users/jacklee/Projects/cordcode-ios  main  61f67bf63a8a5f6e10e9e9c62fbd9fe36f2236cd
上游 SDK=/Users/jacklee/Projects/claude-agent-sdk-npm/package  0.3.260（claudeCodeVersion=2.1.260）
本机 CLI=PATH 2.1.234；Desktop 会话文件仍见 2.1.258
```

相对 r1：Mac HEAD 未再前进；iOS 配套提交与 v2 §0.2 一致。来源门通过。

---

## 1. 结论

**通过（APPROVE）。**

第一轮 3 阻断 + 6 必改 + 10 建议 + 两项裁决，正文均已实质落地，不是只写在 §11。路线未回潮；信封、生产 spawn、代际矩阵、`initialize.models` 主源、plan 层冲突冻结、Managed 默认关、iOS 能力位先行，均能在对应章节独立读到。v2 仍诚实把未 dump 的成功体标成 [待实测]，没有把评审红线提前写成已证形状。

二轮未发现新的阻断。剩余是 **建议级**：S4 修正里标量优先级把 user/project/local 写反；`Alias` 与 `resolvedModel` 方向不同且当前 wire 不下发 Alias；§0.4 haiku 单行计数不准确；「冲突表」有引用无表。这些不改变 Phase 0 探针设计，可在 v2.1 或探针脚本里顺手改，**不阻塞进入 Phase 0**。

---

## 2. 第一轮条款逐项复核

### 阻断

| 项 | 复核 | 证据 |
|---|---|---|
| **B1 信封** | ✅ 完整 | §3.1 写成 `request` 嵌套 + `control_response.response.subtype`；双锚 SDK `:4285-4291` / 树内 `session.go:829` 与写侧 `:1033-1039`。stdout 缺 `control_response` case、发送必须配对收件，写入 §2.4 / Phase 2.1。全文功能性 `payload` 仅剩 hooks 输入字段（§2.2），不再当控制信封 |
| **B2 spawn** | ✅ 完整 | Phase 0.1 逐字 `baseClaudeInnerArgs`（无 `-p`、stdin 保持打开、同 env）；`-p` 对照明确不作判据；引用 SDK streaming-only。与 `session.go:108-118` 一致 |
| **B3 代际** | ✅ 完整 | §0.3 三段锚 + §3.2 矩阵；PostModelSwitch 在 2.1.234 记「文档级不存在」；Phase 1 观测源改口为只能接 `message.model`；Phase 3 收缩后该事件实际不可得。风险 2 同步 |

### 必改

| 项 | 复核 | 证据 |
|---|---|---|
| **M1 initialize.models 主源** | ✅ | §2.1 新行含 `ModelInfo.resolvedModel`；Phase 1.1 主源 / 1.2 `list_models` dump-first；Phase 0.3 双 dump + 副作用。行号实测：`SDKControlInitializeResponse` 起于 **:3989**（v2 写 :3987，差 2 行，见 R2-S6） |
| **M2 caps.modelCatalog** | ✅ | §2.1 / Phase 0.4：`capabilities[]` 搜字符串 + 实发判据；禁止与「无 list_models」等价 |
| **M3 plan 层** | ✅ 实质落地；「表」是段落 | 删除「在途」；配套改为 `plan/approval-layer-ios`；活会话禁 plan/auto；D5 纯 allow 进 §9.7。冲突内容在 Phase 2 第 3 条，**没有独立表**（R2-S2） |
| **M4 Managed** | ✅ | 默认关；`--settings` 仅自 spawn；无 admin=外部轮询不算失败；本机无 `/Library/.../ClaudeCode/` 落点。Phase 0.5 跨层合并标「无可测环境」不记硬门失败 |
| **M5 iOS 新接线** | ✅ 方向对 | §7.3 能力位先行 / adapter 先 conform / interrupt 缺位保持杀进程。Alias 复用有语义风险（R2-S1） |
| **M6 过强断言** | ✅ | 删除 snapshot 控制 subtype；listSessions 磁盘 API 不升格；interrupt 补 `cancel_queued`；Bearer 改口网关兼容双头；`message.model` 改口尚未接线 |

### 建议 S1–S10

全部有落点。S1 四行进 §5（rename / get_context_usage 标候选）；S2 不订 PermissionRequest；S3 活性=验收硬条件；S4 拆开表述（顺序有误，R2-S1）；S5 三列 + opus/identity；S6 四元矩阵；S7 loopback 降级去向；S8 编码前二选一；S9 三段锚；S10 配对冻结 + 风险 11。

### 两项裁决

均已写入 §11.4、§6 Phase 3.2、§9.6–9.7，与 r1 推荐一致。

---

## 3. 二轮新发现（全部建议级）

### R2-S1. §2.2 标量优先级把 user / project / local 写反了

v2：「Managed > `--settings` > **user > project > local**」。

官方 settings 文档（2026-09-04，高→低）是：

1. Managed
2. Command line / `--settings`
3. **Project local** `.claude/settings.local.json`
4. **Shared project** `.claude/settings.json`
5. **User** `~/.claude/settings.json`

r1 已按此顺序写过。v2 为修 S4 拆开表述时把三层文件顺序颠倒。对 CordCode 选用的 `--settings` 注入**无影响**（它仍压过这三层），但这段现在是错误的官方事实，env/标量探针若按 v2 去读文件会读错层。

**建议**：改回官方顺序；hooks 数组合并的表述可以不动。

### R2-S2. `core.ModelOption.Alias` 与 `resolvedModel` 方向相反，且当前 wire 根本不下发 Alias

- `ModelInfo.resolvedModel`：别名 → canonical（官方例 `'sonnet' → 'claude-sonnet-5'`）
- `core.ModelOption.Alias` 注释：canonical 的 **短名**（例 `"codex"` for `"gpt-5.3-codex"`）——方向相反
- 现有 Claude 目录：`Name=haiku/sonnet/opus`，`Desc=*_MODEL_NAME`（`settings_models.go:41-88`），**不填 Alias**
- `modelItemsForWire`（`go-bridge/handlers.go:1801-1828`）只发 `id=Name`、`name=Desc`，**没有 alias/resolved 键**

v2 §7.3「不另造平行字段、复用 Alias」若按字面实施，会把 canonical 塞进短名字段，或发现 iOS 根本收不到。三列（请求名 / 观测改写名 / 槽位）也没给 `resolvedModel` 留位置。

**建议**：Mac wire 增 optional `resolved`（映射 `resolvedModel`）；槽位短名继续用现有 `id`（haiku/sonnet/opus）或显式 `alias`；观测改写名另键（或后续事件补丁）。不要复用 `Alias` 承载 canonical。

### R2-S3. 「冲突表」被多处引用，正文没有表

§2.4、§5、风险 7、§11.2 都指向「§6 Phase 2.3 冲突表」。Phase 2 第 3 条是合格的**段落规则**（四档白名单、SetLiveMode false、D5 纯 allow、auto-answer 仅缺位回退），但不是表。

**建议**：补四行表（SetLiveMode / D5 / 本地 auto-answer / sessionActive），避免实施时找不到「表」。

### R2-S4. §0.4 haiku 行写成「→glm-5.3-flash（66）」，与当前 DB 不符

本轮只读 `proxy_request_logs`：

| request_model | model | count |
|---|---|---|
| claude-haiku-4-5 | claude-haiku-4-5 | **538**（identity） |
| claude-haiku-4-5 | glm-4.7 | **325** |
| claude-haiku-4-5-20251001 | glm-5.3-flash | 146 |
| claude-haiku-4-5 | glm-5.3-flash | **85**（不是 66） |

这与 v2 自己在 S5 写的「不能假设某别名族总是同一目标」一致，但 haiku 仍被写成单映射。sonnet/fable/opus/identity 行仍成立。

**建议**：haiku 改成多行分布，或删掉不准确的 66；三列展示已经覆盖这个教训。

### R2-S5. Phase 4 要求 Phase 0 dump `rename_session`，Phase 0.1 发送清单没有它

Phase 4.2：「Phase 0 dump 其成功体后评估迁移」。Phase 0.1 只列 `initialize → list_models → set_model → set_permission_mode → interrupt`。

**建议**：要么把 `rename_session` 加进 Phase 0.1，要么把 Phase 4.2 改成「实施该候选前再 dump」，不要两处互相指。

### R2-S6. 小残留

| 项 | 说明 |
|---|---|
| initialize 行号 | 类型在 `sdk.d.ts:3989`，v2 写 :3987（注释上一行） |
| §11.1 B1 落点「风险 10」 | 风险 10 是 list_models 无类型，不是信封；信封在 §3.1 / 风险未单列 |
| `bypassPermissions` 探针 | SDK 注释该 mode 需要 `allowDangerouslySkipPermissions`。Phase 0 的 `set_permission_mode` 四档应单独记录 bypass 是 success 还是被拒——本地 auto-approve 今天不经过 CLI |

### R2-S7. 不要把 `initialize.hooks` 与 `--settings` HTTP hook 混成一套

`SDKControlInitializeRequest.hooks` 是 SDK **callback matcher**（随后走 `hook_callback` 控制请求）。`--settings` HTTP hook 是 settings 层，由 CLI 自己 POST。

`hooks_applied`（`sdk.d.ts:4003-4006`）说的是 initialize **携带的** SDK hooks 集合替换先前 initialize 注册的集合；「request carried no hooks」时该字段缺席。因此 Phase 1 为拿 `models` 而发的无 `hooks` 字段的 `initialize`，**按类型不太可能**打掉 Phase 3 的 `--settings` HTTP hook。v2 Phase 0.3「记录 initialize 副作用（hooks 注册）」容易让实施者把两套 hook 当成一件事。

**建议**：Phase 0.3 加一句「省略 `initialize.hooks` 时，确认 `--settings` HTTP hook 仍触发」作为对照，不必当成覆盖风险硬门。

---

## 4. 内容形状（相对 r1 §5）

| 内容类型 | r1 | v2 处置 | 本轮 |
|---|---|---|---|
| `control_request` 信封 `payload` | 🔴 | 改为 `request` | 🟢 |
| `control_response` 嵌套 | 🟡 | §3.1 写明 | 🟢 |
| `list_models` 成功体 | 🔴 无样本 | dump-first，禁止先写解析器 | 🟡 仍待实测（正确） |
| `initialize.models` | 🔴 漏 | 主源 + 待 dump | 🟡 仍待实测（正确） |
| PostModelSwitch 版本门 | 🟡 | 2.1.234 文档级不存在 | 🟢 |
| 网关改写分布 | 🟡 | 补 opus/identity；haiku 仍简化错 | 🟡 R2-S4 |
| 会话列表真空证据 | 🟡 过强 | 改为无 control subtype + 磁盘 API 不升格 | 🟢 |

未 dump 的形状仍在 §7.1 红线清单里，没有假装已核实。

---

## 5. 进入 Phase 0 的条件

设计层可以停。下一步是 **Phase 0 证据包**（按 v2 §6，spawn 必须 `baseClaudeInnerArgs`），不是再开一轮文档评审。

建议在写探针脚本前顺手改 v2.1（均可一行/一小段）：

1. 标量优先级改回 local > project > user
2. 模型行字段：wire `resolved` ≠ `Alias`
3. haiku 分布按 DB 多行写，或删 66
4. 「冲突表」做成四行真表
5. `rename_session` 与 Phase 0 清单对齐

配对已冻结：Mac `plan/approval-layer` × iOS `plan/approval-layer-ios`。配对未再核前不要改 `agent/claudecode` 或 iOS。
