package gobridge

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RelayUpgradeProvision 描述 relay 为一个已信任设备发放的非业务凭据。
type RelayUpgradeProvision struct {
	Endpoint   string `json:"relayEndpoint"`
	RouteID    string `json:"routeId"`
	DeviceAuth string `json:"deviceAuth"`
}

// RelayUpgradeProvisioner 只负责向 opaque relay 注册设备并返回其 endpoint credential。
// device token 与 identity key 永远不交给 relay。
type RelayUpgradeProvisioner func(deviceID string) (RelayUpgradeProvision, error)

type RelayUpgradeResponse struct {
	RelayEndpoint           string `json:"relayEndpoint"`
	RouteID                 string `json:"routeId"`
	DeviceAuth              string `json:"deviceAuth"`
	BridgeIdentityPublicKey string `json:"bridgeIdentityPublicKey"`
	BridgeFingerprint       string `json:"bridgeFingerprint"`
	ChannelGeneration       uint64 `json:"channelGeneration"`
}

// ConfigureRelayUpgrade 安装可信升级依赖。没有真实 provisioner 时 RPC 会明确失败。
func (h *Handlers) ConfigureRelayUpgrade(
	store TrustedDeviceStore,
	identity *RelayCryptoIdentity,
	provisioner RelayUpgradeProvisioner,
) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trustedDevices = store
	h.relayIdentity = identity
	h.relayUpgradeProvisioner = provisioner
	h.deliveryPrekeys.SetIdentityAuthKeyFactory(func(deviceID string) ([]byte, error) {
		if store == nil || identity == nil || h.bridgeID == "" {
			return nil, fmt.Errorf("relay identity is not configured")
		}
		record, err := store.LookupByDeviceID(deviceID)
		if err != nil {
			return nil, err
		}
		if record == nil || record.RevokedAt != nil || record.IdentityPublicKey == "" {
			return nil, fmt.Errorf("relay identity not bound for device %s", safeID(deviceID))
		}
		publicKey, err := base64.StdEncoding.DecodeString(record.IdentityPublicKey)
		if err != nil {
			return nil, fmt.Errorf("decode relay identity public key: %w", err)
		}
		return identity.DeriveIdentityAuthKey(publicKey, h.bridgeID, deviceID)
	})
}

