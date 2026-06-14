import SwiftUI

// App 退出时终止 go-bridge 子进程
class AppDelegate: NSObject, NSApplicationDelegate {
    var runtimeManager: RuntimeManager?

    func applicationWillTerminate(_ notification: Notification) {
        runtimeManager?.shutdownForExit()
    }
}

@main
struct MacBridgeApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var dependencies = AppDependencies()
    @AppStorage("appTheme") private var appTheme: String = ""

    private var selectedTheme: AppTheme {
        AppTheme(rawValue: appTheme) ?? .system
    }

    var body: some Scene {
        WindowGroup {
            ContentView(
                viewModel: dependencies.statusViewModel,
                pairingViewModel: dependencies.pairingViewModel,
                settingsViewModel: dependencies.settingsViewModel
            )
            .preferredColorScheme(selectedTheme.colorScheme)
            .background(Color(NSColor.windowBackgroundColor))
            .background(WindowGlassConfigurator(theme: selectedTheme))
            .environmentObject(dependencies)
            .onAppear {
                appDelegate.runtimeManager = dependencies.runtimeManager
                dependencies.startBridge()
            }
        }
        .defaultSize(width: 960, height: 680)
        .windowResizability(.contentMinSize)

        // 菜单栏图标及下拉菜单
        MenuBarExtra("CCCode Bridge", systemImage: menuBarIcon) {
            MenuBarMenu(
                viewModel: dependencies.statusViewModel,
                onStart: { dependencies.runtimeManager.start() },
                onStop: { dependencies.runtimeManager.stop() },
                onRestart: { dependencies.runtimeManager.restart() }
            )
        }
    }

    // MARK: - 菜单栏图标

    /// 根据状态切换菜单栏 SF Symbol
    private var menuBarIcon: String {
        switch dependencies.statusViewModel.status {
        case .ready: return "pc"
        case .readyNoAgents: return "pc"
        case .crashed: return "pc.trianglebadge.exclamationmark"
        case .starting: return "pc"
        case .stopped: return "pc"
        case .sleeping: return "pc.and.moon"
        case .idle: return "pc"
        }
    }
}

// MARK: - Glass Window

private extension AppTheme {
    var colorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light: return .light
        case .dark: return .dark
        }
    }

    var nsAppearanceName: NSAppearance.Name? {
        switch self {
        case .system: return nil
        case .light: return .aqua
        case .dark: return .darkAqua
        }
    }
}

/// 配置原生 titlebar 外观。正文区域保持不透明，避免透出后方窗口干扰阅读。
private struct WindowGlassConfigurator: NSViewRepresentable {
    let theme: AppTheme

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async { configure(view.window) }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        DispatchQueue.main.async { configure(nsView.window) }
    }

    private func configure(_ window: NSWindow?) {
        guard let window else { return }
        if !window.isOpaque {
            window.isOpaque = true
        }
        if window.backgroundColor != .windowBackgroundColor {
            window.backgroundColor = .windowBackgroundColor
        }
        if !window.titlebarAppearsTransparent {
            window.titlebarAppearsTransparent = true
        }
        if !window.isMovableByWindowBackground {
            window.isMovableByWindowBackground = true
        }

        let targetAppearance = theme.nsAppearanceName
        if window.appearance?.name != targetAppearance {
            window.appearance = targetAppearance.flatMap(NSAppearance.init(named:))
        }
    }
}

extension View {
    /// 标准面板毛玻璃样式
    func glassPanel(cornerRadius: CGFloat = 8) -> some View {
        self
            .background(
                RoundedRectangle(cornerRadius: cornerRadius)
                    .fill(Color(NSColor.controlBackgroundColor).opacity(0.72))
            )
            .overlay(
                RoundedRectangle(cornerRadius: cornerRadius)
                    .stroke(Color(NSColor.separatorColor).opacity(0.7), lineWidth: 0.5)
            )
    }
}
