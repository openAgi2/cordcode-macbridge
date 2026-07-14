import CoreImage.CIFilterBuiltins
import SwiftUI

struct PairingView: View {
    @ObservedObject var viewModel: PairingViewModel
    var showsHeader = true
    var onExit: (() -> Void)?
    @AppStorage("bridgeDisplayName") private var bridgeDisplayName = ""
    @State private var copiedCode = false
    @State private var copiedLink = false
    @State private var isDetailsExpanded = false
    /// Flow C: which QR to show — the iOS deep-link code or the web https code.
    @State private var qrTarget: PairingQRTarget = .ios


    private struct PairingCandidate: Identifiable {
        let id: String
        let title: String
        let url: String
        let icon: String
    }

    /// Which QR surface the pairing view is showing.
    private enum PairingQRTarget: String, CaseIterable, Identifiable {
        case ios
        case web
        var id: String { rawValue }
        var label: String {
            switch self {
            case .ios: return L10n.pairingQRTargetIOS
            case .web: return L10n.pairingQRTargetWeb
            }
        }
    }

    /// 任务式配对的当前步骤（1 扫码 → 2 在 Mac 上确认 → 3 开始使用）。
    /// 仅从既有 uiState 派生，不改变状态机；idle/error 时返回 nil 表示尚未进入流程。
    private var currentStep: Int? {
        switch viewModel.uiState {
        case .idle, .creating, .error: return nil
        case .waitingForClaim: return 1
        case .claimed: return 2
        case .approved, .rejected, .expired: return 3
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            if showsHeader {
                SectionHeader(L10n.pairingNewDevice)
            }

            // 步骤轨迹：只高亮当前步骤，让二维码不再像没有解释的技术对象。
            if let currentStep {
                PairingStepTracker(current: currentStep)
                    .padding(.bottom, 2)
            }

            switch viewModel.uiState {
            case .idle:
                Button(L10n.pairNewDevice) {
                    viewModel.startPairing()
                }
                .buttonStyle(.borderedProminent)

            case .creating:
                ProgressView(L10n.creatingPairingSession)

            case .waitingForClaim(_, let code, let payload):
                waitingView(code: code, payload: payload)

            case .claimed(let deviceName, let platform):
                claimedView(deviceName: deviceName, platform: platform)

            case .approved:
                resultView(
                    icon: "checkmark.circle.fill",
                    color: .green,
                    message: L10n.devicePairedSuccessfully,
                    button: L10n.pairAnotherDevice
                )

            case .rejected:
                resultView(
                    icon: "xmark.circle.fill",
                    color: .red,
                    message: L10n.deviceRejected,
                    button: L10n.pairNewDevice,
                    startsPairing: true
                )

            case .expired:
                resultView(
                    icon: "clock",
                    color: .orange,
                    message: L10n.pairingSessionExpired,
                    button: L10n.pairingGenerateAgain,
                    startsPairing: true
                )

            case .error(let message):
                VStack(alignment: .leading, spacing: 10) {
                    InlineFeedback(style: .error, message: message)
                    Button(L10n.retry) {
                        viewModel.reset()
                        viewModel.startPairing()
                    }
                }
            }
        }
    }

    private func waitingView(code: String, payload: String) -> some View {
        let webPayload = viewModel.webQrPayload
        let activePayload: String = (qrTarget == .web && !webPayload.isEmpty) ? webPayload : payload
        return ViewThatFits(in: .horizontal) {
            HStack(alignment: .top, spacing: 28) {
                qrSection(payload: activePayload, webPayload: webPayload)
                waitingInstructions(code: code, payload: payload)
            }
            VStack(alignment: .leading, spacing: 20) {
                qrSection(payload: activePayload, webPayload: webPayload)
                waitingInstructions(code: code, payload: payload)
            }
        }
    }

    private func qrSection(payload: String, webPayload: String) -> some View {
        VStack(alignment: .center, spacing: 12) {
            // Flow C: offer the web QR alongside the iOS QR (same session). Hidden when relay isn't
            // configured (no web payload).
            if !webPayload.isEmpty {
                Picker(L10n.pairingQRTarget, selection: $qrTarget) {
                    ForEach(PairingQRTarget.allCases) { target in
                        Text(target.label).tag(target)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .accessibilityLabel(L10n.pairingQRTarget)
                if qrTarget == .web {
                    Text(L10n.pairingWebQRHint)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
            }

            qrImage(payload: payload)
                .padding(12)
                .background(
                    RoundedRectangle(cornerRadius: 10)
                        .fill(Color(NSColor.controlBackgroundColor).opacity(0.4))
                )

            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(payload, forType: .string)
                copiedLink = true
                Task {
                    try? await Task.sleep(for: .seconds(2))
                    copiedLink = false
                }
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: copiedLink ? "checkmark" : "doc.on.clipboard")
                    Text(copiedLink ? L10n.pairingLinkCopied : L10n.copyPairingLink)
                }
            }
            .buttonStyle(.bordered)
            .help(copiedLink ? L10n.pairingLinkCopied : L10n.copyPairingLink)

            Button(L10n.back) {
                if let onExit {
                    onExit()
                } else {
                    viewModel.reset()
                }
            }
        }
    }


