# CCCode Mac Bridge 真机验证流程

> 日期：2026-05-08  
> 适用范围：二期 Alpha 闭环真机验收  
> 关联方案：`docs/2026-05-08-CCCode-Mac-Bridge-产品化二期建设方案.md`

## 0. 交给其他 agent 做测试时的用法

这是一份执行 runbook，不是完成情况报告。执行者必须按顺序记录 `pass / fail / N/A / blocked`。

### 0.1 前置条件

| 条件 | 说明 |
| --- | --- |
| Mac | 当前仓库可构建 go-bridge、MacBridge、OpenCodeiOS |
| iPhone | 真实 iPhone 已连接、已信任、Xcode/CoreDevice 可见 |
| 网络 | iPhone 和 Mac 在同一局域网，或明确启用 remote URL 场景 |
| 后端 | 至少一个 agent 可用；推荐先用 Claude Code 或 Codex/OpenCode 中当前最稳定者 |
| 密钥 | 不把 device token、密码、API key 写入本文档或测试记录 |

### 0.2 结果词汇

| 结果 | 含义 |
| --- | --- |
| `pass` | 已执行，符合预期 |
| `fail` | 已执行，不符合预期；保留日志和截图 |
| `N/A` | 条件项，本轮未启用，例如 remote URL 未配置 |
| `blocked` | 前置条件缺失导致无法执行，例如真机不可见、Bridge 端口未启动 |

### 0.3 最低证据

每轮至少保留：

1. `xcrun devicectl list devices` 输出摘要。
2. `xcrun xctrace list devices` 输出摘要。
3. iOS build 或 install/launch 结果。
4. go-bridge/MacBridge 运行状态、端口、最近日志。
5. 每个手工场景的 pass/fail/N/A/blocked 记录。
6. 失败场景的截图或 console log 路径。

## 1. 当前状态与结论

| Bucket | 当前事实 |
| --- | --- |
| Already available now | go-bridge pairing/auth/hello 单元测试，iOS Bridge Phase2/3/4 单元测试，MessageWeb/ThinBridge/Copilot real-device UITest 钩子，devicectl/xcodebuild 手工路径 |
| Manually testable now | go-bridge CLI on port 8777、旧 Server/register 路径、部分 Bridge hello 路径、iOS 保存后自动连接、MessageWeb 真实设备 smoke |
| Still missing | 完整 MacBridge 配对 UI、Mac approve/reject 产品入口、扫码后 pairing complete 全链路、产品模式 auth 强制验证、remote URL 端到端 |

因此本 runbook 分两层：

1. **当前可跑的回归门禁**：确保现有底座没坏。
2. **二期完成后的真机闭环**：等实现补齐后逐项验收。

## 2. 阅读顺序

1. `docs/2026-05-08-CCCode-Mac-Bridge-产品化二期建设方案.md`
2. `docs/CCCode-Mac-Bridge-产品化技术方案.md`
3. `docs/CCCode产品化与Mac端Bridge产品设计.md`
4. `docs/2026-05-07-bridge-pairing-to-connection-plan完成情况.md`
5. `docs/2026-05-07-CCCode-Mac-Bridge-Phase0-4工程路线图与Phase0执行拆分完成情况.md`
6. `docs/CCCode-Bridge-codesign-notarize-验证记录.md`

## 3. 执行前准备

### 3.1 真机检测

```bash
xcrun devicectl list devices
xcrun xctrace list devices
```

判定：

| 结果 | 处理 |
| --- | --- |
| iPhone 在 `devicectl` 中 connected，或在 `xctrace` 的 Devices 中出现 | 继续 |
| 设备不可见、未信任、锁屏不可用 | `blocked: physical device unavailable` |
| 设备可见但后端不可连 | 不是设备 blocker，转到服务预检 |

设置 UDID：

```bash
export DEVICE_UDID="<xctrace device UDID>"
xcrun devicectl device info -d "$DEVICE_UDID"
```

`device info` 用于确认设备信任状态、Developer Mode、iOS 版本和 CoreDevice 可访问性。若 `list devices` 可见但 `device info` 失败，记录为 `blocked: device visible but CoreDevice info unavailable`。

