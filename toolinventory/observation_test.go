package toolinventory

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Add(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func ProductByIDMust(id string) Product {
	product, ok := ProductByID(id)
	if !ok {
		panic("unknown product " + id)
	}
	return product
}

func npmTarget(fingerprint, installed string) CheckTarget {
	product, _ := ProductByID("opencode-web")
	return CheckTarget{
		Product: product, ArtifactPath: "/tools/lib/node_modules/opencode-ai/bin/opencode",
		Origin: InstallOrigin{
			Method: InstallMethodNPM, PackageID: "opencode-ai", Prefix: "/tools",
			ManagerPath: "/tools/bin/npm", NodeBinDir: "/tools/bin",
		},
		InstalledVersion: installed, Scope: "user-global", InstallFingerprint: fingerprint,
	}
}

func TestVersionServiceNPMUpdateCurrentAndNewer(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	service := NewVersionService(VersionDependencies{
		Now: clock.Now,
		NPMLatest: func(context.Context, string) (string, error) {
			calls.Add(1)
			return "1.19.0", nil
		},
	})
	update := service.Check(context.Background(), npmTarget("fingerprint-1", "1.18.0"), true)
	if update.State != VersionStateUpdate || update.TargetVersion != "1.19.0" || update.UpdateRecipe == nil {
		t.Fatalf("update observation: %+v", update)
	}
	if update.UpdateRecipe.Executable != "/tools/bin/npm" || !update.FreshUntil.After(update.CheckedAt) {
		t.Fatalf("update recipe/freshness: recipe=%+v observation=%+v", update.UpdateRecipe, update)
	}

	current := service.Check(context.Background(), CheckTarget{Product: ProductByIDMust("opencode-web"), Origin: InstallOrigin{Method: InstallMethodNPM, PackageID: "opencode-ai", ManagerPath: "/npm"}, InstalledVersion: "1.19.0", InstallFingerprint: "fingerprint-2"}, true)
	if current.State != VersionStateCurrent {
		t.Fatalf("current observation: %+v", current)
	}
	newer := service.Check(context.Background(), CheckTarget{Product: ProductByIDMust("opencode-web"), Origin: InstallOrigin{Method: InstallMethodNPM, PackageID: "opencode-ai", ManagerPath: "/npm"}, InstalledVersion: "1.20.0", InstallFingerprint: "fingerprint-3"}, true)
	if newer.State != VersionStateNewer || newer.UpdateRecipe != nil {
		t.Fatalf("newer observation: %+v", newer)
	}
	if calls.Load() != 3 {
		t.Fatalf("provider calls=%d, want 3", calls.Load())
	}
}

func TestVersionServiceFreshCacheAndFailureRetry(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	service := NewVersionService(VersionDependencies{
		Now: clock.Now,
		NPMLatest: func(context.Context, string) (string, error) {
			calls.Add(1)
			if fail.Load() {
				return "", errors.New("network unavailable")
			}
			return "2.0.0", nil
		},
	})
	target := npmTarget("same", "1.0.0")
	first := service.Check(context.Background(), target, true)
	if first.State != VersionStateFailed || first.ErrorCode != "network" || !first.FreshUntil.IsZero() {
		t.Fatalf("first failure: %+v", first)
	}
	service.Check(context.Background(), target, false)
	if calls.Load() != 1 {
		t.Fatalf("failure was retried before retry TTL: %d", calls.Load())
	}
	clock.Add(2 * time.Minute)
	fail.Store(false)
	second := service.Check(context.Background(), target, false)
	if second.State != VersionStateUpdate || calls.Load() != 2 {
		t.Fatalf("failure was not retried after TTL: calls=%d observation=%+v", calls.Load(), second)
	}
	service.Check(context.Background(), target, false)
	if calls.Load() != 2 {
		t.Fatalf("successful observation was not cached: %d", calls.Load())
	}
	clock.Add(6*time.Hour + time.Second)
	service.Check(context.Background(), target, false)
	if calls.Load() != 3 {
		t.Fatalf("successful observation was not expired: %d", calls.Load())
	}
}

func TestVersionServiceCoalescesSameKeyAndRejectsOldFingerprintSnapshot(t *testing.T) {
	clock := &mutableClock{now: time.Now().UTC()}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	var firstCall sync.Once
	service := NewVersionService(VersionDependencies{
		Now: clock.Now,
		NPMLatest: func(context.Context, string) (string, error) {
			calls.Add(1)
			firstCall.Do(func() { close(started) })
			<-release
			return "2.0.0", nil
		},
	})
	target := npmTarget("old", "1.0.0")
	results := make(chan VersionObservation, 3)
	go func() { results <- service.Check(context.Background(), target, true) }()
	<-started
	go func() { results <- service.Check(context.Background(), target, false) }()
	if snapshot, ok := service.Snapshot("opencode-web"); ok {
		t.Fatalf("in-flight snapshot must be absent, got %+v", snapshot)
	}
	newTarget := npmTarget("new", "1.0.0")
	go func() { results <- service.Check(context.Background(), newTarget, true) }()
	close(release)
	for i := 0; i < 3; i++ {
		observation := <-results
		if observation.State != VersionStateUpdate {
			t.Fatalf("observation %d: %+v", i, observation)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("same-key requests were not coalesced: %d", calls.Load())
	}
	snapshot, ok := service.Snapshot("opencode-web")
	if !ok || snapshot.InstallFingerprint != "new" {
		t.Fatalf("latest fingerprint did not win snapshot: ok=%v %+v", ok, snapshot)
	}
}

func TestNPMLatestUsesEscopedPackageAndStrictDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/@anthropic-ai%2Fclaude-code/latest" {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"name":"@anthropic-ai/claude-code","version":"2.1.299"}`))
	}))
	defer server.Close()
	version, err := npmLatestFromRegistry(context.Background(), server.Client(), server.URL, "@anthropic-ai/claude-code")
	if err != nil || version != "2.1.299" {
		t.Fatalf("valid response: version=%q err=%v", version, err)
	}
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()
	if _, err := npmLatestFromRegistry(context.Background(), notFound.Client(), notFound.URL, "@anthropic-ai/claude-code"); err == nil {
		t.Fatal("expected HTTP 404 failure")
	}
	for name, raw := range map[string]string{
		"array": `[]`, "null": `{"version":null}`, "empty": `{"version":""}`,
		"number": `{"version":1.2}`, "html": `<html></html>`, "trail": `{"version":"1.0.0"} {}`,
		"case-insensitive key": `{"Version":"1.0.0"}`,
	} {
		if _, err := decodeNPMRegistryDocument(strings.NewReader(raw)); err == nil {
			t.Fatalf("%s: expected decode failure", name)
		}
	}
}

