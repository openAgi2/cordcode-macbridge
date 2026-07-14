import Foundation

/// 窗口与内容列的布局契约。
///
/// UX 重设计（2026-07-13）P0-3 要求：`PageContainer` 的 820pt 硬上限改为可配置，
/// 主窗口最小宽度提升至 920pt，工作列扩到 880pt。这些常量集中在此，便于
/// `PageContainer`、`ContentView`、`WorkspaceView` 与连接状态 Sheet 共享，也便于单元测试。
///
/// 注意：这不是样式微调，而是 P0 布局契约——连接状态 Sheet 不再受 820pt 全局上限约束。
enum LayoutConstants {
    /// 主窗口最小宽度。窄窗口保持单列、标题与主动作同行可见。
    static let minWindowWidth: CGFloat = 920

    /// 主窗口最小高度。
    static let minWindowHeight: CGFloat = 560

    /// 默认工作列最大宽度。内容左对齐于该列；正常状态不展示端口/版本/endpoint。
    static let workColumnWidth: CGFloat = 880

    /// 工作站首页的外层内容宽度（含两侧 pageHorizontalPadding）。
    /// 固定阅读宽度，避免宽窗口把状态页拉成稀疏的仪表盘。
    static let workspaceHomeContentWidth: CGFloat = 820

    /// 窗口宽度超过该阈值时，才出现只读的连接健康辅助信息栏；
    /// 不新增独立目的地或操作。
    static let wideSecondaryThreshold: CGFloat = 1180

    /// 连接状态 Sheet 的建议最大宽度。
    /// 为支持内部左右分栏（模拟老版列表+详情），并让右侧配置区有足够空间，设为 1100pt。
    static let connectionSheetWidth: CGFloat = 1100

    /// 连接状态 Sheet 的固定呈现高度。避免 GeometryReader 被 macOS 以标题的最小高度展示。
    static let connectionSheetHeight: CGFloat = 680

    /// 配对 Sheet 的固定尺寸，容纳二维码与流程说明的并列布局。
    static let pairingSheetWidth: CGFloat = 720
    static let pairingSheetHeight: CGFloat = 600

    // MARK: - 第二轮宽屏内容宽度契约 (r5 最终)
    /// PageContainer 水平 padding（两侧各 30pt，总 60pt）。
    static let pageHorizontalPadding: CGFloat = 30

    /// 内层 GeometryReader 看到的可用内容宽度阈值（最小双列触发）。
    /// 1164pt = 900 (main) + 24 (gap) + 240 (inspector)
    static let workspaceWideContentThreshold: CGFloat = 1164

    /// 推荐舒适内容宽度（提升到 920/260 列尺寸）。
    /// 1204pt = 920 + 24 + 260
    static let workspacePreferredContentWidth: CGFloat = 1204

    /// PageContainer.maxContentWidth 上限（内容宽度 + 两侧 padding）。
    /// 用于 Workspace 宽屏时始终传入的最大容器宽度。
    static let workspaceWideContainerWidth: CGFloat = workspaceWideContentThreshold + 2 * pageHorizontalPadding   // 1224
    static let workspaceMaxContainerWidth: CGFloat = workspacePreferredContentWidth + 2 * pageHorizontalPadding  // 1264

    /// 最小双列尺寸（1164pt 时使用）。
    static let dualColumnMainMin: CGFloat = 900
    static let dualColumnGap: CGFloat = 24
    static let dualColumnInspectorMin: CGFloat = 240

    /// 推荐舒适双列尺寸（1204pt+ 时使用）。
    static let dualColumnMainPreferred: CGFloat = 920
    static let dualColumnInspectorPreferred: CGFloat = 260
}
