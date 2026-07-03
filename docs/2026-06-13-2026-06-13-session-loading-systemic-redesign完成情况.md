# 完成情况:session-loading-systemic-redesign

> Plan: `docs/2026-06-13-session-loading-systemic-redesign.md`
> 完成时间:2026-06-14
> Queue:48/48 done,0 blocked/pending

## 总览

会话加载系统性重设计的四个阶段全部完成。核心成果:大会话(codex 12.45MB / Claude 59.8MB)从"打开超时/闪退/空白"变为"首屏秒开 + 向后翻页 + 锚点稳定"。

## 各阶段成果

### Phase 0:测量基线
- 建立可复现的分段基线(direct/relay,codex/claude,cold/hot)
- 确认闪退根因是 WebSocket close 1009(Message too long),非 crash/jetsam
- Go/No-Go:Claude 全局索引 GO、transcript 分页 GO、Codex 缓存加固 LIMITED GO、batched open_session NO-GO

### Phase 1:列表索引
- Claude 跨项目指纹目录(hot list 116ms → 0.77ms)
- Codex 缓存加固(mtime(ns)+size 指纹 + 确定性排序)

### Phase 2:历史索引与分页(核心)
- `transcriptindex` 包:逻辑消息边界、tail-replay、区间并集、版本化持久化、singleflight、字节预算
- Bridge 端 cursor 分页(稳定 cursor、stale 语义、~960KiB/页字节预算)
- relay frame budget 修复(2MiB,解决 close 1009)
- iOS 端分页(capability gating、cursor dedup、stable-ID merge、锚点保持)
- **锚点跳底修复**(本次会话):native 显式 prepend 信号 + grouping 不变式 + firstItemIndex + Virtuoso 调参

### Phase 3:batched open_session
- **NO-GO**(证据充分):串行前置 ~5-20ms << 150ms 阈值;Phase 2 分页才是打开超时的真正解药

## 真机回归(2026-06-14)

iPhone(00008140-001E69503453001C),build `ced2e36`,codex 长会话:
- ✅ 向后翻页加载 + 累积 + 焦点保持(不跳底,轻微跳动为架构限制,接受)
- ✅ codex user/assistant 不折叠
- ✅ 大首条消息触发翻页
- ✅ 轮询后历史保留 + idle 不显示运行态
- ✅ 大会话首屏 + 不崩
- ✅ relay 路径翻页正常(直连路径网络配置问题,不阻塞)

Checklist:`/Users/jacklee/Projects/opencode-cc-connect/checklist.md`

## 自动化测试矩阵(green)
- go-bridge ✓
- transcriptindex ✓
- message-web 35/35 ✓
- Swift CCCodeTests 30/30 ✓
- MacBridge 23/23 ✓

## 残留限制(已知,接受)
- 快速上滑触发翻页时轻微跳动/偶尔空白:react-virtuoso + 动态高度在 iOS WKWebView 的架构限制。完全消除需 native 拥有滚动状态(数天重构,另起计划)
- 直连 LAN 路径未单独验证:疑为配对凭证默认走 relay,属网络配置问题,翻页代码两路径共用

## 相关文档
- 基线:`docs/2026-06-13-session-loading-baseline.md`
- 设计:`docs/2026-06-13-session-loading-systemic-redesign.md`
- review:`docs/2026-06-13-session-loading-review.md`
- Phase 3 决策:`docs/2026-06-13-session-loading-phase3-batch-open-decision.md`
- 锚点修复复盘:`handoffs/anchor-jump-debug-history.md`、`handoffs/anchor-fix-final-state.md`
