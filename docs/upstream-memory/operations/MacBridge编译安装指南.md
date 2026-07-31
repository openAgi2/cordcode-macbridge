# CCCode Bridge 编译安装指南

## 架构概览

CCCodeBridge.app 是一个 **SwiftUI macOS 菜单栏应用**，由两部分组成：

| 组件 | 语言 | 路径 | 作用 |
|---|---|---|---|
| Swift Launcher | Swift | `MacOS/CCCodeBridge` | NSApplication 事件循环、菜单栏 UI、进程管理 |
| Go Runtime | Go | `Resources/cccode-bridge-runtime` | WebSocket 服务（port 8777）、agent 调度 |

Swift launcher 通过 `RuntimeManager` 以子进程方式启动 Go runtime，负责生命周期管理（启动、崩溃重启、休眠/唤醒、退出清理）。

产品化后的运行原则：

- **不要手动常驻 `go-bridge`**。`CCCodeBridge.app` 必须是 port `8777` 的唯一 owner。
- `go-bridge` 只能作为 `CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime` 子进程运行。
- 旧的开发态 LaunchAgent（例如 `com.opencode.gobridge.plist`）会抢占 `8777`，导致 iOS 可用但 MacBridge UI 显示崩溃或状态错误。
- `runtime_ready` / `runtime.json` 只代表 Go runtime 已经成功监听 `8777` 并启动 management API；如果 runtime 没有真正监听成功，不应写 ready 状态。

MacBridge 已内置启动自修机制：

- 启动前自动清理旧 `runtime.json`。
- 自动停用指向旧开发版 `go-bridge` 且占用 `8777` 的用户级 LaunchAgent。
- 自动接管旧的 `CCCodeBridge.app` runtime 或仓库开发版 `go-bridge`。
- 如果 `8777` 被未知进程占用，MacBridge 不会伪装成运行成功，会在总览页显示具体占用原因。
- Bridge runtime 状态和 Claude / Codex / OpenCode backend 状态分离；单个 backend 未安装、未登录或服务未运行，不应导致 Bridge 显示崩溃。

**关键文件**：

| 文件 | 作用 |
|---|---|
| `MacBridge/project.yml` | XcodeGen 项目定义，含 `preBuildScripts` 自动编译 Go |
| `MacBridge/MacBridge/App/MacBridgeApp.swift` | SwiftUI App 入口 |
| `MacBridge/MacBridge/Services/RuntimeManager.swift` | Go 子进程管理 |
| `go-bridge/` | Go 源码，通过 `go.mod replace` 引用 cc-connect |

## 安装前置清理（开发机建议）

普通用户不需要执行本节；新版 MacBridge 会在启动时自动处理已知旧进程。开发机上如果历史上用过手动 `go-bridge` 或 LaunchAgent，可以先跑这段命令把现场清干净，便于验证。

```bash
# 1. 停掉旧 MacBridge UI
killall CCCodeBridge 2>/dev/null || true

# 2. 停掉旧开发版 go-bridge
pkill -f "/Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge" 2>/dev/null || true
pkill -f "/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime" 2>/dev/null || true

# 3. 禁用旧 LaunchAgent（如果存在）
if [ -f "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist" ]; then
  launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist" 2>/dev/null || true
  mv "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist" \
     "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist.disabled-$(date +%Y%m%d-%H%M%S)"
fi

# 4. 清理旧 ready 状态，避免 UI 读到过期 runtime.json
rm -f "$HOME/Library/Application Support/CCCode Bridge/runtime.json"

# 5. 确认 8777 没有旧进程占用
lsof -nP -iTCP:8777 -sTCP:LISTEN || true
```

期望结果：第 5 步没有输出。若仍有输出，先确认进程路径；只有 `/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime` 才是正确的产品态 runtime。

## 一键编译安装

