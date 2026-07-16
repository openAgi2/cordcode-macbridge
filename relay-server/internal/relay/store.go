package relay

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type MailboxFrame struct {
	Cursor    uint64 `json:"cursor"`
	Envelope  []byte `json:"envelope"`
	ExpiresAt int64  `json:"expiresAt"`
}

type RouteStatus struct {
	DeviceCount        int   `json:"deviceCount"`
	PendingFrameCount  int   `json:"pendingFrameCount"`
	PendingMailboxSize int64 `json:"pendingMailboxBytes"`
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open relay database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS routes (
	route_id TEXT PRIMARY KEY,
	bridge_auth_hash BLOB NOT NULL,
	created_at INTEGER NOT NULL,
	revoked_at INTEGER,
	last_bridge_seen_at INTEGER
);
CREATE TABLE IF NOT EXISTS route_activations (
	install_id TEXT PRIMARY KEY,
	signing_public_key BLOB NOT NULL UNIQUE,
	route_id TEXT NOT NULL UNIQUE,
	activated_at INTEGER NOT NULL,
	FOREIGN KEY (route_id) REFERENCES routes(route_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS route_devices (
	route_id TEXT NOT NULL,
	device_id TEXT NOT NULL,
	device_auth_hash BLOB NOT NULL,
	created_at INTEGER NOT NULL,
	revoked_at INTEGER,
	last_seen_at INTEGER,
	next_cursor INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY (route_id, device_id),
	FOREIGN KEY (route_id) REFERENCES routes(route_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS mailbox_frames (
	route_id TEXT NOT NULL,
	device_id TEXT NOT NULL,
	cursor INTEGER NOT NULL,
	envelope BLOB NOT NULL,
	envelope_size INTEGER NOT NULL,
	inserted_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	acked_at INTEGER,
	PRIMARY KEY (route_id, device_id, cursor),
	FOREIGN KEY (route_id, device_id) REFERENCES route_devices(route_id, device_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_mailbox_pending
	ON mailbox_frames(route_id, device_id, cursor)
	WHERE acked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mailbox_expiry
	ON mailbox_frames(expires_at)
	WHERE acked_at IS NULL;
CREATE TABLE IF NOT EXISTS pending_pairing_claims (
	route_id TEXT NOT NULL,
	claim_id TEXT NOT NULL,
	capability_hash BLOB NOT NULL,
	sealed_claim BLOB,
	sealed_result BLOB,
	state TEXT NOT NULL DEFAULT 'pending',
	expires_at INTEGER NOT NULL,
	consumed_at INTEGER,
	PRIMARY KEY (route_id, claim_id),
	FOREIGN KEY (route_id) REFERENCES routes(route_id) ON DELETE CASCADE
)
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate relay database: %w", err)
	}
	return nil
}

func CredentialDigest(credential string) []byte {
	sum := sha256.Sum256([]byte(credential))
	return sum[:]
}

func VerifyCredential(want []byte, credential string) bool {
	return hmac.Equal(want, CredentialDigest(credential))
}

func NewCredential() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func NewID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value), nil
}

func (s *Store) CreateRoute(ctx context.Context, now time.Time) (string, string, error) {
	routeID, err := NewID("rt")
	if err != nil {
		return "", "", err
	}
	auth, err := NewCredential()
	if err != nil {
		return "", "", err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO routes(route_id, bridge_auth_hash, created_at) VALUES (?, ?, ?)`,
		routeID, CredentialDigest(auth), now.Unix(),
	)
	if err != nil {
		return "", "", fmt.Errorf("insert route: %w", err)
	}
	return routeID, auth, nil
}

func (s *Store) ActivateRoute(ctx context.Context, installID string, publicKey []byte, bridgeAuth string, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var routeID string
	var savedKey []byte
	err = tx.QueryRowContext(ctx,
		`SELECT route_id, signing_public_key FROM route_activations WHERE install_id = ?`,
		installID,
	).Scan(&routeID, &savedKey)
	if err == nil {
		if !hmac.Equal(savedKey, publicKey) {
			return "", fmt.Errorf("activation identity mismatch")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE routes SET bridge_auth_hash = ? WHERE route_id = ? AND revoked_at IS NULL`,
			CredentialDigest(bridgeAuth), routeID); err != nil {
			return "", fmt.Errorf("rotate activated route credential: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return routeID, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("read activation: %w", err)
	}

	routeID, err = NewID("rt")
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO routes(route_id, bridge_auth_hash, created_at) VALUES (?, ?, ?)`,
		routeID, CredentialDigest(bridgeAuth), now.Unix()); err != nil {
		return "", fmt.Errorf("insert activated route: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO route_activations(install_id, signing_public_key, route_id, activated_at) VALUES (?, ?, ?, ?)`,
		installID, publicKey, routeID, now.Unix()); err != nil {
		return "", fmt.Errorf("bind activation identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return routeID, nil
}

func (s *Store) AuthenticateBridge(ctx context.Context, routeID, credential string, now time.Time) bool {
	var digest []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT bridge_auth_hash FROM routes WHERE route_id = ? AND revoked_at IS NULL`, routeID,
	).Scan(&digest); err != nil || !VerifyCredential(digest, credential) {
		return false
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE routes SET last_bridge_seen_at = ? WHERE route_id = ?`, now.Unix(), routeID)
	return true
}

func (s *Store) RegisterDevice(ctx context.Context, routeID, deviceID string, now time.Time) (string, error) {
	auth, err := NewCredential()
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO route_devices(route_id, device_id, device_auth_hash, created_at)
SELECT route_id, ?, ?, ? FROM routes WHERE route_id = ? AND revoked_at IS NULL
ON CONFLICT(route_id, device_id) DO UPDATE SET
	device_auth_hash = excluded.device_auth_hash,
	revoked_at = NULL,
	created_at = excluded.created_at`, deviceID, CredentialDigest(auth), now.Unix(), routeID)
	if err != nil {
		return "", fmt.Errorf("insert device: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return "", fmt.Errorf("route not found")
	}
	return auth, nil
}

func (s *Store) AuthenticateDevice(ctx context.Context, routeID, deviceID, credential string, now time.Time) bool {
	var digest []byte
	if err := s.db.QueryRowContext(ctx, `
SELECT d.device_auth_hash
FROM route_devices d JOIN routes r ON r.route_id = d.route_id
WHERE d.route_id = ? AND d.device_id = ? AND d.revoked_at IS NULL AND r.revoked_at IS NULL`,
		routeID, deviceID).Scan(&digest); err != nil || !VerifyCredential(digest, credential) {
		return false
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE route_devices SET last_seen_at = ? WHERE route_id = ? AND device_id = ?`,
		now.Unix(), routeID, deviceID)
	return true
}

func (s *Store) DeviceActive(ctx context.Context, routeID, deviceID string) bool {
	var exists int
	err := s.db.QueryRowContext(ctx, `
SELECT 1 FROM route_devices d JOIN routes r ON r.route_id = d.route_id
WHERE d.route_id = ? AND d.device_id = ? AND d.revoked_at IS NULL AND r.revoked_at IS NULL`,
		routeID, deviceID).Scan(&exists)
	return err == nil
}

func (s *Store) RevokeDevice(ctx context.Context, routeID, deviceID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE route_devices SET revoked_at = ? WHERE route_id = ? AND device_id = ? AND revoked_at IS NULL`,
		now.Unix(), routeID, deviceID)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("active device not found")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM mailbox_frames WHERE route_id = ? AND device_id = ?`, routeID, deviceID); err != nil {
		return fmt.Errorf("clear revoked mailbox: %w", err)
	}
	return tx.Commit()
}

func (s *Store) AppendFrame(ctx context.Context, routeID, deviceID string, envelope []byte, now time.Time, ttl time.Duration, maxBytes int64) (uint64, int, error) {
	if int64(len(envelope)) > maxBytes {
		return 0, 0, fmt.Errorf("frame exceeds mailbox capacity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var cursor uint64
	if err := tx.QueryRowContext(ctx, `
SELECT d.next_cursor
FROM route_devices d JOIN routes r ON r.route_id = d.route_id
WHERE d.route_id = ? AND d.device_id = ? AND d.revoked_at IS NULL AND r.revoked_at IS NULL`,
		routeID, deviceID).Scan(&cursor); err != nil {
		return 0, 0, fmt.Errorf("inactive destination: %w", err)
	}
	_, _ = tx.ExecContext(ctx,
		`DELETE FROM mailbox_frames WHERE route_id = ? AND device_id = ? AND (expires_at <= ? OR acked_at IS NOT NULL)`,
		routeID, deviceID, now.Unix())
	var current int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(envelope_size), 0) FROM mailbox_frames WHERE route_id = ? AND device_id = ? AND acked_at IS NULL`,
		routeID, deviceID).Scan(&current); err != nil {
		return 0, 0, err
	}
	evicted := 0
	for current+int64(len(envelope)) > maxBytes {
		var oldCursor uint64
		var oldSize int64
		if err := tx.QueryRowContext(ctx, `
SELECT cursor, envelope_size FROM mailbox_frames
WHERE route_id = ? AND device_id = ? AND acked_at IS NULL ORDER BY cursor LIMIT 1`,
			routeID, deviceID).Scan(&oldCursor, &oldSize); err != nil {
			return 0, 0, fmt.Errorf("evict mailbox frame: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM mailbox_frames WHERE route_id = ? AND device_id = ? AND cursor = ?`,
			routeID, deviceID, oldCursor); err != nil {
			return 0, 0, err
		}
		current -= oldSize
		evicted++
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mailbox_frames(route_id, device_id, cursor, envelope, envelope_size, inserted_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, routeID, deviceID, cursor, envelope, len(envelope), now.Unix(), now.Add(ttl).Unix()); err != nil {
		return 0, 0, fmt.Errorf("append mailbox frame: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE route_devices SET next_cursor = next_cursor + 1 WHERE route_id = ? AND device_id = ?`,
		routeID, deviceID); err != nil {
		return 0, 0, err
	}
	return cursor, evicted, tx.Commit()
}

func (s *Store) FetchFrames(ctx context.Context, routeID, deviceID string, after uint64, limit int, now time.Time) ([]MailboxFrame, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mailbox_frames WHERE route_id = ? AND device_id = ? AND expires_at <= ?`,
		routeID, deviceID, now.Unix()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT cursor, envelope, expires_at FROM mailbox_frames
WHERE route_id = ? AND device_id = ? AND cursor > ? AND acked_at IS NULL AND expires_at > ?
ORDER BY cursor LIMIT ?`, routeID, deviceID, after, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// The mailbox HTTP contract represents an empty result as `frames: []`, never
	// JSON null. Browser clients must be able to distinguish a real empty mailbox
	// from a malformed response without relying on language-specific nil handling.
	frames := make([]MailboxFrame, 0)
	for rows.Next() {
		var frame MailboxFrame
		if err := rows.Scan(&frame.Cursor, &frame.Envelope, &frame.ExpiresAt); err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, rows.Err()
}

func (s *Store) AckFrames(ctx context.Context, routeID, deviceID string, through uint64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE mailbox_frames SET acked_at = ?
WHERE route_id = ? AND device_id = ? AND cursor <= ? AND acked_at IS NULL`, now.Unix(), routeID, deviceID, through)
	return err
}

func (s *Store) ExpireFrames(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM mailbox_frames WHERE expires_at <= ? OR acked_at IS NOT NULL`, now.Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) Status(ctx context.Context, routeID string, now time.Time) (RouteStatus, error) {
	var status RouteStatus
	err := s.db.QueryRowContext(ctx, `
SELECT
	(SELECT COUNT(*) FROM route_devices WHERE route_id = ? AND revoked_at IS NULL),
	COUNT(f.cursor),
	COALESCE(SUM(f.envelope_size), 0)
FROM routes r LEFT JOIN mailbox_frames f
	ON f.route_id = r.route_id AND f.acked_at IS NULL AND f.expires_at > ?
WHERE r.route_id = ? AND r.revoked_at IS NULL`,
		routeID, now.Unix(), routeID).Scan(&status.DeviceCount, &status.PendingFrameCount, &status.PendingMailboxSize)
	return status, err
}

// MARK: - Pairing Claims

type PairingClaim struct {
	ClaimID      string `json:"claimId"`
	SealedClaim  []byte `json:"sealedClaim"`
	SealedResult []byte `json:"sealedResult,omitempty"`
	State        string `json:"state"`
}

// ErrPairingClaimConflict 表示 (route_id, claim_id) 已存在且与本次提交不同（P2-9）。
// 相同请求（capability_hash + sealed_claim 一致）视为幂等成功，返回 nil。
var ErrPairingClaimConflict = fmt.Errorf("pairing claim conflict")

func (s *Store) SubmitPairingClaim(ctx context.Context, routeID, claimID string, capabilityHash []byte, sealedClaim []byte, now time.Time, ttl time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pairing claim: %w", err)
	}
	defer tx.Rollback()

	// 清理同 route 的过期 claim（与 PendingClaims 一致）。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pending_pairing_claims WHERE route_id = ? AND expires_at <= ?`, routeID, now.Unix()); err != nil {
		return fmt.Errorf("expire pairing claims: %w", err)
	}

	var existingCap, existingSealed []byte
	err = tx.QueryRowContext(ctx,
		`SELECT capability_hash, sealed_claim FROM pending_pairing_claims WHERE route_id = ? AND claim_id = ?`,
		routeID, claimID).Scan(&existingCap, &existingSealed)
	if err == nil {
		// 已存在同 (route, claim)：相同请求幂等成功，不同请求返回冲突（P2-9）。
		if hmac.Equal(existingCap, capabilityHash) && hmac.Equal(existingSealed, sealedClaim) {
			return tx.Commit()
		}
		return ErrPairingClaimConflict
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read existing pairing claim: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pending_pairing_claims(route_id, claim_id, capability_hash, sealed_claim, state, expires_at) SELECT route_id, ?, ?, ?, 'pending', ? FROM routes WHERE route_id = ? AND revoked_at IS NULL`,
		claimID, capabilityHash, sealedClaim, now.Add(ttl).Unix(), routeID); err != nil {
		return fmt.Errorf("insert pairing claim: %w", err)
	}
	return tx.Commit()
}

func (s *Store) VerifyPairingCapability(ctx context.Context, routeID, claimID string, capability string) bool {
	var hash []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT capability_hash FROM pending_pairing_claims WHERE route_id = ? AND claim_id = ? AND state IN ('pending', 'approved', 'rejected', 'consumed')`,
		routeID, claimID).Scan(&hash)
	if err != nil {
		return false
	}
	return VerifyCredential(hash, capability)
}

func (s *Store) PendingClaims(ctx context.Context, routeID string, now time.Time) ([]PairingClaim, error) {
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM pending_pairing_claims WHERE route_id = ? AND expires_at <= ?`, routeID, now.Unix())
	rows, err := s.db.QueryContext(ctx, `
	SELECT claim_id, sealed_claim, sealed_result, state
	FROM pending_pairing_claims
	WHERE route_id = ? AND expires_at > ? AND state != 'consumed'
	ORDER BY claim_id`, routeID, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var claims []PairingClaim
	for rows.Next() {
		var c PairingClaim
		var sealedResult []byte
		if err := rows.Scan(&c.ClaimID, &c.SealedClaim, &sealedResult, &c.State); err != nil {
			return nil, err
		}
		if sealedResult != nil {
			c.SealedResult = sealedResult
		}
		claims = append(claims, c)
	}
	return claims, rows.Err()
}

func (s *Store) CompletePairingClaim(ctx context.Context, routeID, claimID, newState string, sealedResult []byte, now time.Time) error {
	if newState != "approved" && newState != "rejected" {
		return fmt.Errorf("invalid pairing claim state: %s", newState)
	}
	result, err := s.db.ExecContext(ctx, `
	UPDATE pending_pairing_claims SET sealed_result = ?, state = ?
	WHERE route_id = ? AND claim_id = ? AND state = 'pending'`,
		sealedResult, newState, routeID, claimID)
	if err != nil {
		return fmt.Errorf("complete pairing claim: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("pending claim not found")
	}
	return nil
}

func (s *Store) GetPairingResult(ctx context.Context, routeID, claimID string, now time.Time) (*PairingClaim, error) {
	var c PairingClaim
	var sealedResult []byte
	err := s.db.QueryRowContext(ctx, `
	SELECT claim_id, state, sealed_result
	FROM pending_pairing_claims
	WHERE route_id = ? AND claim_id = ? AND expires_at > ? AND consumed_at IS NULL`,
		routeID, claimID, now.Unix()).Scan(&c.ClaimID, &c.State, &sealedResult)
	if err != nil {
		return nil, err
	}
	if sealedResult != nil {
		c.SealedResult = sealedResult
	}
	return &c, nil
}

func (s *Store) ConsumePairingResult(ctx context.Context, routeID, claimID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
	UPDATE pending_pairing_claims SET consumed_at = ?, state = 'consumed'
	WHERE route_id = ? AND claim_id = ? AND state IN ('approved', 'rejected') AND consumed_at IS NULL`,
		now.Unix(), routeID, claimID)
	return err
}
