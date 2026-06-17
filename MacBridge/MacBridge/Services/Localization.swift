import Foundation

// MARK: - 语言枚举

/// 支持的语言
enum AppLanguage: String, CaseIterable, Identifiable {
    case system = ""
    case en = "en"
    case zhHans = "zh-Hans"

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .system: return L10n.tr("system_default")
        case .en: return "English"
        case .zhHans: return "简体中文"
        }
    }
}

// MARK: - 外观枚举

/// 支持的窗口外观
enum AppTheme: String, CaseIterable, Identifiable {
    case system = ""
    case light = "light"
    case dark = "dark"

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .system: return L10n.tr("theme_system")
        case .light: return L10n.tr("theme_light")
        case .dark: return L10n.tr("theme_dark")
        }
    }
}

// MARK: - 本地化字符串查找

/// 统一本地化字符串入口
///
/// 使用方式：L10n.overview / L10n.pairDevice 等
/// 语言由 UserDefaults "appLanguage" 控制，默认跟随系统
enum L10n {
    static var current: AppLanguage {
        let raw = UserDefaults.standard.string(forKey: "appLanguage") ?? ""
        if raw.isEmpty {
            let preferred = Locale.preferredLanguages.first ?? "en"
            return preferred.hasPrefix("zh") ? .zhHans : .en
        }
        return AppLanguage(rawValue: raw) ?? .en
    }

    // 查表
    static func tr(_ key: String) -> String {
        table[current]?[key] ?? table[.en]?[key] ?? key
    }

    // MARK: - Tab 标题

    static var overview: String { tr("overview") }
    static var devices: String { tr("devices") }
    static var aiTools: String { tr("ai_tools") }
    static var settings: String { tr("settings") }
    static var diagnostics: String { tr("diagnostics") }
    static var remoteAccessTab: String { tr("remote_access_tab") }
    static var devicesPairing: String { tr("devices_pairing") }
    static var logsDiagnostics: String { tr("logs_diagnostics") }

    // MARK: - Overview

    static var ccCodeBridge: String { tr("cccode_bridge") }
    static var status: String { tr("status") }
    static var bridgeRunning: String { tr("bridge_running") }
    static var aiToolsReady: String { tr("ai_tools_ready") }
    static var trustedDevices: String { tr("trusted_devices") }
    static var noTrustedDevices: String { tr("no_trusted_devices") }
    static var loadingDevices: String { tr("loading_devices") }
    static var noAiToolsDetected: String { tr("no_ai_tools_detected") }
    static var remoteAccess: String { tr("remote_access") }
    static var remoteAccessHint: String { tr("remote_access_hint") }
    static var configured: String { tr("configured") }
    static var notConfigured: String { tr("not_configured") }
    static var edit: String { tr("edit") }
    static var connectionMode: String { tr("connection_mode") }
    static var localOnly: String { tr("local_only") }
    static var remoteConfigured: String { tr("remote_configured") }
    static var securityLevel: String { tr("security_level") }
    static var secEncrypted: String { tr("sec_encrypted") }
    static var secTailscaleTunnel: String { tr("sec_tailscale_tunnel") }
    static var secLan: String { tr("sec_lan") }
    static var secInsecure: String { tr("sec_insecure") }
    static var secUnknown: String { tr("sec_unknown") }
    static var insecureWarning: String { tr("insecure_warning") }
    static var insecureWarningDetail: String { tr("insecure_warning_detail") }
    static var tailscalePath: String { tr("tailscale_path") }
    static var tailscalePathHint: String { tr("tailscale_path_hint") }
    static var frpPath: String { tr("frp_path") }
    static var frpPathHint: String { tr("frp_path_hint") }
    static var remoteURLLabel: String { tr("remote_url_label") }
    static var localURLLabel: String { tr("local_url_label") }
    static var diagnosisInfo: String { tr("diagnosis_info") }
    static var diagnosisDisclaimer: String { tr("diagnosis_disclaimer") }
    static var checkStatus: String { tr("check_status") }
    static var saveAndRestart: String { tr("save_and_restart") }
    static var lastConnected: String { tr("last_connected") }
    static var overviewSubtitle: String { tr("overview_subtitle") }
    static var overviewRuntime: String { tr("overview_runtime") }
    static var overviewRunning: String { tr("overview_running") }
    static var overviewRunningNoAgents: String { tr("overview_running_no_agents") }
    static var overviewStarting: String { tr("overview_starting") }
    static var overviewStopped: String { tr("overview_stopped") }
    static var overviewStartFailed: String { tr("overview_start_failed") }
    static var overviewSleeping: String { tr("overview_sleeping") }
    static var overviewIdle: String { tr("overview_idle") }
    static var overviewRestart: String { tr("overview_restart") }
    static var overviewRestarting: String { tr("overview_restarting") }
    static var overviewDetectAgain: String { tr("overview_detect_again") }
    static var overviewTestConnection: String { tr("overview_test_connection") }
    static var overviewManageDevices: String { tr("overview_manage_devices") }
    static var overviewViewSettings: String { tr("overview_view_settings") }
    static var overviewMoreActions: String { tr("overview_more_actions") }
    static var overviewStopConfirmTitle: String { tr("overview_stop_confirm_title") }
    static var overviewStopConfirmMessage: String { tr("overview_stop_confirm_message") }
    static var overviewPort: String { tr("overview_port") }
    static var overviewUptime: String { tr("overview_uptime") }
    static var overviewVersion: String { tr("overview_version") }
    static var overviewDetailsUnavailable: String { tr("overview_details_unavailable") }
    static var overviewUptimeUnderMinute: String { tr("overview_uptime_under_minute") }
    static var overviewUptimeMinutes: String { tr("overview_uptime_minutes") }
    static var overviewUptimeHoursMinutes: String { tr("overview_uptime_hours_minutes") }
    static var overviewRecentlySeen: String { tr("overview_recently_seen") }
    static var overviewOfficialRelayConfigured: String { tr("overview_official_relay_configured") }
    static var overviewCustomRelayConfigured: String { tr("overview_custom_relay_configured") }
    static var overviewRelayNotConfigured: String { tr("overview_relay_not_configured") }
    static var overviewRelayUnavailable: String { tr("overview_relay_unavailable") }
    static var overviewRuntimeDetailsFailed: String { tr("overview_runtime_details_failed") }
    static var overviewRelayStatusFailed: String { tr("overview_relay_status_failed") }

