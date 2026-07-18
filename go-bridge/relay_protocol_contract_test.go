package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type relayProtocolContract struct {
	SchemaRevision string `json:"schemaRevision"`
	Protocol       struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"protocol"`
	InnerProtocol struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"innerProtocol"`
	Crypto struct {
		KeyAgreement       string `json:"keyAgreement"`
		KDF                string `json:"kdf"`
		AEAD               string `json:"aead"`
		HPKEMode           string `json:"hpkeMode"`
		NoncePrefixHex     string `json:"noncePrefixHex"`
		CounterBytes       int    `json:"counterBytes"`
		CounterByteOrder   string `json:"counterByteOrder"`
		PaddingBucketBytes int    `json:"paddingBucketBytes"`
	} `json:"crypto"`
	EnvelopeFields            []string `json:"envelopeFields"`
	AADFields                 []string `json:"aadFields"`
	MailboxOnlyEnvelopeFields []string `json:"mailboxOnlyEnvelopeFields"`
	OptionalEnvelopeFields    []string `json:"optionalEnvelopeFields"`
	Mailbox                   struct {
		Direction            string   `json:"direction"`
		LowWatermark         int      `json:"lowWatermark"`
		TargetCount          int      `json:"targetCount"`
		MaxCount             int      `json:"maxCount"`
		DurableEvents        []string `json:"durableEvents"`
		ExcludedEventClasses []string `json:"excludedEventClasses"`
	} `json:"mailbox"`
	Observation struct {
		Modes                  []string `json:"modes"`
		ForegroundLeaseSeconds int      `json:"foregroundLeaseSeconds"`
	} `json:"observation"`
	Errors []string `json:"errors"`
}

func loadRelayProtocolContract(t *testing.T) relayProtocolContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "relay-v1", "protocol_contract.json"))
	if err != nil {
		t.Fatalf("read relay protocol contract: %v", err)
	}
	var contract relayProtocolContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("decode relay protocol contract: %v", err)
	}
	return contract
}

func TestRelayProtocolContractFreezesCipherSuiteAndNonce(t *testing.T) {
	contract := loadRelayProtocolContract(t)

	if contract.SchemaRevision != "2026-05-24-r1" ||
		contract.Protocol.Name != "cccode-relay" ||
		contract.Protocol.Version != 1 {
		t.Fatalf("unexpected relay contract identity: %+v", contract.Protocol)
	}
	if contract.InnerProtocol.Name != BridgeProtocolName ||
		contract.InnerProtocol.Version != BridgeProtocolVersion {
		t.Fatalf("inner contract does not preserve Bridge v1: %+v", contract.InnerProtocol)
	}

	gotSuite := []string{
		contract.Crypto.KeyAgreement,
		contract.Crypto.KDF,
		contract.Crypto.AEAD,
		contract.Crypto.HPKEMode,
	}
	wantSuite := []string{
		"X25519",
		"HKDF-SHA256",
		"ChaCha20-Poly1305",
		"RFC9180-Base-X25519-HKDF-SHA256-ChaCha20Poly1305",
	}
	if !reflect.DeepEqual(gotSuite, wantSuite) {
		t.Fatalf("cipher suite drift: got %v want %v", gotSuite, wantSuite)
	}
	if contract.Crypto.NoncePrefixHex != "00000000" ||
		contract.Crypto.CounterBytes != 8 ||
		contract.Crypto.CounterByteOrder != "big-endian" ||
		contract.Crypto.PaddingBucketBytes != 256 {
		t.Fatalf("nonce or padding contract drift: %+v", contract.Crypto)
	}
}

func TestRelayProtocolContractAuthenticatesAllReadableEnvelopeMetadata(t *testing.T) {
	contract := loadRelayProtocolContract(t)

	wantEnvelope := []string{
		"version", "routeId", "senderId", "destinationId", "channelGeneration",
		"keyEpochId", "prekeyId", "epochIndex", "epochEphemeralPublicKey",
		"previousEpochDigest", "epochAuthTag", "messageId", "counter",
		"contentEncoding", "chunk", "ciphertext", "createdAt", "expiresAt",
	}
	wantAAD := []string{
		"version", "routeId", "senderId", "destinationId", "channelGeneration",
		"keyEpochId", "prekeyId", "epochIndex", "epochEphemeralPublicKey",
		"previousEpochDigest", "epochAuthTag", "messageId", "counter",
		"contentEncoding", "chunk", "createdAt", "expiresAt",
	}
	wantMailboxOnly := []string{
		"prekeyId", "epochIndex", "epochEphemeralPublicKey",
		"previousEpochDigest", "epochAuthTag",
	}
	wantOptional := []string{"contentEncoding", "chunk"}

	if !reflect.DeepEqual(contract.EnvelopeFields, wantEnvelope) {
		t.Fatalf("envelope field contract drift: got %v want %v", contract.EnvelopeFields, wantEnvelope)
	}
	if !reflect.DeepEqual(contract.AADFields, wantAAD) {
		t.Fatalf("AAD field contract drift: got %v want %v", contract.AADFields, wantAAD)
	}
	if !reflect.DeepEqual(contract.MailboxOnlyEnvelopeFields, wantMailboxOnly) {
		t.Fatalf("mailbox envelope contract drift: got %v want %v", contract.MailboxOnlyEnvelopeFields, wantMailboxOnly)
	}
	if !reflect.DeepEqual(contract.OptionalEnvelopeFields, wantOptional) {
		t.Fatalf("optional envelope contract drift: got %v want %v", contract.OptionalEnvelopeFields, wantOptional)
	}
}

func TestRelayProtocolContractKeepsMailboxBoundedAndFailClosed(t *testing.T) {
	contract := loadRelayProtocolContract(t)

	if contract.Mailbox.Direction != "mac_to_ios_only" ||
		contract.Mailbox.LowWatermark != 10 ||
		contract.Mailbox.TargetCount != 32 ||
		contract.Mailbox.MaxCount != 64 {
		t.Fatalf("mailbox/prekey limits drift: %+v", contract.Mailbox)
	}
	wantMilestones := []string{
		"turn_completed", "turn_error", "todos_updated",
		"session_running_signal", "delivery_reconcile_required",
	}
	if !reflect.DeepEqual(contract.Mailbox.DurableEvents, wantMilestones) {
		t.Fatalf("durable event allowlist drift: got %v want %v", contract.Mailbox.DurableEvents, wantMilestones)
	}
	wantModes := []string{"full_stream", "milestones_only"}
	if !reflect.DeepEqual(contract.Observation.Modes, wantModes) ||
		contract.Observation.ForegroundLeaseSeconds != 45 {
		t.Fatalf("observation scope contract drift: %+v", contract.Observation)
	}

	requiredErrors := map[string]bool{
		"relay.bridge_offline":   false,
		"relay.counter_invalid":  false,
		"relay.chain_mismatch":   false,
		"prekey_limit_exceeded":  false,
		"relay.prekey_exhausted": false,
		"relay.device_revoked":   false,
	}
	for _, code := range contract.Errors {
		if _, exists := requiredErrors[code]; exists {
			requiredErrors[code] = true
		}
	}
	for code, present := range requiredErrors {
		if !present {
			t.Fatalf("missing fail-closed error code %q", code)
		}
	}
}
