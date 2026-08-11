package admission

import (
	"encoding/json"
	"fmt"
)

// 本文件是 strict codec 的“消费侧”解码器：把 committed fixtures（proposed bytes）逐字段 strict
// 解码成 Go 类型，用于证明 Go 端 codec 能消费同一 fixture bytes。拒绝 duplicate/unknown/missing/
// null/类型错误/数字形态错误/32-hex 错误。规范：plan §3.6.3 / R11 P1-2/P2-1。

// decodeRuntimeIdentity strict-decode expectedRuntime{pid,bridgeEpoch}。
func decodeRuntimeIdentity(raw json.RawMessage) (RuntimeIdentity, error) {
	m, err := DecodeStrictObject(raw, []string{"pid", "bridgeEpoch"})
	if err != nil {
		return RuntimeIdentity{}, err
	}
	pid, err := ParseStrictInt32Positive(m["pid"])
	if err != nil {
		return RuntimeIdentity{}, err
	}
	be, err := ParseStrictUInt(m["bridgeEpoch"], 64)
	if err != nil {
		return RuntimeIdentity{}, err
	}
	return RuntimeIdentity{PID: pid, BridgeEpoch: be}, nil
}

// DecodeQuiesceRequest strict-decode POST /internal/runtime/quiesce 请求。
func DecodeQuiesceRequest(raw json.RawMessage) (QuiesceRequest, error) {
	m, err := DecodeStrictObject(raw, []string{"managementSchemaVersion", "operationId", "expectedRuntime", "expectedHealthEpoch"})
	if err != nil {
		return QuiesceRequest{}, err
	}
	msv, err := ParseStrictUInt(m["managementSchemaVersion"], 64)
	if err != nil {
		return QuiesceRequest{}, err
	}
	opIDStr, err := DecodeStrictString(m["operationId"])
	if err != nil {
		return QuiesceRequest{}, err
	}
	opID, err := DecodeOperationID(opIDStr)
	if err != nil {
		return QuiesceRequest{}, err
	}
	rt, err := decodeRuntimeIdentity(m["expectedRuntime"])
	if err != nil {
		return QuiesceRequest{}, err
	}
	hep, err := ParseStrictUInt(m["expectedHealthEpoch"], 64)
	if err != nil {
		return QuiesceRequest{}, err
	}
	return QuiesceRequest{
		ManagementSchemaVersion: msv, OperationID: opID,
		ExpectedRuntime: rt, ExpectedHealthEpoch: hep,
	}, nil
}

// DecodeCommitRequest strict-decode commit / abort 请求（同字段集）。
func DecodeCommitRequest(raw json.RawMessage) (CommitRequest, error) {
	m, err := DecodeStrictObject(raw, []string{"managementSchemaVersion", "operationId", "expectedRuntime", "expectedHealthEpoch", "quiesceEpoch", "token"})
	if err != nil {
		return CommitRequest{}, err
	}
	msv, err := ParseStrictUInt(m["managementSchemaVersion"], 64)
	if err != nil {
		return CommitRequest{}, err
	}
	opIDStr, err := DecodeStrictString(m["operationId"])
	if err != nil {
		return CommitRequest{}, err
	}
	opID, err := DecodeOperationID(opIDStr)
	if err != nil {
		return CommitRequest{}, err
	}
	rt, err := decodeRuntimeIdentity(m["expectedRuntime"])
	if err != nil {
		return CommitRequest{}, err
	}
	hep, err := ParseStrictUInt(m["expectedHealthEpoch"], 64)
	if err != nil {
		return CommitRequest{}, err
	}
	qep, err := ParseStrictUInt(m["quiesceEpoch"], 64)
	if err != nil {
		return CommitRequest{}, err
	}
	tokStr, err := DecodeStrictString(m["token"])
	if err != nil {
		return CommitRequest{}, err
	}
	tok, err := DecodeToken(tokStr)
	if err != nil {
		return CommitRequest{}, err
	}
	return CommitRequest{
		ManagementSchemaVersion: msv, OperationID: opID,
		ExpectedRuntime: rt, ExpectedHealthEpoch: hep,
		QuiesceEpoch: qep, Token: tok,
	}, nil
}

// resultAllowedKeys 返回某 outcome 允许的额外字段（除 common managementSchemaVersion/operationId/outcome 外）。
func quiesceResultExtra(outcome string) []string {
	switch outcome {
	case "safe":
		return []string{"runtimeIdentity", "healthEpoch", "quiesceEpoch", "token", "leaseMillis", "leaseRemainingMillis"}
	case "deferred":
		return []string{"activeTurns", "pendingInteractions", "retryAfterMillis"}
	default:
		return nil // identity_mismatch/epoch_mismatch/already_committed/already_quiescing/operation_reused/token_generation_failed
	}
}

