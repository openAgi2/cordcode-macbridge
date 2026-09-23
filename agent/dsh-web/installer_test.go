package dshweb

// Installer tests (2026-09-22 install-and-start plan §4/§7): argv assertions
// against a FAKE npm script (never the registry) — exactly
// `npm install -g @deepseek-ai/dsh` (no sudo, no npx, no version pin), the
// `--prefix` CordCode-data-dir fallback, no npm run when a binary already
// exists, node_missing when npm is nowhere, and the install record (0600, no
// credentials) + resolver write-back.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeNpm writes a shell script that records every argv line and creates the
// expected dsh bin, then puts it on PATH. mode selects the behavior:
//
//	ok      — global install succeeds (creates <FAKE_PREFIX>/bin/dsh)
//	fail    — global install exits 1 (prefix fallback then creates
//	          <arg --prefix>/node_modules/.bin/dsh and succeeds)
//	nofail2 — both attempts fail
//
// PATH is the fake dir plus /usr/bin:/bin (the script's mkdir/chmod/sleep),
// deliberately excluding homebrew/nvm locations so no real dsh/npm leaks in.
func fakeNpm(t *testing.T, mode string, fakePrefix string) (npmPath, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	npmPath = filepath.Join(dir, "npm")
	argvLog = filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> \"" + argvLog + "\"\n" +
		"case \"$1\" in\n" +
		"  prefix) echo \"" + fakePrefix + "\"; exit 0 ;;\n" +
		"esac\n" +
		"case \"" + mode + "\" in\n" +
		"  ok)\n" +
		"    mkdir -p \"" + fakePrefix + "/bin\"\n" +
		"    printf '#!/bin/sh\\necho 0.1.5-fake\\n' > \"" + fakePrefix + "/bin/dsh\"\n" +
		"    chmod +x \"" + fakePrefix + "/bin/dsh\"\n" +
		"    exit 0 ;;\n" +
		"  fail)\n" +
		"    if [ \"$2\" = \"-g\" ]; then echo 'EACCES permission denied' >&2; exit 1; fi\n" +
		"    # --prefix fallback: argv is install --prefix <dir> <pkg>; bins\n" +
		"    # land in <dir>/node_modules/.bin ($3 = the prefix dir).\n" +
		"    mkdir -p \"$3/node_modules/.bin\"\n" +
		"    printf '#!/bin/sh\\necho 0.1.5-fake\\n' > \"$3/node_modules/.bin/dsh\"\n" +
		"    chmod +x \"$3/node_modules/.bin/dsh\"\n" +
		"    exit 0 ;;\n" +
		"  nofail2)\n" +
		"    echo 'network unreachable' >&2; exit 1 ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(npmPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Isolate discovery: empty HOME (no real nvm), PATH = fake dir + system
	// basics only.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return npmPath, argvLog
}

func readArgvLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

// waitForInstallDone polls until the agent's install/start flags settle.
func waitForInstallDone(t *testing.T, a *Agent, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.seatActionMu.Lock()
		busy := a.installing || a.starting
		a.seatActionMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("install did not settle in time")
}