func TestVersionSourceBoundariesAndUnsupportedObservation(t *testing.T) {
	grok, _ := ProductByID("grokbuild")
	if versionSource(CheckTarget{Product: grok, ArtifactPath: "/grok"}) != VersionSourceGrokCheck {
		t.Fatal("located Grok must use its verified update-check source")
	}
	if versionSource(npmTarget("f", "1.0.0")) != VersionSourceNPMRegistry {
		t.Fatal("positive npm origin must use npm registry source")
	}
	observation := NewVersionService(VersionDependencies{}).Check(context.Background(), CheckTarget{Product: ProductByIDMust("codex-remote"), InstalledVersion: "26.1"}, true)
	if observation.State != VersionStateUnsupported || observation.Source != VersionSourceUnsupported {
		t.Fatalf("Codex observation: %+v", observation)
	}
}

func TestArtifactFingerprintChangesFileIdentity(t *testing.T) {
	first := ArtifactFingerprint("id", "/tool", "/package.json", fakeFileInfo{size: 1, modTime: time.Unix(1, 1)}, fakeFileInfo{size: 2, modTime: time.Unix(2, 2)})
	same := ArtifactFingerprint("id", "/tool", "/package.json", fakeFileInfo{size: 1, modTime: time.Unix(1, 1)}, fakeFileInfo{size: 2, modTime: time.Unix(2, 2)})
	changed := ArtifactFingerprint("id", "/tool", "/package.json", fakeFileInfo{size: 3, modTime: time.Unix(1, 1)}, fakeFileInfo{size: 2, modTime: time.Unix(2, 2)})
	if first != same || first == changed || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("fingerprints first=%q same=%q changed=%q", first, same, changed)
	}
}

type fakeFileInfo struct {
	size    int64
	modTime time.Time
}

func (i fakeFileInfo) Name() string       { return "file" }
func (i fakeFileInfo) Size() int64        { return i.size }
func (i fakeFileInfo) Mode() fs.FileMode  { return 0o755 }
func (i fakeFileInfo) ModTime() time.Time { return i.modTime }
func (i fakeFileInfo) IsDir() bool        { return false }
func (i fakeFileInfo) Sys() any           { return nil }
