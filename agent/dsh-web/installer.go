package dshweb

// 代装 installer (2026-09-22 install-and-start plan §4): `npm install -g
// @deepseek-ai/dsh` via the discovered npm absolute path — no version pin,
// no sudo, no npx. On an unwritable global prefix, fall back to
// `npm install --prefix <CordCode data dir>/dsh-install`. The install record
// is 0600 with no credentials. A verified install (`dsh --version` must
// succeed) writes the bin path back to the resolver and starts the seat —
// the install back-half is the same explicit StartSeat as the 启动 button.
//
// The management POSTs kick async work and return immediately (the
// management server's 2s WriteTimeout cannot carry a 10-minute npm install
// or a 30s seat boot); progress and errors surface through SeatActionState
// and the descriptor re-read.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	npmPackage = "@deepseek-ai/dsh"
	// installTimeout bounds one npm install; on expiry the whole npm process
	// group is killed (plan §4: 10 分钟超时杀进程组).
	installTimeout = 10 * time.Minute
	// installQueryTimeout bounds the read-only `npm prefix -g` query.
	installQueryTimeout = 15 * time.Second
	// installBinTimeout bounds the `dsh --version` verification.
	installBinTimeout = 15 * time.Second
	// fallbackPrefixDir is the CordCode data-dir prefix used when the global
	// prefix is unwritable.
	fallbackPrefixDir = "dsh-install"
)

// SeatActionKick is the immediate POST response for install/start.
type SeatActionKick struct {
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	BinPath string `json:"binPath,omitempty"`
}

// SeatActionSnapshot is the GET action-state response the Mac row polls.
type SeatActionSnapshot struct {
	Installing       bool   `json:"installing"`
	Starting         bool   `json:"starting"`
	NpmFound         bool   `json:"npmFound"`
	NpmPath          string `json:"npmPath,omitempty"`
	BinPath          string `json:"binPath,omitempty"`
	LastInstallError string `json:"lastInstallError,omitempty"`
	LastStartError   string `json:"lastStartError,omitempty"`
	LastInstallNote  string `json:"lastInstallNote,omitempty"`
}

// InstallDSH kicks off the 代装 (async, single-flight). Already-installed
// means no npm run at all (§4: 已有可执行 dsh 时不显示「安装」，也不再跑 npm).
func (a *Agent) InstallDSH() SeatActionKick {
	a.seatActionMu.Lock()
	if a.installing {
		a.seatActionMu.Unlock()
		return SeatActionKick{Status: "already_installing"}
	}
	a.installing = true
	a.lastInstallErr = ""
	a.lastInstallNote = ""
	a.seatActionMu.Unlock()

	if bin := a.resolver.effectiveBinary(); bin != "" {
		a.seatActionMu.Lock()
		a.installing = false
		a.seatActionMu.Unlock()
		return SeatActionKick{Status: "not_needed", Detail: "dsh already installed", BinPath: bin}
	}
	npm := findNpmBinary()
	if npm == "" {
		a.seatActionMu.Lock()
		a.installing = false
		a.lastInstallErr = "未找到 node/npm（安装 Node.js 后重试）"
		a.seatActionMu.Unlock()
		return SeatActionKick{Status: "node_missing", Detail: "node/npm not found (PATH, nvm)"}
	}

	go a.runInstall(npm)
	return SeatActionKick{Status: "started", Detail: "npm install -g " + npmPackage, BinPath: npm}
}

// StartSeatAction is the 启动 button's management entry: an async kick of
// the explicit StartSeat (the POST must return fast — the seat boot can take
// up to managedBootTimeout).
func (a *Agent) StartSeatAction() SeatActionKick {
	a.seatActionMu.Lock()
	if a.starting {
		a.seatActionMu.Unlock()
		return SeatActionKick{Status: "already_starting"}
	}
	a.seatActionMu.Unlock()
	if inst := a.resolver.Current(); inst != nil {
		return SeatActionKick{Status: "already_running", Detail: inst.BaseURL}
	}
	if bin := a.resolver.effectiveBinary(); bin == "" {
		return SeatActionKick{Status: "no_binary", Detail: "dsh binary not found — install first"}
	}

	a.seatActionMu.Lock()
	a.starting = true
	a.lastStartErr = ""
	a.seatActionMu.Unlock()
	go func() {
		if _, err := a.resolver.StartSeat(context.Background()); err != nil {
			a.seatActionMu.Lock()
			a.lastStartErr = err.Error()
			a.seatActionMu.Unlock()
			slog.Warn("dsh-web: seat start failed", "error", err.Error())
		}
		a.seatActionMu.Lock()
		a.starting = false
		a.seatActionMu.Unlock()
	}()
	return SeatActionKick{Status: "started"}
}

