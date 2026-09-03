# 同名 iOS 设备配对互踢修复方案（ReplaceDevice 匹配键收紧）

> 状态：**已实施（2026-09-03，`codex/grokbuild-leader-mode` 分支）**——owner 裁决搭车当前
> 功能分支（§8-1）；identityPublicKey 加固已采纳（§8-2）；iOS 上报名机型后缀维持另案（§8-3）。
> 实现与验证见 §10。
> 分级：D3（配对/设备状态语义）。

## 0. 来源清单（P0）

```text
MacBridge 仓库路径=/Users/jacklee/Projects/cordcode-macbridge-grokbuild-leader（功能工作树）
MacBridge 分支=codex/grokbuild-leader-mode
MacBridge 提交=6dc9353ce6c569849b0b7b9b28411a5a4c622159
MacBridge 未提交状态=M docs/2026-08-28-grokbuild-leader-mode-design.md；?? tmp-leader-probe/（均与本修复无代码重叠）
被测生产 runtime=/Applications/CordCodeLink.app 内嵌 cordcode-bridge-runtime 0.1.0
  （commit 6dc9353ce6c5，built 2026-09-02T05:09:22Z）——与上述提交一致，源码分析直接有效
iOS 仓库路径=/Users/jacklee/Projects/cordcode-ios（main 工作树）
iOS 分支=main 提交=de264a2cbe255d1ca399efedd747e74a487fcd56
iOS 未提交状态=IOS_REAL_DEVICE_E2E_AUTOMATION.md 新增、REAL_DEVICE_DEBUGGING.md / CHANGELOG.md 修改（本文档只读引用 iOS 源码，无代码改动）
运行态证据=生产 runtime 日志与 devices.json（2026-09-02 13:35 事故窗口，原始证据见 §2）
```

## 1. 现象

iPhone 16 Pro（dev_c5ad42a3…，已配对且正常使用）在 owner 用 iPhone 11 完成扫码+批准后，
被踢回配对扫码页。owner 预期多设备共存。

## 2. 原始证据（与源码解释分离）

**生产日志**（`~/Library/Application Support/CordCode Link/logs/go-bridge.log`，runtime
6dc9353）：

```text
13:35:23.592 set_observation_scope deviceID=dev_c5ad42a3 …（iPhone 16 正常活动，最后一刻）
13:35:23.965 relay-bridge-client: received client hello deviceID=dev_9ea011ca …
13:35:23.966 relay-bridge-client: device not found deviceID=dev_9ea011ca（11 携旧凭据首连被拒→进配对流）
13:35:48.758 pairing: claim verified deviceID=dev_9ea011ca deviceName=iPhone
13:35:51.373 pairing: replaced previous device records deviceID=dev_9ea011ca replaced=1   ← 16 的记录在此被删
13:35:53.865 syncV2_mark remote=192.168.1.5 device=dev_9ea011ca…（11 经 LAN 上线；16 此前在 192.168.1.3）
```

**Management API `GET /internal/devices`（事后）**：仅剩 2 条 08-28 的 web 设备 +
1 条 13:35 新建（11）；iPhone 16 的 dev_c5ad42a3 记录不存在。

**设备侧真值**（`xcrun devicectl device info details`）：

| 设备 | Device Name（设置本名） | app 实际上报 |
| --- | --- | --- |
| iPhone 16 Pro | `iPhone` | `iPhone` |
| iPhone 11 | `chuck的iPhone11` | `iPhone`（本名未被读到） |

**iOS 26 SDK 系统头文件**（本机 Xcode-beta）：

```objc
@property NSString *name;   // Synonym for model. Prior to iOS 16, user-assigned device name
@property NSString *model;  // e.g. @"iPhone"
```

## 3. 根因（源码解释）

1. iOS 端 claim 用 `UIDevice.current.name`（iOS 仓
   `OpenCodeiOS/Services/Bridge/BridgePairingClient.swift:427`）。iOS 16 起该 API 等于
   `model`，永远返回泛称 **"iPhone"**——与设置里的本名无关。因此**所有**现代 iOS 原生
   客户端上报的 (platform, displayName) 都是 `("ios", "iPhone")`，撞名是系统性必然。