```bash
# 1. 生成 Xcode 项目（从 project.yml）
cd MacBridge && xcodegen generate && cd ..

# 2. 全量编译（Xcode preBuildScript 自动执行 go build）
xcodebuild \
  -project MacBridge/CCCodeBridge.xcodeproj \
  -scheme CCCodeBridge \
  -configuration Release \
  -destination 'platform=macOS' \
  clean build

# 3. 安装到应用程序文件夹
rm -rf /Applications/CCCodeBridge.app
cp -R ~/Library/Developer/Xcode/DerivedData/CCCodeBridge-*/Build/Products/Release/CCCodeBridge.app /Applications/

# 4. 启动
open /Applications/CCCodeBridge.app

# 5. 验证
lsof -nP -iTCP:8777 -sTCP:LISTEN
```

## 验证清单

- [ ] `lsof` 显示 `/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime` 监听 port 8777
- [ ] 菜单栏出现 CCCode Bridge 图标
- [ ] MacBridge 总览页显示 `CCCode Bridge 运行中`
- [ ] AI 工具列表显示 Claude / Codex / OpenCode 的真实状态
- [ ] iOS 端可发现并连接到 Mac
- [ ] `runtime.json` 中的 `pid` 与监听 `8777` 的 runtime PID 一致

完整健康检查：

```bash
# 1. 监听进程必须是 app bundle 内 runtime
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "go-bridge|cccode-bridge-runtime|CCCodeBridge"

# 2. runtime.json 必须存在 managementUrl 和 pid
cat "$HOME/Library/Application Support/CCCode Bridge/runtime.json"

# 3. management API 必须返回 ready
MGMT_URL=$(ruby -rjson -e 'j=JSON.parse(File.read(ARGV[0])); print j["managementUrl"]' \
  "$HOME/Library/Application Support/CCCode Bridge/runtime.json")
TOKEN=$(cat "$HOME/Library/Application Support/CCCode Bridge/management-token")
curl -sS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/status"
curl -sS -H "Authorization: Bearer $TOKEN" "$MGMT_URL/internal/agents"
```

`/internal/status` 期望包含：

```json
{"status":"ready"}
```

`/internal/agents` 期望每个可用后端返回 `status:"available"`；如果某个后端未登录或服务未运行，应显示真实原因，而不是 MacBridge 崩溃。

## 常见问题

### Q: 双击图标弹跳后"没有响应"

**原因**：直接用 Go 二进制替换了 `MacOS/CCCodeBridge`，但这个位置应该是 Swift 编译的 launcher，不是 Go binary。macOS 发现进程不响应 AppKit 事件就判定为"没有响应"。

**修复**：按上面的"一键编译安装"流程完整重建 app bundle，不要手动替换二进制。

### Q: 只改了 go-bridge 代码，不想重编 Swift

可以用增量方式只更新 Go runtime：

```bash
killall CCCodeBridge 2>/dev/null || true
cd go-bridge && go build -o /tmp/cccode-bridge-runtime .
cp /tmp/cccode-bridge-runtime /Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime
open /Applications/CCCodeBridge.app
```

注意：不要把 Go binary 复制到 `Contents/MacOS/CCCodeBridge`。`Contents/MacOS/CCCodeBridge` 是 Swift launcher，`Contents/Resources/cccode-bridge-runtime` 才是 Go runtime。

### Q: iOS 端能连接，MacBridge 却显示“连续意外退出”

**最高概率原因**：旧的开发态 `go-bridge` 或 LaunchAgent 抢占了 port `8777`。iOS 连到旧进程，所以看起来能用；MacBridge 自己启动内置 runtime 时绑定端口失败，于是 UI 显示崩溃。

排查：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
pgrep -fl "go-bridge|cccode-bridge-runtime|CCCodeBridge"
ls "$HOME/Library/LaunchAgents" | grep -E "gobridge|opencode|cccode|bridge" || true
grep -R "opencode-cc-connect/go-bridge/go-bridge\\|8777" "$HOME/Library/LaunchAgents" 2>/dev/null || true
```

正确状态：

```text
/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime ... -management-host 127.0.0.1 ...
```

错误状态：

```text
/Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge ... -port 8777 ...
```

修复：

```bash
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist" 2>/dev/null || true
mv "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist" \
   "$HOME/Library/LaunchAgents/com.opencode.gobridge.plist.disabled-$(date +%Y%m%d-%H%M%S)" 2>/dev/null || true