// SeatActionState snapshots the action surface for the Mac row (npmFound
// decides 「安装」 vs 「需要 Node.js」; installing/starting drive the
// in-progress states; the error fields are the row subtitles).
func (a *Agent) SeatActionState() SeatActionSnapshot {
	npm := findNpmBinary()
	bin := a.resolver.effectiveBinary()
	a.seatActionMu.Lock()
	defer a.seatActionMu.Unlock()
	return SeatActionSnapshot{
		Installing:       a.installing,
		Starting:         a.starting,
		NpmFound:         npm != "",
		NpmPath:          npm,
		BinPath:          bin,
		LastInstallError: a.lastInstallErr,
		LastStartError:   a.lastStartErr,
		LastInstallNote:  a.lastInstallNote,
	}
}

// runInstall is the background install: npm install → locate bin → verify
// `dsh --version` → write record → write back to resolver → StartSeat.
func (a *Agent) runInstall(npm string) {
	defer func() {
		a.seatActionMu.Lock()
		a.installing = false
		a.seatActionMu.Unlock()
	}()

	ctx := context.Background()
	bin, scope, note, dshVersion, err := a.npmInstall(ctx, npm)
	if err != nil {
		a.seatActionMu.Lock()
		a.lastInstallErr = err.Error()
		a.seatActionMu.Unlock()
		slog.Warn("dsh-web: install failed", "error", err.Error())
		return
	}

	if err := writeInstallRecord(a.resolver.dataDirOf(), bin, scope, dshVersion); err != nil {
		slog.Warn("dsh-web: install record write failed", "error", err.Error())
	}
	a.resolver.SetManagedBinary(bin)
	a.seatActionMu.Lock()
	a.lastInstallNote = note
	a.seatActionMu.Unlock()
	slog.Info("dsh-web: dsh installed", "bin", bin, "scope", scope, "version", dshVersion)

	// Install back-half: the same explicit StartSeat as the 启动 button.
	a.seatActionMu.Lock()
	a.starting = true
	a.seatActionMu.Unlock()
	if _, err := a.resolver.StartSeat(ctx); err != nil {
		a.seatActionMu.Lock()
		a.lastStartErr = err.Error()
		a.seatActionMu.Unlock()
		slog.Warn("dsh-web: post-install seat start failed", "error", err.Error())
	}
	a.seatActionMu.Lock()
	a.starting = false
	a.seatActionMu.Unlock()
}

// npmInstall runs the install attempts and returns the verified dsh bin
// path plus its scope/note/version. User-global first; on failure the
// CordCode data-dir prefix fallback (no sudo, no privilege escalation).
func (a *Agent) npmInstall(ctx context.Context, npm string) (binPath, scope, note, dshVersion string, err error) {
	bin, version, gErr := npmInstallGlobal(ctx, npm)
	if gErr == nil {
		return bin, InstallScopeUserGlobal, "", version, nil
	}
	slog.Info("dsh-web: user-global install failed, trying CordCode prefix fallback", "error", gErr.Error())

	prefix := filepath.Join(a.resolver.dataDirOf(), fallbackPrefixDir)
	bin, version, pErr := npmInstallPrefix(ctx, npm, prefix)
	if pErr != nil {
		return "", "", "", "", fmt.Errorf("user-global 安装失败：%v；CordCode 目录安装失败：%v", gErr, pErr)
	}
	return bin, InstallScopeCordcodePrefix, "已装到 CordCode 目录，终端里不一定有 dsh", version, nil
}

// npmInstallGlobal runs `npm install -g @deepseek-ai/dsh`, locates the bin
// via the same npm's `prefix -g`, and verifies `dsh --version`.
func npmInstallGlobal(ctx context.Context, npm string) (bin, dshVersion string, err error) {
	if _, _, err := runNpm(ctx, npm, []string{"install", "-g", npmPackage}); err != nil {
		return "", "", err
	}
	prefix, err := npmGlobalPrefix(ctx, npm)
	if err != nil {
		return "", "", err
	}
	bin = filepath.Join(prefix, "bin", "dsh")
	if !isExecutableFile(bin) {
		return "", "", fmt.Errorf("npm 安装完成但未找到可执行 dsh（期望 %s）", bin)
	}
	version, err := verifyDSHVersion(bin)
	if err != nil {
		return "", "", err
	}
	return bin, version, nil
}