// handleRelayUpgradeRPC 处理已有受信 direct channel 上的 relay 身份升级。
func (h *Handlers) handleRelayUpgradeRPC(conn Connection, msg WireMessage) bool {
	if msg.Method != "enable_relay_pairing" {
		return false
	}
	// 将 identity 绑定与 credential 发放作为一次操作，避免并发重绑泄露有效 credential。
	h.relayUpgradeMu.Lock()
	defer h.relayUpgradeMu.Unlock()

	device := conn.AuthedDevice()
	if device == nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "auth.required", Message: "relay upgrade requires an authenticated device"})
		return true
	}

	h.mu.Lock()
	store := h.trustedDevices
	identity := h.relayIdentity
	provisioner := h.relayUpgradeProvisioner
	bridgeID := h.bridgeID
	enabled := h.relayEnabled
	h.mu.Unlock()
	if store == nil || identity == nil || provisioner == nil || bridgeID == "" || !enabled {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.not_configured", Message: "encrypted relay is not configured or disabled"})
		return true
	}

	var params struct {
		IdentityPublicKey string `json:"identityPublicKey"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "invalid relay identity binding"})
		return true
	}
	publicKey, err := base64.StdEncoding.DecodeString(params.IdentityPublicKey)
	if err != nil || len(publicKey) != 32 {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.invalid_identity_key", Message: "invalid relay identity public key"})
		return true
	}
	if _, err := ecdh.X25519().NewPublicKey(publicKey); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.invalid_identity_key", Message: "invalid relay identity public key"})
		return true
	}
	if _, err := identity.DeriveIdentityAuthKey(publicKey, bridgeID, device.DeviceID); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.invalid_identity_key", Message: "invalid relay identity public key"})
		return true
	}

	const generation uint64 = 1
	existing, err := store.LookupByDeviceID(device.DeviceID)
	if err != nil || existing == nil || existing.RevokedAt != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.identity_binding_failed", Message: "authenticated device binding is unavailable"})
		return true
	}
	if existing.IdentityPublicKey != "" && existing.IdentityPublicKey != params.IdentityPublicKey {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.identity_binding_failed", Message: "device identity public key is already bound"})
		return true
	}
	provision, err := provisioner(device.DeviceID)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.provision_failed", Message: err.Error()})
		return true
	}
	if provision.Endpoint == "" || provision.RouteID == "" || provision.DeviceAuth == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.provision_failed", Message: "relay returned incomplete device provision"})
		return true
	}
	if err := store.EnableRelay(device.DeviceID, params.IdentityPublicKey, generation); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "relay.identity_binding_failed", Message: err.Error()})
		return true
	}

	conn.SendResult(msg.RequestID, RelayUpgradeResponse{
		RelayEndpoint:           provision.Endpoint,
		RouteID:                 provision.RouteID,
		DeviceAuth:              provision.DeviceAuth,
		BridgeIdentityPublicKey: base64.StdEncoding.EncodeToString(identity.PublicKeyBytes()),
		BridgeFingerprint:       identity.Fingerprint(),
		ChannelGeneration:       generation,
	}, nil)
	return true
}

func relayUpgradeProvisionerForHub(endpoint, routeID, bridgeAuth string, hub *RelayHub) RelayUpgradeProvisioner {
	return func(deviceID string) (RelayUpgradeProvision, error) {
		if endpoint == "" || routeID == "" || bridgeAuth == "" || hub == nil {
			return RelayUpgradeProvision{}, fmt.Errorf("relay route is not configured")
		}
		if !hub.AuthorizeBridge(routeID, bridgeAuth) {
			return RelayUpgradeProvision{}, fmt.Errorf("relay bridge credential rejected")
		}
		deviceAuth, err := hub.RegisterDevice(routeID, deviceID)
		if err != nil {
			return RelayUpgradeProvision{}, err
		}
		return RelayUpgradeProvision{Endpoint: endpoint, RouteID: routeID, DeviceAuth: deviceAuth}, nil
	}
}

func relayUpgradeProvisioner(endpoint, routeID, bridgeAuth string, hub *RelayHub) RelayUpgradeProvisioner {
	if hub != nil {
		return relayUpgradeProvisionerForHub(endpoint, routeID, bridgeAuth, hub)
	}
	return func(deviceID string) (RelayUpgradeProvision, error) {
		if endpoint == "" || routeID == "" || bridgeAuth == "" {
			return RelayUpgradeProvision{}, fmt.Errorf("relay route is not configured")
		}
		serviceEndpoint, err := relayHTTPEndpoint(endpoint)
		if err != nil {
			return RelayUpgradeProvision{}, err
		}
		body, _ := json.Marshal(map[string]string{"deviceId": deviceID})
		request, err := http.NewRequest(
			http.MethodPost,
			serviceEndpoint+"/v1/routes/"+url.PathEscape(routeID)+"/devices/register",
			bytes.NewReader(body),
		)
		if err != nil {
			return RelayUpgradeProvision{}, err
		}
		request.Header.Set("Authorization", "Bearer "+bridgeAuth)
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			return RelayUpgradeProvision{}, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			return RelayUpgradeProvision{}, fmt.Errorf("relay device registration failed: status %d", response.StatusCode)
		}
		var registered struct {
			DeviceAuth string `json:"deviceAuth"`
		}
		if err := json.NewDecoder(response.Body).Decode(&registered); err != nil || registered.DeviceAuth == "" {
			return RelayUpgradeProvision{}, fmt.Errorf("relay device registration returned incomplete credential")
		}
		return RelayUpgradeProvision{Endpoint: endpoint, RouteID: routeID, DeviceAuth: registered.DeviceAuth}, nil
	}
}

func relayHTTPEndpoint(endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid relay endpoint")
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("invalid relay endpoint scheme")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