2. Mac 侧 `MemoryDeviceStore.ReplaceDevice`（`go-bridge/trusted_device_store.go:62-74`）
   的替换条件：

   ```go
   sameDeviceID := deviceID == record.DeviceID
   sameLegacyIdentity := existing.Platform == record.Platform &&
       existing.DisplayName == record.DisplayName
   ```

   `sameLegacyIdentity` 本意是"清理升级前的随机 ID 记录"（同物理设备从随机 ID 升级到
   稳定 ID 后重配，删旧记录），但**没有校验旧记录是否真是 legacy 随机 ID 格式**。两台
   不同 iPhone 同报 `("ios","iPhone")` → 批准第二台时删掉第一台的稳定 ID 记录 → 第一台
   token 失效、回扫码页。
3. 两个调用点同样受影响：`go-bridge/management_api.go:889`（Mac app 批准路径，本次事故
   路径）与 `go-bridge/pairing_handler.go:340`（LAN 配对完成路径
   `NotifyPairingComplete`）。修复落在 store 层即同时覆盖两者；`FileDeviceStore` 经
   `clone.ReplaceDevice` 事务继承（trusted_device_store.go:295-299）。

**影响面推论**：当前逻辑下，同一 bridge 上原生 iOS 客户端**最多共存一台**（web 客户端
displayName 自定，如 "iPhone/iPad web"/"web"，未撞）。改名设置里的设备名**无法**规避。

## 4. 修复方案

### 4.1 目标匹配语义

| 条件（对每条既有记录） | 动作 | 依据 |
| --- | --- | --- |
| `deviceID == record.DeviceID` | 替换（现状保留） | 同一设备重配换 token |
| （可选加固）双方 `IdentityPublicKey` 非空且相等 | 替换 | 同一安装身份、deviceID 轮换（Keychain 异常丢失）场景；identity key 为每安装生成，等价即同源 |
| `existing` 是 **legacy 随机 ID 格式**（无 `dev_` 前缀）**且** platform+displayName 相同 | 替换（清理，现状保留但收窄） | 原本设计意图：老客户端升级到稳定 ID 后清旧记录 |
| 其他（含：既有记录是稳定 `dev_` ID，名字平台相同） | **不动** | 本次事故修复点：稳定 ID 记录永不因名字被顶 |

稳定格式判定：`strings.HasPrefix(deviceID, "dev_")`。iOS 端生成式即
`"dev_" + UUID().hex`（BridgePairingClient.swift:1148，Keychain 持久）；现存 web/旧
记录为裸 32-hex 或其他格式；`pairing_handler.go` 兜底的 `dev-%x`（短横线、每配对随机）
仍按 legacy 处理，行为不变。

### 4.2 实现范围

- **只改** `go-bridge/trusted_device_store.go` 的 `MemoryDeviceStore.ReplaceDevice`
  匹配逻辑（约 ±8 行）；`FileDeviceStore` 事务路径自动继承。
- **不改**：两个调用点、Management API、wire 协议、iOS 端、持久化 schema
  （`devices.json` 无需迁移；已发生的错误删除不可恢复，重配即可）。
- 明确不做（另案）：remote-web 客户端稳定 deviceID 改造（web 记录现为 legacy 格式，
  名字清理行为对 web 保持现状）；iOS 上报名附加机型/尾缀（显示层改良，防不了顶掉）。

### 4.3 与"上游源码优先门"的关系

配对/设备信任库是 CordCode 私有 bridge 语义（无外部产品对应实现），属允许自建范围。
自建边界：本修复不引入新协议字段，仅收紧服务端既有清理启发式。

## 5. 测试计划

`go-bridge/trusted_device_store_test.go`（沿用 `makeTestRecord` helper）：

| 用例 | 预期 |
| --- | --- |
| 既有 `ReplaceDeviceReplacesStableIDCredentials` | 继续通过（同 ID 换 token） |
| 既有 `ReplaceDeviceRemovesLegacyRandomID` | 继续通过（legacy 记录按名字清理保留——把用例里 old 的 ID 明确为无 `dev_` 前缀形式） |
| **新增（事故回归）**：`dev_a` 与 `dev_b` 同 platform 同 displayName，先后 ReplaceDevice | 两条记录共存，`replaced` 为空 |
| **新增（若采纳 identityPublicKey 加固）**：ID 不同、identityPublicKey 相同 | 替换旧记录 |
| **新增边界**：旧记录 `dev-xxxx`（短横线兜底格式）+ 同名新稳定 ID | 按 legacy 清理（行为不变，锁定语义） |

