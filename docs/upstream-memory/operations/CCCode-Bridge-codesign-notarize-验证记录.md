# CCCode Bridge — Codesign & Notarize 验证记录

> Spike date: 2026-05-07
> Machine: macOS arm64 (Apple Silicon), Xcode 26.4 beta

## 1. 验证摘要

| 步骤 | 结果 |
|------|------|
| Go binary build (GOOS=darwin GOARCH=arm64) | ✅ 成功，输出 Mach-O 64-bit arm64，~11.4 MB |
| Ad-hoc codesign | ✅ 成功 |
| Ad-hoc codesign + hardened runtime | ✅ 成功 |
| Binary 从 .app bundle 内运行 | ✅ 成功 (`-version` 正常输出) |
| MacBridge Xcode build | ✅ BUILD SUCCEEDED |
| Notarization | ⚠️ 未验证 — 本机无 notarytool credentials |

## 2. 逐步命令与输出

### 2.1 Build Go binary

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
GOOS=darwin GOARCH=arm64 go build -o /tmp/cccode-bridge-runtime .
```

结果：成功。`file` 确认 `Mach-O 64-bit executable arm64`。

### 2.2 Ad-hoc codesign

```bash
codesign --force --sign - /tmp/cccode-bridge-runtime
codesign -dv /tmp/cccode-bridge-runtime
```

输出：

```
Format=Mach-O thin (arm64)
CodeDirectory v=20400 size=23383 flags=0x2(adhoc) hashes=724+2
Signature=adhoc
TeamIdentifier=not set
```

### 2.3 Hardened runtime codesign

```bash
codesign --force --options runtime --sign - /tmp/cccode-bridge-runtime
codesign -dv /tmp/cccode-bridge-runtime
```

输出：

```
CodeDirectory v=20500 size=23391 flags=0x10002(adhoc,runtime) hashes=724+2
Signature=adhoc
Runtime Version=12.0.0
```

结论：Go binary 支持 hardened runtime 标志，这是 notarization 的前提条件。

### 2.4 放入 .app bundle 并运行

```bash
mkdir -p /tmp/CCCodeBridge-spike/CCCodeBridge.app/Contents/Resources
cp /tmp/cccode-bridge-runtime /tmp/CCCodeBridge-spike/CCCodeBridge.app/Contents/Resources/

/tmp/CCCodeBridge-spike/CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime -version
```

输出：

```
cccode-bridge-runtime 0.1.0-dev (unknown, unknown)
```

结论：binary 在 bundle 内正常执行，macOS Gatekeeper 未阻止。

### 2.5 MacBridge Xcode build

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/MacBridge
xcodegen generate
xcodebuild -project CCCodeBridge.xcodeproj -scheme CCCodeBridge \
  -destination 'platform=macOS,arch=arm64' build
```

结果：**BUILD SUCCEEDED**

### 2.6 本机签名身份

```bash
security find-identity -v -p codesigning
```

输出：

```
1) 2C13F5476BA7F8D8CF64ED4D805D7667464B0CA6 "Apple Development: xzqxiaoqing@outlook.com (6L3SKKKWK5)"
   1 valid identities found
```

本机有一个 Apple Development 证书。注意：分发时需要 "Developer ID Application" 证书（非 "Apple Development"），Apple Development 仅用于 App Store 或 TestFlight 分发。

### 2.7 Notarization 检查

```bash
xcrun notarytool history
```

输出：

```
Error: Must provide credentials.
```

结论：本机未配置 notarytool credentials，notarization 步骤未在此次 spike 中验证。

## 3. 生产环境推荐签名流程

以下是完整的推荐流程，已验证步骤标记 ✅，未验证步骤标记 ⚠️：

### Step 1: Build Go binary ✅

```bash
cd go-bridge
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -ldflags="-s -w" -o cccode-bridge-runtime .
```

- `-s -w` 去 debug 信息减小体积
- `CGO_ENABLED=0` 确保纯静态链接（如果不需要 cgo）

### Step 2: Codesign Go binary (hardened runtime) ✅

```bash
codesign --force --options runtime --timestamp \
  --sign "Developer ID Application: <TEAM_NAME> (<TEAM_ID>)" \
  cccode-bridge-runtime
```

