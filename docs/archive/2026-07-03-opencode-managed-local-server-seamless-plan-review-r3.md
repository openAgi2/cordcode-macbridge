# 第三轮评审：OpenCode managed local server 无缝接入方案（二次修订版）

> 评审对象：[docs/2026-07-03-opencode-managed-local-server-seamless-plan.md](../2026-07-03-opencode-managed-local-server-seamless-plan.md)（二次修订版，788 行）
> 评审日期：2026-07-03
> 前序：[首轮](2026-07-03-opencode-managed-local-server-seamless-plan-review.md) / [第二轮](2026-07-03-opencode-managed-local-server-seamless-plan-review-r2.md)

## 0. 总体结论

**本轮是一次 sign-off 倾向的评审。** 二次修订把 R2-1~R2-10 全部落到了具体条款，没有任何一项停留在口号层面；并且作者抓住了一个我本该在第二轮点破的点——"Terminal 里跑 `osascript` 不能代表 CordCode Link 的 TCC 行为，因为 TCC 的发送方是发起进程"——并据此把 DQ-1 的方法论写对了（必须用 CordCode Link 同签名上下文或临时开发分支 harness）。

**没有新的阻断项。** 方案可以进入开发，前置条件是 owner 授权并完成 DQ-1 / DQ-2 两项硬 gate。

下面只有一条**值得在跑 gate 之前顺手做的优化**（它有可能让 DQ-1 的 TCC 风险直接消失），以及几条实现阶段的 minor 契约/数值澄清。不再需要第四轮评审——剩下的都是"开发中顺手处理"或"owner gate 结果回来后再定"。

### 各轮意见状态总览

| 轮次 | 项数 | 本轮状态 |
| --- | --- | --- |
| 首轮 B1–B3 / S1–S5 / D1–D8 | 11 | 全部落地（B1 部分采纳，理由清晰） |
| 二轮 R2-1 / R2-3 | 2 | 提升为 DQ-1 / DQ-2 硬 gate，方法论正确 |
| 二轮 R2-2 / R2-4 / R2-6 / R2-5 / R2-7 / R2-8 / R2-9 / R2-10 | 8 | 全部落到具体条款与测试 |

---

## 1. 本轮最值得关注：先把"Desktop 是否热重载配置"测了，可能让 TCC 问题直接消失

### 观察

整套 graceful quit + reopen 机制（§7.542-556）和它的 TCC 风险（DQ-1）都建立在一个未经验证的前提上：**§7.542 "Desktop 运行中不一定立即重读"**。这句把"Desktop 不会热重载 `opencode.global.dat`"当成了事实，但**它和 DQ-2 一样是一个外部 App 行为假设，应当被实测而不是断言**。

如果 OpenCode Desktop 在运行时 watch `opencode.global.dat`（很多 Electron 应用的 `app.getPath`/文件 watcher 行为），那么 CordCode 写完配置后 Desktop 会自己切到 managedURL，**根本不需要 quit + reopen**——于是：

- DQ-1（TCC）整个失效：不发 Apple Event，就不存在 Automation 授权框。
- "中断用户正在进行 turn"的代价（§7.555）消失。
- orphan / 启发式误判（§11.717-718）大幅简化。
- §14 的"无需手动重启 Desktop"在"已运行"分支里天然成立。

这是**可能用一次 5 分钟实测换取整条最复杂路径消失**的交易，性价比极高。

### 建议

把"Desktop 是否热重载配置"作为 DQ-2 的**第一步前置子项**（或独立 DQ-0），先于 quit/reopen 实测：

```text
DQ-0（新增，先做）：
1. 启动外部 opencode serve（带 Basic Auth），如 http://127.0.0.1:4198。
2. 在 OpenCode Desktop 运行状态下（连着 vlocal），直接写 opencode.global.dat
   把 currentSidecarUrl / defaultServerUrl 改成 http://127.0.0.1:4198。
3. 不 quit Desktop，观察 5~30 秒：
   - Desktop 是否自动切到 4198（用 lsof ESTABLISHED / Desktop 网络面板确认）？
4. 若热重载成立 → 本方案优先走"只写配置、不重启 Desktop"，graceful quit 降级为
   热重载失败时的兜底；DQ-1 的 TCC 风险面大幅收窄。
5. 若热重载不成立 → 维持现有 graceful quit + reopen 方案，继续 DQ-1/DQ-2。
```

这不是阻断项——即使不测、维持现有 quit+reopen 方案也能推进；但**跑 DQ-0 的成本远低于它可能省下的风险**，强烈建议在 owner 授权实测时把它插在 DQ-1/DQ-2 之前。

---

## 2. 实现契约缺口：`previousURL` 没暴露给重启决策点

### 问题

§7.546 写：

> Desktop 是否"已经连接 managedURL"只能用启发式判断：在写配置之前读取现有 `previousURL`（**现有 `RuntimeManager.configureOpenCodeDesktopSettings` 已有该变量**）...

但实测核对：

- `RuntimeManager.swift:730` 的 `configureOpenCodeDesktopSettings(...)` 返回 `Void`。
- `previousURL`（`RuntimeManager.swift:752`，`let previousURL = server["currentSidecarUrl"] as? String`）是**函数内 local**，仅在 `:767` 喂给 `preferredSources`，**不返回、不暴露**。

