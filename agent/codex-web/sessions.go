package codexweb

// sessions.go —— thread/list catalog（设计 §7 list_sessions/§5.2 catalog）。
//
// 保留官方排序、cursor、archive/source 语义；请求字段以 Phase 0 样本为准
// （dumps/catalog：cursor 为时间戳字符串、limit 分页、archived 列表）。
