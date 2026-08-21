# 开发报告 9：Audit-008 集中产品路径补洞

- 完整报告：`docs/2026-08-20-opencode-web-audit009-repair完成情况.md`
- Mac commits：`851c2c6`、`cb1d8e6`、`d763d82`
- iOS commit：`71007a4`
- 开发 agent 声明：canonical 未改；四个 Wave 连续完成；exec-plan 恢复全 done；owner UI 未执行。

## 开发 agent 的主要完成主张

- C4 routed session 不再复制到 passive tap；active relay 成为唯一 Kernel ingest owner，unopened+unsubscribed 不建隐藏 timeline。
- A7 question 解码 `tool.messageID/callID` 并携带 TurnID/ItemID 进入 Projection。
- Todo/provider/config/mutation 删除 Audit-008 指出的 alias/default/silent-skip/any-2xx 行为。
- UIKit 与 SwiftUI 两个模型配置入口消费同一 `ModelVariantSelection` contract。
- capability 测试改用 production `deriveBackendCapabilities`。
- Mac full/race/build/vet、真实 1.18.18 sandbox、iOS 11 项定向测试通过；Mac Release 已安装。
- iPhone 在开发 agent 安装时点 unavailable，因此 iOS install/launch 未完成。

本文件保存报告入口和完成主张；独立裁决见同号 audit。