定向运行：`go test ./go-bridge -run TestMemoryStore_ReplaceDevice -count=1`。

## 6. 交付与验证

1. **分支**：从 `main` 另切修复分支（如 `fix/device-replace-stable-id`），**不并入**
   `codex/grokbuild-leader-mode` 功能分支——owner 决策点，见 §8。
2. 单测定向通过后，按构建纪律 Release 构建并覆盖安装 `/Applications`（产物 commit 与
   HEAD 一致、双进程名重启、新 PID + 特征输出核验）。
3. **真机验证**（可用已授权的真机 E2E lane，见 iOS 仓
   `IOS_REAL_DEVICE_E2E_AUTOMATION.md` §5.2/§6.5，或手动）：
   - iPhone 11 已在册 → iPhone 16 重新扫码+批准 → **两条记录并存**，两台同时在线收发；
   - 反向再验一次（16 在册时配 11）；
   - Mac app 设备列表显示两台，撤销任一台只影响该台。
4. 回灌：MacBridge `think.md` 复盘条目（同名设备互踢 + UIDevice.name 语义）+ 两仓
   `CHANGELOG.md`。

## 7. 风险与兼容性

- **升级路径不受影响**：老（随机 ID）客户端升级到稳定 ID 后重配，旧记录仍按名字清理
  （legacy 分支保留）。
- **设备列表可能出现的历史重复**：仅当同一物理设备曾以 legacy + 稳定两种 ID 配对且
  名字不同时不再自动合并——现状本就如此的概率场景，可用 Mac 端 revoke 手工清理。
- **无法恢复已删记录**：本修复只阻止未来的错误删除；被顶掉的设备重配即可恢复。
- 无 wire/协议/持久化格式变化，旧 runtime 升级后已配对设备不受影响。

## 8. 待 owner 决策（已于 2026-09-03 裁决）

1. 实现分支：owner 裁决**搭车当前功能分支** `codex/grokbuild-leader-mode`（不从 main 另切）。
2. identityPublicKey 等价替换加固：**采纳**（含空 key 不视为同身份的守卫，见 §10）。
3. iOS 上报名加机型后缀：维持另案，本轮不做。

## 10. 实施记录（2026-09-03）

- 实现：`go-bridge/trusted_device_store.go` `MemoryDeviceStore.ReplaceDevice` 匹配收紧
  （`stableDeviceIDPrefix = "dev_"` 常量 + 三条件：同 ID / 双方非空 identityPublicKey
  相等 / legacy 无 `dev_` 前缀且名字平台相同）。`FileDeviceStore` 事务路径自动继承。
- 测试：新增 5 例（同名稳定 ID 共存、identity key 等价替换、空 identity key 不替换、
  `dev-` 短横线兜底仍按 legacy 清理等）；既有 2 例不动全过。
  `go test ./go-bridge -count=1` 包全量 75.2s 全绿。
- 部署：Release `505eefe` 工作树（含本修复）四门核对（新 PID、lstart 晚于构建、
  8777 = /Applications 内嵌 runtime、无违规残留）。
- 生产路径 wire 探针（`/tmp/cccode-samename-fix/probe_samename.py`，事后 revoke）：
  两台探针设备均以 `("ios","iPhone")` claim、先后经 Management API 批准 → **两条记录
  共存**、首台 token 经主 WS hello 仍认证通过。首轮误用 `dev-` 短横线 ID 的失败跑
  恰好实证了 legacy 清理分支仍生效（与单测一致）。
- 遗留：owner 双真机（iPhone 11 + 16 Pro）正向复测**已于 2026-09-03 通过**
  （「测试结果符合预期✅」）；已发生的错误删除（16 的 dev_c5ad42a3 记录）重配恢复。

## 9. 相关文档

- iOS 仓 `IOS_REAL_DEVICE_E2E_AUTOMATION.md`：真机 E2E 验证 lane（本修复真机验证可复用）。
- iOS 仓 `IOS_MAC_INTERACTION_FLOW.md`：配对/撤销协议语义。
- 事故现场日志与 devices.json 快照：2026-09-02 13:35 窗口（本文 §2 已摘录关键行）。
