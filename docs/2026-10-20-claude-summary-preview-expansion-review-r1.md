# Claude 预览扩容方案 v1 独立评审 R1

评审员：独立 plan-reviewer；日期2026-10-20。结论：REVISION_REQUIRED，1阻塞/1建议。报告由主会话按评审员返回内容存档（评审员只读）。
对象：docs/2026-10-20-claude-summary-preview-expansion-plan.md v1，SHA256=5558e21f834ba2eeab3769124321e8f148f27db18d3b6451078e6853ade3ab19。

来源（评审员独立亲核两次，无变化）：

|仓库路径|分支|完整HEAD|未提交|预期/配套/能力|
|---|---|---|---|---|
|/Users/jacklee/Projects/cordcode-macbridge-claude-long-session-projection-loading|feat/claude-long-session-projection-loading|088e4f532791722b6508ee6185cb1b0171364900|干净|任务Mac，配下行iOS；Claude summary扩容，其他backend缓存保持|
|/Users/jacklee/Projects/cordcode-ios-claude-long-session-projection-loading|feat/claude-long-session-projection-loading|8a813c47b4303916eef83dd2f855d5c8963ca397|干净|任务iOS，配上行Mac；同wire与Claude局部缓存revision|

M相对业务基线7eba7e33d7a3852fff86bd4fd44b90a53cb3824e仅新增方案文档；任务预期分支均为feat/claude-long-session-projection-loading。按plan-contract-v1.1 fresh定向评审，未改代码、build/test或UI；截图、日志计数未独立重取，不冒充实测。

## F-01 阻塞：四字节Unicode预算低估

M/go-bridge/projection_window_v2.go:229 descriptorPayloadBytes按RuneCount*3计量，四字节码点会低估。拟议1024+1024+256阈值下151个completed turn、无例外时，账本151×2304×3=1,043,712B，不触发1MiB降级；正文实际151×2304×4=1,391,616B。151在允许≤256窗口内。这是静态确定反例，不是运行实测。

最小修订：现有预算函数改为实际UTF-8字节或不会低估的上界；预算对象仍是摘要净载荷，保留例外与老轮整轮降级；E-2加入151个四字节Unicode反例，验证预算/降级顺序/保留正文。修订后定向复核。

## F-02 建议：内容patch后的缓存往返

E-3除state-only外补合法upsertTurns或partOps→flush→restore→anchor，断言正文/状态/generation不回退、触及token失效、未触及行summary保持、最终形态正确。I/ProjectionRestoreThenIncrement.swift:594已有token失效；恢复读row.turn而非从旧descriptor重建正文，所以不能仅因保descriptor就判内容丢失，也不应一律清所有触及摘要行。

## 其余核验

- 128→1024/head64→256为内部策略，wire string无解码字数限制；10KiB×256+4KiB低于4MiB。
- 前缀去重保留较长brief、不丢字符；不同首末段继续保留。
- schema3保持、可选Claude revision只失效Claude方案可行，覆盖历史误标full种子。
- I/ProjectionStore.swift:1362-1372确实漏summaryRows；pull full:1213显式清。实施保持所有采用full替换入口一致。
- M/projection_anchor_newer.go:134-147/:232-270 token来自full turn，不含摘要策略，单改字数不足以刷新旧种子。
- 原实现后测试门E-2…E-6仍为pending，不构成方案额外阻塞；不增加owner产品裁决。
