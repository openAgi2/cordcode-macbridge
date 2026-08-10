package gobridge

// catalog_redact.go 提供 catalog 可观测性日志的脱敏 helper（Phase 7 §444：不记录绝对文件路径）。
//
// list_sessions 的 v2/v1 日志原本直接打 "directory", dir（agent workDir / 客户端 cwd），它是绝对
// 路径，会泄漏 owner 的 home 结构与项目绝对位置（如 /Users/<name>/Projects/<repo>）。§444 明确
// 禁止记录绝对文件路径、标题正文、用户内容或认证信息。
//
// redactDirForLog 只保留路径末段（workspace 名），满足「记录到哪个 scope/workspace」的操作需求，
// 同时不泄漏绝对前缀。这与 handlers.go:1463（displayName := filepath.Base(realDir)）和
// opencode-proxy.go:304/348 的既有 basename 脱敏惯例一致——不引入新的脱敏策略，只把既有惯例
// 显式应用到 catalog 日志站点。

import "path/filepath"

// redactDirForLog 把目录脱敏为末段（basename）用于日志。空串原样返回（表示「无 dir / 非 cwd-scoped」，
// 如 Grok session/list）。绝不返回绝对路径。
func redactDirForLog(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}