### 3.2 服务与端口预检

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
go build -o go-bridge .
./go-bridge -port 8777 -drivers claude,opencode,codex \
  -work-dir /Users/jacklee/Projects/opencode-cc-connect
```

另开终端检查：

```bash
lsof -nP -iTCP:8777 -sTCP:LISTEN
```

如果用 launchctl 常驻：

```bash
launchctl print gui/$(id -u)/com.opencode.gobridge | egrep 'state =|pid =|path =' || true
tail -50 /tmp/go-bridge.log || true
```

### 3.3 自动门禁

Go：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
go test ./... -run 'Pairing|DeviceAuth|Auth|Hello|Management' -count=1
go test ./... -count=1
```

iOS 定向 XCTest：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj \
  -scheme CCCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  test \
  -only-testing:CCCodeTests/CCCodeBridgePhase2Tests \
  -only-testing:CCCodeTests/CCCodeBridgePhase3Tests \
  -only-testing:CCCodeTests/CCCodeBridgePhase4Tests
```

iOS 真机 build：

```bash
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj \
  -scheme CCCode \
  -destination "id=$DEVICE_UDID" \
  build \
  -resultBundlePath /tmp/cccode-bridge-device-build.xcresult
```

MacBridge build：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj \
  -scheme CCCodeBridge \
  -destination 'platform=macOS' \
  build \
  -resultBundlePath /tmp/cccode-macbridge-build.xcresult
```

### 3.4 可复用 UITest

当前可复用但不是完整 pairing 闭环的 real-device helper：

| 文件 | 用途 | 注意 |
| --- | --- | --- |
| `OpenCodeiOS/OpenCodeiOSUITests/MessageWebSmokeUITests.swift` | MessageWeb、Bridge/legacy server、permission/image/link action | 依赖 `UITEST_OPENCODE_HOST`/`UITEST_BRIDGE_HOST`、seed session；不是扫码配对测试 |
| `OpenCodeiOS/OpenCodeiOSUITests/ThinBridgeSmokeUITests.swift` | Claude/bridge 类路径 smoke | 默认 host/path 是本机历史值，执行前必须覆盖 env |
| `OpenCodeiOS/OpenCodeiOSUITests/CopilotSmokeUITests.swift` | Copilot smoke | Copilot backend 条件项；本轮可 `N/A` |

示例命令：

```bash
UITEST_BRIDGE_HOST="<Mac LAN IP>" \
UITEST_BRIDGE_PORT="8777" \
xcodebuild test \
  -project OpenCodeiOS/CCCode.xcodeproj \
  -scheme CCCode \
  -destination "id=$DEVICE_UDID" \
  -only-testing:CCCodeUITests/MessageWebSmokeUITests/testMessageWebRealDevice_bootstrapsFromDraftSession \
  -resultBundlePath /tmp/cccode-messageweb-smoke.xcresult
```

## 4. 真机 Checklist

### 4.1 P0 当前底座回归

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P0-1 | go-bridge build + targeted Go tests | build 成功，Pairing/Auth/Hello/Management 测试通过 | terminal log |
| P0-2 | iOS Bridge Phase2/3/4 XCTest | targeted XCTest 通过 | `.xcresult` |
| P0-3 | iOS 真机 build/install | App 安装到 iPhone | xcodebuild result |
| P0-4 | go-bridge port 8777 可达 | `lsof` 显示 LISTEN，iPhone 同网可连接 | `lsof` + console |
| P0-5 | MacBridge build | macOS target build 成功 | `/tmp/cccode-macbridge-build.xcresult` |

### 4.2 P1 首次配对闭环

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P1-1 | 打开 MacBridge App | 显示 Bridge 状态，不要求用户启动 CLI | screenshot |
| P1-2 | MacBridge 创建 pairing session | 显示二维码和手动码，二维码包含 iOS 可连接地址 | screenshot + decoded QR |
| P1-3 | iOS 点击连接 Mac 并扫码 | iOS 进入 waiting for approval | iPhone screenshot |
| P1-4 | MacBridge 显示待批准设备 | 设备名/platform 可见，approve/reject 可操作 | screenshot |
| P1-5 | Mac approve | iOS 收到 pairing complete，保存 SavedBridge | iOS console + UI |
| P1-6 | 重启 iOS App | 自动连接上次 Saved Mac | console + screenshot |