    private func qrImage(payload: String) -> some View {
        makeQRImage(payload: payload)
            .interpolation(.none)
            .resizable()
            .frame(width: 176, height: 176)
            .accessibilityLabel(L10n.pairingQRCode)
    }

    private func waitingInstructions(code: String, payload: String) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            instruction(1, L10n.pairingStepScan)
            instruction(2, L10n.pairingStepConfirm)
            instruction(3, L10n.pairingStepComplete)

            HStack(spacing: 8) {
                Text(L10n.manualCode)
                    .foregroundStyle(.secondary)
                Text(code)
                    .font(.system(.title3, design: .monospaced).weight(.semibold))
                    .textSelection(.enabled)
                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(code, forType: .string)
                    copiedCode = true
                    Task {
                        try? await Task.sleep(for: .seconds(2))
                        copiedCode = false
                    }
                } label: {
                    Image(systemName: copiedCode ? "checkmark" : "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .help(copiedCode ? L10n.pairingCopied : L10n.pairingCopyCode)
                .accessibilityLabel(copiedCode ? L10n.pairingCopied : L10n.pairingCopyCode)
            }

            if let remaining = viewModel.remainingSeconds {
                Text(String(format: L10n.pairingExpiresIn, formatCountdown(remaining)))
                    .font(.caption)
                    .foregroundStyle(remaining < 60 ? .orange : .secondary)
            }

            ProgressView(L10n.waitingForDevice)
                .controlSize(.small)

            let candidates = pairingCandidates(from: payload)
            if !candidates.isEmpty {
                VStack(alignment: .leading, spacing: 0) {
                    Button {
                        withAnimation(.spring(response: 0.3, dampingFraction: 0.7)) {
                            isDetailsExpanded.toggle()
                        }
                    } label: {
                        HStack(spacing: 6) {
                            Image(systemName: "chevron.right")
                                .font(.system(size: 9, weight: .bold))
                                .rotationEffect(.degrees(isDetailsExpanded ? 90 : 0))
                            Text(L10n.pairingConnectionDetails)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .foregroundColor(.secondary)
                    
                    if isDetailsExpanded {
                        VStack(alignment: .leading, spacing: 8) {
                            ForEach(candidates) { candidate in
                                Label {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(candidate.title)
                                        Text(candidate.url)
                                            .font(.system(.caption, design: .monospaced))
                                            .foregroundStyle(.secondary)
                                            .textSelection(.enabled)
                                    }
                                } icon: {
                                    Image(systemName: candidate.icon)
                                }
                            }
                        }
                        .padding(.leading, 15)
                        .padding(.top, 8)
                        .transition(.opacity.combined(with: .move(edge: .top)))
                    }
                }
            }
        }
        .frame(maxWidth: 390, alignment: .leading)
    }

