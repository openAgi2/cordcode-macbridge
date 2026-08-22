# Codex Web Phase 5 部署核对证据

执行时间：2026-08-22 12:00（Asia/Shanghai）。

## Mac Release

- `pgrep -fal 'CordCodeLink|cordcode-bridge-runtime'`：仅 `/Applications/CordCodeLink.app` 的 launcher 与内嵌 runtime 命中。
- runtime 参数包含 `-port 8777` 与 `-drivers claude,codex,codex-web,grokbuild,dsh-web,opencode-web`。
- `lsof -nP -iTCP:8777 -sTCP:LISTEN`：内嵌 `cordcode-bridge-runtime` 在 `*:8777` 监听。
- 内嵌 runtime 版本：`cordcode-bridge-runtime 0.1.0 commit 4e28845d85d7`。

## iOS 真机

- `xcrun devicectl list devices`：一台物理 iPhone 为 connected。
- `scripts/run.sh device --device <connected-physical-device>`：Debug device build 成功、安装成功、`org.openagi.cordcode` 启动成功。
- 未运行 UI test、snapshot test 或视觉自动化。

## Bridge 运行日志

Release 日志在真机启动后出现以下独立 `backendId=codex-web` 请求：

- `list_models`
- `get_session_projection`
- `set_observation_scope`
- `projection_shadow stage=hydrate_commit`
- `projection_rpc stage=hydrate_ready`

日志中的 device/session/epoch 标识未复制到本证据文档。上述事实证明 Release 与真机接线可达，不替代 owner 的 14 行视觉/交互验收。
