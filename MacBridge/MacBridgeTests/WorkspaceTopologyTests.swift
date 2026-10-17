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
            L10n.devicePresenceLAN
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .connected([
                .init(kind: "direct", directClass: "loopback")
            ])),
            L10n.devicePresenceLAN
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .connected([
                .init(kind: "direct", directClass: "tailscale")
            ])),
            L10n.devicePresenceTailscale
        )
        XCTAssertEqual(
            WorkspaceView.presenceText(for: .connected([
                .init(kind: "direct", directClass: "unknown")
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


    func testAgentTopologyKeepsTrunkAndStubsClearOfCardAndLogo() {
        let gutter = WorkspaceView.agentGutterWidth
        let inset = WorkspaceView.agentTrunkInset
        let gap = WorkspaceView.agentStubGap
        let dot = WorkspaceView.agentEndpointDotRadius
        let service = CGRect(x: 0, y: 80, width: 640, height: 120)
        let cardMinX = service.minX + gutter
        let nodes = [
            WorkspaceView.AgentTopologyNode(midY: 260, minX: cardMinX, available: true),
            WorkspaceView.AgentTopologyNode(midY: 340, minX: cardMinX, available: false)
        ]
        let layout = WorkspaceView.agentTopologyPaths(service: service, nodes: nodes)
        let trunkX = cardMinX - inset
        let stubEndX = cardMinX - gap
        XCTAssertEqual(layout.trunk, [
            CGPoint(x: trunkX, y: 200),
            CGPoint(x: trunkX, y: 340)
        ])
        XCTAssertEqual(layout.stubs, [
            WorkspaceView.AgentTopologyStub(
                from: CGPoint(x: trunkX, y: 260),
                to: CGPoint(x: stubEndX, y: 260),
                available: true
            ),
            WorkspaceView.AgentTopologyStub(
                from: CGPoint(x: trunkX, y: 340),
                to: CGPoint(x: stubEndX, y: 340),
                available: false
            )
        ])
        XCTAssertGreaterThanOrEqual(gutter, inset)
        XCTAssertGreaterThan(gap, dot)
        XCTAssertGreaterThanOrEqual(stubEndX - trunkX, 16)
        XCTAssertLessThan(trunkX, cardMinX)
        for stub in layout.stubs {
            XCTAssertLessThan(stub.to.x + dot, cardMinX, "endpoint dot must stay left of the card")
            // Agent row padding is 14pt; 28pt brand mark starts there.
            XCTAssertLessThan(stub.to.x + dot, cardMinX + 14, "endpoint must stay left of the logo")
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
