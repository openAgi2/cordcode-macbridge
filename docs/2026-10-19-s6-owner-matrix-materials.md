# S-6 owner 真机验收矩阵执行材料（方案 v30 §7.4/§7.3）

日期：2026-10-19。配套版本：iOS `f74b96fe`+（E-10 测试后 iOS 仓再 +1 commit），Mac
`a90b3c08`（Release 已覆盖安装 + 五项运行态核验 20:58 通过）。

**这些材料 = 方案 §7.4 的 15 场景可直接照做清单。agent 不运行 UI automation、不代验；
每场景做完在「结果」列打勾/记值即可，日志侧证据（Mac trace / fg-trace）由 agent 事后
拉取对账，owner 无需采日志。**

## ③ 前提条件（当前状态）

| 项 | 状态 |
|---|---|
| Mac Release 覆盖安装 + 运行态核验 | ✅ 20:58（PID 65265、8777 监听者=新 PID、无违规残留、日志活跃） |
| iOS 包安装 | ✅ f74b96fe（含 S-4 完成交付 + E-10 测试增补） |
| 事故样本 session 可达 | ✅ 7f389d08（48+ turns，投影 22.8MB / gzip ~2.3MB） |
| 双路径 | Direct 已连；Relay 需关 WiFi 或断开直连触发（场景 5） |
| 2026-10-19 晚已有证据 | 场景 1/3/9/11 当晚验收已覆盖（下表标注），可跳过或复跑 |

## ① 15 场景 checklist

执行前每场景记录 Mac warm/cold：Mac 侧 agent 已有 materialization 日志（无需 owner 操作）；
owner 只需按「操作」列执行并对照「应看到」。

| # | 场景 | 操作 | 应看到 | Mac trace 核对点（agent 侧） | 结果 |
|---|---|---|---|---|---|
| 1 | 冷 App + idle 长 session，Direct | 杀掉 iOS App 重启 → 打开 7f389d08 | 全屏加载；**目标 ≤8s**（今晚 WiFi 实测 ~10-15s——链路差时如实记录，基线判定注明链路状态）；历史完整 | `get_session_projection` 一次 + gzip binary 帧；零重试 | ☐ |
| 2 | 冷 App + **running** session | Mac 侧让该 session 执行中 → iOS 冷启动打开 | 执行中状态不丢（不显示空闲）；若有权限卡则输入框禁用 | full 原生含 execution | ☐ |
| 3 | 切走再切回 | 打开 A → 切 B → 切回 A | **立即显示基底** + 顶部「正在同步…」胶囊短暂出现后消失；无整屏加载层 | anchor RPC（非 full）；✅当晚已验 | ☐ |
| 4 | Mac runtime 重启后重开 | 重开 Mac App → iOS 切回 session | 先显示缓存基底 + 同步标记；增量到达后替换清除；不退回纯 loading 不清缓存 | epoch 变化 → 对账重开或 FallbackFull | ☐ |
| 5 | **Relay 弱网重开** | iOS 关 WiFi（走蜂窝/Relay）→ 重开 session | 经 Relay 完成重开（时长如实记录） | relay 路径 hello echo（已布观测）+ anchor/full | ☐ |
| 6 | 断网时有 cache | 飞行模式 → 打开有缓存的 session | 显示 stale 基底 + 明确标记，不假成功、不显示「已同步」 | 无请求（离线） | ☐ |
| 7 | 恢复网络 | 场景 6 后关飞行模式 | 权威增量到达替换 preview，标记清除 | 重连后 anchor/full | ☐ |
| 8 | 滚动到顶 | 暖开 A → 上滑到顶 | **不触发网络加载**；顶部显示「更早的 N 轮未加载」（7f389d08 预期 N≈40） | 无 `direction=older` 请求 | ☐ |
| 9 | 首开种子写入 | 新 session 或清缓存后首开 → 切走切回 | 首开全量；重开走增量 | ✅当晚已验（hit rows=8/10） | ☐ |
| 10 | 重开零新增 | 无新消息时切回 | 无感（不闪加载、无同步残留） | anchor 空页/零 turns；水位线更新 | ☐ |
| 11 | 重开少量新增（1-5 轮） | Mac 发几条消息 → iOS 切回 | 旧内容立即 + 新 turn 追加；执行态正确 | anchor 页含新 turns；✅当晚已验（8→10 行） | ☐ |
| 12 | 重开大量新增（>2 页） | Mac 连续大量消息（≈200+ turns）→ iOS 切回 | 退全量重建（合法回退）；截断提示正确 | FallbackFull（typed/超 2 turns 页） | ☐ |
| 13 | Mac 重启后重开（水位线跨重启） | 重启 Mac App → iOS 切回 | 水位线仍有效（turnId 身份），增量照常完成 | anchor 照常（epoch 变化走对账重开） | ☐ |
| 14 | 水位线失效回退 | （难构造：anchor 离开 Mac 保留区——需 compaction 或极旧缓存）可跳过并在结果记 N/A | typed stale → 全量冷路径，不假成功不清缓存身份 | `cursor_stale` + FallbackFull（服务端已加分光日志） | ☐/N/A |
| 15 | 双设备 | iPad 同账号打开同一 session | 各自水位线各自增量，互不影响 | 两设备各自 anchor 序列 | ☐ |

**重试计数回报格式**（每场景一行）：`场景#：打开次数 N，其中全量请求 X 次（含合法回退
Y 次——stale/超页/未 echo），异常重试 Z 次`（agent 从 Mac trace 对账后填写，owner 只需
记录「打开了几次、是否报错」）。

## ② §7.3 性能基线记录模板（agent 事后从双端日志填写）

| 指标 | 目标 | 实测（待填） |
|---|---|---|
| 首开 full（Direct，Mac warm） | ≤8s（34MB 样本口径；22.8MB 今晚 ~7-15s 视链路） | |
| 首开 full（Relay） | ≤10s | |
| 重开（restore + anchor）权威完成 | ≤1s 目标（**今晚实测：磁盘恢复 23-33ms + anchor 0-179ms + 对账——总量级亚秒** ✓） | ✅ 已达标 |
| 重开缓存首屏 | ≤500ms 目标（**今晚实测：hit 后同秒上屏** ✓） | ✅ 已达标 |
| 15s timeout | 0 次 | |
| 单次打开异常重试 | 0 次（合法回退不计） | |
| full open（重开路径） | 仅 FallbackFull 触发 | ✅ 当晚干净窗口 0 次 |
| direct gzip 压缩比 | 样本 ~28% 体积 | 22.8MB→~2.3MB ≈ 10% ✓ |
| anchor 增量页 bytes | ≤8MiB/页（本样本 KB 级） | ✅ 当晚 1-21KB |

## 已知缺口（执行时可能遇到，均为登记项非阻断）

1. **fence/快照不一致竞态**（Mac，55ms 窗口）：Mac 消息刚落地时 iOS 重开偶发
   cursor_stale → 多一次全量（内容正确）。场景 11 快速连续操作时若见一次全量回退即此
   因——服务端已有分光日志，follow-up 已登记（BeginProjectionSnapshot 与 cutRev 一致性）。
2. **巨型尾轮会话**（单 turn >4MiB）无法播种 → 该类会话永远全量（已知限制）。
3. relay hello 能力回显日志已布（设备 relay 重连自动落证）；场景 5 执行时自然覆盖。

## 执行后

把本表结果告诉 agent（哪几项打勾、异常描述），agent 拉取双端日志完成对账与 §7.3 实测
列填写，出最终完成报告并收口 exec-plan。
