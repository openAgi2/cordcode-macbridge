# Grok Build Leader 模式开关设计 v9 第九轮机械闭合复核

- 日期：2026-08-29
- 对象：`docs/2026-08-28-grokbuild-leader-mode-design.md` v9（1490 行）
- 文档 SHA-256：`11261954b887f743894723633aaf442a8ca7a9afd0ad6b440636d4eb81118287`
- 范围：严格按 R8 的 2B + 8M + 2S 有限清单做机械复核；未重新开启全量架构评审
- 操作：只读源码与文档；未修改设计稿，未构建、未测试

## 1. 来源核对

评审开始与写报告前两次来源门结果一致：

```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge
分支=main
提交=032fdd8105ce41d4e41063922eb8eae39aaed0e5
未提交状态=设计稿及 r1–r8 评审报告均为未跟踪文档；无源码改动
任务预期分支=main

配套仓库路径=/Users/jacklee/Projects/grok-build
配套分支=main
配套提交=9684fa3cdbf2995e30ea8b9b637f1db008f144fc
配套未提交状态=干净
```

两仓 HEAD 与指定 pin 完全一致，无来源漂移。`git diff --check` 对 v9 设计稿通过。写入本报告后 Mac 仓新增本文件这一项未跟踪文档。

## 2. R8 十二项机械对照

| R8 项 | v9 落点 | 结果 |
| --- | --- | --- |
| B1 catalog 快照遮蔽 | §3.5.2-7 采用现成 `FenceBackend`，披露 cursor stale；G7 有 running→release→unknown、unknown→reclaim→running 与 cursor stale | **部分闭合**：遗漏正常 terminal/F-7 真断开的 fence 测试，且正文只列“正常 terminal”，见 R9-B1 |
| B2 closing 语义 | `isKnownActive = running || closing`；G8-⑤ 覆盖既有域 | **闭合** |
| M1 终态事实/行号 | relayEvents 两处均改为 `turn_completed`；cleanup 改 `857-883` | **闭合** |
| M2 范围/功能面/编号 | §3.2 补 `types.go`；§4 改三处行为面；§15 改 G5–G8 | **主体闭合**；Phase 1/§7.4 仍残留 T1–T30，见 R9-M2 |
| M3 假时钟 seam | §3.5.2-3 冻结纯 helper + 短接线测试；G5 不跑 60 秒 | **主体闭合**；helper 伪代码有一句反向歧义，见 R9-M3 |
| M4 locator 节边界 | 正文冻结边界；T31/T32 | **闭合** |
| M5 symlink inode | 正文双重复核；T23 覆盖同路径同内容不同 inode | **闭合** |
| M6 备份碰撞 | 纳秒时间戳 + UUID + exclusive create；T10 碰撞断言 | **闭合** |
| M7 非法 Bool | 正文裁决 F1；T33 | **闭合** |
| M8 Phase 0 门 | §0.2 唯一权威；§15 明确不单独授权 | **闭合** |
| S1 历史非规范性 | 头部与 §12 前均声明 §0–§11 才是规范 | **闭合** |
| S2 结构化日志 | §3.5.2-8 冻结字段 | **部分闭合**：三值 outcome 与 bool API 矛盾，见 R9-M1 |

## 3. 分级发现

### B（阻断）

#### R9-B1：catalog fence 仍漏掉真 source 断开分支，正常 terminal 也没有缓存命中回归

**核查结论：R8-B1 仅部分闭合。**

§3.5.2-7（约 756–775 行）规定在 claim、release、**正常 terminal** 后 fence，但没有把真 source 断开的 F-7 `markIdle` 纳入。实际 `go-bridge/handlers_relay.go:225-243` 在 armed turn 的 leader 断开 defer 中执行 `markIdle` 并广播 aborted + idle；若此前 Grok catalog 已缓存 `running`，没有 fence 时下一次 list 仍可复用最长 10 分钟的旧富化快照。实时 F-7 事件收口不能证明后续 catalog page-0 已摆脱旧 `running`。

同时，G7 只验证 release/reclaim 两向 fence；G8-①虽有正常 `turn_completed`，但只断言 claimGen/idle 不被覆盖，没有预热 catalog、page-0 重建和 cursor stale 断言。因此正文声称的 normal-terminal fence 也未被测试冻结。

**最后闭合要求：**把 fence 规则改为 Grok registry 的四类有效状态变化——claim running、正常 terminal→idle、真 source 断开→idle、self-cancel release→unknown/delete——成功后均 fence。G7/G8 增加两个预热 `running` 快照用例：正常 terminal 后 page-0=idle；真 source 断开后 page-0=idle 且 F-7 仍只产生既定 aborted/idle；两者旧 cursor 均 stale。该修订仍只调用现成函数，不扩大文件范围。

### M（必改）

#### R9-M1：`releasePassiveClaim` 的 bool 返回值无法无竞态地产生三值 `registryOutcome`