    // MARK: - Pairing

    static var pairNewDevice: String { tr("pair_new_device") }
    static var scanWithCCCode: String { tr("scan_with_cccode") }
    static var manualCode: String { tr("manual_code") }
    static var waitingForDevice: String { tr("waiting_for_device") }
    static var creatingPairingSession: String { tr("creating_pairing_session") }
    static var securityHint1: String { tr("security_hint_1") }
    static var securityHint2: String { tr("security_hint_2") }
    static var securityHint3: String { tr("security_hint_3") }
    static var bridgeLabel: String { tr("bridge_label") }
    static var deviceRequest: String { tr("device_request") }
    static var approve: String { tr("approve") }
    static var reject: String { tr("reject") }
    static var devicePairedSuccessfully: String { tr("device_paired_successfully") }
    static var pairAnotherDevice: String { tr("pair_another_device") }
    static var deviceRejected: String { tr("device_rejected") }
    static var pairingSessionExpired: String { tr("pairing_session_expired") }
    static var tryAgain: String { tr("try_again") }
    static var retry: String { tr("retry") }
    static var pairingNewDevice: String { tr("pairing_new_device") }
    static var pairingManagementUnavailable: String { tr("pairing_management_unavailable") }
    static var pairingInvalidExpiry: String { tr("pairing_invalid_expiry") }
    static var pairingUnknownDevice: String { tr("pairing_unknown_device") }
    static var pairingQRCode: String { tr("pairing_qr_code") }
    static var pairingStepScan: String { tr("pairing_step_scan") }
    static var pairingStepConfirm: String { tr("pairing_step_confirm") }
    static var pairingStepComplete: String { tr("pairing_step_complete") }
    static var pairingCopyCode: String { tr("pairing_copy_code") }
    static var pairingCopied: String { tr("pairing_copied") }
    static var pairingExpiresIn: String { tr("pairing_expires_in") }
    static var pairingConnectionDetails: String { tr("pairing_connection_details") }
    static var pairingConnectTo: String { tr("pairing_connect_to") }
    static var pairingApprovalExplanation: String { tr("pairing_approval_explanation") }
    static var pairingGenerateAgain: String { tr("pairing_generate_again") }
    static var pairingLAN: String { tr("pairing_lan") }
    static var pairingRelay: String { tr("pairing_relay") }
    static var pairingAdvancedPath: String { tr("pairing_advanced_path") }

    // MARK: - Devices

    static var authorizedDevices: String { tr("authorized_devices") }
    static var noAuthorizedDevices: String { tr("no_authorized_devices") }
    static var remove: String { tr("remove") }
    static var cancel: String { tr("cancel") }
    static var removeDeviceConfirm: String { tr("remove_device_confirm") }
    static var removeDeviceMessage: String { tr("remove_device_message") }
    static var paired: String { tr("paired") }
    static var lastSeen: String { tr("last_seen") }
    static var justNow: String { tr("just_now") }
    static var minAgo: String { tr("min_ago") }
    static var hrAgo: String { tr("hr_ago") }
    static var daysAgo: String { tr("days_ago") }
    static var devicesSubtitle: String { tr("devices_subtitle") }
    static var devicesRevokeAuthorization: String { tr("devices_revoke_authorization") }
    static var devicesRevokeConfirm: String { tr("devices_revoke_confirm") }
    static var devicesRevokeMessage: String { tr("devices_revoke_message") }
    static var devicesActions: String { tr("devices_actions") }
    static var devicesUnknownDevice: String { tr("devices_unknown_device") }

    // MARK: - AI Tools

    static var refreshAll: String { tr("refresh_all") }
    static var noAiToolsConfigured: String { tr("no_ai_tools_configured") }
    static var allUnavailableGuidance: String { tr("all_unavailable_guidance") }
    static var test: String { tr("test") }
    static var externalTurnsPolling: String { tr("external_turns_polling") }
    static var notInstalled: String { tr("not_installed") }
    static var serviceNotRunning: String { tr("service_not_running") }
    static var loginRequired: String { tr("login_required") }
    static var detectionTimedOut: String { tr("detection_timed_out") }
    static var cannotReachService: String { tr("cannot_reach_service") }
    static var checkDocsGuidance: String { tr("check_docs_guidance") }

    // MARK: - AI Tools status

