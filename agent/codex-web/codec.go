package codexweb

// codec.go —— 官方 Thread/Turn/Item 事件 → core.Event（§5.2/§7.1 红线）。
//
// 红线：turn/started 唯一开始真相（mid-turn attach 场景由 status/changed(active)+item/started
// 推导开始事实）；(threadId,turnId,itemId) 为 reducer 身份；delta 与 completed snapshot
// 不双发；禁止正文相似度去重；未识别通知记录 method/version 不崩溃。