Blocked 规则：如果 MacBridge 还没有二维码/批准 UI，本节标记为 `blocked: MacBridge pairing UI missing`，不要改用手写 token 当作 pass。

### 4.3 P2 默认 hello 连接与后端同步

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P2-1 | iOS 自动连接 SavedBridge | go-bridge 收到 `hello`，返回 `hello_ack` | `/tmp/go-bridge.log` 或 console |
| P2-2 | Server 列表同步 | Claude Code、OpenCode、Codex 至少按 go-bridge descriptor 正确出现 | screenshot |
| P2-3 | Claude kind 验证 | Claude 不显示/缓存为 OpenCode | screenshot + logs |
| P2-4 | 切换 backend | 选中不同 backend 后能进入对应 session 列表或给出真实错误 | screenshot |
| P2-5 | 创建/继续 session | 能发送 prompt 并收到 response 或真实 backend 错误 | screenshot + console |

### 4.4 P3 认证与撤销

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P3-1 | 无 token 连接 `/bridge` | WebSocket 被拒绝或 hello 返回 `auth.missing_token` | curl/websocket log |
| P3-2 | 错 token 连接 `/bridge` | 返回 `auth.invalid_token`，不泄露 backend/session | log |
| P3-3 | MacBridge revoke 已授权设备 | 设备从列表消失或标记 revoked | screenshot |
| P3-4 | iOS 用旧 token 重连 | 进入重新配对路径，不继续访问 session | iPhone screenshot |

### 4.5 P4 断连、睡眠、重启

前置条件：二期建设方案中的 P4-6 已实现，即 `RuntimeManager` 已订阅 `NSWorkspace.willSleepNotification` / `didWakeNotification`。如果未实现，P4-4 标记为 `blocked: sleep/wake lifecycle hook missing`。

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P4-1 | MacBridge Stop Bridge | iOS 显示离线/Bridge stopped，不误报 token 错误 | screenshot |
| P4-2 | MacBridge Start/Restart Bridge | iOS 自动重连或给出可执行重试入口 | screenshot + logs |
| P4-3 | 运行中 session 时杀掉 go-bridge | MacBridge 显示 crashed/restarting，iOS 不假成功 | logs |
| P4-4 | Mac 睡眠/唤醒 | iOS 文案区分 Mac may be asleep，唤醒后恢复 | screenshot |

触发睡眠的建议步骤：

```bash
# 触发 Mac 睡眠，通常需要管理员密码
sudo pmset sleepnow
```

等待至少 10 秒后，用键盘、触控板或电源键唤醒 Mac。唤醒后记录：

1. MacBridge 是否从 `.sleeping` 恢复到 ready/reconnecting。
2. iOS 是否显示 Mac may be asleep 或等价断连状态。
3. iOS 是否在 runtime 恢复后自动重连，或提供明确重试入口。

### 4.6 P5 离线与草稿

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P5-1 | 已打开 session 后断网 | iOS 保留最后可读内容 | screenshot |
| P5-2 | 离线输入草稿 | 不自动发送，明确标为草稿 | screenshot |
| P5-3 | 重连且 session 已结束 | 草稿保留，提示新建发送/丢弃 | screenshot |

### 4.7 P6 远程访问条件项

| Step | Action | Pass criteria | Evidence |
| --- | --- | --- | --- |
| P6-1 | 未配置 remote URL | 标记 `N/A`，不是 fail | run log |
| P6-2 | 配置 Tailscale/frp/Cloudflare URL | iOS 可选择 remote mode，显示安全提示 | screenshot |
| P6-3 | remote URL 无效 | iOS 显示远程地址不可达，不误报 pairing/auth | screenshot |
| P6-4 | remote URL 有效 | 已授权设备可连接；未授权设备仍失败 | logs |

## 5. 证据采集

### 5.1 iOS console

