# dsh-web 收敛专项 OD 裁决记录（owner 授权代裁，2026-09-23）

- Date: 2026-09-23
- 性质：owner 裁决表的**决定记录**，不是方案修订。方案 v8
  （[2026-09-23-dsh-web-source-first-convergence-plan.md](2026-09-23-dsh-web-source-first-convergence-plan.md)）
  保持冻结，本文只填「决定状态」并给出依据；两文冲突时以方案 v8 的 Gate A 证据门为准
  ——本裁决**不豁免任何样本门**，只解除「等 owner 决定」的阻塞。

## owner 授权原文（2026-09-23，本 session）

> 「看不懂，你直接把计划文档里的任务全部完成吧，我不认为中间有什么需要我裁决，
> 如果你遇到需要裁决的，可以开启 workflow，开启 subagent 帮你调研评审等」

据此：owner 明确授权剩余切片全部实施，OD 由 agent 按方案倾向与「全部完成」指令
代裁，不再回问 owner。凡样本证据与本文预期冲突时，按方案 §0 证据优先级以样本为准
（fail-closed），并在对应切片如实记录。

## 裁决

| # | 决定 | 依据 | 影响切片 |
| --- | --- | --- | --- |
| OD-1 | **A 官方语义**（置顶切 `workspace/pinSession`、归档/恢复切 `archiveSession`/`unarchiveSession` 并纳入 iOS 列表） | 方案倾向 A；「全部完成」要求 S5 实施（B = S5 整体不实施，与指令矛盾）。验收按方案 A 选项：iPhone 置顶在 Mac web 可见且反向一致；归档行双端隐藏、恢复双端可见；与 Mac web 操作互不覆盖 | S5（行 22–25） |
| OD-2a | **A 镜像官方**（运行中 steer、空闲 queue） | 方案倾向 A；依赖 A7 样本（先取证后实施；样本与源码映射冲突时以样本为准 fail-closed） | 发送路径（A7） |
| OD-2b | **B 可见+管理**（纳入 `session/updateQueue` 编辑/撤回） | 「全部完成」取完整产品面；依赖 A3b 样本（updateQueue 编辑/撤回/steer 变更 + steer-unavailable 活体负例） | S3 管理范围（行 13） |
| OD-3 | **B 图片+文件 receipts**（含 `fileUploads.upload` 链） | 「全部完成」取完整产品面；依赖 A4a + A4b 双样本门（文件范围不得靠 image 样本放行） | S4（行 12、100） |
| OD-4 | **A 对齐官方**（`maxMessages:500 + turnWindow`） | 方案倾向 A 且给了可测阈值（records ≤ 500、JSON ≤ 2 MiB、解码 p95 ≤ 500ms，探针可测）；B 无对齐收益 | S1 后续调优 |
| OD-5 | **A 只钉 alpha** | owner 生产座位是 alpha.1、下一目标是 alpha.2（npm alpha 通道已确认有 0.1.7-alpha.2）；rc 矩阵需另起 rc 座位取证，owner 不用 rc 通道，无用户价值。范围外版本维持 fail-closed 可诊断 | 仅 S2 跨版本防御部分（alpha.1+alpha.2 两代）与 rc 矩阵（不做） |

## 与方案门的关系（不豁免声明）

- 本裁决只解除「待 owner 裁决」阻塞；每个切片仍受其 Gate A 样本门约束（A2/A3a/A3b/
  A4a/A4b/A5/A6/A7），缺样本即停该切片，不得凭源码形状直接实施。
- A2 取证座位：npm alpha 通道有 0.1.7-alpha.2（2026-09-23 实查），采用**本地 prefix
  安装 + 另起测试座位**（不动全局 alpha.1、不升级 owner 生产座位）；测试座位先做
  状态隔离核验（只读 session/list——若与生产座位共享 owner 会话/工作区状态，写入探针
  仍只限新建一次性会话，绝不触碰 owner 既有会话）。
- S6（future 池）按方案 §5.6 不在本轮捆绑范围；「全部完成」指 §4 底表的「已支持/切片」
  行四层对齐（方案 §8 完成定义），future 行保持明确再入口。
- owner 真机矩阵（§7.3：iPhone 流式/斜杠命令/goal/排队可见/附件/置顶归档双端一致/
  dsh 重启行）中需要真机的项，agent 侧完成生产 runtime 级验证后汇总成最短 checklist
  交 owner，不冒充已验。
