# 公开前硬化记录：CCCode iOS / MacBridge 拆仓与 cc-connect 合并方案

生成时间：2026-06-01T04:01:32Z

## 1. 结论

本轮针对“公开前必须处理的地方”完成了安全清理与发布前硬化。两个拆分仓库仍保持私有 public-candidate 状态，不能直接宣称可公开发布；剩余必须由 owner 手动处理的事项是正式签名/provisioning、是否公开 hosted relay 信息、配置最终 remote/default branch 并执行远端扫描，以及 Task E 真实 iOS ↔ MacBridge 集成验证。

本轮新增提交：

| 仓库 | Commit | 说明 |
| --- | --- | --- |
| `/Users/jacklee/Projects/cccode-macbridge` | `3c9fa43 Public release hardening` | MacBridge 去个人化 bundle/keychain id、补 Gitleaks、配置示例、后端/签名文档、CI secret scan |
| `/Users/jacklee/Projects/cccode-macbridge` | `b14fcbe Adopt AGPL license` | 将 placeholder LICENSE 替换为 AGPL-3.0-only |
| `/Users/jacklee/Projects/cccode-macbridge` | `6e4ede5 Avoid runtime secret argv exposure` | management token、OpenCode password、relay route/credential 改为不经 argv 传递 |
| `/Users/jacklee/Projects/cccode-macbridge` | `4ff89f8 Disable official relay when endpoint is absent` | 公开构建无 official endpoint 时不再尝试开通官方 Relay，避免旧配置/TLS 误导 |
| `/Users/jacklee/Projects/cccode-ios` | `2e1231e Public release hardening` | iOS 去个人化 bundle/url/logger/storage id、补 Gitleaks、签名文档、CI secret scan |
| `/Users/jacklee/Projects/cccode-ios` | `d1102e8 Adopt AGPL license` | 将 placeholder LICENSE 替换为 AGPL-3.0-only |

## 2. 已处理项

- 已扫描并清理已知生产/私人标记：`relay.byteseek.uk`、`47.236.182.45`、`/Users/jacklee`、`DEVELOPMENT_TEAM`、`PROVISIONING_PROFILE`、`HB8YMMP798`、`com.jacklee`。
- 已将两个仓库中的公开候选 bundle id 从个人域名改为 `org.openagi.cccode*`。
- 已将 MacBridge keychain service 从 `com.jacklee.CCCodeBridge.relay` 改为 `org.openagi.cccode.macbridge.relay`。
- 已将测试 fixture 中类似真实 route token 前缀的 `rt_*` 改为 `route_*`，降低误报和品牌混淆。
- 已新增 Gitleaks 配置并把 `gitleaks git`、`gitleaks dir` 加入两个仓库 CI。
- 已新增配置/部署边界文档，明确真实 relay endpoint、route credential、OpenCode credential、pairing token、management token、Apple signing credential 不应提交到 Git。
- 已更新 README，避免外部用户误以为仓库包含生产 relay/VPS 或可以直接连接 owner 生产服务。
- 已采用 AGPL-3.0-only license。
- 已修复 MacBridge runtime argv 泄露：management token、OpenCode password、relay endpoint/route/credential 不再作为子进程命令行参数传递。
- 已修复公开构建官方 Relay 行为：没有 `CCCODEOfficialRelayEndpoint` 时，不自动开通、不读取旧官方 route/credential、不显示“重试启用”按钮。

## 3. 新增 / 更新文档

MacBridge:

- `/Users/jacklee/Projects/cccode-macbridge/.gitleaks.toml`
- `/Users/jacklee/Projects/cccode-macbridge/config.example.env`
- `/Users/jacklee/Projects/cccode-macbridge/docs/backends-and-config.md`
- `/Users/jacklee/Projects/cccode-macbridge/docs/signing-and-release.md`
- `/Users/jacklee/Projects/cccode-macbridge/docs/public-readiness.md`
- `/Users/jacklee/Projects/cccode-macbridge/README.md`
- `/Users/jacklee/Projects/cccode-macbridge/.github/workflows/ci.yml`

iOS:

- `/Users/jacklee/Projects/cccode-ios/.gitleaks.toml`
- `/Users/jacklee/Projects/cccode-ios/docs/signing-and-release.md`
- `/Users/jacklee/Projects/cccode-ios/docs/public-readiness.md`
- `/Users/jacklee/Projects/cccode-ios/README.md`
- `/Users/jacklee/Projects/cccode-ios/.github/workflows/ci.yml`

