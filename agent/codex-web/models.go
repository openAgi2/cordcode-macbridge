package codexweb

// models.go —— model/list、permissionProfile/list、config/read 只读映射（§7）。
//
// Phase 0 实测：typed Model 无 provider；custom provider 未实现 /v1/models 时 codex 回落
// 内置目录并 warning；config/read typed model_provider + flatten additional 实测为空
// （不含 model_providers）。禁止递归提取 provider 目录、禁止写 config.toml（⛔ 行）。