    static var statusReady: String { tr("status_ready") }
    static var statusNotFound: String { tr("status_not_found") }
    static var statusLoginRequired: String { tr("status_login_required") }
    static var statusNotRunning: String { tr("status_not_running") }
    static var statusPortConflict: String { tr("status_port_conflict") }
    static var statusVersionIncompatible: String { tr("status_version_incompatible") }
    static var statusPermissionDenied: String { tr("status_permission_denied") }

    // MARK: - Diagnostics

    static var rawLogs: String { tr("raw_logs") }
    static var last200Lines: String { tr("last_200_lines") }
    static var copyRawLogs: String { tr("copy_raw_logs") }
    static var noLogsAvailable: String { tr("no_logs_available") }
    static var diagnosticsSubtitle: String { tr("diagnostics_subtitle") }
    static var diagnosticsReading: String { tr("diagnostics_reading") }

    // MARK: - Settings

    static var bridgeName: String { tr("bridge_name") }
    static var bridgeNameHint: String { tr("bridge_name_hint") }
    static var save: String { tr("save") }
    static var openCodeAuth: String { tr("opencode_auth") }
    static var openCodeAuthHint: String { tr("opencode_auth_hint") }
    static var openCodeAuthGuidanceAuto: String { tr("opencode_auth_guidance_auto") }
    static var openCodeAuthGuidanceManual: String { tr("opencode_auth_guidance_manual") }
    static var username: String { tr("username") }
    static var password: String { tr("password") }
    static var language: String { tr("language") }
    static var appearance: String { tr("appearance") }
    static var themeSystem: String { tr("theme_system") }
    static var themeLight: String { tr("theme_light") }
    static var themeDark: String { tr("theme_dark") }
    static var settingsGeneral: String { tr("settings_general") }
    static var settingsMacBridge: String { tr("settings_macbridge") }
    static var settingsName: String { tr("settings_name") }
    static var settingsNamePlaceholder: String { tr("settings_name_placeholder") }
    static var settingsAuthenticationStatus: String { tr("settings_authentication_status") }
    static var settingsAutomaticallyConfigured: String { tr("settings_automatically_configured") }
    static var settingsCredentialsApplyAfterRestart: String { tr("settings_credentials_apply_after_restart") }
    static var settingsManualAuthentication: String { tr("settings_manual_authentication") }
    static var settingsShowPassword: String { tr("settings_show_password") }
    static var settingsHidePassword: String { tr("settings_hide_password") }
    static var settingsRegenerate: String { tr("settings_regenerate") }
    static var settingsLaunchCommand: String { tr("settings_launch_command") }
    static var settingsCopyCommand: String { tr("settings_copy_command") }
    static var settingsSaveCredentialsRestart: String { tr("settings_save_credentials_restart") }
    static var settingsRegenerateConfirmTitle: String { tr("settings_regenerate_confirm_title") }
    static var settingsRegenerateConfirmMessage: String { tr("settings_regenerate_confirm_message") }
    static var settingsSaving: String { tr("settings_saving") }
    static var settingsOpenCodeCommand: String { tr("settings_opencode_command") }
    static var settingsAutoRestartTitle: String { tr("settings_auto_restart_title") }
    static var settingsAutoRestartEnable: String { tr("settings_auto_restart_enable") }
    static var settingsAutoRestartInterval: String { tr("settings_auto_restart_interval") }
    static var settingsAutoRestartHint: String { tr("settings_auto_restart_hint") }

    // MARK: - Remote

    static var remoteTitle: String { tr("remote_title") }
    static var remoteSubtitle: String { tr("remote_subtitle") }
    static var remoteConnectionPaths: String { tr("remote_connection_paths") }
    static var remoteLAN: String { tr("remote_lan") }
    static var remoteUnavailable: String { tr("remote_unavailable") }
    static var remoteAutomatic: String { tr("remote_automatic") }
    static var remoteCustomRelay: String { tr("remote_custom_relay") }
    static var remoteOfficialRelay: String { tr("remote_official_relay") }
    static var remoteRelay: String { tr("remote_relay") }
    static var remoteCustomize: String { tr("remote_customize") }
    static var remoteCurrentCustomRelay: String { tr("remote_current_custom_relay") }
    static var remoteDefaultRelay: String { tr("remote_default_relay") }
    static var remoteRestoreDefault: String { tr("remote_restore_default") }
    static var remoteRelayValidation: String { tr("remote_relay_validation") }
    static var remoteValidatingRelay: String { tr("remote_validating_relay") }
    static var remoteValidating: String { tr("remote_validating") }
    static var remoteConnectionStrategy: String { tr("remote_connection_strategy") }
    static var remoteStrategySummary: String { tr("remote_strategy_summary") }
    static var remoteAdvancedConnections: String { tr("remote_advanced_connections") }
    static var remoteTailscaleUnavailable: String { tr("remote_tailscale_unavailable") }
    static var remoteVPS: String { tr("remote_vps") }
    static var remoteVPSValidation: String { tr("remote_vps_validation") }
    static var remoteRelayFailed: String { tr("remote_relay_failed") }
    static var remoteTailscale: String { tr("remote_tailscale") }
    static var remoteVPSPlaceholder: String { tr("remote_vps_placeholder") }

    // MARK: - Settings messages