## 4. 验证证据

MacBridge:

```bash
cd /Users/jacklee/Projects/cccode-macbridge
go build ./go-bridge
go test ./go-bridge/... -count=1
(cd relay-server && go test ./... -count=1)
xcodebuild -project MacBridge/CCCodeBridge.xcodeproj -scheme CCCodeBridge -configuration Debug -destination 'platform=macOS' build
/tmp/codex-gobin/gitleaks git --redact --config .gitleaks.toml .
/tmp/codex-gobin/gitleaks dir --redact --config .gitleaks.toml .
rg -n "relay\.byteseek\.uk|47\.236\.182\.45|/Users/jacklee|DEVELOPMENT_TEAM|PROVISIONING_PROFILE|HB8YMMP798|com\.jacklee" . --glob '!.git/**'
git diff --check
git status --short
```

结果：

- Go build/test passed.
- relay-server tests passed.
- MacBridge Debug macOS xcodebuild passed.
- Gitleaks git + dir scan passed.
- 已知私人/生产 marker scan 无输出。
- `git diff --check` passed.
- Working tree clean after commit `3c9fa43`.
- License commit `b14fcbe` 后 Gitleaks git + dir scan passed。
- Runtime argv hardening commit `6e4ede5` 后 Gitleaks git scan passed。
- MacBridge app 启动后 runtime 监听 `8777`，进程 argv 未命中 `management-token|opencode-pass|relay-route-id|relay-credential|relay-endpoint`。
- Official Relay endpoint absent fix commit `4ff89f8` 后 MacBridge Go test、MacBridge xcodebuild、Gitleaks git + dir scan passed。
- 重启 MacBridge 后 management status 显示 `relay.configured=false endpoint="" routeId=""`。

iOS:

```bash
cd /Users/jacklee/Projects/cccode-ios
(cd message-web && npm run test)
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' build
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test -only-testing:CCCodeTests/RelayCryptoVectorTests -only-testing:CCCodeTests/NetworkStateManagerTests
/tmp/codex-gobin/gitleaks git --redact --config .gitleaks.toml .
/tmp/codex-gobin/gitleaks dir --redact --config .gitleaks.toml .
rg -n "relay\.byteseek\.uk|47\.236\.182\.45|/Users/jacklee|DEVELOPMENT_TEAM|PROVISIONING_PROFILE|HB8YMMP798|com\.jacklee" . --glob '!.git/**' --glob '!message-web/node_modules/**' --glob '!message-web/dist/**' --glob '!OpenCodeiOS/OpenCodeiOS/Resources/MessageWeb/**'
git diff --check
git status --short
```

结果：

- message-web Vitest passed: 33 tests.
- iOS simulator build passed.
- Targeted XCTest passed: 7 tests, 0 failures.
- Gitleaks git + dir scan passed.
- 已知私人/生产 marker scan 无输出。
- `git diff --check` passed.
- Working tree clean after commit `2e1231e`.
- License commit `d1102e8` 后 Gitleaks git + dir scan passed。

说明：第一次并行运行 iOS targeted XCTest 时曾遇到 Xcode `build.db: database is locked`，根因是和 iOS build 并发占用同一 DerivedData；串行重跑后通过，不属于代码失败。

## 5. 仍需 owner 手动处理

- 配置 Apple Team / provisioning profile / notarization。当前暂不走 App Store；owner 已完成一次真实 iOS 安装。
- 决定公开构建是否配置 owner-approved hosted Relay endpoint；未配置时官方加密 Relay 在 UI 中保持“未配置”。
- 配置最终 Git remote；当前两个拆分仓库没有 remote，因此无法对 GitHub default branch 执行远端扫描。本地 default branch Gitleaks 已通过。
- 执行 Task E 剩余验收：LAN 扫码配对已由 owner 验证；relay path、offline mailbox、revoke、reconnect、backend switching 仍待 official/self-hosted Relay 配置后继续。

## 6. 未做事项

- 未运行 UI tests、snapshot tests、真机自动化或视觉验证。
- 未引入 mock/fallback/假数据来替代真实产品路径。
- 未把 handoffs、`.exec-plan`、内部完成情况文档或真实部署配置迁入两个公开候选仓库。