pkill -f "/Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge" 2>/dev/null || true
killall CCCodeBridge 2>/dev/null || true
rm -f "$HOME/Library/Application Support/CCCode Bridge/runtime.json"
open /Applications/CCCodeBridge.app
```

### Q: `runtime.json` 显示 ready，但 management API 连不上

这说明 runtime ready 状态可能是旧版本留下的残留文件，或旧版本在绑定 `8777` 之前提前写了 ready。

修复：

```bash
killall CCCodeBridge 2>/dev/null || true
rm -f "$HOME/Library/Application Support/CCCode Bridge/runtime.json"
open /Applications/CCCodeBridge.app
```

然后重新跑“完整健康检查”。新版 go-bridge 应该只在成功监听 `8777` 后写 `runtime_ready`。

### Q: 编译报错 `go build` 失败

确认 cc-connect 本地 replace 路径正确：

```bash
grep replace go-bridge/go.mod
# 应显示: replace github.com/openAgi2/cc-connect-cccode => /Users/jacklee/Projects/cc-connect
```

### Q: Xcode 签名失败

确认 `project.yml` 中 `DEVELOPMENT_TEAM` 设置正确，或在 Xcode 中手动选择签名团队。

### Q: 如何配置 OpenCode 的 Basic Auth 认证用户名和密码？

**完全不需要手动配置**。
新版 `CCCodeBridge` 已经实现自动配对与凭证同步：
- 首次运行（或凭据缺失）时，App 会在本地自动生成随机 Basic Auth 凭据（用户名 `opencode`，密码为随机 UUID 字符串），保存在 `~/Library/Application Support/CCCode Bridge/credentials.json` 目录。
- 同时，App 会**自动将这组凭据注入写入** OpenCode Desktop 的配置文件：`~/Library/Application Support/ai.opencode.desktop/opencode.settings` 以及 `opencode.global.dat`。
- 当 OpenCode Desktop 启动其自带的服务端（port `64667`）时，就会强制校验这组凭据。而 CCCodeBridge 也会带着这组凭据启动 Go Runtime。整套鉴权流程全自动闭环，免去了用户和开发者的繁琐配对。

### Q: 点击菜单栏“Restart (重启)”会重启什么？

- **重启的组件**：仅重启 `cccode-bridge-runtime` (Go) 子进程本身。
- **对 Backends 的影响**：
  - **Claude Code**：Claude Code 作为按需调起的 CLI 进程（仅在 active turn/session 临时执行），所以重启 Go Runtime 会强制杀掉当前的 Claude 会话子进程，并在下次 iOS 端发消息时重新生成并调起。
  - **OpenCode 与 Codex app-server**：这两者属于运行在 Mac 本地其它端口的外部独立服务进程。重启 CCCodeBridge 仅会**断开并重新建立**与它们的 WebSocket/HTTP 通信连接，**不会**重启 OpenCode Desktop App 本身，也不会重启 Codex app-server 进程。

### Q: iOS 输入框面板的授权按钮如何微调了？

为了节省横向屏幕空间并提升审美，iOS 端输入框上的权限请求/授权确认按钮已精简为**纯图标按钮**（30x30 圆形，14pt Medium 粗细 SF Symbol 图标，不包含“允许/拒绝”文字标签）。

## 相关命令参考

```bash
# 查看运行日志
tail -f /tmp/go-bridge.log

# 查看端口占用
lsof -nP -iTCP:8777 -sTCP:LISTEN

# 查看真实进程路径（确认是否有 cccode-bridge-runtime 正确托管运行）
pgrep -fl "go-bridge|cccode-bridge-runtime|CCCodeBridge"

# 强制停止 MacBridge UI
killall CCCodeBridge

# 停止旧开发版 go-bridge
pkill -f "/Users/jacklee/Projects/opencode-cc-connect/go-bridge/go-bridge"

# 停止产品态 runtime
pkill -f "/Applications/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime"
```
