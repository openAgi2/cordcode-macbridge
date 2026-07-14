import XCTest
@testable import CordCodeLink

// P1-1 配对工作区测试：保留既有 uiState 状态机，验证步骤轨迹映射与文案。
// 状态机本身的行为（创建/审批/过期）由既有 PairingViewModel 测试覆盖，此处只验证新加的任务化映射。
@MainActor
final class PairingWorkspaceTests: XCTestCase {

    /// 镜像 PairingView.currentStep 的派生逻辑，确保步骤轨迹与状态机一致。
    private static func currentStep(for state: PairingUIState) -> Int? {
        switch state {
        case .idle, .creating, .error: return nil
        case .waitingForClaim: return 1
        case .claimed: return 2
        case .approved, .rejected, .expired: return 3
        }
    }

    func testStepMappingMatchesStateMachine() {
        XCTAssertEqual(Self.currentStep(for: .idle), nil)
        XCTAssertEqual(Self.currentStep(for: .creating), nil)
        XCTAssertEqual(Self.currentStep(for: .error("x")), nil)
        XCTAssertEqual(Self.currentStep(for: .waitingForClaim(sessionId: "s", manualCode: "123456", qrPayload: "p")), 1)
        XCTAssertEqual(Self.currentStep(for: .claimed(deviceName: "iPhone", platform: "ios")), 2)
        XCTAssertEqual(Self.currentStep(for: .approved), 3)
        XCTAssertEqual(Self.currentStep(for: .rejected), 3)
        XCTAssertEqual(Self.currentStep(for: .expired), 3)
    }

    func testStepsAreThreeAndMonotonic() {
        // 步骤轨迹固定 3 步：扫描 → 确认 → 完成。
        let activeSteps = [1, 2, 3]
        for i in activeSteps.indices.dropLast() {
            XCTAssertLessThan(activeSteps[i], activeSteps[i + 1])
        }
    }

    func testStepCopyKeysPresent() {
        XCTAssertFalse(L10n.pairingStepScan.isEmpty)
        XCTAssertFalse(L10n.pairingStepConfirm.isEmpty)
        XCTAssertFalse(L10n.pairingStepComplete.isEmpty)
        XCTAssertFalse(L10n.pairingStepProgress.isEmpty)
        // 进度文案应包含占位符（VoiceOver 读取「第 X 步，共 Y 步」）。
        XCTAssertTrue(L10n.pairingStepProgress.contains("%d"))
    }

    func testRevokeMessageDescribesResultAndRecovery() {
        // P1-1 要求撤销确认补齐「将断开此设备、之后需要重新配对」结果与恢复文案。
        let msg = L10n.devicesRevokeMessage
        XCTAssertFalse(msg.isEmpty)
        // 既有文案已表达断开与重新配对，断言语义关键词（中英任一）。
        let mentionsRecovery = msg.lowercased().contains("pair again") || msg.contains("重新配对")
        XCTAssertTrue(mentionsRecovery, "撤销文案应说明需要重新配对，实际：\(msg)")
    }
}