也就是说，重启决策点（bootstrap / coordinator）**拿不到** `previousURL`。方案"已有该变量"的措辞字面为真但会误导实现者。

### 建议（二选一，写到 §7）

- **改签名**：`configureOpenCodeDesktopSettings` 返回一个 `(previousSidecarURL: String?)` 或 `didSidecarChange: Bool`，决策点据此决定是否触发 graceful restart。
- **或前置读**：决策点在调用 `configureOpenCodeDesktopSettings` 之前，自己先读一次 `opencode.global.dat` 的 `currentSidecarUrl`，传给决策逻辑。

无论哪种，把"previousURL 从何而来"在 §7 写成一个明确的契约，而不是依赖一个 void 函数的内部 local。

---

## 3. Minor：实现阶段的数值/范围澄清

- **R3-3 bootstrap 超时未给数**：§7.506 "若 managed server 在**限定时间内**失败"——"限定时间"是关键 UX 参数。太短，慢 Mac 上冷启动误判失败；太长，Claude/Codex 启动被拖。建议给一个具体上限，例如"managed server bootstrap 等待 ≤ 5s；超时后放行 bridge 启动（Claude/Codex 先可用），OpenCode 继续异步重试并最终把状态从 `starting` 推进到 `running` 或 `unavailable`"。

- **R3-4 DQ-1 的"开发机通过"不等于"终端用户体验"**：TCC 的 Automation 授权绑定到发送方的签名身份/cdhash。开发机很可能历史上给过某个 CordCode 上下文授权，第一次不再弹；但终端用户的干净机器上仍会弹。ad-hoc 签名身份不稳定时尤其如此（这点 §Gate DQ-1.110 已经点到，但只说了"重建后再弹"，没说到"不同机器首次必弹"）。建议在 §12 step 12（真机端到端验收）里**显式包含"干净账户首次触发 Desktop 自动切换，确认无 TCC 弹窗"**，把 DQ-1 的 dev 机结论在真机验收里复验一次。

- **R3-5 DQ-1 helper 路径去重**：§Gate DQ-1.108 给了两个选择："临时开发分支中的最小 `DesktopProcessController` harness" 或 "与 CordCode Link 同 bundle id / signing mode 等价的最小 helper"。后者有坑——两个 App 抢同一个 bundle id 会引发 LaunchServices 冲突，而且 helper 的签名身份仍要复刻 ad-hoc 才有 TCC 等价性，并不比"在 CordCode Link 临时分支里加个触发按钮"更省事。建议把"临时开发分支 harness"明确为**首选且唯一推荐**路径，把 minimal helper 降为"如确实需要时的备选"，避免实现者陷入 bundle id 冲突。

---

## 4. 已经做对的、值得记下的几点（不再要求改动）

- **§7.509 的 generation 顺序写得很精准**："`ensure managed server` → `write/sync Desktop config` → `derive RuntimeConfig` → `start/restart bridge`" + "只有 resolved endpoint 字节值变化时才触发 bridge restart"。这条不变量把 managed crash→同端口重启（endpoint 不变，不 restart bridge）和 managed 换端口（endpoint 变，restart bridge）两种情形自然区分开了，逻辑无误。
- **§11 失败表把 `previousURL` 启发式的两种误判各列一行**（`:717` 多余 restart / `:718` 漏判 scope 分裂 + `Desktop sync suspect`），这是少见地把"启发式可能错"诚实地写进验收判定，而不是假设启发式永远对。
- **§14.787 的诚实条款**："若 Gate DQ-1 或 Gate DQ-2 在开发前实测不通过，本节必须先重写。不得在已知需要 TCC 手动授权或 Desktop 不服从 managedURL 的情况下继续声称'无需手动'。"——这是整份方案最重要的一句话，它把"目标"和"事实"的优先级摆对了：事实优先。
- **§13 Reviewer 检查点扩到 17 条**，新增的 13/14（TCC 同签名实测 / 冷启动服从 managedURL 实测）正是本轮 gate 的对应物，reviewer 能据此复核。

---

## 5. 结论与建议状态

**方案已达到可进入开发的成熟度。** 不需要第四轮文档评审。

进入开发的条件：

1. **owner 授权并完成 DQ-0（强烈建议新增）→ DQ-1 → DQ-2**。三项实测任一不通过，回到 §1/§14 重定目标（按 §14.787 的诚实条款执行）。
2. 实现阶段顺手处理 §2 的 `previousURL` 契约、§3 的三个 minor。
3. 真机端到端验收（§12 step 12）必须包含"干净账户首次自动切换无 TCC 弹窗"这一条（§3 R3-4）。

**建议把本文档状态从"已按第二轮评审修订；进入开发前仍需完成 Desktop 两项 owner 授权实测 gate"改为"评审通过（conditional on DQ-0/DQ-1/DQ-2 gate 结果）；进入开发前完成 gate 实测"。** 三轮评审到此收口，剩余风险都已显式归到 owner gate 或真机验收，不属于文档层面的未决项。
