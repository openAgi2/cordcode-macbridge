package codexremote

// P1 auth-cache unit tests (docs/2026-10-12-codex-remote-probe-economy-p1-
// plan.md §3 D2 / S-1): the cached auth must eliminate probe spawns while
// valid, re-probe on expiry/explicit invalidation, and never cache failures
// (r1 F-6). Probe spawn counting goes through the existing authTokenFn seam
// (r1 F-4 — no parallel seam); real-spawn counting in loadChatGPTAuth is
// covered separately by the stub-based reap tests.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// cacheTestJWT builds a JWT-shaped token with the account claim (and an
// optional top-level exp) that accountIDFromJWT/authJWTExpiry parse.
func cacheTestJWT(t *testing.T, accountID string, exp time.Time, withExp bool) string {
	t.Helper()
	claims := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	if withExp {
		claims["exp"] = exp.Unix()
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "h." + base64.URLEncoding.EncodeToString(payload) + ".s"
}

// newCacheTestController returns a controller whose auth probe is a counting
// stub serving the given token.
func newCacheTestController(token string, probeErr error) (*PairingController, *int) {
	calls := 0
	return &PairingController{
		authTokenFn: func(context.Context) (string, string, error) {
			calls++
			if probeErr != nil {
				return "", "", probeErr
			}
			id, err := accountIDFromJWT(token)
			if err != nil {
				return "", "", err
			}
			return token, id, nil
		},
	}, &calls
}

func TestAuthJWTExpiryParsesTopLevelClaim(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	got, ok := authJWTExpiry(cacheTestJWT(t, "a1", exp, true))
	if !ok {
		t.Fatal("exp claim not parsed")
	}
	if !got.Equal(exp) {
		t.Fatalf("exp mismatch: %v vs %v", got, exp)
	}
	if _, ok := authJWTExpiry(cacheTestJWT(t, "a1", time.Time{}, false)); ok {
		t.Fatal("token without exp must not parse an expiry")
	}
	if _, ok := authJWTExpiry("not-a-jwt"); ok {
		t.Fatal("non-JWT must not parse an expiry")
	}
}

func TestChatGPTAuthCachedHitSkipsProbe(t *testing.T) {
	token := cacheTestJWT(t, "acct-hit", time.Now().Add(2*time.Hour), true)
	p, calls := newCacheTestController(token, nil)
	ctx := context.Background()

	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatalf("first (miss) call: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("miss must probe once, got %d", *calls)
	}
	gotToken, gotAccount, err := p.chatGPTAuthCached(ctx)
	if err != nil {
		t.Fatalf("second (hit) call: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("hit must not probe, got %d calls", *calls)
	}
	if gotToken != token || gotAccount != "acct-hit" {
		t.Fatalf("hit returned %q/%q", gotToken, gotAccount)
	}
	// Hit writes the token back into pairing state (cold rounds must never
	// see an empty state.token; plan D2).
	p.mu.Lock()
	stateToken := p.state.token
	p.mu.Unlock()
	if stateToken != token {
		t.Fatalf("state.token not written back: %q", stateToken)
	}
}

func TestChatGPTAuthCachedExpiredReprobes(t *testing.T) {
	// exp inside the 60s freshness margin — the cache must not serve it.
	token := cacheTestJWT(t, "acct-exp", time.Now().Add(30*time.Second), true)
	p, calls := newCacheTestController(token, nil)
	ctx := context.Background()

	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("near-expiry cache entry must re-probe, got %d calls", *calls)
	}
}

func TestChatGPTAuthCachedNoExpServesUntilInvalidated(t *testing.T) {
	// Defensive branch (E-9 negative): no parseable exp → official no-TTL
	// posture — cached until explicitly invalidated (failure-driven only).
	token := cacheTestJWT(t, "acct-noexp", time.Time{}, false)
	p, calls := newCacheTestController(token, nil)
	ctx := context.Background()

	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("no-exp token must serve from cache until invalidated, got %d calls", *calls)
	}
	p.invalidateAuthCache()
	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("invalidation must force a re-probe, got %d calls", *calls)
	}
}

func TestChatGPTAuthCachedFailureNotCached(t *testing.T) {
	// r1 F-6: a failed probe must not occupy the cache — the 401-recovery
	// path and the next restore round both need a genuine re-probe.
	probeErr := fmt.Errorf("读取 ChatGPT 登录态超时")
	p, calls := newCacheTestController("", probeErr)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, _, err := p.chatGPTAuthCached(ctx); !errors.Is(err, probeErr) {
			t.Fatalf("call %d: expected probe error, got %v", i+1, err)
		}
	}
	if *calls != 2 {
		t.Fatalf("failed probes must not be cached (every call re-probes), got %d calls", *calls)
	}
	p.mu.Lock()
	cached := p.auth
	p.mu.Unlock()
	if cached != nil {
		t.Fatal("failure must not populate the cache")
	}
}

func TestInvalidateRevokedPairingClearsAuthCache(t *testing.T) {
	// P1 §3 D2: revocation ends the identity the cached auth is bound to.
	token := cacheTestJWT(t, "acct-revoked", time.Now().Add(2*time.Hour), true)
	p, calls := newCacheTestController(token, nil)
	if _, _, err := p.chatGPTAuthCached(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Nil agent/store path: invalidateRevokedPairing handles a bare
	// controller (Snapshot on zero state).
	_ = p.invalidateRevokedPairing("配对已失效")
	p.mu.Lock()
	cached := p.auth
	p.mu.Unlock()
	if cached != nil {
		t.Fatal("revocation must clear the auth cache")
	}
	if _, _, err := p.chatGPTAuthCached(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("post-revocation call must re-probe, got %d calls", *calls)
	}
}

func TestChatGPTAuthCachedConcurrentAccess(t *testing.T) {
	// Cache read/write all under p.mu; run the hit path concurrently under
	// -race to prove it (plan risk table: 缓存并发竞态).
	token := cacheTestJWT(t, "acct-race", time.Now().Add(2*time.Hour), true)
	p, calls := newCacheTestController(token, nil)
	ctx := context.Background()
	if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := p.chatGPTAuthCached(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if *calls != 1 {
		t.Fatalf("concurrent hits must not probe, got %d calls", *calls)
	}
}