// isolatedSeatResolver builds a resolver on a free loopback seat with a
// fake starter (deterministic StartSeat: bind + describe, no real spawn).
func isolatedSeatResolver(t *testing.T) *Resolver {
	t.Helper()
	return NewResolver(
		WithProbeURLs([]string{freeLoopbackSeat(t)}),
		WithDataDir(t.TempDir()),
		withManagedStarter(&countingStarter{}),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
}

func TestInstallerGlobalArgvExact(t *testing.T) {
	fakePrefix := t.TempDir()
	_, argvLog := fakeNpm(t, "ok", fakePrefix)

	a := &Agent{resolver: isolatedSeatResolver(t)}
	dataDir := a.resolver.dataDirOf()

	kick := a.InstallDSH()
	if kick.Status != "started" {
		t.Fatalf("kick status = %q (%q), want started", kick.Status, kick.Detail)
	}
	waitForInstallDone(t, a, 10*time.Second)

	argvs := readArgvLog(t, argvLog)
	if len(argvs) == 0 {
		t.Fatal("fake npm was not executed")
	}
	// First install argv must be exactly `install -g @deepseek-ai/dsh` —
	// no sudo, no npx, no version pin.
	if argvs[0] != "install -g "+npmPackage {
		t.Fatalf("install argv = %q, want %q", argvs[0], "install -g "+npmPackage)
	}
	for _, argv := range argvs {
		for _, banned := range []string{"sudo", "npx", "@" + npmPackage + "@"} {
			if strings.Contains(" "+argv+" ", " "+banned+" ") {
				t.Fatalf("banned token %q in argv %q", banned, argv)
			}
		}
	}

	// Record written: 0600, no credentials, user-global scope, fake bin.
	recPath := filepath.Join(dataDir, installRecordFile)
	info, err := os.Stat(recPath)
	if err != nil {
		t.Fatalf("install record missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("install record mode %v, want 0600", info.Mode().Perm())
	}
	b, _ := os.ReadFile(recPath)
	var rec installRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.BinPath != filepath.Join(fakePrefix, "bin", "dsh") {
		t.Fatalf("record bin = %q", rec.BinPath)
	}
	if rec.Scope != InstallScopeUserGlobal || rec.DshVersion != "0.1.5-fake" {
		t.Fatalf("record scope/version = %q/%q", rec.Scope, rec.DshVersion)
	}

	// Write-back: the resolver now resolves the installed bin.
	if got := a.resolver.effectiveBinary(); got != rec.BinPath {
		t.Fatalf("resolver write-back = %q, want %q", got, rec.BinPath)
	}
}

func TestInstallerPrefixFallbackArgvAndNote(t *testing.T) {
	fakePrefix := t.TempDir()
	_, argvLog := fakeNpm(t, "fail", fakePrefix)

	a := &Agent{resolver: isolatedSeatResolver(t)}
	dataDir := a.resolver.dataDirOf()

	kick := a.InstallDSH()
	if kick.Status != "started" {
		t.Fatalf("kick status = %q (%q)", kick.Status, kick.Detail)
	}
	waitForInstallDone(t, a, 10*time.Second)

	argvs := readArgvLog(t, argvLog)
	wantPrefix := filepath.Join(dataDir, fallbackPrefixDir)
	foundFallback := false
	for _, argv := range argvs {
		if strings.HasPrefix(argv, "install --prefix "+wantPrefix+" "+npmPackage) {
			foundFallback = true
		}
	}
	if !foundFallback {
		t.Fatalf("no --prefix fallback argv in %v", argvs)
	}

	// Record: cordcode-prefix scope + the honest note for the row subtitle.
	b, _ := os.ReadFile(filepath.Join(dataDir, installRecordFile))
	var rec installRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Scope != InstallScopeCordcodePrefix {
		t.Fatalf("scope = %q, want cordcode-prefix", rec.Scope)
	}
	if rec.BinPath != filepath.Join(wantPrefix, "node_modules", ".bin", "dsh") {
		t.Fatalf("fallback bin = %q", rec.BinPath)
	}
	snap := a.SeatActionState()
	if snap.LastInstallNote == "" || !strings.Contains(snap.LastInstallNote, "CordCode") {
		t.Fatalf("prefix fallback note missing: %+v", snap)
	}
}

func TestInstallerBothAttemptsFailKeepsError(t *testing.T) {
	fakePrefix := t.TempDir()
	_, argvLog := fakeNpm(t, "nofail2", fakePrefix)

	dataDir := t.TempDir()
	a := &Agent{resolver: NewResolver(WithDataDir(dataDir))}

	if kick := a.InstallDSH(); kick.Status != "started" {
		t.Fatalf("kick status = %q", kick.Status)
	}
	waitForInstallDone(t, a, 10*time.Second)

	if argvs := readArgvLog(t, argvLog); len(argvs) < 2 {
		t.Fatalf("both attempts must run, argv log: %v", argvs)
	}
	snap := a.SeatActionState()
	if snap.LastInstallError == "" {
		t.Fatal("both-fail must retain the honest error")
	}
	if !strings.Contains(snap.LastInstallError, "失败") {
		t.Fatalf("error must name both attempts: %q", snap.LastInstallError)
	}
	// No record written on failure.
	if _, err := os.Stat(filepath.Join(dataDir, installRecordFile)); !os.IsNotExist(err) {
		t.Fatalf("failed install must not write a record (err=%v)", err)
	}
}

func TestInstallerSkipsNpmWhenBinaryExists(t *testing.T) {
	fakePrefix := t.TempDir()
	_, argvLog := fakeNpm(t, "ok", fakePrefix)

	a := &Agent{resolver: NewResolver(WithManagedBinary("/usr/local/bin/dsh-fake", nil))}
	kick := a.InstallDSH()
	if kick.Status != "not_needed" {
		t.Fatalf("kick status = %q (%q), want not_needed", kick.Status, kick.Detail)
	}
	if argvs := readArgvLog(t, argvLog); len(argvs) != 0 {
		t.Fatalf("npm must not run when a binary exists: %v", argvs)
	}
}

func TestInstallerNodeMissing(t *testing.T) {
	// No npm on PATH, HOME pinned to an empty dir (no real nvm).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	a := &Agent{resolver: NewResolver(WithDataDir(t.TempDir()))}
	kick := a.InstallDSH()
	if kick.Status != "node_missing" {
		t.Fatalf("kick status = %q (%q), want node_missing", kick.Status, kick.Detail)
	}
	snap := a.SeatActionState()
	if snap.NpmFound {
		t.Fatal("snapshot must report npm missing")
	}
	if snap.LastInstallError == "" {
		t.Fatal("node_missing must retain the guidance error")
	}
}

func TestInstallerSingleFlight(t *testing.T) {
	fakePrefix := t.TempDir()

	// A slow fake npm: hold the install open until released.
	dir := t.TempDir()
	npm := filepath.Join(dir, "npm")
	release := filepath.Join(dir, "release")
	script := "#!/bin/sh\n" +
		"while [ ! -f \"" + release + "\" ]; do sleep 0.05; done\n" +
		"mkdir -p \"" + fakePrefix + "/bin\"\n" +
		"printf '#!/bin/sh\\necho 0.1.5-fake\\n' > \"" + fakePrefix + "/bin/dsh\"\n" +
		"chmod +x \"" + fakePrefix + "/bin/dsh\"\n"
	if err := os.WriteFile(npm, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", dir+":/usr/bin:/bin")

	a := &Agent{resolver: isolatedSeatResolver(t)}
	if kick := a.InstallDSH(); kick.Status != "started" {
		t.Fatalf("first kick = %q", kick.Status)
	}
	// Second click while the first is in flight: rejected, no second npm.
	if kick := a.InstallDSH(); kick.Status != "already_installing" {
		t.Fatalf("second kick = %q, want already_installing", kick.Status)
	}
	if err := os.WriteFile(release, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForInstallDone(t, a, 10*time.Second)
}

func TestStartSeatActionKicksAndReports(t *testing.T) {
	// no_binary: nothing installed anywhere.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	a := &Agent{resolver: NewResolver(WithDataDir(t.TempDir()))}
	if kick := a.StartSeatAction(); kick.Status != "no_binary" {
		t.Fatalf("kick = %q, want no_binary", kick.Status)
	}

	// With a binary: the kick starts the seat asynchronously (fake starter
	// binds a describe server).
	seat := freeLoopbackSeat(t)
	starter := &countingStarter{}
	r := NewResolver(
		WithProbeURLs([]string{seat}),
		WithDataDir(t.TempDir()),
		withManagedStarter(starter),
		WithManagedBinary("/usr/local/bin/dsh-fake", nil),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}),
	)
	a2 := &Agent{resolver: r}
	kick := a2.StartSeatAction()
	if kick.Status != "started" {
		t.Fatalf("kick = %q, want started", kick.Status)
	}
	waitForInstallDone(t, a2, 10*time.Second)
	if starter.starts != 1 {
		t.Fatalf("StartSeatAction must spawn exactly once (starts=%d)", starter.starts)
	}
	if inst := a2.resolver.Current(); inst == nil {
		t.Fatal("seat must be held after the start action settles")
	}
	// Already running: idempotent.
	if kick := a2.StartSeatAction(); kick.Status != "already_running" {
		t.Fatalf("second kick = %q, want already_running", kick.Status)
	}
}

func TestNpmOutputRedaction(t *testing.T) {
	in := "added 1 package npm_AbCdEf1234567890AbCd in 3s //user:secret@registry.npmjs.org/"
	out := redactNpmOutput(in)
	if strings.Contains(out, "npm_AbCdEf1234567890AbCd") {
		t.Fatalf("npm token leaked: %q", out)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("registry auth leaked: %q", out)
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	if got := lastNonEmptyLine("", "line1\n\nline2\n"); got != "line2" {
		t.Fatalf("got %q", got)
	}
	if got := lastNonEmptyLine("only-stderr\n"); got != "only-stderr" {
		t.Fatalf("got %q", got)
	}
	if got := lastNonEmptyLine("", ""); got != "unknown npm error" {
		t.Fatalf("got %q", got)
	}
}
