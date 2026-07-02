import SwiftUI

struct SettingsView: View {
    @ObservedObject var viewModel: SettingsViewModel
    @AppStorage("appLanguage") private var appLanguage = ""
    @AppStorage("appTheme") private var appTheme = ""
    @AppStorage("autoRestartEnabled") private var autoRestartEnabled = true
    @AppStorage("autoRestartIntervalMinutes") private var autoRestartIntervalMinutes = 120
    @State private var showManualAuthentication = false
    @State private var showPassword = false
    @State private var showRegenerateConfirmation = false

    private let labelWidth: CGFloat = 150

    /// 可选的定时重启周期（分钟）
    private let intervalOptions: [(label: String, minutes: Int)] = [
        ("30 分钟", 30),
        ("1 小时", 60),
        ("2 小时", 120),
        ("4 小时", 240),
        ("8 小时", 480),
    ]

    var body: some View {
        PageContainer {
            VStack(alignment: .leading, spacing: 24) {
                PageHeader(L10n.settings)
                settingsGroup(L10n.settingsGeneral) {
                    settingRow(L10n.language) {
                        Picker("", selection: $appLanguage) {
                            ForEach(AppLanguage.allCases) { language in
                                Text(language.displayName).tag(language.rawValue)
                            }
                        }
                        .labelsHidden()
                        .pickerStyle(.menu)
                        .frame(width: 320, alignment: .leading)
                    }
                    settingRow(L10n.appearance) {
                        Picker("", selection: $appTheme) {
                            ForEach(AppTheme.allCases) { theme in
                                Text(theme.displayName).tag(theme.rawValue)
                            }
                        }
                        .labelsHidden()
                        .pickerStyle(.segmented)
                        .frame(width: 320, alignment: .leading)
                    }
                }

                Divider()

                settingsGroup(L10n.settingsMacBridge) {
                    settingRow(L10n.settingsName) {
                        HStack {
                            TextField(L10n.settingsNamePlaceholder, text: $viewModel.displayName)
                                .textFieldStyle(.roundedBorder)
                            Button(L10n.save) {
                                viewModel.saveDisplayName()
                            }
                            .disabled(
                                !viewModel.isDisplayNameDirty ||
                                viewModel.displayNameFeedback == .saving
                            )
                        }
                    }
                    feedbackView(viewModel.displayNameFeedback)
                }

                Divider()

                settingsGroup(L10n.settingsAutoRestartTitle) {
                    settingRow(L10n.settingsAutoRestartEnable) {
                        Toggle("", isOn: $autoRestartEnabled)
                            .labelsHidden()
                            .frame(width: 320, alignment: .leading)
                    }
                    settingRow(L10n.settingsAutoRestartInterval) {
                        Picker("", selection: $autoRestartIntervalMinutes) {
                            ForEach(intervalOptions, id: \.minutes) { option in
                                Text(option.label).tag(option.minutes)
                            }
                        }
                        .labelsHidden()
                        .pickerStyle(.menu)
                        .frame(width: 320, alignment: .leading)
                        .disabled(!autoRestartEnabled)
                    }
                    Text(L10n.settingsAutoRestartHint)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .padding(.leading, labelWidth + 16)
                }

                settingsGroup("OpenCode") {
                    settingRow(L10n.opencodeServerSource) {
                        Picker("", selection: $viewModel.opencodeSource) {
                            Text(L10n.opencodeSourceExternalHttp).tag(OpenCodeServerSource.externalHttp)
                            Text(L10n.opencodeSourceLegacy64667).tag(OpenCodeServerSource.legacy64667)
                            Text(L10n.opencodeSourceDisabled).tag(OpenCodeServerSource.disabled)
                        }
                        .labelsHidden()
                        .pickerStyle(.menu)
                        .frame(width: 320, alignment: .leading)
                    }
                    Text(currentSourceDescription)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .padding(.leading, labelWidth + 16)

                    if viewModel.opencodeSource == .externalHttp {
                        settingRow(L10n.opencodeServerURL) {
                            TextField(L10n.opencodeServerURLPlaceholder, text: $viewModel.opencodeURL)
                                .textFieldStyle(.roundedBorder)
                        }
                        Text(L10n.opencodeBringYourOwnHint)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .padding(.leading, labelWidth + 16)
                    }

                    if viewModel.canValidateEndpoint {
                        HStack(spacing: 12) {
                            Button {
                                viewModel.validateEndpoint()
                            } label: {
                                if viewModel.endpointValidation == .validating {
                                    HStack(spacing: 6) {
                                        ProgressView().controlSize(.small)
                                        Text(L10n.opencodeValidating)
                                    }
                                } else {
                                    Text(L10n.opencodeValidateEndpoint)
                                }
                            }
                            .disabled(viewModel.endpointValidation == .validating)
                            endpointValidationView(viewModel.endpointValidation)
                        }
                        .padding(.leading, labelWidth + 16)
                    }

                    Divider()

                    VStack(alignment: .leading, spacing: 0) {
                        Button {
                            withAnimation(.spring(response: 0.3, dampingFraction: 0.7)) {
                                showManualAuthentication.toggle()
                            }
                        } label: {
                            HStack(spacing: 6) {
                                Image(systemName: "chevron.right")
                                    .font(.system(size: 9, weight: .bold))
                                    .rotationEffect(.degrees(showManualAuthentication ? 90 : 0))
                                    .foregroundColor(.secondary)
                                Text(L10n.settingsManualAuthentication)
                                    .foregroundColor(.primary)
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)

                        if showManualAuthentication {
                            VStack(alignment: .leading, spacing: 12) {
                                settingRow(L10n.username) {
                                    TextField(L10n.username, text: $viewModel.opencodeUser)
                                        .textFieldStyle(.roundedBorder)
                                }
                                settingRow(L10n.password) {
                                    HStack {
                                        Group {
                                            if showPassword {
                                                TextField(L10n.password, text: $viewModel.opencodePass)
                                            } else {
                                                SecureField(L10n.password, text: $viewModel.opencodePass)
                                            }
                                        }
                                        .textFieldStyle(.roundedBorder)

                                        Button {
                                            showPassword.toggle()
                                        } label: {
                                            Image(systemName: showPassword ? "eye.slash" : "eye")
                                        }
                                        .help(showPassword ? L10n.settingsHidePassword : L10n.settingsShowPassword)
                                        .accessibilityLabel(showPassword ? L10n.settingsHidePassword : L10n.settingsShowPassword)

                                        Button(L10n.settingsRegenerate) {
                                            showRegenerateConfirmation = true
                                        }
                                    }
                                }
                                settingRow(L10n.settingsLaunchCommand) {
                                    HStack {
                                        Text(L10n.settingsOpenCodeCommand)
                                            .font(.system(.caption, design: .monospaced))
                                            .textSelection(.enabled)
                                        Button {
                                            NSPasteboard.general.clearContents()
                                            NSPasteboard.general.setString(
                                                L10n.settingsOpenCodeCommand,
                                                forType: .string
                                            )
                                        } label: {
                                            Image(systemName: "doc.on.doc")
                                        }
                                        .help(L10n.settingsCopyCommand)
                                        .accessibilityLabel(L10n.settingsCopyCommand)
                                    }
                                }

                                Button(L10n.settingsSaveCredentialsRestart) {
                                    viewModel.saveCredentials()
                                }
                                .buttonStyle(.borderedProminent)
                                .disabled(
                                    !viewModel.isCredentialsDirty ||
                                    viewModel.credentialsFeedback == .saving
                                )

                                feedbackView(viewModel.credentialsFeedback)
                            }
                            .padding(.leading, 15)
                            .padding(.top, 12)
                            .transition(.opacity.combined(with: .move(edge: .top)))
                        }
                    }
                }
            }
        }
        .confirmationDialog(
            L10n.settingsRegenerateConfirmTitle,
            isPresented: $showRegenerateConfirmation,
            titleVisibility: .visible
        ) {
            Button(L10n.settingsRegenerate) {
                viewModel.regeneratePassword()
            }
            Button(L10n.cancel, role: .cancel) {}
        } message: {
            Text(L10n.settingsRegenerateConfirmMessage)
        }
    }

    private func settingsGroup<Content: View>(
        _ title: String,
        @ViewBuilder content: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            SectionHeader(title)
            content()
        }
    }

