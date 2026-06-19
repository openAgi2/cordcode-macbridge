package gobridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RelayFirstResult 是 Mac 批准后只交给该 iPhone 的密文内容。
type RelayFirstResult struct {
	DeviceID                string `json:"deviceId"`
	DeviceToken             string `json:"deviceToken"`
	BridgeID                string `json:"bridgeId"`
	DisplayName             string `json:"displayName"`
	RelayEndpoint           string `json:"relayEndpoint"`
	RouteID                 string `json:"routeId"`
	DeviceAuth              string `json:"deviceAuth"`
	BridgeIdentityPublicKey string `json:"bridgeIdentityPublicKey"`
	BridgeFingerprint       string `json:"bridgeFingerprint"`
	ChannelGeneration       uint64 `json:"channelGeneration"`
}

type relayPendingClaim struct {
	ClaimID     string `json:"claimId"`
	SealedClaim []byte `json:"sealedClaim"`
	State       string `json:"state"`
}

func addRelayFirstPairingPayload(session *PairingSession, endpoint, routeID string, identity *RelayCryptoIdentity) error {
	qr, err := GeneratePairingQR(routeID, identity.PublicKeyBytes(), endpoint)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(session.QRPayload)
	if err != nil {
		return fmt.Errorf("parse direct pairing QR: %w", err)
	}
	query := parsed.Query()
	query.Set("relay", qr.RelayEndpoint)
	query.Set("relayRoute", qr.RouteID)
	query.Set("relayBridgeKey", qr.BridgePubKey)
	query.Set("relayFingerprint", qr.BridgeFP)
	query.Set("relayCapability", "paircap_"+generateRandomString(32))
	parsed.RawQuery = query.Encode()
	session.QRPayload = parsed.String()
	return nil
}

func (s *ManagementServer) syncRelayPairingClaim(ctx context.Context, session *PairingSession) error {
	if !s.cfg.RelayEnabled {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.relayURL("/v1/routes/"+sessionSafePath(s.cfg.RelayRouteID)+"/pairing-claims"), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.cfg.RelayCredential)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("list relay claims: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("list relay claims: HTTP %d", response.StatusCode)
	}
	var payload struct {
		Claims []relayPendingClaim `json:"claims"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode relay claims: %w", err)
	}
	for _, pending := range payload.Claims {
		if pending.ClaimID != session.ID || pending.State != "pending" {
			continue
		}
		var claim PairingClaim
		if err := json.Unmarshal(pending.SealedClaim, &claim); err != nil {
			return fmt.Errorf("decode relay claim: %w", err)
		}
		approved, err := ProcessPairingClaim(&claim, s.cfg.RelayIdentity.PrivateKeyBytes())
		if err != nil {
			return fmt.Errorf("verify relay claim: %w", err)
		}
		if approved == nil {
			return fmt.Errorf("verify relay claim: invalid HPKE claim")
		}
		if !approved.Approved {
			return fmt.Errorf("verify relay claim: %s", approved.Reason)
		}
		if err := session.Claim(claim.DeviceID, claim.DeviceName, "ios"); err != nil {
			return err
		}
		session.RelayClaim = &claim
		return s.cfg.PairingStore.Update(session)
	}
	return nil
}

func (s *ManagementServer) approveRelayPairing(ctx context.Context, session *PairingSession, deviceToken string) (*RelayFirstResult, error) {
	if !s.cfg.RelayEnabled {
		return nil, fmt.Errorf("relay is disabled")
	}
	if session.RelayClaim == nil {
		return nil, fmt.Errorf("missing relay claim")
	}
	deviceAuth, err := s.registerRelayDevice(ctx, session.ClaimingDeviceID)
	if err != nil {
		return nil, err
	}
	s.dnMu.RLock()
	displayName := s.cfg.DisplayName
	s.dnMu.RUnlock()
	result := &RelayFirstResult{
		DeviceID:                session.ClaimingDeviceID,
		DeviceToken:             deviceToken,
		BridgeID:                s.cfg.BridgeID,
		DisplayName:             displayName,
		RelayEndpoint:           s.cfg.RelayEndpoint,
		RouteID:                 s.cfg.RelayRouteID,
		DeviceAuth:              deviceAuth,
		BridgeIdentityPublicKey: base64.StdEncoding.EncodeToString(s.cfg.RelayIdentity.PublicKeyBytes()),
		BridgeFingerprint:       s.cfg.RelayIdentity.Fingerprint(),
		ChannelGeneration:       1,
	}
	plaintext, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	devicePublicKey, err := base64.StdEncoding.DecodeString(session.RelayClaim.DevicePubKey)
	if err != nil {
		return nil, fmt.Errorf("decode device identity public key: %w", err)
	}
	ciphertext, _, err := HPKESeal(
		devicePublicKey,
		[]byte(pairingContextLabel),
		[]byte("pairing-result:"+session.ClaimingDeviceID),
		plaintext,
	)
	if err != nil {
		return nil, fmt.Errorf("encrypt pairing result: %w", err)
	}
	sealedResult, err := json.Marshal(ciphertext)
	if err != nil {
		return nil, err
	}
	if err := s.completeRelayClaim(ctx, session.ID, "approved", sealedResult); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ManagementServer) rejectRelayPairing(ctx context.Context, session *PairingSession) error {
	return s.completeRelayClaim(ctx, session.ID, "rejected", nil)
}

func (s *ManagementServer) registerRelayDevice(ctx context.Context, deviceID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"deviceId": deviceID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.relayURL("/v1/routes/"+sessionSafePath(s.cfg.RelayRouteID)+"/devices/register"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+s.cfg.RelayCredential)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("register relay device: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("register relay device: HTTP %d", response.StatusCode)
	}
	var provision struct {
		DeviceAuth string `json:"deviceAuth"`
	}
	if err := json.NewDecoder(response.Body).Decode(&provision); err != nil || provision.DeviceAuth == "" {
		return "", fmt.Errorf("register relay device: incomplete response")
	}
	return provision.DeviceAuth, nil
}

func (s *ManagementServer) completeRelayClaim(ctx context.Context, claimID, state string, sealedResult []byte) error {
	body, _ := json.Marshal(map[string]any{"state": state, "sealedResult": sealedResult})
	path := "/v1/routes/" + sessionSafePath(s.cfg.RelayRouteID) + "/pairing-claims/" + sessionSafePath(claimID) + "/complete"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.relayURL(path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.cfg.RelayCredential)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("complete relay claim: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("complete relay claim: HTTP %d", response.StatusCode)
	}
	return nil
}

func (s *ManagementServer) relayURL(path string) string {
	endpoint := strings.TrimRight(s.cfg.RelayEndpoint, "/")
	endpoint = strings.Replace(endpoint, "wss://", "https://", 1)
	endpoint = strings.Replace(endpoint, "ws://", "http://", 1)
	return endpoint + path
}

func sessionSafePath(value string) string {
	return url.PathEscape(value)
}
