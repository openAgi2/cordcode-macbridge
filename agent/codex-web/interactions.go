package codexweb

// interactions.go —— 审批/提问/elicitation registry（§5.2/§7.2/§7.3）。
//
// Phase 0 实测语义：command approval 的 availableDecisions 未声明 experimentalApi 也物理到达
// （additionalPermissions 被剥除）；decision 枚举 accept/cancel/acceptWithExecpolicyAmendment，
// cancel → turn 终态 interrupted；requestUserInput 批结构按题 id 应答
// {qid:{answers:[..]}}；permission approval 为 RequestPermissionProfile →
// GrantedPermissionProfile+scope（无 availableDecisions）。
