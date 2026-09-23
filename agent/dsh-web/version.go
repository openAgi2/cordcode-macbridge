package dshweb

// Seat CLI version note (convergence plan §2.4): the backend descriptor's
// statusMessage carries ONLY the dsh version and the source identifier (the
// resolved CLI path) — never credentials, cookies, or other non-version info.
// The version is probed from the resolved binary (`<bin> --version`); for an
// externally-held seat that binary is the one the seat was started from, so the
// note is labeled by its path rather than assumed to be the seat's exact build.
// Descriptor polls (hello_ack rebuilds, GET /internal/agents) must not re-pay
// the probe: the verdict is memoized like the readiness cache.

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// versionCacheTTL memoizes the version probe. Longer than readinessCacheTTL:
// a version does not drift mid-process, and a dsh upgrade shows up on the next
// TTL expiry anyway.
const versionCacheTTL = 60 * time.Second

// versionProbeTimeout bounds one `<bin> --version` run.
const versionProbeTimeout = 2 * time.Second

// runVersionProbe is the exec seam, fakeable in tests.
var runVersionProbe = func(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// versionCache memoizes one version verdict.
type versionCache struct {
	mu        sync.Mutex
	expiresAt time.Time
	version   string
}

// DescriptorStatusMessage implements core.StatusMessageProvider. Empty string
// (field omitted on the wire) whenever no binary resolves or the probe fails —
// a missing version is not an error state, the readiness seam owns those.
func (a *Agent) DescriptorStatusMessage() string {
	bin := a.resolver.effectiveBinary()
	if bin == "" {
		return ""
	}
	a.version.mu.Lock()
	if time.Now().Before(a.version.expiresAt) {
		v := a.version.version
		a.version.mu.Unlock()
		return formatStatusMessage(v, bin)
	}
	a.version.mu.Unlock()

	out, err := runVersionProbe(bin)
	v := ""
	if err == nil {
		// `dsh --version` prints the bare semver line; take the first token of
		// the first non-empty line so trailing banners cannot leak in.
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			v = strings.Fields(line)[0]
			break
		}
	}
	a.version.mu.Lock()
	a.version.version = v
	a.version.expiresAt = time.Now().Add(versionCacheTTL)
	a.version.mu.Unlock()
	return formatStatusMessage(v, bin)
}

// formatStatusMessage renders the credential-free note. An empty version still
// names the source binary — the path alone is a legal source identifier.
func formatStatusMessage(version, bin string) string {
	if version == "" {
		return "dsh (cli " + bin + ")"
	}
	return "dsh " + version + " (cli " + bin + ")"
}