    static var nameUpdated: String { tr("name_updated") }
    static var saveFailed: String { tr("save_failed") }
    static var saveFailedHttp: String { tr("save_failed_http") }
    static var savedRestarting: String { tr("saved_restarting") }
    static var failedLoadAgents: String { tr("failed_load_agents") }
    static var failedRefreshAgents: String { tr("failed_refresh_agents") }
    static var failedTestAgent: String { tr("failed_test_agent") }
    static var showingLastAgentResults: String { tr("showing_last_agent_results") }
    static var errorCannotConnect: String { tr("error_cannot_connect") }
    static var errorRemoveDevice: String { tr("error_remove_device") }
    static var error: String { tr("error") }
    static var unknownError: String { tr("unknown_error") }
    static var ok: String { tr("ok") }

    // MARK: - MenuBar

    static var restartBridge: String { tr("restart_bridge") }
    static var stopBridge: String { tr("stop_bridge") }
    static var startBridge: String { tr("start_bridge") }
    static var openBridge: String { tr("open_bridge") }
    static var quit: String { tr("quit") }
    static var start: String { tr("start") }
    static var stop: String { tr("stop") }
    static var restart: String { tr("restart") }

    // MARK: - 翻译表

    private static let table: [AppLanguage: [String: String]] = [
        .en: [
            "overview": "Overview",
            "system_default": "System Default",
            "devices": "Devices",
            "ai_tools": "AI Tools",
            "settings": "Settings",
            "diagnostics": "Diagnostics",
            "cccode_bridge": "CCCode Bridge",
            "status": "Status",
            "bridge_running": "Bridge running",
            "ai_tools_ready": "%d AI tool(s) ready",
            "trusted_devices": "%d trusted device(s)",
            "no_trusted_devices": "No trusted devices",
            "loading_devices": "Loading devices...",
            "no_ai_tools_detected": "No AI tools detected",
            "remote_access": "Remote Access",
            "remote_access_hint": "Optional. Allows iPhone to connect via a public URL.",
            "remote_access_tab": "Remote Access",
            "devices_pairing": "Devices & Pairing",
            "logs_diagnostics": "Logs & Diagnostics",
            "configured": "Configured",
            "not_configured": "Not configured",
            "edit": "Edit",
            "connection_mode": "Connection Mode",
            "local_only": "Local only",
            "remote_configured": "Remote configured",
            "security_level": "Security",
            "sec_encrypted": "WSS encrypted",
            "sec_tailscale_tunnel": "Tailscale tunnel (WireGuard)",
            "sec_lan": "LAN",
            "sec_insecure": "Insecure (public ws://)",
            "sec_unknown": "Unknown",
            "insecure_warning": "⚠️ Insecure connection",
            "insecure_warning_detail": "This URL uses ws:// over a public address. Data is transmitted unencrypted. Use wss:// or Tailscale instead.",
            "tailscale_path": "Tailscale (Recommended)",
            "tailscale_path_hint": "Install Tailscale on both Mac and iPhone. Use the Tailscale IP (100.x.x.x) as the Remote URL.",
            "frp_path": "FRP / VPS / Reverse Proxy",
            "frp_path_hint": "Set up a reverse proxy with TLS termination and use the wss:// URL as the Remote URL.",
            "remote_url_label": "Remote URL",
            "local_url_label": "Local URL",
            "diagnosis_info": "Remote URL Diagnosis",
            "diagnosis_disclaimer": "Diagnosis shows local configuration state, not external reachability.",
            "check_status": "Check Status",
            "configuration_paths": "Configuration Paths",
            "save_and_restart": "Save & Restart",
            "last_connected": "— last connected %@",
            "overview_subtitle": "Current MacBridge runtime and connection status",
            "overview_runtime": "Runtime",
            "overview_running": "Running",
            "overview_running_no_agents": "Running, no AI tools available",
            "overview_starting": "Starting",
            "overview_stopped": "Stopped",
            "overview_start_failed": "Start failed",
            "overview_sleeping": "Paused",
            "overview_idle": "Not started",
            "overview_restart": "Start Again",
            "overview_restarting": "Restarting...",
            "overview_detect_again": "Detect Again",
            "overview_test_connection": "Test Connection",
            "overview_manage_devices": "Manage Devices",
            "overview_view_settings": "View Settings",
            "overview_more_actions": "More runtime actions",
            "overview_stop_confirm_title": "Stop MacBridge?",
            "overview_stop_confirm_message": "All connected devices will disconnect immediately. Connections can resume after restarting.",
            "overview_port": "Port %d",
            "overview_uptime": "Up %@",
            "overview_version": "Version %@",
            "overview_details_unavailable": "Runtime details temporarily unavailable",
            "overview_uptime_under_minute": "less than 1 minute",
            "overview_uptime_minutes": "%d minutes",
            "overview_uptime_hours_minutes": "%d hours %d minutes",
            "overview_recently_seen": "Recently seen: %@",
            "overview_official_relay_configured": "Official Relay configured",
            "overview_custom_relay_configured": "Custom Relay configured",
            "overview_relay_not_configured": "Relay not configured",
            "overview_relay_unavailable": "Relay status temporarily unavailable",
            "overview_runtime_details_failed": "Runtime details unavailable: %@",
            "overview_relay_status_failed": "Relay status unavailable: %@",
            "pair_new_device": "Pair New Device",
            "scan_with_cccode": "Scan with CCCode on iPhone",
            "manual_code": "Manual code:",
            "waiting_for_device": "Waiting for device...",
            "creating_pairing_session": "Creating pairing session...",
            "security_hint_1": "Only approve devices you recognize.",
            "security_hint_2": "This Mac will ask before granting access.",
            "security_hint_3": "Pairing codes expire after a few minutes.",
            "bridge_label": "Bridge: %@",
            "device_request": "Device Request",
            "approve": "Approve",
            "reject": "Reject",
            "device_paired_successfully": "Device paired successfully",
            "pair_another_device": "Pair Another Device",
            "device_rejected": "Device rejected",
            "pairing_session_expired": "Pairing session expired",
            "try_again": "Try Again",
            "retry": "Retry",
            "pairing_new_device": "Pair New Device",
            "pairing_management_unavailable": "MacBridge management service is unavailable.",
            "pairing_invalid_expiry": "The pairing session returned an invalid expiration time.",
            "pairing_unknown_device": "Unknown Device",
            "pairing_qr_code": "Pairing QR code",
            "pairing_step_scan": "Scan with iPhone",
            "pairing_step_confirm": "Confirm the device on this Mac",
            "pairing_step_complete": "Complete the connection",
            "pairing_copy_code": "Copy manual code",
            "pairing_copied": "Copied",
            "pairing_expires_in": "QR code expires in %@",
            "pairing_connection_details": "Connection Details",
            "pairing_connect_to": "Connect to: %@",
            "pairing_approval_explanation": "After approval, this device can use the currently enabled AI tools through this MacBridge.",
            "pairing_generate_again": "Generate New QR Code",
            "pairing_lan": "Local Network",
            "pairing_relay": "Encrypted Relay",
            "pairing_advanced_path": "Advanced Connection",
            "authorized_devices": "Authorized Devices",
            "no_authorized_devices": "No authorized devices. Use the pairing section above to add one.",
            "remove": "Remove",
            "cancel": "Cancel",
            "remove_device_confirm": "Remove %@?",
            "remove_device_message": "This device will need to be paired again to access this Mac.",
            "paired": "paired %@",
            "last_seen": "last seen %@",
            "just_now": "just now",
            "min_ago": "%d min ago",
            "hr_ago": "%d hr ago",
            "days_ago": "%d days ago",
            "devices_subtitle": "Pair a new device and manage authorized devices",
            "devices_revoke_authorization": "Revoke Authorization...",
            "devices_revoke_confirm": "Revoke authorization for “%@”?",
            "devices_revoke_message": "The device will disconnect immediately and must pair again before its next use.",
            "devices_actions": "Device actions",
            "devices_unknown_device": "Device",
            "refresh_all": "Refresh All",
            "no_ai_tools_configured": "No AI tools configured",
            "all_unavailable_guidance": "CCCode Bridge is ready. Install or log in to an AI coding tool to get started.",
            "test": "Test",
            "external_turns_polling": "External turns are refreshed by polling",
            "not_installed": "Not installed. Install %@ to enable this tool.",
            "service_not_running": "Service is not running. Start it to enable detection.",
            "login_required": "Login required. Run the tool's login command first.",
            "detection_timed_out": "Detection timed out. The service may not be responding.",
            "cannot_reach_service": "Cannot reach the service. Check your connection.",
            "check_docs_guidance": "Check the tool's documentation for setup instructions.",
            "status_ready": "Ready",
            "status_not_found": "Not Found",
            "status_login_required": "Login Required",
            "status_not_running": "Not Running",
            "status_port_conflict": "Port Conflict",
            "status_version_incompatible": "Version Incompatible",
            "status_permission_denied": "Permission Denied",
            "raw_logs": "Raw Logs",
            "last_200_lines": "Last 200 lines from bridge log",
            "copy_raw_logs": "Copy Raw Logs",
            "no_logs_available": "No logs available",
            "diagnostics_subtitle": "Inspect recent MacBridge runtime logs",
            "diagnostics_reading": "Reading logs...",
            "bridge_name": "Bridge Name",
            "bridge_name_hint": "The name other devices see when connecting. Changes take effect immediately.",
            "save": "Save",
            "opencode_auth": "OpenCode Authentication",
            "opencode_auth_hint": "OpenCode HTTP service requires Basic Auth. After saving, Bridge will restart with the new credentials.",
            "opencode_auth_guidance_auto": "• Auto-Pairing: MacBridge automatically generates random credentials and writes them to OpenCode Desktop configuration folder. No manual setup is needed.",
            "opencode_auth_guidance_manual": "• Manual/CLI: If running OpenCode via command line, start it using:",
            "username": "Username",
            "password": "Password",
            "language": "Language",
            "appearance": "Appearance",
            "theme_system": "Follow System",
            "theme_light": "Light",
            "theme_dark": "Dark",
            "settings_general": "General",
            "settings_macbridge": "MacBridge",
            "settings_name": "Name",
            "settings_name_placeholder": "Mac",
            "settings_authentication_status": "Authentication Status",
            "settings_automatically_configured": "Automatically configured",
            "settings_credentials_apply_after_restart": "OpenCode will use the latest credentials after MacBridge restarts.",
            "settings_manual_authentication": "Manual Authentication Settings",
            "settings_show_password": "Show password",
            "settings_hide_password": "Hide password",
            "settings_regenerate": "Regenerate",
            "settings_launch_command": "Launch Command",
            "settings_copy_command": "Copy launch command",
            "settings_save_credentials_restart": "Save Credentials and Restart MacBridge",
            "settings_regenerate_confirm_title": "Regenerate password?",
            "settings_regenerate_confirm_message": "The old password becomes invalid only after you save and restart MacBridge.",
            "settings_saving": "Saving...",
            "settings_opencode_command": "opencode serve --port 64667 --hostname 127.0.0.1",
            "settings_auto_restart_title": "Auto Restart",
            "settings_auto_restart_enable": "Enable auto restart",
            "settings_auto_restart_interval": "Restart interval",
            "settings_auto_restart_hint": "Restarts Bridge if it gets stuck, and on a regular schedule to keep connections stable. Changes apply immediately.",
            "remote_title": "Remote Connection",
            "remote_subtitle": "Configure how iPhone connects when it is not on the same network",
            "remote_connection_paths": "Connection Paths",
            "remote_lan": "Local Network",
            "remote_unavailable": "Unavailable",
            "remote_automatic": "Automatic",
            "remote_custom_relay": "Custom Relay",
            "remote_official_relay": "Official Relay",
            "remote_relay": "Relay",
            "remote_customize": "Customize...",
            "remote_current_custom_relay": "Custom Relay: %@",
            "remote_default_relay": "Default Relay: %@",
            "remote_restore_default": "Restore Default",
            "remote_relay_validation": "Relay address must use wss:// and include a valid host.",
            "remote_validating_relay": "Validating Relay configuration...",
            "remote_validating": "Validating",
            "remote_connection_strategy": "Connection Strategy",
            "remote_strategy_summary": "Prefer the local network when available; otherwise automatically try the encrypted Relay.",
            "remote_advanced_connections": "Advanced Connection Methods",
            "remote_tailscale_unavailable": "No Tailscale address detected",
            "remote_vps": "VPS / FRP",
            "remote_vps_validation": "VPS / FRP accepts ws://, wss://, or https:// addresses. Prefer wss:// for public endpoints.",
            "remote_relay_failed": "Relay configuration failed: %@",
            "remote_tailscale": "Tailscale",
            "remote_vps_placeholder": "wss://bridge.example.com/bridge",
            "name_updated": "Name updated",
            "save_failed": "Save failed: %@",
            "save_failed_http": "Save failed (HTTP %d)",
            "saved_restarting": "Saved. Restarting Bridge...",
            "failed_load_agents": "Failed to load agent status: %@",
            "failed_refresh_agents": "Failed to refresh agent status: %@",
            "failed_test_agent": "Failed to test agent: %@",
            "showing_last_agent_results": "Refresh failed: %@. Showing last known results.",
            "error_cannot_connect": "Cannot connect to CCCode Bridge. Make sure Bridge is running.",
            "error_remove_device": "Failed to remove device: %@",
            "error": "Error",
            "unknown_error": "Unknown error",
            "ok": "OK",
            "restart_bridge": "Restart CCCode Bridge",
            "stop_bridge": "Stop CCCode Bridge",
            "start_bridge": "Start CCCode Bridge",
            "open_bridge": "Open CCCode Bridge",
            "quit": "Quit",
            "start": "Start",
            "stop": "Stop",
            "restart": "Restart",
        ],
        .zhHans: [
            "overview": "总览",
            "system_default": "跟随系统",
            "devices": "设备",
            "ai_tools": "AI 工具",
            "settings": "设置",
            "diagnostics": "诊断",
            "cccode_bridge": "CCCode Bridge",
            "status": "状态",
            "bridge_running": "Bridge 运行中",
            "ai_tools_ready": "%d 个 AI 工具就绪",
            "trusted_devices": "%d 个已授权设备",
            "no_trusted_devices": "暂无已授权设备",
            "loading_devices": "正在加载设备…",
            "no_ai_tools_detected": "未检测到 AI 工具",
            "remote_access": "远程访问",
            "remote_access_hint": "可选。允许 iPhone 通过公网地址连接。",
            "remote_access_tab": "远程访问",
            "devices_pairing": "设备与配对",
            "logs_diagnostics": "日志与诊断",
            "configured": "已配置",
            "not_configured": "未配置",
            "edit": "编辑",
            "connection_mode": "连接模式",
            "local_only": "仅局域网",
            "remote_configured": "远程已配置",
            "security_level": "安全性",
            "sec_encrypted": "WSS 加密",
            "sec_tailscale_tunnel": "Tailscale 隧道 (WireGuard)",
            "sec_lan": "局域网",
            "sec_insecure": "不安全（公网 ws://）",
            "sec_unknown": "未知",
            "insecure_warning": "⚠️ 不安全连接",
            "insecure_warning_detail": "此 URL 使用公网 ws://，数据未加密传输。请使用 wss:// 或 Tailscale。",
            "tailscale_path": "Tailscale（推荐）",
            "tailscale_path_hint": "在 Mac 和 iPhone 上安装 Tailscale，使用 Tailscale IP (100.x.x.x) 作为远程 URL。",
            "frp_path": "FRP / VPS / 反向代理",
            "frp_path_hint": "配置带 TLS 终结的反向代理，使用 wss:// URL 作为远程 URL。",
            "remote_url_label": "远程 URL",
            "local_url_label": "本地 URL",
            "diagnosis_info": "远程 URL 诊断",
            "diagnosis_disclaimer": "诊断仅显示本机配置状态，不代表外部可达性。",
            "check_status": "检查状态",
            "configuration_paths": "配置路径",
            "save_and_restart": "保存并重启",
            "last_connected": "— 上次连接 %@",
            "overview_subtitle": "查看 MacBridge 当前运行状态和关键连接摘要",
            "overview_runtime": "运行服务",
            "overview_running": "运行中",
            "overview_running_no_agents": "运行中，没有可用 AI 工具",
            "overview_starting": "正在启动",
            "overview_stopped": "已停止",
            "overview_start_failed": "启动失败",
            "overview_sleeping": "已暂停",
            "overview_idle": "尚未启动",
            "overview_restart": "重新启动",
            "overview_restarting": "正在重启…",
            "overview_detect_again": "重新检测",
            "overview_test_connection": "测试连接",
            "overview_manage_devices": "管理设备",
            "overview_view_settings": "查看设置",
            "overview_more_actions": "更多运行服务操作",
            "overview_stop_confirm_title": "停止 MacBridge？",
            "overview_stop_confirm_message": "所有已连接设备将立即断开，重新启动后可恢复连接。",
            "overview_port": "端口 %d",
            "overview_uptime": "已运行 %@",
            "overview_version": "版本 %@",
            "overview_details_unavailable": "运行服务详情暂不可用",
            "overview_uptime_under_minute": "少于 1 分钟",
            "overview_uptime_minutes": "%d 分钟",
            "overview_uptime_hours_minutes": "%d 小时 %d 分钟",
            "overview_recently_seen": "最近出现：%@",
            "overview_official_relay_configured": "官方 Relay 已配置",
            "overview_custom_relay_configured": "自定义 Relay 已配置",
            "overview_relay_not_configured": "Relay 未配置",
            "overview_relay_unavailable": "Relay 状态暂不可用",
            "overview_runtime_details_failed": "运行服务详情暂不可用：%@",
            "overview_relay_status_failed": "Relay 状态暂不可用：%@",
            "pair_new_device": "配对新设备",
            "scan_with_cccode": "使用 iPhone 上的 CCCode 扫码",
            "manual_code": "手动码：",
            "waiting_for_device": "等待设备连接…",
            "creating_pairing_session": "正在创建配对会话…",
            "security_hint_1": "仅批准你识别的设备。",
            "security_hint_2": "此 Mac 会在授权前请求确认。",
            "security_hint_3": "配对码几分钟后会过期。",
            "bridge_label": "Bridge：%@",
            "device_request": "设备请求",
            "approve": "批准",
            "reject": "拒绝",
            "device_paired_successfully": "设备配对成功",
            "pair_another_device": "配对另一个设备",
            "device_rejected": "已拒绝设备",
            "pairing_session_expired": "配对会话已过期",
            "try_again": "重试",
            "retry": "重试",
            "pairing_new_device": "配对新设备",
            "pairing_management_unavailable": "MacBridge 管理服务暂不可用。",
            "pairing_invalid_expiry": "配对会话返回了无效的过期时间。",
            "pairing_unknown_device": "未知设备",
            "pairing_qr_code": "配对二维码",
            "pairing_step_scan": "使用 iPhone 扫码",
            "pairing_step_confirm": "在此 Mac 上确认设备",
            "pairing_step_complete": "完成连接",
            "pairing_copy_code": "复制手动码",
            "pairing_copied": "已复制",
            "pairing_expires_in": "二维码将在 %@ 后过期",
            "pairing_connection_details": "连接详情",
            "pairing_connect_to": "连接到：%@",
            "pairing_approval_explanation": "批准后，该设备可以通过此 MacBridge 使用当前已启用的 AI 工具。",
            "pairing_generate_again": "重新生成二维码",
            "pairing_lan": "局域网",
            "pairing_relay": "加密 Relay",
            "pairing_advanced_path": "高级连接",
            "authorized_devices": "已授权设备",
            "no_authorized_devices": "暂无已授权设备。使用上方的配对区域添加设备。",
            "remove": "移除",
            "cancel": "取消",
            "remove_device_confirm": "移除 %@？",
            "remove_device_message": "此设备需要重新配对才能访问此 Mac。",
            "paired": "配对于 %@",
            "last_seen": "最近连接 %@",
            "just_now": "刚刚",
            "min_ago": "%d 分钟前",
            "hr_ago": "%d 小时前",
            "days_ago": "%d 天前",
            "devices_subtitle": "配对新设备并管理已授权设备",
            "devices_revoke_authorization": "撤销授权…",
            "devices_revoke_confirm": "撤销“%@”的授权？",
            "devices_revoke_message": "该设备将立即断开，下次使用需要重新配对。",
            "devices_actions": "设备操作",
            "devices_unknown_device": "设备",
            "refresh_all": "全部刷新",
            "no_ai_tools_configured": "未配置 AI 工具",
            "all_unavailable_guidance": "CCCode Bridge 已就绪。安装或登录 AI 编程工具以开始使用。",
            "test": "测试",
            "external_turns_polling": "外部会话通过轮询刷新",
            "not_installed": "未安装。请安装 %@ 以启用此工具。",
            "service_not_running": "服务未运行。请先启动服务。",
            "login_required": "需要登录。请先运行工具的登录命令。",
            "detection_timed_out": "检测超时。服务可能未响应。",
            "cannot_reach_service": "无法连接服务。请检查网络。",
            "check_docs_guidance": "请查看工具文档了解安装方法。",
            "status_ready": "就绪",
            "status_not_found": "未找到",
            "status_login_required": "需要登录",
            "status_not_running": "未运行",
            "status_port_conflict": "端口冲突",
            "status_version_incompatible": "版本不兼容",
            "status_permission_denied": "权限被拒",
            "raw_logs": "原始日志",
            "last_200_lines": "最近 200 行 Bridge 日志",
            "copy_raw_logs": "复制原始日志",
            "no_logs_available": "暂无日志",
            "diagnostics_subtitle": "查看 MacBridge 最近的运行日志",
            "diagnostics_reading": "正在读取日志…",
            "bridge_name": "Bridge 名称",
            "bridge_name_hint": "其他设备连接时看到的名称。修改后立即生效。",
            "save": "保存",
            "opencode_auth": "OpenCode 认证",
            "opencode_auth_hint": "OpenCode HTTP 服务需要 Basic Auth 认证。保存后 Bridge 会自动重启并携带新凭据。",
            "opencode_auth_guidance_auto": "• 自动配对：MacBridge 会自动生成随机凭据并将其写入 OpenCode 桌面版配置，无需手动设置即可自动连接。",
            "opencode_auth_guidance_manual": "• 命令行/手动：若使用终端启动 OpenCode，请使用以下命令，并在下方配置对应账密：",
            "username": "用户名",
            "password": "密码",
            "language": "语言",
            "appearance": "外观",
            "theme_system": "跟随系统",
            "theme_light": "白天",
            "theme_dark": "黑夜",
            "settings_general": "通用",
            "settings_macbridge": "MacBridge",
            "settings_name": "名称",
            "settings_name_placeholder": "Mac",
            "settings_authentication_status": "认证状态",
            "settings_automatically_configured": "已自动配置",
            "settings_credentials_apply_after_restart": "MacBridge 重启后 OpenCode 将使用最新凭据。",
            "settings_manual_authentication": "手动认证设置",
            "settings_show_password": "显示密码",
            "settings_hide_password": "隐藏密码",
            "settings_regenerate": "重新生成",
            "settings_launch_command": "启动命令",
            "settings_copy_command": "复制启动命令",
            "settings_save_credentials_restart": "保存凭据并重启 MacBridge",
            "settings_regenerate_confirm_title": "重新生成密码？",
            "settings_regenerate_confirm_message": "重新生成后，旧密码将在保存并重启 MacBridge 后失效。",
            "settings_saving": "正在保存…",
            "settings_opencode_command": "opencode serve --port 64667 --hostname 127.0.0.1",
            "settings_auto_restart_title": "自动重启",
            "settings_auto_restart_enable": "启用自动重启",
            "settings_auto_restart_interval": "重启周期",
            "settings_auto_restart_hint": "Bridge 卡住时自动重启，并按周期定时兜底重启，保持连接稳定。修改后立即生效。",
            "remote_title": "远程连接",
            "remote_subtitle": "配置 iPhone 不在同一网络时的连接方式",
            "remote_connection_paths": "连接路径",
            "remote_lan": "局域网",
            "remote_unavailable": "暂不可用",
            "remote_automatic": "自动使用",
            "remote_custom_relay": "自定义 Relay",
            "remote_official_relay": "官方 Relay",
            "remote_relay": "Relay",
            "remote_customize": "自定义…",
            "remote_current_custom_relay": "当前使用自定义 Relay：%@",
            "remote_default_relay": "默认 Relay：%@",
            "remote_restore_default": "恢复默认",
            "remote_relay_validation": "Relay 地址必须使用 wss://，并包含有效主机名。",
            "remote_validating_relay": "正在验证 Relay 配置…",
            "remote_validating": "正在验证",
            "remote_connection_strategy": "连接策略",
            "remote_strategy_summary": "同一网络优先使用局域网，不同网络自动尝试加密 Relay。",
            "remote_advanced_connections": "高级连接方式",
            "remote_tailscale_unavailable": "未检测到 Tailscale 地址",
            "remote_vps": "VPS / FRP",
            "remote_vps_validation": "VPS / FRP 只接受 ws://、wss:// 或 https:// 地址；公网地址建议使用 wss://。",
            "remote_relay_failed": "Relay 配置失败：%@",
            "remote_tailscale": "Tailscale",
            "remote_vps_placeholder": "wss://bridge.example.com/bridge",
            "name_updated": "名称已更新",
            "save_failed": "保存失败：%@",
            "save_failed_http": "保存失败 (HTTP %d)",
            "saved_restarting": "已保存，正在重启 Bridge…",
            "failed_load_agents": "加载 AI 工具状态失败：%@",
            "failed_refresh_agents": "刷新 AI 工具状态失败：%@",
            "failed_test_agent": "测试 AI 工具失败：%@",
            "showing_last_agent_results": "刷新失败：%@。正在显示上次结果。",
            "error_cannot_connect": "无法连接到 CCCode Bridge。请确认 Bridge 正在运行。",
            "error_remove_device": "移除设备失败：%@",
            "error": "错误",
            "unknown_error": "未知错误",
            "ok": "确定",
            "restart_bridge": "重启 CCCode Bridge",
            "stop_bridge": "停止 CCCode Bridge",
            "start_bridge": "启动 CCCode Bridge",
            "open_bridge": "打开 CCCode Bridge",
            "quit": "退出",
            "start": "启动",
            "stop": "停止",
            "restart": "重启",
        ],
    ]
}
