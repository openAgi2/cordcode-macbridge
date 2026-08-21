package codexweb

// diagnostics.go —— 版本/transport/能力/失败原文诊断（§5.2/§6.2/§11.2）。
//
// 初始化记录 CLI version、app-server version、capabilities；stable 与 experimental
// fixture 分开；未知版本只读 probe，能力逐项开关，fail closed。
