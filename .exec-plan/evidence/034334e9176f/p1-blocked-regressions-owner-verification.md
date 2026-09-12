# P1 blocked 真机回归 owner 验证收口（2026-09-13）

三个 P1 真机回归项在 2026-09-12 执行中因连锁缺陷标记 blocked。阻塞链的修复
（unopened-session 投递、replay-free eventId、Claude watcher dedup/baseline）均已
实现并通过自动化测试；owner 在后续真机轮次中逐项复测通过，最后一项离线 Topic
合并测试于 2026-09-13 确认通过。全部为 owner 手工真机验证，self-attested，不可重放。

## p1-session-topic-regression（离线 Topic 合并）

- 原失败（2026-09-12 13:34 CST，codex session 01a093bb）: 只有 iOS Web App 打开过的
  session 才收到通知；未打开的 Claude/Codex session 无通知。
- 修复链: p1-unopened-session-push-review-fix（已 done）→ p1-replay-free-event-id
  （已 done）→ p1-claude-watcher-dedup-baseline（已 done，真机回归已过）。
- owner 复测（2026-09-13）: 「iPhone 已安装 Home Screen Web App；通知开启；手机真正
  断网同一 session 连续完成多个 turn，随后恢复网络 Push Service 尚未交付的消息只交付
  该 session 最新一条；普通锁屏不算断网——已经测试通过了✅」
- 结论: 真机矩阵 §7 第 4 项（离线合并）PASS。resolved-via:p1-claude-watcher-dedup-baseline-regression

## p1-unopened-session-push-review-fix-regression（未打开 session 投递）

- 原失败: 同上（05:34 轮）。
- owner 复测（2026-09-12 14:12 CST）: session E/F 从未在 iOS Web App 打开，Mac 端
  发送后 iOS 端能收到消息（该轮暴露的重复通知/历史回灌为独立缺陷，由
  p1-claude-watcher-dedup-baseline 修复）。
- owner 复测（2026-09-12 14:33 CST）: 「重新打开 iOS web App……mac 端测试了几条消息，
  iOS 端能正常收到通知，没有异常通知和多余的通知✅」
- 结论: 未打开 session 投递 PASS。resolved-via:p1-claude-watcher-dedup-baseline-regression

## p1-replay-free-event-id-regression（replay-free eventId 载荷）

- 原失败（2026-09-12 13:57 CST）: 未打开 session 通知显示「cordcode 推送数据错误」。
- 修复: 2bf41b2 fix(web-push): preview replay-free Codex turns + eventId 派生修复
  （p1-replay-free-event-id-impl，已 done）。
- owner 复测（2026-09-12 14:12 起）: 「推送数据错误」通知不再出现；14:33 轮确认无
  异常通知。
- 结论: eventId 载荷真机 PASS。resolved-via:p1-claude-watcher-dedup-baseline-regression

## 原始记录来源

- codex session: ~/.codex/sessions/2026/09/12/rollout-2026-09-12T11-48-56-01a093bb-897b-7472-a75b-29be66874e06.jsonl
- owner 2026-09-13 离线测试确认: 本文件开头引述（ZCode 会话接手时收到）。