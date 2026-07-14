import SwiftUI

// App 退出时终止 go-bridge 子进程
class AppDelegate: NSObject, NSApplicationDelegate {
    var runtimeManager: RuntimeManager?
    /// M1: 通知协调器(由 MacBridgeApp.onAppear 注入),启动时请求通知授权。
    var notificationCoordinator: NotificationCoordinator?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // M1: 请求系统通知授权(.alert/.sound/.badge)。
        // NOTE: notificationCoordinator is assigned in MacBridgeApp.onAppear (below), which runs
        // AFTER applicationDidFinishLaunching for SwiftUI apps. So it is nil here on first launch —
        // requesting authorization here would silently no-op and notifications would never be granted
        // (the real-world symptom: pairing claims never produced a system notification, only the
        // dock-badge fallback). The actual request is made in onAppear right after assignment.
    }

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
                appDelegate.notificationCoordinator = dependencies.notificationCoordinator
                dependencies.startBridge()
                // M1: request notification authorization here (not in applicationDidFinishLaunching,
                // where notificationCoordinator is still nil for SwiftUI apps — see note above).
                appDelegate.notificationCoordinator?.requestAuthorization()
            }
        }
        .defaultSize(width: 1280, height: 840)
        .windowResizability(.contentMinSize)
        // P2-3：键盘快捷键 ⌘⇧D 打开「帮助与诊断」。Settings 由原生 Settings scene 承接 ⌘,。
        .commands {
            CommandGroup(replacing: .help) {
                Button(L10n.helpDiagnostics) {
                    NotificationCenter.default.post(name: .openDiagnosticsRequest, object: nil)
                }
                .keyboardShortcut("d", modifiers: [.command, .shift])

                Button(L10n.connectionStatus) {
                    NotificationCenter.default.post(name: .openConnectionStatusRequest, object: nil)
                }
                .keyboardShortcut("l", modifiers: [.command, .shift])
            }
        }

        // 原生 Settings 场景（⌘, 打开）。UX 重设计 P0-1：从 sidebar 移入；
        // P2-1 将按「通用 / 高级」重组内容，此处先承载现有 SettingsView。
        Settings {
            SettingsView(viewModel: dependencies.settingsViewModel)
        }

        // 菜单栏图标及下拉菜单
        MenuBarExtra("CordCode Link", systemImage: menuBarIcon) {
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
        if window.titleVisibility != .hidden {
            window.titleVisibility = .hidden
        }
        if !window.isMovableByWindowBackground {
            window.isMovableByWindowBackground = true
        }

        let targetAppearance = theme.nsAppearanceName
        if window.appearance?.name != targetAppearance {
            window.appearance = targetAppearance.flatMap(NSAppearance.init(named:))
        }

        if window.frame.size.width < 1000 {
            var frame = window.frame
            let oldSize = frame.size
            frame.size = CGSize(width: 1280, height: 840)
            frame.origin.y -= (840 - oldSize.height)
            window.setFrame(frame, display: true)
        }

        if let closeButton = window.standardWindowButton(.closeButton),
           closeButton.frame.origin.x < 15 {
            let closeX: CGFloat = 24
            let spacing: CGFloat = 23
            
            let closeBtn = window.standardWindowButton(.closeButton)
            let minBtn = window.standardWindowButton(.miniaturizeButton)
            let zoomBtn = window.standardWindowButton(.zoomButton)
            
            closeBtn?.frame.origin.x = closeX
            closeBtn?.frame.origin.y -= 9
            
            minBtn?.frame.origin.x = closeX + spacing
            minBtn?.frame.origin.y -= 9
            
            zoomBtn?.frame.origin.x = closeX + spacing * 2
            zoomBtn?.frame.origin.y -= 9
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
