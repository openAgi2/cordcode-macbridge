import CoreImage.CIFilterBuiltins
import SwiftUI

struct PairingView: View {
    @ObservedObject var viewModel: PairingViewModel
    @AppStorage("bridgeDisplayName") private var bridgeDisplayName = ""
    @State private var copiedCode = false

    private struct PairingCandidate: Identifiable {
        let id: String
        let title: String
        let url: String
        let icon: String
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            SectionHeader(L10n.pairingNewDevice)

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
        ViewThatFits(in: .horizontal) {
            HStack(alignment: .top, spacing: 28) {
                qrImage(payload: payload)
                waitingInstructions(code: code, payload: payload)
            }
            VStack(alignment: .leading, spacing: 20) {
                qrImage(payload: payload)
                waitingInstructions(code: code, payload: payload)
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
                DisclosureGroup(L10n.pairingConnectionDetails) {
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
                    .padding(.top, 8)
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
