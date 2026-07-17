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
        let didUserSet = UserDefaults.standard.bool(forKey: "didUserSetLanguage")
        if !didUserSet {
            let languages = UserDefaults.standard.stringArray(forKey: "AppleLanguages") ?? Locale.preferredLanguages
            let preferred = languages.first ?? "en"
            return preferred.hasPrefix("zh") ? .zhHans : .en
        }
        let raw = UserDefaults.standard.string(forKey: "appLanguage") ?? ""
        return AppLanguage(rawValue: raw) ?? .en
    }

    // 查表
    static func tr(_ key: String) -> String {
        table[current]?[key] ?? table[.en]?[key] ?? key
    }

    // MARK: - Tab 标题

    static var overview: String { tr("overview") }
    static var workspace: String { tr("workspace") }
    static var workspaceSubtitle: String { tr("workspace_subtitle") }
    static var devices: String { tr("devices") }
    static var aiTools: String { tr("ai_tools") }
    static var settings: String { tr("settings") }
    static var diagnostics: String { tr("diagnostics") }
    static var remoteAccessTab: String { tr("remote_access_tab") }
    static var devicesPairing: String { tr("devices_pairing") }
    static var logsDiagnostics: String { tr("logs_diagnostics") }
    static var connectionStatus: String { tr("connection_status") }
    static var helpDiagnostics: String { tr("help_diagnostics") }
    static var general: String { tr("general") }
    static var advanced: String { tr("advanced") }
    static var addDevice: String { tr("add_device") }
    static var viewDevices: String { tr("view_devices") }
    static var workspaceReadyTitle: String { tr("workspace_ready_title") }
    static var workspaceReadySubtitle: String { tr("workspace_ready_subtitle") }
    static var workspaceCanConnect: String { tr("workspace_can_connect") }
    static var workspaceOneStepAway: String { tr("workspace_one_step_away") }
    static var workspaceNeedsAttention: String { tr("workspace_needs_attention") }
    static var workspaceNeedsAttentionHint: String { tr("workspace_needs_attention_hint") }
    static var workspaceSecureConnection: String { tr("workspace_secure_connection") }
    static var workspaceSecureRelayOn: String { tr("workspace_secure_relay_on") }
    static var workspaceRelayOff: String { tr("workspace_relay_off") }
    static var workspaceFirstDeviceTitle: String { tr("workspace_first_device_title") }
    static var workspaceFirstDeviceSubtitle: String { tr("workspace_first_device_subtitle") }
    static var workspacePausedTitle: String { tr("workspace_paused_title") }
    static var workspacePausedSubtitle: String { tr("workspace_paused_subtitle") }
    static var workspaceStart: String { tr("workspace_start") }
    static var workspaceRecheck: String { tr("workspace_recheck") }
    static var workspaceNoToolsTitle: String { tr("workspace_no_tools_title") }
    static var workspaceNoToolsSubtitle: String { tr("workspace_no_tools_subtitle") }

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
    static var pairingReturnToHome: String { tr("pairing_return_to_home") }
    static var deviceRejected: String { tr("device_rejected") }
    static var pairingSessionExpired: String { tr("pairing_session_expired") }
    static var tryAgain: String { tr("try_again") }
    static var retry: String { tr("retry") }
    static var pairingNewDevice: String { tr("pairing_new_device") }
    static var pairingManagementUnavailable: String { tr("pairing_management_unavailable") }
    static var pairingInvalidExpiry: String { tr("pairing_invalid_expiry") }
    static var pairingUnknownDevice: String { tr("pairing_unknown_device") }
    static var pairingRequestTitle: String { tr("pairing_request_title") }
    static var pairingQRCode: String { tr("pairing_qr_code") }
    static var pairingStepScan: String { tr("pairing_step_scan") }
    static var pairingStepConfirm: String { tr("pairing_step_confirm") }
    static var pairingStepComplete: String { tr("pairing_step_complete") }
    static var pairingStepProgress: String { tr("pairing_step_progress") }
    static var connectionStatusSecureRemote: String { tr("connection_secure_remote") }
    static var connectionStatusSecureRemoteHint: String { tr("connection_secure_remote_hint") }
    static var connectionStatusLocalNetwork: String { tr("connection_local_network") }
    static var connectionStatusLocalNetworkHint: String { tr("connection_local_network_hint") }
    static var connectionStatusAdvanced: String { tr("connection_advanced") }
    static var connectionStatusShowAdvanced: String { tr("connection_show_advanced") }
    static var connectionStatusHideAdvanced: String { tr("connection_hide_advanced") }
    static var opencodeManagedDefault: String { tr("opencode_managed_default") }
    static var opencodeUseOwnService: String { tr("opencode_use_own_service") }
    static var diagnosticsHealthSummary: String { tr("diagnostics_health_summary") }
    static var diagnosticsCopySupportInfo: String { tr("diagnostics_copy_support_info") }
    static var diagnosticsSupportInfoCopied: String { tr("diagnostics_support_info_copied") }
    static var diagnosticsHealthBridge: String { tr("diagnostics_health_bridge") }
    static var diagnosticsHealthConnection: String { tr("diagnostics_health_connection") }
    static var diagnosticsHealthAiTools: String { tr("diagnostics_health_ai_tools") }
    static var diagnosticsViewRawLogs: String { tr("diagnostics_view_raw_logs") }
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
    static var copyPairingLink: String { tr("copy_pairing_link") }
    static var pairingLinkCopied: String { tr("pairing_link_copied") }
    // Flow C: web QR (https URL) shown alongside the iOS QR.
    static var pairingQRTarget: String { tr("pairing_qr_target") }
    static var pairingQRTargetIOS: String { tr("pairing_qr_target_ios") }
    static var pairingQRTargetWeb: String { tr("pairing_qr_target_web") }
    static var pairingWebQRHint: String { tr("pairing_web_qr_hint") }


    // MARK: - Devices

    static var authorizedDevices: String { tr("authorized_devices") }
    static var noAuthorizedDevices: String { tr("no_authorized_devices") }
    static var remove: String { tr("remove") }
    static var cancel: String { tr("cancel") }

    static var done: String { tr("done") }
    static var back: String { tr("back") }
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
    static var settingsSessionListLimit: String { tr("settings_session_list_limit") }
    static var settingsSessionListLimitHint: String { tr("settings_session_list_limit_hint") }

    // MARK: - OpenCode endpoint (shared server)

    static var opencodeServerSource: String { tr("opencode_server_source") }
    static var opencodeServerURL: String { tr("opencode_server_url") }
    static var opencodeSourceManagedLocal: String { tr("opencode_source_managed_local") }
    static var opencodeSourceManagedLocalDesc: String { tr("opencode_source_managed_local_desc") }
    static var opencodeSourceExternalHttp: String { tr("opencode_source_external_http") }
    static var opencodeSourceExternalHttpDesc: String { tr("opencode_source_external_http_desc") }
    static var opencodeSourceLegacy64667: String { tr("opencode_source_legacy_64667") }
    static var opencodeSourceLegacy64667Desc: String { tr("opencode_source_legacy_64667_desc") }
    static var opencodeSourceServiceDiscoveryFuture: String { tr("opencode_source_service_discovery_future") }
    static var opencodeSourceServiceDiscoveryFutureDesc: String { tr("opencode_source_service_discovery_future_desc") }
    static var opencodeSourceDisabled: String { tr("opencode_source_disabled") }
    static var opencodeSourceDisabledDesc: String { tr("opencode_source_disabled_desc") }
    static var opencodeServerURLPlaceholder: String { tr("opencode_server_url_placeholder") }
    static var opencodeValidateEndpoint: String { tr("opencode_validate_endpoint") }
    static var opencodeValidating: String { tr("opencode_validating") }
    static var opencodeEndpointValid: String { tr("opencode_endpoint_valid") }
    static var opencodeBringYourOwnHint: String { tr("opencode_bring_your_own_hint") }
    static var opencodeLegacyInsecureWarning: String { tr("opencode_legacy_insecure_warning") }
    static var opencodeMigrationNotice: String { tr("opencode_migration_notice") }

    // MARK: - OpenCode endpoint errors

    static var opencodeErrNotConfigured: String { tr("opencode_err_not_configured") }
    static var opencodeErrPasswordRequired: String { tr("opencode_err_password_required") }
    static var opencodeErrNonLoopback: String { tr("opencode_err_non_loopback") }
    static var opencodeErrMalformedURL: String { tr("opencode_err_malformed_url") }
    static var opencodeErrServiceDiscoveryUnavailable: String { tr("opencode_err_service_discovery_unavailable") }
    static var opencodeErrUnreachable: String { tr("opencode_err_unreachable") }
    static var opencodeErrServerUnauthenticated: String { tr("opencode_err_server_unauthenticated") }
    static var opencodeErrAuthFailed: String { tr("opencode_err_auth_failed") }
    static var opencodeErrNotOpencodeServer: String { tr("opencode_err_not_opencode_server") }

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
    static var remotePublishInPairing: String { tr("remote_publish_in_pairing") }
    static var remoteLANHint: String { tr("remote_lan_hint") }
    static var remoteUnencryptedWarning: String { tr("remote_unencrypted_warning") }
    static var remoteNoTailscaleIP: String { tr("remote_no_tailscale_ip") }
    static var remoteTailscaleIPDetected: String { tr("remote_tailscale_ip_detected") }
    static var remoteRefreshFailed: String { tr("remote_refresh_failed") }
    static var remoteRelayEnabled: String { tr("remote_relay_enabled") }
    static var remoteRelayDisabled: String { tr("remote_relay_disabled") }
    static var remoteRelaySwitchHint: String { tr("remote_relay_switch_hint") }
    static var remoteRelayConfirmTitle: String { tr("remote_relay_confirm_title") }
    static var remoteRelayConfirmMessage: String { tr("remote_relay_confirm_message") }
    static var remoteRelayEnabling: String { tr("remote_relay_enabling") }
    static var remoteRelayDisabling: String { tr("remote_relay_disabling") }

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
            "workspace": "Workstation",
            "workspace_subtitle": "This Mac is ready for your devices",
            "system_default": "System Default",
            "devices": "Devices",
            "ai_tools": "AI Tools",
            "settings": "Settings",
            "diagnostics": "Diagnostics",
            "connection_status": "Connection Status",
            "help_diagnostics": "Help & Diagnostics",
            "general": "General",
            "advanced": "Advanced",
            "add_device": "Add Device",
            "view_devices": "View Devices",
            "workspace_ready_title": "This Mac is ready",
            "workspace_ready_subtitle": "Keep using the AI coding tools on this Mac from iPhone, iPad, or Web.",
            "workspace_can_connect": "Ready to connect",
            "workspace_one_step_away": "One step away",
            "workspace_needs_attention": "Needs attention",
            "workspace_needs_attention_hint": "Resolve these to reconnect from your devices.",
            "workspace_secure_connection": "Secure connection",
            "workspace_secure_relay_on": "Relay is on, so you can keep connecting securely when you leave the local network.",
            "workspace_relay_off": "Relay is off; connecting away from the local network is unavailable.",
            "workspace_first_device_title": "Connect your first device",
            "workspace_first_device_subtitle": "Scan once to keep using this Mac's AI tools from iPhone or iPad.",
            "workspace_paused_title": "Workstation paused",
            "workspace_paused_subtitle": "Authorized devices can't reach this Mac right now.",
            "workspace_start": "Start Workstation",
            "workspace_recheck": "Recheck",
            "workspace_no_tools_title": "One step away",
            "workspace_no_tools_subtitle": "CordCode Link is running, but no AI tools are available yet.",
            "cccode_bridge": "CordCode Link",
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
            "scan_with_cccode": "Scan with CordCode on iPhone",
            "manual_code": "Manual code:",
            "waiting_for_device": "Waiting for device...",
            "creating_pairing_session": "Creating pairing session...",
            "security_hint_1": "Only approve devices you recognize.",
            "security_hint_2": "This Mac will ask before granting access.",
            "security_hint_3": "Pairing codes expire after a few minutes.",
            "bridge_label": "Bridge: %@",
            "device_request": "Device Request",
            "pairing_request_title": "Pairing Request",
            "approve": "Authorize Mobile Device",
            "reject": "Reject",
            "device_paired_successfully": "Device paired successfully",
            "pair_another_device": "Pair Another Device",
            "pairing_return_to_home": "Return to Homepage",
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
            "pairing_step_progress": "Step %d of %d",
            "connection_secure_remote": "Secure remote connection",
            "connection_secure_remote_hint": "Relay keeps the connection when you leave the local network. Content is always end-to-end encrypted.",
            "connection_local_network": "Local network",
            "connection_local_network_hint": "On the same Wi-Fi, a faster direct connection is used automatically.",
            "connection_advanced": "Advanced connections",
            "connection_show_advanced": "Show advanced options",
            "connection_hide_advanced": "Hide advanced options",
            "opencode_managed_default": "CordCode Link is managing OpenCode automatically. No action needed.",
            "opencode_use_own_service": "Use my own OpenCode service",
            "diagnostics_health_summary": "Health summary",
            "diagnostics_copy_support_info": "Copy support info",
            "diagnostics_support_info_copied": "Copied",
            "diagnostics_health_bridge": "Bridge",
            "diagnostics_health_connection": "Connection",
            "diagnostics_health_ai_tools": "AI tools",
            "diagnostics_view_raw_logs": "View raw logs",
            "pairing_copy_code": "Copy manual code",
            "pairing_copied": "Copied",
            "pairing_expires_in": "QR code expires in %@",
            "pairing_connection_details": "Connection Details",
            "pairing_connect_to": "Connect to: %@",
            "pairing_approval_explanation": "After approval, this device can use the currently enabled AI tools through this Mac.",
            "pairing_generate_again": "Generate New QR Code",
            "pairing_lan": "Local Network",
            "pairing_relay": "Encrypted Relay",
            "pairing_advanced_path": "Advanced Connection",
            "copy_pairing_link": "Copy Pairing Link",
            "pairing_link_copied": "Copied",
            "pairing_qr_target": "Pairing code",
            "pairing_qr_target_ios": "iOS",
            "pairing_qr_target_web": "Web",
            "pairing_web_qr_hint": "Scan with your phone camera — it opens the web client in the browser.",

            "authorized_devices": "Authorized Devices",
            "no_authorized_devices": "No authorized devices. Use the pairing section above to add one.",
            "remove": "Remove",
            "cancel": "Cancel",
            "done": "Done",
            "back": "Back",
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
            "all_unavailable_guidance": "CordCode Link is ready. Install or log in to an AI coding tool to get started.",
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
            "settings_opencode_command": "OPENCODE_SERVER_PASSWORD='your-password' opencode serve --hostname 127.0.0.1 --port 64667",
            "settings_auto_restart_title": "Auto Restart",
            "settings_auto_restart_enable": "Enable auto restart",
            "settings_auto_restart_interval": "Restart interval",
            "settings_auto_restart_hint": "Restarts Bridge if it gets stuck, and on a regular schedule to keep connections stable. Changes apply immediately.",
            "settings_session_list_limit": "Sessions to load",
            "settings_session_list_limit_hint": "Loads the newest sessions only. Default 50; maximum 150. Changes restart the Bridge automatically.",
            "opencode_server_source": "Server Source",
            "opencode_server_url": "Server URL",
            "opencode_source_managed_local": "Automatic (Recommended)",
            "opencode_source_managed_local_desc": "CordCode starts a local OpenCode server and connects Desktop and iOS to it.",
            "opencode_source_external_http": "External HTTP server",
            "opencode_source_external_http_desc": "Connect to a stable `opencode serve` you started. Loopback + Basic Auth required. CordCode does not start or keep it alive.",
            "opencode_source_legacy_64667": "Legacy 127.0.0.1:64667",
            "opencode_source_legacy_64667_desc": "Compatibility mode for existing setups. Not a secure shared server; may be unverified.",
            "opencode_source_service_discovery_future": "Service discovery (future)",
            "opencode_source_service_discovery_future_desc": "Reserved. Current stable opencode does not expose `service` / `--register`; unavailable.",
            "opencode_source_disabled": "Disabled",
            "opencode_source_disabled_desc": "OpenCode backend is off. Claude and Codex are unaffected.",
            "opencode_server_url_placeholder": "http://127.0.0.1:4096",
            "opencode_validate_endpoint": "Validate",
            "opencode_validating": "Validating...",
            "opencode_endpoint_valid": "Endpoint reachable and authenticated.",
            "opencode_bring_your_own_hint": "CordCode connects to this OpenCode server but does not start or keep it alive. Keep the command running, or install your own local service.",
            "opencode_legacy_insecure_warning": "⚠️ Legacy endpoint could not prove it requires authentication. It may be a passwordless or 0.0.0.0 listener from an older bridge. Clean it up and switch to External HTTP.",
            "opencode_migration_notice": "Migrated from the legacy 127.0.0.1:64667 server. Configure an External HTTP server for a secure shared OpenCode.",
            "opencode_err_not_configured": "OpenCode endpoint is not configured.",
            "opencode_err_password_required": "Password is required for an External HTTP endpoint.",
            "opencode_err_non_loopback": "Only loopback HTTP URLs are accepted (127.0.0.1).",
            "opencode_err_malformed_url": "The server URL is malformed.",
            "opencode_err_service_discovery_unavailable": "Service discovery needs a newer opencode CLI with `service` / `--register`.",
            "opencode_err_unreachable": "Could not reach the OpenCode server.",
            "opencode_err_server_unauthenticated": "Server did not require authentication. Start it with OPENCODE_SERVER_PASSWORD set.",
            "opencode_err_auth_failed": "Username or password was rejected (401).",
            "opencode_err_not_opencode_server": "The endpoint did not respond like an OpenCode server.",
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
            "remote_publish_in_pairing": "Include in new pairing",
            "remote_lan_hint": "Local network address, copy for diagnostics or manual connection. Use only in trusted LAN.",
            "remote_unencrypted_warning": "Warning: Unencrypted connection detected. Data sent over public networks without encryption is unsafe.",
            "remote_no_tailscale_ip": "No available Tailscale address detected",
            "remote_tailscale_ip_detected": "Tailscale address detected",
            "remote_refresh_failed": "Last updated: %@ · Refresh failed",
            "remote_relay_enabled": "Use Encrypted Relay",
            "remote_relay_disabled": "Disabled",
            "remote_relay_switch_hint": "When disabled, this Mac will no longer connect to the Relay. LAN, Tailscale, and custom addresses can still be used.",
            "remote_relay_confirm_title": "Disable Encrypted Relay?",
            "remote_relay_confirm_message": "After disabling, you will not be able to connect to this Mac when outside the current local network.",
            "remote_relay_enabling": "Enabling...",
            "remote_relay_disabling": "Disabling...",
            "name_updated": "Name updated",
            "save_failed": "Save failed: %@",
            "save_failed_http": "Save failed (HTTP %d)",
            "saved_restarting": "Saved. Restarting Bridge...",
            "failed_load_agents": "Failed to load agent status: %@",
            "failed_refresh_agents": "Failed to refresh agent status: %@",
            "failed_test_agent": "Failed to test agent: %@",
            "showing_last_agent_results": "Refresh failed: %@. Showing last known results.",
            "error_cannot_connect": "Cannot connect to CordCode Link. Make sure Bridge is running.",
            "error_remove_device": "Failed to remove device: %@",
            "error": "Error",
            "unknown_error": "Unknown error",
            "ok": "OK",
            "restart_bridge": "Restart CordCode Link",
            "stop_bridge": "Stop CordCode Link",
            "start_bridge": "Start CordCode Link",
            "open_bridge": "Open CordCode Link",
            "quit": "Quit",
            "start": "Start",
            "stop": "Stop",
            "restart": "Restart",
        ],
        .zhHans: [
            "overview": "总览",
            "workspace": "工作站",
            "workspace_subtitle": "这台 Mac 已准备好",
            "system_default": "跟随系统",
            "devices": "设备",
            "ai_tools": "AI 工具",
            "settings": "设置",
            "diagnostics": "诊断",
            "connection_status": "连接状态",
            "help_diagnostics": "帮助与诊断",
            "general": "通用",
            "advanced": "高级",
            "add_device": "添加设备",
            "view_devices": "查看设备",
            "workspace_ready_title": "这台 Mac 已准备好",
            "workspace_ready_subtitle": "从 iPhone、iPad 或 Web 继续使用这台 Mac 上的 AI 编程工具。",
            "workspace_can_connect": "可以连接",
            "workspace_one_step_away": "还差一步",
            "workspace_needs_attention": "需要处理",
            "workspace_needs_attention_hint": "处理后即可从设备重新连接。",
            "workspace_secure_connection": "安全连接",
            "workspace_secure_relay_on": "Relay 已开启，离开局域网后仍可安全使用。",
            "workspace_relay_off": "Relay 未开启，离开局域网后无法远程使用。",
            "workspace_first_device_title": "连接你的第一台设备",
            "workspace_first_device_subtitle": "扫描一次，即可从 iPhone 或 iPad 继续使用这台 Mac 的 AI 工具。",
            "workspace_paused_title": "工作站已暂停",
            "workspace_paused_subtitle": "已授权设备暂时无法连接此 Mac。",
            "workspace_start": "启动工作站",
            "workspace_recheck": "重新检查",
            "workspace_no_tools_title": "还差一步才能开始",
            "workspace_no_tools_subtitle": "CordCode Link 正在运行，但还没有可用的 AI 工具。",
            "cccode_bridge": "CordCode Link",
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
            "scan_with_cccode": "使用 iPhone 上的 CordCode 扫码",
            "manual_code": "手动码：",
            "waiting_for_device": "等待设备连接…",
            "creating_pairing_session": "正在创建配对会话…",
            "security_hint_1": "仅批准你识别的设备。",
            "security_hint_2": "此 Mac 会在授权前请求确认。",
            "security_hint_3": "配对码几分钟后会过期。",
            "bridge_label": "Bridge：%@",
            "device_request": "设备请求",
            "pairing_request_title": "配对请求",
            "approve": "授权移动端设备",
            "reject": "拒绝",
            "device_paired_successfully": "设备配对成功",
            "pair_another_device": "配对另一个设备",
            "pairing_return_to_home": "返回首页",
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
            "pairing_step_progress": "第 %d 步，共 %d 步",
            "connection_secure_remote": "安全远程连接",
            "connection_secure_remote_hint": "Relay 在你离开局域网时自动保持连接，内容始终端到端加密。",
            "connection_local_network": "本地网络",
            "connection_local_network_hint": "同一 Wi-Fi 下会自动使用更快的直接连接。",
            "connection_advanced": "高级连接",
            "connection_show_advanced": "显示高级选项",
            "connection_hide_advanced": "隐藏高级选项",
            "opencode_managed_default": "CordCode Link 正在自动管理 OpenCode，无需操作。",
            "opencode_use_own_service": "使用我自己的 OpenCode 服务",
            "diagnostics_health_summary": "健康摘要",
            "diagnostics_copy_support_info": "复制支持信息",
            "diagnostics_support_info_copied": "已复制",
            "diagnostics_health_bridge": "Bridge",
            "diagnostics_health_connection": "连接",
            "diagnostics_health_ai_tools": "AI 工具",
            "diagnostics_view_raw_logs": "查看原始日志",
            "pairing_copy_code": "复制手动码",
            "pairing_copied": "已复制",
            "pairing_expires_in": "二维码将在 %@ 后过期",
            "pairing_connection_details": "连接详情",
            "pairing_connect_to": "连接到：%@",
            "pairing_approval_explanation": "批准后，该设备可以通过这台 Mac 使用当前已启用的 AI 工具。",
            "pairing_generate_again": "重新生成二维码",
            "pairing_lan": "局域网",
            "pairing_relay": "加密 Relay",
            "pairing_advanced_path": "高级连接",
            "copy_pairing_link": "复制配对链接",
            "pairing_link_copied": "已复制",
            "pairing_qr_target": "配对码",
            "pairing_qr_target_ios": "iOS",
            "pairing_qr_target_web": "Web",
            "pairing_web_qr_hint": "用手机相机扫码,会自动在浏览器打开网页客户端。",

            "authorized_devices": "已授权设备",
            "no_authorized_devices": "暂无已授权设备。使用上方的配对区域添加设备。",
            "remove": "移除",
            "cancel": "取消",
            "done": "完成",
            "back": "返回",
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
            "all_unavailable_guidance": "CordCode Link 已就绪。安装或登录 AI 编程工具以开始使用。",
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
            "settings_opencode_command": "OPENCODE_SERVER_PASSWORD='your-password' opencode serve --hostname 127.0.0.1 --port 64667",
            "settings_auto_restart_title": "自动重启",
            "settings_auto_restart_enable": "启用自动重启",
            "settings_auto_restart_interval": "重启周期",
            "settings_auto_restart_hint": "Bridge 卡住时自动重启，并按周期定时兜底重启，保持连接稳定。修改后立即生效。",
            "settings_session_list_limit": "Session 加载条数",
            "settings_session_list_limit_hint": "仅加载最新 Session。默认 50 条，最多 150 条；修改后 Bridge 会自动重启。",
            "opencode_server_source": "Server 来源",
            "opencode_server_url": "Server URL",
            "opencode_source_managed_local": "自动托管（推荐）",
            "opencode_source_managed_local_desc": "CordCode 自动启动本机 OpenCode server，并把 Desktop 与 iOS 连接到同一个 server。",
            "opencode_source_external_http": "外部 HTTP server",
            "opencode_source_external_http_desc": "连接你已启动的 stable `opencode serve`。要求 loopback + Basic Auth。CordCode 不启动也不保活它。",
            "opencode_source_legacy_64667": "兼容模式 127.0.0.1:64667",
            "opencode_source_legacy_64667_desc": "为存量配置保留的兼容模式。不是安全共享 server，可能未经验证。",
            "opencode_source_service_discovery_future": "服务发现（未来）",
            "opencode_source_service_discovery_future_desc": "预留。当前 stable opencode 未暴露 `service` / `--register`，不可用。",
            "opencode_source_disabled": "未启用",
            "opencode_source_disabled_desc": "关闭 OpenCode backend。Claude 与 Codex 不受影响。",
            "opencode_server_url_placeholder": "http://127.0.0.1:4096",
            "opencode_validate_endpoint": "验证",
            "opencode_validating": "正在验证…",
            "opencode_endpoint_valid": "Endpoint 可达且认证通过。",
            "opencode_bring_your_own_hint": "CordCode 只连接这个 OpenCode server，不会启动或保活它。请自行保持命令运行，或安装本地常驻服务。",
            "opencode_legacy_insecure_warning": "⚠️ 该兼容 endpoint 未能证明其要求认证，可能来自旧 bridge 的无密码或 0.0.0.0 监听进程。请清理后改用外部 HTTP server。",
            "opencode_migration_notice": "已从旧 127.0.0.1:64667 server 迁移。请配置外部 HTTP server 以获得安全的共享 OpenCode。",
            "opencode_err_not_configured": "OpenCode endpoint 未配置。",
            "opencode_err_password_required": "外部 HTTP endpoint 必须填写密码。",
            "opencode_err_non_loopback": "仅接受 loopback HTTP URL（127.0.0.1）。",
            "opencode_err_malformed_url": "Server URL 格式无效。",
            "opencode_err_service_discovery_unavailable": "服务发现需要更新版本的 opencode CLI（带 `service` / `--register`）。",
            "opencode_err_unreachable": "无法连接 OpenCode server。",
            "opencode_err_server_unauthenticated": "Server 未要求认证。请设置 OPENCODE_SERVER_PASSWORD 后重新启动它。",
            "opencode_err_auth_failed": "用户名或密码被拒（401）。",
            "opencode_err_not_opencode_server": "该 endpoint 的响应不像 OpenCode server。",
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
            "remote_publish_in_pairing": "配对时提供此连接方式",
            "remote_lan_hint": "局域网连接地址，可复制用于诊断或手动连接。仅应在可信局域网内使用。",
            "remote_unencrypted_warning": "警告：检测到您使用的是未加密的公网连接方式 (ws://)。数据在公共网络传输时存在安全风险，强烈建议使用加密协议。",
            "remote_no_tailscale_ip": "未检测到可用的 Tailscale 地址",
            "remote_tailscale_ip_detected": "已检测到 Tailscale 地址",
            "remote_refresh_failed": "上次更新时间：%@ · 刷新未成功",
            "remote_relay_enabled": "使用加密 Relay",
            "remote_relay_disabled": "已关闭",
            "remote_relay_switch_hint": "关闭后，此 Mac 不再连接 Relay。局域网、Tailscale 和自定义地址仍可使用。",
            "remote_relay_confirm_title": "关闭加密 Relay？",
            "remote_relay_confirm_message": "关闭后，离开当前局域网将无法连接此 Mac。",
            "remote_relay_enabling": "正在启用…",
            "remote_relay_disabling": "正在关闭…",
            "name_updated": "名称已更新",
            "save_failed": "保存失败：%@",
            "save_failed_http": "保存失败 (HTTP %d)",
            "saved_restarting": "已保存，正在重启 Bridge…",
            "failed_load_agents": "加载 AI 工具状态失败：%@",
            "failed_refresh_agents": "刷新 AI 工具状态失败：%@",
            "failed_test_agent": "测试 AI 工具失败：%@",
            "showing_last_agent_results": "刷新失败：%@。正在显示上次结果。",
            "error_cannot_connect": "无法连接到 CordCode Link。请确认 Bridge 正在运行。",
            "error_remove_device": "移除设备失败：%@",
            "error": "错误",
            "unknown_error": "未知错误",
            "ok": "确定",
            "restart_bridge": "重启 CordCode Link",
            "stop_bridge": "停止 CordCode Link",
            "start_bridge": "启动 CordCode Link",
            "open_bridge": "打开 CordCode Link",
            "quit": "退出",
            "start": "启动",
            "stop": "停止",
            "restart": "重启",
        ],
    ]
}
