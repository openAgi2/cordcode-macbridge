import XCTest
@testable import CordCodeLink

@MainActor
final class WorkspaceTopologyTests: XCTestCase {
    func testAgentGroupUsesInstallationFactOnly() {
        XCTAssertEqual(WorkspaceView.agentGroup(for: "installed"), .installed)
        XCTAssertEqual(WorkspaceView.agentGroup(for: "missing"), .missing)
        XCTAssertEqual(WorkspaceView.agentGroup(for: "not_required"), .external)
        XCTAssertEqual(WorkspaceView.agentGroup(for: "failed"), .pending)
        XCTAssertEqual(WorkspaceView.agentGroup(for: nil), .pending)
    }

    func testPresenceTextDistinguishesUnknownDisconnectedAndTransports() {
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .unknown),
            L10n.devicePresenceUnknown
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .disconnected),
            L10n.devicePresenceDisconnected
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .connected([
                .init(kind: "direct", directClass: "private")
            ])),
            L10n.devicePresenceDirect
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .connected([
                .init(kind: "relay", directClass: nil)
            ])),
            L10n.devicePresenceRelay
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .connected([
                .init(kind: "relay", directClass: nil),
                .init(kind: "direct", directClass: "private")
            ])),
            L10n.devicePresenceSwitching
        )
    }

    func testTopologyPolylineStaysOrthogonalAndUsesStableMiddleY() {
        let points = WorkspaceView.topologyPolylinePoints(
            from: CGPoint(x: 20, y: 40),
            to: CGPoint(x: 180, y: 140)
        )
        XCTAssertEqual(points, [
            CGPoint(x: 20, y: 40),
            CGPoint(x: 20, y: 90),
            CGPoint(x: 180, y: 90),
            CGPoint(x: 180, y: 140)
        ])
        for index in 1..<points.count {
            let previous = points[index - 1]
            let current = points[index]
            XCTAssertTrue(
                previous.x == current.x || previous.y == current.y,
                "segment \(index) is not orthogonal"
            )
        }
    }

    func testSnapshotExpandsRowsToFixedProductCatalogAndMarksStaleObservation() {
        let viewModel = BackendStatusViewModel()
        viewModel.agents = [
            BackendAgentStatus(
                id: "claude",
                displayName: "Claude Code",
                kind: "claude_code",
                status: "available",
                reason: nil,
                isRefreshing: false,
                requiresPollingForExternalTurns: false
            )
        ]
        let snapshot = WorkspaceSnapshot(
            schemaVersion: 1,
            runtimeIdentity: .init(pid: 77, bridgeEpoch: 88),
            selectionRevision: 4,
            requestedDrivers: ["claude"],
            activeDrivers: ["claude"],
            sampledAt: "2026-10-15T00:00:00Z",
            agents: [
                makeAgent(id: "claude", installationState: "installed", readinessStatus: "available"),
                makeAgent(id: "codex-remote", installationState: "missing", readinessStatus: nil),
                makeAgent(id: "dsh-web", installationState: "installed", readinessStatus: nil),
                makeAgent(id: "grokbuild", installationState: "installed", readinessStatus: "service_not_running"),
                makeAgent(id: "opencode-web", installationState: "not_required", readinessStatus: nil)
            ],
            devicePresence: [],
            relay: .init(enabled: true, configured: true, connected: true)
        )

        viewModel.applyWorkspaceSnapshotPhase(.available(snapshot), isObservationCurrent: true)

        XCTAssertEqual(viewModel.agents.map(\.id), ProductAgentCatalog.ids)
        XCTAssertEqual(viewModel.agents[0].isActive, true)
        XCTAssertEqual(viewModel.agents[0].status, "available")
        XCTAssertEqual(viewModel.agents[1].isActive, false)
        XCTAssertEqual(viewModel.agents[1].installation?.state, "missing")
        XCTAssertEqual(viewModel.agents[1].status, "inactive")
        XCTAssertEqual(viewModel.agents[2].isActive, false)
        XCTAssertEqual(viewModel.agents[3].isActive, true)
        XCTAssertEqual(viewModel.agents[3].status, "service_not_running")
        XCTAssertEqual(viewModel.agents[4].installation?.state, "not_required")
        XCTAssertEqual(viewModel.activeDrivers, ["claude"])
        XCTAssertEqual(viewModel.requestedDrivers, ["claude"])
        XCTAssertEqual(viewModel.selectionRevision, 4)
        XCTAssertEqual(viewModel.workspaceRelay?.connected, true)

        viewModel.applyWorkspaceSnapshotPhase(.available(snapshot), isObservationCurrent: false)
        XCTAssertTrue(viewModel.agents.allSatisfy { !$0.isObservationCurrent })
        XCTAssertTrue(viewModel.agents.isEmpty == false)
        XCTAssertNil(viewModel.workspaceRelay)
    }

    private func makeAgent(
        id: String,
        installationState: String,
        readinessStatus: String?
    ) -> WorkspaceSnapshot.Agent {
        let product = ProductAgentCatalog.products.first { $0.id == id }
        let readiness = readinessStatus.map { status in
            AgentInfo(
                id: id,
                kind: product?.kind ?? id,
                displayName: product?.name ?? id,
                status: status,
                reason: nil,
                liveEvents: "none",
                requiresPollingForExternalTurns: false
            )
        }
        return WorkspaceSnapshot.Agent(
            id: id,
            installation: .init(
                state: installationState,
                errorCode: nil,
                reason: nil,
                artifactKind: installationState == "not_required" ? "external_service" : "cli",
                installMethod: nil
            ),
            installGuidance: nil,
            version: nil,
            versionBuild: nil,
            versionSource: nil,
            versionCheck: .init(state: "unchecked"),
            readiness: readiness
        )
    }
}
