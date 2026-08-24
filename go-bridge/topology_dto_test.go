package gobridge

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestDecodeEnabledFixture(t *testing.T) {
	s, err := DecodeTopologySnapshot(loadFixture(t, "topology_snapshot.json"))
	if err != nil {
		t.Fatalf("decode enabled fixture: %v", err)
	}
	if s.SchemaVersion != TopologySchemaVersion || s.State != TopologyStateEnabled {
		t.Fatalf("unexpected header: %+v", s)
	}
	if s.BridgeEpoch != 1710893634113558 || s.SampledAtMs != 1710893634113558 {
		t.Fatalf("epoch/sampledAt mismatch: %d/%d", s.BridgeEpoch, s.SampledAtMs)
	}
	if s.SyncHealth != string(SyncHealthy) {
		t.Fatalf("syncHealth = %q, want healthy", s.SyncHealth)
	}
	// 8 个固定维度键全在，bridge 维度的 enum/source 与 §2.4 示例一致。
	if len(s.Dimensions) != 8 {
		t.Fatalf("dimensions = %d, want 8", len(s.Dimensions))
	}
	if d := s.Dimensions[DimBridgeAttachment]; d.Enum != string(AttachmentShared) || d.Source != DimSourceProviderSnap || d.ErrorCode != ErrorNone || d.Stale || d.AgeMs != 1200 {
		t.Fatalf("bridge dimension mismatch: %+v", d)
	}
	// instances tagged union：kind/state 原样保留。
	if len(s.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(s.Instances))
	}
	inst := s.Instances[0]
	if inst.PID != 4242 || inst.Classification != string(DesktopClassificationSharedOnly) {
		t.Fatalf("instance mismatch: %+v", inst)
	}
	if len(inst.Evidence) != 2 || inst.Evidence[0] != (TopologyInstanceEvidence{Kind: string(EvidenceKindSharedFD), State: string(EvidenceStateConfirmed)}) ||
		inst.Evidence[1] != (TopologyInstanceEvidence{Kind: string(EvidenceKindPrivateStdio), State: string(EvidenceStateUnavailable)}) {
		t.Fatalf("evidence mismatch: %+v", inst.Evidence)
	}
}

func TestDecodeDisabledFixture(t *testing.T) {
	s, err := DecodeTopologySnapshot(loadFixture(t, "topology_snapshot_disabled.json"))
	if err != nil {
		t.Fatalf("decode disabled fixture: %v", err)
	}
	if s.State != TopologyStateDisabled || s.SyncHealth != "" || s.Dimensions != nil || s.Instances != nil {
		t.Fatalf("disabled snapshot carries forbidden fields: %+v", s)
	}
}