    private func instruction(_ number: Int, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text("\(number).")
                .font(.body.weight(.semibold))
            Text(text)
        }
    }

    private func claimedView(deviceName: String, platform: String) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 12) {
                Image(systemName: platform.lowercased().contains("ios") ? "iphone" : "desktopcomputer")
                    .font(.title2)
                VStack(alignment: .leading, spacing: 3) {
                    Text(deviceName)
                        .font(.headline)
                    Text(platform)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            if !bridgeDisplayName.isEmpty {
                Text(String(format: L10n.pairingConnectTo, bridgeDisplayName))
            }
            Text(L10n.pairingApprovalExplanation)
                .font(.subheadline)
                .foregroundStyle(.secondary)

            HStack(spacing: 10) {
                Button(L10n.approve) {
                    viewModel.approve()
                }
                .buttonStyle(.borderedProminent)
                Button(L10n.reject, role: .destructive) {
                    viewModel.reject()
                }
            }
        }
    }

    private func resultView(
        icon: String,
        color: Color,
        message: String,
        button: String,
        startsPairing: Bool = false
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Label(message, systemImage: icon)
                .font(.headline)
                .foregroundStyle(color)
            Button(button) {
                viewModel.reset()
                if startsPairing {
                    viewModel.startPairing()
                }
            }
        }
    }

    private func formatCountdown(_ seconds: Int) -> String {
        String(format: "%02d:%02d", seconds / 60, seconds % 60)
    }

    private func makeQRImage(payload: String) -> Image {
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(payload.utf8)
        filter.correctionLevel = "M"
        if let outputImage = filter.outputImage {
            let scaled = outputImage.transformed(by: CGAffineTransform(scaleX: 6, y: 6))
            if let cgImage = context.createCGImage(scaled, from: scaled.extent) {
                return Image(nsImage: NSImage(cgImage: cgImage, size: NSSize(width: 176, height: 176)))
            }
        }
        return Image(systemName: "qrcode")
    }

    private func pairingCandidates(from payload: String) -> [PairingCandidate] {
        guard let components = URLComponents(string: payload) else { return [] }
        var candidates: [PairingCandidate] = []
        var seen = Set<String>()
        for item in components.queryItems ?? [] {
            guard let value = item.value, !value.isEmpty, seen.insert(value).inserted else { continue }
            switch item.name {
            case "local":
                candidates.append(.init(id: value, title: L10n.pairingLAN, url: value, icon: "wifi"))
            case "relay":
                candidates.append(.init(id: value, title: L10n.pairingRelay, url: value, icon: "lock.shield"))
            case "remote":
                candidates.append(.init(id: value, title: L10n.pairingAdvancedPath, url: value, icon: "network"))
            default:
                break
            }
        }
        return candidates
    }
}

/// 配对是一次专注任务：使用固定尺寸 sheet 承载既有二维码、流程、手动码、倒计时与连接详情，
/// 使首页在配对期间继续保持为稳定的设备与运行状态总览。
struct PairingSheet: View {
    @ObservedObject var viewModel: PairingViewModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        PageContainer(maxContentWidth: LayoutConstants.pairingSheetWidth) {
            VStack(alignment: .leading, spacing: 20) {
                PageHeader(L10n.pairingNewDevice) {
                    Button(L10n.done, action: close)
                        .buttonStyle(.bordered)
                }
                PairingView(
                    viewModel: viewModel,
                    showsHeader: false,
                    onExit: close
                )
            }
        }
        .frame(width: LayoutConstants.pairingSheetWidth, height: LayoutConstants.pairingSheetHeight)
    }

    private func close() {
        viewModel.reset()
        dismiss()
    }
}

/// 配对步骤轨迹：1 扫描二维码 → 2 在 Mac 上确认 → 3 开始使用。
/// 仅高亮当前步骤；已完成步骤弱化，未到步骤保持次要色。VoiceOver 可读。
struct PairingStepTracker: View {
    let current: Int

    private var steps: [(number: Int, title: String)] {
        [
            (1, L10n.pairingStepScan),
            (2, L10n.pairingStepConfirm),
            (3, L10n.pairingStepComplete),
        ]
    }

    var body: some View {
        HStack(spacing: 0) {
            ForEach(steps, id: \.number) { step in
                if step.number > 1 {
                    connector(active: step.number <= current)
                }
                stepNode(step)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(Text(L10n.pairingNewDevice))
        .accessibilityValue(Text(String(format: L10n.pairingStepProgress, current, steps.count)))
    }

    private func stepNode(_ step: (number: Int, title: String)) -> some View {
        let isCurrent = step.number == current
        let isDone = step.number < current
        return HStack(spacing: 6) {
            ZStack {
                Circle()
                    .strokeBorder(isCurrent ? Color.accentColor : Color.secondary.opacity(0.4), lineWidth: 1.5)
                    .background(Circle().fill(isCurrent ? Color.accentColor.opacity(0.18) : Color.clear))
                    .frame(width: 18, height: 18)
                Text("\(step.number)")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(isCurrent ? Color.accentColor : (isDone ? .secondary : .secondary.opacity(0.7)))
            }
            Text(step.title)
                .font(.caption)
                .foregroundStyle(isCurrent ? Color.primary : .secondary)
        }
    }

    private func connector(active: Bool) -> some View {
        RoundedRectangle(cornerRadius: 1)
            .fill(active ? Color.accentColor.opacity(0.5) : Color.secondary.opacity(0.25))
            .frame(width: 18, height: 2)
            .padding(.horizontal, 4)
    }
}
