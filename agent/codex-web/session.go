package codexweb

// session.go —— thin thread binding + turn actions（§5.2/§7）。
//
// turn 语义（Phase 0 实测）：steer 必填 expectedTurnId（turn/start 响应的 turn.id）；
// interrupt 必填 turnId；turn/start 响应先于 active-turn 注册（同毫秒操作报
// no active turn）；terminal 只认 turn/completed（completed/failed/interrupted）。