```bash
rm -f /tmp/cccode_ios_console.log
xcrun devicectl device process launch --console --terminate-existing \
  -d "$DEVICE_UDID" com.jacklee.CCCode \
  > /tmp/cccode_ios_console.log 2>&1 &
tail -f /tmp/cccode_ios_console.log
```

### 5.2 go-bridge log

如果用 launchctl：

```bash
tail -f /tmp/go-bridge.log
```

如果手动启动，把 stdout/stderr 保存：

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
./go-bridge -port 8777 -drivers claude,opencode,codex \
  -work-dir /Users/jacklee/Projects/opencode-cc-connect \
  > /tmp/go-bridge.log 2>&1
```

### 5.3 xcode result bundle

所有真机 `xcodebuild` 命令使用 `-resultBundlePath /tmp/<name>.xcresult`。失败时保留该路径。

### 5.4 截图

手工验证至少截：

1. MacBridge pairing page。
2. MacBridge pending approval。
3. iOS waiting for approval。
4. iOS connected Server list。
5. iOS active session。
6. 任何 failure state。

## 6. 当前不能诚实声称已自动完成的内容

| 内容 | 当前状态 |
| --- | --- |
| 扫码配对完整 UITest | 缺失。需要 MacBridge UI 和真实二维码/批准流程后再补 |
| MacBridge approve/reject UI 自动化 | 缺失。当前 MacBridge 只有基础状态页 |
| 产品模式 auth 强制端到端 | 需要二期实现后真机验证 |
| remote URL 真机端到端 | 条件项，未配置时 `N/A` |
| notarized app 分发安装 | 当前只有脚本和 spike，未完整 notarization gate |

## 7. 最小自动化补齐路线

1. Go integration test：management create -> pairing WS claim -> management approve -> iOS conn receives `pairing_complete`。
2. iOS unit test：QR parser 接受包含 host/port 的 payload，manual code path 不假失败。
3. iOS unit test：SavedBridge + hello_ack backend sync 正确识别 `claude_code`、`opencode`、`codex`。
4. MacBridge unit/UI-light test：RuntimeManager ready/error frame 映射、pairing view model 状态流。
5. Real-device smoke：使用 launch env 注入已配对 SavedBridge fixture，只验证 hello auto-connect 和 Server sync。
6. 完整 E2E 真机：保留手工 Mac approve，自动化 iOS 前后状态。

## 8. 给下一位 agent 的要求

1. 不要把 helper UITest 通过当作 pairing 产品闭环通过。
2. 不要把手动写入 SavedBridge/token 当作首次配对通过。
3. 不要在真实路径加入 fake token、fake backend、fake QR 来绕过阻塞。
4. 遇到失败先保留日志和截图，再定位根因。
5. 每次验证都追加到第 10 节，不覆盖旧记录。

## 9. 评审建议采纳记录

| 建议 | 处理 | 原因 |
| --- | --- | --- |
| 自动门禁补 MacBridge build | 采纳 | P1/P4 都依赖 MacBridge，缺少 build gate 会让后续手工验证浪费时间 |
| P4-4 sleep/wake 补具体触发方式和前置条件 | 采纳 | 当前没有 sleep/wake hook 时应标 blocked，不能把合盖/唤醒当成已可验收路径 |
| 真机检测补 `xcrun devicectl device info` | 采纳 | 可提前发现 Developer Mode、信任状态或 CoreDevice 访问问题 |
| 验收模板增加 Scope | 采纳 | 允许当前只跑 P0 底座回归，不必把 P1-P6 全部误写成 blocked |
| 不采纳项 | 无 | 本轮评审建议均为补充或澄清，未发现需要拒绝的建议 |

## 10. 验收记录模板

```text
Run ID:
Date:
Tester:
Scope: [P0-only | full P0-P6]
Commit:
Mac:
iPhone model / iOS:
DEVICE_UDID:
Network:
go-bridge command / MacBridge build:

Automated gates:
- go test:
- MacBridge build:
- iOS targeted XCTest:
- iOS device build:
- UITest helper:

Manual checklist:
- P0:
- P1:
- P2:
- P3:
- P4:
- P5:
- P6:

Evidence:
- iOS console:
- go-bridge log:
- xcresult:
- screenshots:

Failures / blockers:
- 

Final verdict:
```