// npmInstallPrefix runs the fallback `npm install --prefix <dir>
// @deepseek-ai/dsh` into the CordCode data dir (bins land in
// <dir>/node_modules/.bin).
func npmInstallPrefix(ctx context.Context, npm, prefix string) (bin, dshVersion string, err error) {
	if _, _, err := runNpm(ctx, npm, []string{"install", "--prefix", prefix, npmPackage}); err != nil {
		return "", "", err
	}
	bin = filepath.Join(prefix, "node_modules", ".bin", "dsh")
	if !isExecutableFile(bin) {
		return "", "", fmt.Errorf("npm 安装完成但未找到可执行 dsh（期望 %s）", bin)
	}
	version, err := verifyDSHVersion(bin)
	if err != nil {
		return "", "", err
	}
	return bin, version, nil
}

// runNpm executes npm with the exact argv (no sudo, no npx, no version pin —
// asserted by tests) under the 10-minute budget; on timeout the whole npm
// process group is killed. Output is redacted before logging.
func runNpm(ctx context.Context, npm string, args []string) (stdout, stderr string, err error) {
	cctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, npm, args...)
	prepareCmdForProcessGroup(cmd)
	cmd.Env = prependBinDirToPath(os.Environ(), filepath.Dir(npm))
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		so, se := redactNpmOutput(out.String()), redactNpmOutput(errBuf.String())
		slog.Info("dsh-web: npm finished", "args", args, "stdout", so, "stderr", se)
		if err != nil {
			return so, se, fmt.Errorf("npm %s 失败: %s", strings.Join(args, " "), lastNonEmptyLine(se, so))
		}
		return so, se, nil
	case <-cctx.Done():
		// CommandContext kills the direct child; escalate to the group so
		// npm's node children die too. The wait goroutine owns the single
		// Wait — no reaping here.
		if cmd.Process != nil {
			killProcessGroupNoWait(cmd.Process.Pid)
		}
		err = <-waitErr
		so, se := redactNpmOutput(out.String()), redactNpmOutput(errBuf.String())
		return so, se, fmt.Errorf("npm %s 超时（%s），已终止", strings.Join(args, " "), installTimeout)
	}
}

// npmGlobalPrefix asks the same npm binary for its global prefix (read-only
// query; the install command itself stays exactly `npm install -g <pkg>`).
func npmGlobalPrefix(ctx context.Context, npm string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, installQueryTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, npm, "prefix", "-g")
	cmd.Env = prependBinDirToPath(os.Environ(), filepath.Dir(npm))
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("npm prefix -g 失败: %v", err)
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", fmt.Errorf("npm prefix -g 返回空")
	}
	return p, nil
}

// verifyDSHVersion runs `dsh --version` — the install only counts when this
// exits 0 (§4: --version 失败 = 安装失败，不启动). Returns the version string
// for the install record (diagnostics only).
func verifyDSHVersion(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installBinTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = prependBinDirToPath(os.Environ(), filepath.Dir(bin))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("dsh --version 验证失败: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// writeInstallRecord persists the install (0600, no credentials): bin path,
// npm scope, version, time.
func writeInstallRecord(dataDir, bin, scope, dshVersion string) error {
	if dataDir == "" {
		return fmt.Errorf("no data dir")
	}
	rec := installRecord{
		Version:     installRecordVersion,
		BinPath:     bin,
		Scope:       scope,
		DshVersion:  dshVersion,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(dataDir, 0o700)
	return core.AtomicWriteFile(filepath.Join(dataDir, installRecordFile), b, 0o600)
}

// npmTokenPattern matches npm token-shaped secrets in npm output; the
// install command itself passes no credentials, this only guards logs.
var (
	npmTokenPattern   = regexp.MustCompile(`npm_[A-Za-z0-9]{16,}`)
	npmAuthURLPattern = regexp.MustCompile(`//[^/\s]+:[^@\s]+@`)
)

func redactNpmOutput(s string) string {
	s = npmTokenPattern.ReplaceAllString(s, "npm_[REDACTED]")
	return npmAuthURLPattern.ReplaceAllString(s, "//[REDACTED]@")
}

// lastNonEmptyLine picks the last non-empty line of the given sources (stderr
// first) as the user-facing install error (§4: 界面字幕用最后一段非空错误).
func lastNonEmptyLine(sources ...string) string {
	for _, src := range sources {
		lines := strings.Split(strings.TrimSpace(src), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); l != "" {
				return l
			}
		}
	}
	return "unknown npm error"
}