    private func settingRow<Content: View>(
        _ label: String,
        @ViewBuilder content: () -> Content
    ) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 16) {
            Text(label)
                .foregroundStyle(.secondary)
                .frame(width: labelWidth, alignment: .leading)
            content()
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var currentSourceDescription: String {
        switch viewModel.opencodeSource {
        case .externalHttp:
            return L10n.opencodeSourceExternalHttpDesc
        case .legacy64667:
            return L10n.opencodeSourceLegacy64667Desc
        case .serviceDiscoveryFuture:
            return L10n.opencodeSourceServiceDiscoveryFutureDesc
        case .disabled:
            return L10n.opencodeSourceDisabledDesc
        }
    }

    @ViewBuilder
    private func endpointValidationView(_ state: EndpointValidationState) -> some View {
        switch state {
        case .idle, .validating:
            EmptyView()
        case .valid:
            InlineFeedback(style: .success, message: L10n.opencodeEndpointValid)
        case .warning(let message):
            InlineFeedback(style: .warning, message: message)
        case .failed(let message):
            InlineFeedback(style: .error, message: message)
        }
    }

    @ViewBuilder
    private func feedbackView(_ feedback: SaveFeedback) -> some View {
        switch feedback {
        case .idle:
            EmptyView()
        case .saving:
            HStack(spacing: 8) {
                ProgressView()
                    .controlSize(.small)
                Text(L10n.settingsSaving)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        case .success(let message):
            InlineFeedback(style: .success, message: message)
        case .failure(let message):
            InlineFeedback(style: .error, message: message)
        }
    }
}
