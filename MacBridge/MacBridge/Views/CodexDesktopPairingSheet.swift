import SwiftUI

struct CodexDesktopPairingSheet: View {
    @ObservedObject var viewModel: BackendStatusViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var statusMessage = ""
    @State private var busy = false
    @State private var phase = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(L10n.codexDesktopPairTitle)
                .font(.title2)
                .fontWeight(.semibold)
            Text(L10n.codexDesktopPairBody)
                .font(.body)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if phase == "authorizing" {
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    Text(statusMessage.isEmpty ? L10n.codexDesktopPairWaitingAuth : statusMessage)
                        .font(.callout)
                }
            } else if !statusMessage.isEmpty {
                Text(statusMessage)
                    .font(.callout)
            }
            SecureField(L10n.codexDesktopPairCode, text: $code)
                .textFieldStyle(.roundedBorder)
                .disabled(busy || phase != "awaiting_code")
            HStack {
                Spacer()
                Button(L10n.cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(L10n.codexDesktopPairSubmit) {
                    Task { await submit() }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(busy || phase != "awaiting_code" || code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(24)
        .frame(minWidth: 420)
        .task { await start() }
    }

    private func apply(_ snap: CodexRemotePairingStatus) {
        phase = snap.phase
        if let message = snap.message, !message.isEmpty {
            statusMessage = message
        }
    }

    private func start() async {
        busy = true
        do {
            let snap = try await viewModel.startCodexDesktopPairing()
            apply(snap)
            if snap.phase == "ready" {
                busy = false
                await viewModel.refreshAgents()
                dismiss()
                return
            }
            if snap.phase == "offline" {
                busy = false
                await viewModel.refreshAgents()
                return
            }
        } catch {
            statusMessage = error.localizedDescription
            busy = false
            return
        }
        busy = false
        await pollUntilReadyForCode()
    }

    private func pollUntilReadyForCode() async {
        while !Task.isCancelled {
            if phase == "awaiting_code" || phase == "ready" || phase == "failed" || phase == "offline" {
                return
            }
            try? await Task.sleep(nanoseconds: 500_000_000)
            do {
                apply(try await viewModel.codexDesktopPairingStatus())
            } catch {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
    }

    private func submit() async {
        busy = true
        defer { busy = false }
        do {
            let snap = try await viewModel.submitCodexDesktopPairingCode(code)
            apply(snap)
            if snap.phase == "ready" {
                await viewModel.refreshAgents()
                dismiss()
            }
        } catch {
            statusMessage = error.localizedDescription
        }
    }
}