- 生产环境使用 Developer ID Application 证书（非 ad-hoc）
- `--options runtime` 启用 hardened runtime（notarization 必须）
- `--timestamp` 嵌入受信时间戳（notarization 必须）

### Step 3: Place binary in App bundle ✅

```
CCCodeBridge.app/
├── Contents/
│   ├── Info.plist
│   ├── MacOS/
│   │   └── CCCodeBridge          ← Swift 主程序
│   └── Resources/
│       └── cccode-bridge-runtime  ← Go binary
```

将 Go binary 放入 `Contents/Resources/`。MacBridge 的 Swift 代码通过 `Bundle.main.resourcePath` 找到它。

### Step 4: Codesign entire App bundle ⚠️（需 Developer ID 证书）

```bash
# 先签名所有内嵌的辅助可执行文件（按依赖顺序，叶子先签）
codesign --force --options runtime --timestamp --sign "Developer ID Application: ..." \
  CCCodeBridge.app/Contents/Resources/cccode-bridge-runtime

# 最后签名整个 .app
codesign --force --options runtime --timestamp --deep --strict \
  --sign "Developer ID Application: ..." \
  CCCodeBridge.app
```

注意：`--deep` 从外向内遍历，但官方推荐手动按依赖顺序签名。如果 bundle 内有 frameworks 或 plugins，需要先签它们。

### Step 5: Notarize ⚠️（需 Apple Developer 账号 + app-specific password）

```bash
# 1) 存储 notarytool credentials（一次性）
xcrun notarytool store-credentials "AC_PASSWORD" \
  --apple-id "your@email.com" \
  --team-id "TEAMID" \
  --password "xxxx-xxxx-xxxx-xxxx"

# 2) 打包并提交
ditto -c -k --keepParent CCCodeBridge.app CCCodeBridge.zip
xcrun notarytool submit CCCodeBridge.zip --keychain-profile "AC_PASSWORD" --wait

# 3) 检查结果（--wait 会自动等）
xcrun notarytool log <submission-id> --keychain-profile "AC_PASSWORD"
```

前提条件：
- 付费 Apple Developer Program 成员
- 在 [appleid.apple.com](https://appleid.apple.com) 生成 app-specific password
- 使用 Developer ID Application 证书签名（非 Apple Development）
- macOS 10.15+ 目标

### Step 6: Staple ⚠️（需 notarization 通过后）

```bash
xcrun stapler staple CCCodeBridge.app
xcrun stapler validate CCCodeBridge.app
```

Staple 将 notarization ticket 附加到 bundle，使离线用户也能验证。

## 4. 已发现的注意事项

1. **Hardened runtime 对 Go binary 无影响**：Go 编译的 Mach-O 不使用动态链接器特性（dyld env vars、unsigned memory exec 等），`--options runtime` 完全兼容。

2. **Developer ID vs Apple Development 证书**：本机只有 "Apple Development" 证书。直接分发（不在 App Store）需要 "Developer ID Application" 证书，需要在 [developer.apple.com](https://developer.apple.com) 额外申请。

3. **Entitlements**：此次 spike 未测试 entitlements。如果 Go binary 需要网络权限或文件访问，可能需要额外的 entitlements plist。但 hardened runtime 默认不阻止出站网络连接。

4. **Universal binary**：当前只构建了 arm64。如果需要支持 Intel Mac，需要 `GOARCH=amd64` 另外构建，然后用 `lipo -create` 合并为 universal binary。

5. **`--deep` 的坑**：`codesign --deep` 会遍历 bundle 内所有可执行文件并签名，但顺序不可控。如果存在签名依赖（如 framework 依赖内部的 dylib），必须手动按依赖顺序签名。对于当前只有单个 Go binary 的简单 bundle，`--deep` 足够。

## 5. 未验证项

| 项目 | 原因 | 所需条件 |
|------|------|---------|
| Developer ID Application 签名 | 本机无此证书 | Apple Developer Program + 创建 Developer ID 证书 |
| Notarization 提交 | 无 notarytool credentials | app-specific password + Team ID |
| Staple | 依赖 notarization | notarization 通过后的 ticket |
| Entitlements 对 Go runtime 的影响 | 无具体 entitlements 需求 | 确定所需权限后再测 |
| Universal binary (arm64 + x86_64) | spike 只验证 arm64 | 额外 `GOARCH=amd64` build + lipo |