func commitResultExtra(outcome string) []string {
	switch outcome {
	case "committed", "already_committed":
		return []string{"runtimeIdentity", "healthEpoch", "quiesceEpoch"}
	default:
		return nil // identity_mismatch/epoch_mismatch/quiesce_mismatch/token_mismatch/lease_expired
	}
}

func abortResultExtra(outcome string) []string {
	switch outcome {
	case "aborted":
		return []string{"runtimeIdentity", "healthEpoch"}
	default:
		return nil // already_accepting/already_committed/identity_mismatch/epoch_mismatch/quiesce_mismatch/token_mismatch/lease_expired
	}
}

// DecodeResultShape strict-decode 一个 result tagged union（common managementSchemaVersion/operationId/outcome
// + outcome-specific extra），用于校验 committed result fixtures 字段集合正确。返回 (outcome, extraRaw)。
// which ∈ "quiesce"|"commit"|"abort" 决定每种 outcome 的允许额外字段。
func DecodeResultShape(raw json.RawMessage, which string) (outcome string, m map[string]json.RawMessage, err error) {
	// 先 loose 解码以读 outcome（此时还不能拒绝 extra —— extra 是否合法取决于 outcome）。
	var loose map[string]json.RawMessage
	if err = json.Unmarshal(raw, &loose); err != nil {
		return "", nil, ErrInvalidJSON
	}
	outcomeRaw, ok := loose["outcome"]
	if !ok {
		return "", nil, fmt.Errorf("%w: outcome", ErrMissingField)
	}
	outcome, err = DecodeStrictString(outcomeRaw)
	if err != nil {
		return "", nil, err
	}
	var extra []string
	switch which {
	case "quiesce":
		extra = quiesceResultExtra(outcome)
	case "commit":
		extra = commitResultExtra(outcome)
	case "abort":
		extra = abortResultExtra(outcome)
	default:
		return "", nil, fmt.Errorf("unknown result group %q", which)
	}
	// outcome 合法性：未知 outcome 直接拒绝（quiesce/commit/abort 各自枚举）。
	if extra == nil && !isKnownOutcome(which, outcome) {
		return "", nil, fmt.Errorf("%w: outcome %s not valid for %s", ErrInvalidString, outcome, which)
	}
	// 现在做真正的 strict exact-key 校验（common + 该 outcome 允许的 extra）。
	allowed := append([]string{"managementSchemaVersion", "operationId", "outcome"}, extra...)
	m, err = DecodeStrictObject(raw, allowed)
	if err != nil {
		return "", nil, err
	}
	// 校验 common 字段类型
	if _, err := ParseStrictUInt(m["managementSchemaVersion"], 64); err != nil {
		return "", nil, err
	}
	if _, err := DecodeOperationIDmust(m["operationId"]); err != nil {
		return "", nil, err
	}
	// 校验 extra 字段类型（数字域 integer token；token/runtimeIdentity 各自合法）
	for _, k := range extra {
		switch k {
		case "token":
			s, e := DecodeStrictString(m[k])
			if e != nil {
				return "", nil, e
			}
			if _, e := DecodeToken(s); e != nil {
				return "", nil, e
			}
		case "runtimeIdentity":
			if _, e := decodeRuntimeIdentity(m[k]); e != nil {
				return "", nil, e
			}
		case "healthEpoch", "quiesceEpoch":
			if _, e := ParseStrictUInt(m[k], 64); e != nil {
				return "", nil, e
			}
		case "leaseMillis", "leaseRemainingMillis", "retryAfterMillis":
			if _, e := ParseStrictUInt(m[k], 32); e != nil {
				return "", nil, e
			}
		case "activeTurns", "pendingInteractions":
			if _, e := ParseStrictUInt(m[k], 32); e != nil {
				return "", nil, e
			}
		}
	}
	return outcome, m, nil
}

// isKnownOutcome 判断 outcome 是否属于该 group 的合法无-extra outcome（extra==nil 时调用）。
func isKnownOutcome(which, outcome string) bool {
	switch which {
	case "quiesce":
		switch outcome {
		case "identity_mismatch", "epoch_mismatch", "already_committed", "already_quiescing", "operation_reused", "token_generation_failed":
			return true
		}
	case "commit":
		switch outcome {
		case "identity_mismatch", "epoch_mismatch", "quiesce_mismatch", "token_mismatch", "lease_expired":
			return true
		}
	case "abort":
		switch outcome {
		case "already_accepting", "already_committed", "identity_mismatch", "epoch_mismatch", "quiesce_mismatch", "token_mismatch", "lease_expired":
			return true
		}
	}
	return false
}

// DecodeOperationIDmust 把 operationId 字段 strict 解码成 OperationID（结果 fixture 也带 operationId）。
func DecodeOperationIDmust(raw json.RawMessage) (OperationID, error) {
	s, err := DecodeStrictString(raw)
	if err != nil {
		return OperationID{}, err
	}
	return DecodeOperationID(s)
}