§3.5.2-2 约 714 行仍冻结 `releasePassiveClaim(sessionID, gen) bool`，而第 8 条要求日志输出 `registryOutcome=deleted|unknown|noop`。`bool` 只能表达成功/失败；调用方若 release 后重读 registry 推断 deleted/unknown，会引入竞态且无法可靠区分“被其他更新替换后的 no-op”。

应直接冻结 typed outcome，例如 `passiveClaimReleaseOutcomeNoop/Deleted/Unknown`；`claimReleased = outcome != noop`，是否 fence 也以 outcome 判定。G7 覆盖三种返回值与对应日志，禁止二次读 registry 猜结果。

#### R9-M2：新增 T31–T33 后，实施拆分和构建表仍写 T1–T30

- §7.4 manager 行约 1101：`T1–T30`；
- Phase 1 约 1121：`T1–T30 单测`。

二者必须改为 T1–T33。§3.2/§0.3/§8 中“registry 第三态”也应改为“新增 unknown 状态/第四状态”，避免开发 agent 误以为 closing 不在域内；§10 的 `types.go:227-228,243-377` 应改为能覆盖 closing 的 `:226-230,243-377`。

#### R9-M3：纯 helper 的括号说明可能被实现为每次负样本重置计时

§3.5.2-3 约 664–668 行写“负样本→置零后记 now”，字面上像是每次负样本先清零再记 `now`，会导致计时永远达不到 60 秒；同节前文的正确规则是“负样本且 `firstNegativeAt` 为零时才记 now，连续负样本保持原锚点；正样本清零”。应按此明确改写，并让 G5 直接断言连续第二/第三个负样本不移动锚点。

#### R9-M4：audit-plan manifest 未登记新增 TOML 内容形状

T31/T32/T33 在测试表中已正确标为 synthetic，但 §3.3-9 的样本 manifest 仍只列到 T27–T30 与 symlink。为维持文档自己的 manifest 完整性，补两行：

- 表/子表/数组表边界 + 无尾随换行（T31/T32）：synthetic；
- 合法 TOML、非法 Bool 类型（T33）：synthetic，并注明只冻结 fail-closed 行为。

这不要求新 wire/API 样本。真实本机 config、官方 canonical fixture 与 synthetic 的等级边界仍正确，audit-plan 无其他未背书内容形状。

### S（建议）

#### R9-S1：历史章节中仍有旧“两态/第三态/下一轮”原文

§12–§20 已明确为非规范性审计记录，因此这些旧文本不再阻断实施。建议在 v10 的 §20 处集中写一条“R9 机械闭合已通过”，不要继续逐行修订旧历史表；规范正文只需清掉 §0–§11 中的 stale 术语。

## 4. 五维结论

### 事实核查

R8 的 `closing`、relayEvents 两个 `turn_completed`、cleanup 行号、catalog TTL/fingerprint/fence 源码事实均已正确写入。唯一未闭合的事实到行为映射是：真 source 断开同样执行 registry `markIdle`，却没有进入 fence 规则与缓存测试。

### 设计闭合性

`isKnownActive`、generation CAS、两类 release、纯 helper 方向、TOML/symlink/备份边界均已达到可实施粒度。剩余设计缺口是四类 Grok registry 状态变化未统一 fence，以及 release outcome 类型未闭合日志契约。

### 受控 Go 改动

无需新增协议客户端、codec、follower 写方向或删除 tailer；diff 仍可严格保持 D-G1=`grokbuild.go`、D-G2=`handlers_relay.go + types.go`。R9-B1 只是把同一个现成 fence 补到已存在的 F-7 markIdle 分支，不扩大架构范围。

### 纪律与样本

source-first、宿主拓扑先证、fail-visible、可逆、防偏航和 §0.4 不适用项均保持正确。新增 TOML 用例已经明确为 synthetic；只需同步登记到 manifest，不存在新的外部 wire/content shape P0。

### 内部一致性与可交接性

v9 已完成绝大多数有限闭合项，但 T1–T30 残留、bool/三值矛盾和 source-disconnect fence 缺口仍会迫使开发 agent 自行猜接口与测试范围。因此当前尚不能 APPROVE。修订只需针对本报告 1B + 4M 做一次文字/测试矩阵机械核对，不再进行全量评审。

## 5. 总结论

**修改后通过。当前暂不交开发 agent，不启动 Phase 0。**

最终必改清单：

1. fence 覆盖 claim、正常 terminal、真 source 断开、self-cancel release 四类状态变化，并补 normal/F-7 预热缓存测试；
2. `releasePassiveClaim` 返回 typed outcome，统一日志与 fence 判定；
3. T1–T30 改 T1–T33，并清理规范正文的第三态术语/错误源码范围；
4. 明确连续负样本不移动 `firstNegativeAt`；
5. manifest 登记 T31–T33 synthetic 样本。

完成以上有限项后，下一次只需逐行对照本清单；全部命中即可 **APPROVE**，无需再次扩展评审范围。