func TestBridgeEpochPreservesUnsignedIdentityBitPattern(t *testing.T) {
	liveEpoch := uint64(1<<63) + 42
	wireEpoch := int64(liveEpoch)
	data := loadFixture(t, "topology_snapshot.json")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["bridgeEpoch"] = json.RawMessage([]byte(fmt.Sprintf("%d", wireEpoch)))
	patched, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	s, err := DecodeTopologySnapshot(patched)
	if err != nil {
		t.Fatalf("decode signed bridge epoch bit pattern: %v", err)
	}
	if uint64(s.BridgeEpoch) != liveEpoch {
		t.Fatalf("bridgeEpoch bits = %d, want %d", uint64(s.BridgeEpoch), liveEpoch)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	s, err := DecodeTopologySnapshot(loadFixture(t, "topology_snapshot.json"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := DecodeTopologySnapshot(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if !reflect.DeepEqual(s, back) {
		t.Fatalf("round trip mismatch:\nfirst: %+v\nsecond: %+v", s, back)
	}
}

func TestUnknownFieldsIgnored(t *testing.T) {
	data := loadFixture(t, "topology_snapshot.json")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw["unexpectedField"] = json.RawMessage(`true`)
	patched, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// 未知顶层字段忽略。
	if _, err := DecodeTopologySnapshot(patched); err != nil {
		t.Fatalf("unknown top-level field must be ignored: %v", err)
	}
	// 维度内的未知字段同样忽略：往 bridge 维度塞一个额外键。
	dimRaw := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw["dimensions"], &dimRaw); err != nil {
		t.Fatalf("unmarshal dims: %v", err)
	}
	bridge := map[string]json.RawMessage{}
	if err := json.Unmarshal(dimRaw[DimBridgeAttachment], &bridge); err != nil {
		t.Fatalf("unmarshal bridge: %v", err)
	}
	bridge["extra"] = json.RawMessage(`"x"`)
	bridgeOut, _ := json.Marshal(bridge)
	dimRaw[DimBridgeAttachment] = bridgeOut
	dimsOut, _ := json.Marshal(dimRaw)
	raw["dimensions"] = dimsOut
	patched2, _ := json.Marshal(raw)
	if _, err := DecodeTopologySnapshot(patched2); err != nil {
		t.Fatalf("dim unknown field must be ignored: %v", err)
	}
}

func TestFailClosedUnknownEnum(t *testing.T) {
	mutate := func(handle func(m map[string]json.RawMessage)) []byte {
		t.Helper()
		data := loadFixture(t, "topology_snapshot.json")
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		handle(m)
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return out
	}
	// 未知 enum（未在任意维度白名单）。
	bad := func(m map[string]json.RawMessage) {
		dims := map[string]json.RawMessage{}
		if err := json.Unmarshal(m["dimensions"], &dims); err != nil {
			t.Fatal(err)
		}
		dim := map[string]json.RawMessage{}
		if err := json.Unmarshal(dims[DimBridgeAttachment], &dim); err != nil {
			t.Fatal(err)
		}
		dim["enum"] = json.RawMessage(`"super_healthy"`)
		out, _ := json.Marshal(dim)
		dims[DimBridgeAttachment] = out
		out2, _ := json.Marshal(dims)
		m["dimensions"] = out2
	}
	if _, err := DecodeTopologySnapshot(mutate(bad)); err == nil || !strings.Contains(err.Error(), "invalid enum") {
		t.Fatalf("unknown enum must fail closed, got err=%v", err)
	}
	// 未知 syncHealth。
	if _, err := DecodeTopologySnapshot(mutate(func(m map[string]json.RawMessage) {
		m["syncHealth"] = json.RawMessage(`"awesome"`)
	})); err == nil || !strings.Contains(err.Error(), "syncHealth") {
		t.Fatalf("unknown syncHealth must fail closed, got err=%v", err)
	}
	// 未知 evidence state。
	if _, err := DecodeTopologySnapshot(mutate(func(m map[string]json.RawMessage) {
		var insts []map[string]json.RawMessage
		if err := json.Unmarshal(m["instances"], &insts); err != nil {
			t.Fatal(err)
		}
		var ev []map[string]json.RawMessage
		if err := json.Unmarshal(insts[0]["evidence"], &ev); err != nil {
			t.Fatal(err)
		}
		ev[0]["state"] = json.RawMessage(`"maybe_so"`)
		out, _ := json.Marshal(ev)
		insts[0]["evidence"] = out
		out2, _ := json.Marshal(insts)
		m["instances"] = out2
	})); err == nil || !strings.Contains(err.Error(), "evidence state") {
		t.Fatalf("unknown evidence state must fail closed, got err=%v", err)
	}
}

func TestFailClosedStructuralErrors(t *testing.T) {
	// schemaVersion 不匹配。
	if _, err := DecodeTopologySnapshot([]byte(`{"schemaVersion":"topology-monitor.v2","state":"enabled"}`)); err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("schemaVersion mismatch must fail, got %v", err)
	}
	// 未知 state。
	if _, err := DecodeTopologySnapshot([]byte(`{"schemaVersion":"topology-monitor.v1","state":"on","bridgeEpoch":1,"sampledAtMs":1}`)); err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("unknown state must fail, got %v", err)
	}
	// disabled 夹带 dimensions 必须失败（fail-closed 拒绝形状外证据）。
	disabledWithDims := `{"schemaVersion":"topology-monitor.v1","state":"disabled","bridgeEpoch":1,"sampledAtMs":1,"dimensions":{"x":{"enum":"y","ageMs":0,"stale":false,"source":"process_tree","errorCode":"none"}}}`
	if _, err := DecodeTopologySnapshot([]byte(disabledWithDims)); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled with dims must fail, got %v", err)
	}
	// enabled 缺一个维度键。
	data := loadFixture(t, "topology_snapshot.json")
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	dims := map[string]json.RawMessage{}
	if err := json.Unmarshal(m["dimensions"], &dims); err != nil {
		t.Fatal(err)
	}
	delete(dims, DimSeatHealthDaemon)
	out, _ := json.Marshal(dims)
	m["dimensions"] = out
	patched, _ := json.Marshal(m)
	if _, err := DecodeTopologySnapshot(patched); err == nil || !strings.Contains(err.Error(), "missing dimension") {
		t.Fatalf("missing dimension must fail, got %v", err)
	}
}

func TestStaleDimensionDecodes(t *testing.T) {
	data := loadFixture(t, "topology_snapshot.json")
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	dims := map[string]json.RawMessage{}
	if err := json.Unmarshal(m["dimensions"], &dims); err != nil {
		t.Fatal(err)
	}
	dim := map[string]json.RawMessage{}
	if err := json.Unmarshal(dims[DimDesktopAggregate], &dim); err != nil {
		t.Fatal(err)
	}
	dim["stale"] = json.RawMessage(`true`)
	dim["enum"] = json.RawMessage(`"unknown"`)
	out, _ := json.Marshal(dim)
	dims[DimDesktopAggregate] = out
	out2, _ := json.Marshal(dims)
	m["dimensions"] = out2
	patched, _ := json.Marshal(m)
	s, err := DecodeTopologySnapshot(patched)
	if err != nil {
		t.Fatalf("stale dimension must decode: %v", err)
	}
	if !s.Dimensions[DimDesktopAggregate].Stale || s.Dimensions[DimDesktopAggregate].Enum != string(AggregateUnknown) {
		t.Fatalf("stale dim not preserved: %+v", s.Dimensions[DimDesktopAggregate])
	}
}

func TestAllDimensionKeysValidEnums(t *testing.T) {
	// 每个维度白名单非空，且含 §4.4 各自的证据不足态（unresolved 或 unknown
	// —— desktopAggregate/versionCompatibility 用 unknown，其余用 unresolved）。
	for _, k := range DimensionKeys() {
		if len(topologyDimEnums[k]) == 0 {
			t.Errorf("dimension %q has empty enum whitelist", k)
		}
		if !topologyDimEnums[k]["unresolved"] && !topologyDimEnums[k][string(AggregateUnknown)] {
			t.Errorf("dimension %q missing insufficient-evidence enum", k)
		}
	}
}
