package codexweb

// events.go —— agent 级有序事件泵（设计 §5.2，参考 dsh-web 事件泵组织、非 DSH 语义）。
//
// Phase 0 通知分级（§7.1）：thread/started、thread/status/changed 全局；turn/*、item/*
// 仅订阅连接且不重放。订阅路径 = thread/start/resume/revert 自动 attach；
// 同 daemon 多连接 resume 无 writer 冲突。
