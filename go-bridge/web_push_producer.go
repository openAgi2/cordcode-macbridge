package gobridge

// web_push_producer.go — PushIntent producer 位点与样本门（web push 方案 §8.1）。
//
// 位点清单（唯一两处允许设置 PushIntent 的 producer）：
//  1. agent relay loop（relayEvents）：该 loop 天然是 session 的 ingest owner——
//     存在 relay 时 passiveFeedAllowed 恒 false（agentRelayRunning 互斥），无需重复检查；
//  2. startPassiveSubscription 被动泵：仅对 passiveFeedAllowed(...) == true 放行的
//     事件补投（覆盖 PWA 不在线时的外部 turn terminal）。
// 其余路径（publishPreReducedTimeline、hydrate source、recovery replay、derived
// question_asked、catalog/control）永不设置——EventPublisher 的 sink 位点对非 kernel
// 路径的 intent 显式 fail closed（web_push_candidate.go）。
//
// 样本门：每个 kind 绑定实现计划 §3.2 的真实样本门 id。样本未归档前 Passed=false，
// producer 不产生 intent；翻转必须由 owner 归档样本后显式修改本表，不得用 analogous
// fixture、mock 或缓存样本替代。

// webPushKindGate 声明一个通知类别的样本门状态。
type webPushKindGate struct {
	// GateID 是实现计划 §3.2 的真实样本门标识（证据归档编号）。
	GateID string
	// Passed 只能在对应真实样本归档并经 owner 审核后置 true。
	Passed bool
}

// webPushKindGates 是全部通知类别的样本门表。首版交付时四类全部未通过
// （EVT-TURN-1/EVT-PERM-1/EVT-INPUT-1/EVT-ERROR-1 均待 owner 真机采样）——
// 这不是缺陷，是 D4 样本门的诚实状态（完成标准明文允许）。
var webPushKindGates = map[WebPushNotificationKind]webPushKindGate{
	WebPushKindCompletion: {GateID: "EVT-TURN-1", Passed: false},
	WebPushKindPermission: {GateID: "EVT-PERM-1", Passed: false},
	WebPushKindInput:      {GateID: "EVT-INPUT-1", Passed: false},
	WebPushKindError:      {GateID: "EVT-ERROR-1", Passed: false},
}

func webPushKindEnabled(kind WebPushNotificationKind) bool {
	gate, ok := webPushKindGates[kind]
	return ok && gate.Passed
}

// webPushActiveTurnID 从 authoritative kernel 读取该 session 当前 active turn
// （turn_completed 的 notification key 需要 turnId；relay 数据本身不携带）。
// kernel 未接线 / 无 state 时返回空——identity 缺失即不发送（§8.2，不得退回
// session-only key）。
func webPushActiveTurnID(kernel *ProjectionKernel, backendID, sessionID string) string {
	return kernel.ActiveTurnID(backendID, sessionID)
}

// pushIntentForRelayTerminal 为 agent relay loop（ingest owner）派生 terminal/permission
// 事件的 PushIntent。sessionTitle 来自 bridge 内 authoritative 标题缓存（设计 delta
// §2.2；可为空）。返回 nil = 不发送（样本门未过 / identity 缺失 / 事件不在清单）。
// 样本门拦下的真实事件会（在采集开关开启时）落脱敏 EVT 样本（设计 delta §3）。
// anchor 只允许 turn|interaction（§7.3），无已验证样本时 input/error 的 intent 在
// 样本门前也进不来——这里再加一道 kind 门，双保险。
func pushIntentForRelayTerminal(kernel *ProjectionKernel, backendID, sessionID, eventName string, data interface{}, sessionTitle string) *PushIntent {
	title := webPushSanitizeSessionTitle(sessionTitle)
	switch eventName {
	case "turn_completed":
		if !webPushKindEnabled(WebPushKindCompletion) {
			captureWebPushSample("EVT-TURN-1", map[string]interface{}{
				"backend":    backendID,
				"event":      eventName,
				"session":    webPushRedactID(sessionID),
				"activeTurn": webPushRedactID(webPushActiveTurnID(kernel, backendID, sessionID)),
				"rawShape":   webPushRedactShape(data, 0),
			})
			return nil
		}
		turnID := webPushActiveTurnID(kernel, backendID, sessionID)
		if turnID == "" {
			return nil
		}
		return &PushIntent{
			Kind:            WebPushKindCompletion,
			NotificationKey: backendID + "|" + sessionID + "|" + turnID + "|completed",
			AnchorKind:      "turn",
			AnchorID:        turnID,
			SessionTitle:    title,
		}
	case "permission_request":
		requestID := ""
		if m, ok := data.(map[string]interface{}); ok {
			requestID, _ = m["requestId"].(string)
			if requestID == "" {
				requestID, _ = m["itemId"].(string)
			}
		}
		if !webPushKindEnabled(WebPushKindPermission) {
			captureWebPushSample("EVT-PERM-1", map[string]interface{}{
				"backend":   backendID,
				"event":     eventName,
				"session":   webPushRedactID(sessionID),
				"requestId": webPushRedactID(requestID),
				"rawShape":  webPushRedactShape(data, 0),
			})
			return nil
		}
		if requestID == "" {
			return nil
		}
		return &PushIntent{
			Kind:            WebPushKindPermission,
			NotificationKey: backendID + "|" + sessionID + "|" + requestID + "|permission",
			AnchorKind:      "interaction",
			AnchorID:        requestID,
			SessionTitle:    title,
		}
	default:
		return nil
	}
}

// pushIntentForPassiveEvent 为被动泵补投路径派生 PushIntent。与 relay 侧共享同一
// 派生逻辑，但调用前提是 passiveFeedAllowed(...) == true（单一摄入所有者的被动侧
// 表达：agent relay 在跑时永不为真）。terminal 补投只认 completion；permission
// 属于交互等待，不属于被动 terminal 收口。
func pushIntentForPassiveEvent(kernel *ProjectionKernel, backendID, sessionID, eventName string, data interface{}, sessionTitle string) *PushIntent {
	if eventName != "turn_completed" {
		return nil
	}
	return pushIntentForRelayTerminal(kernel, backendID, sessionID, eventName, data, sessionTitle)
}
