package toolinventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"

	"github.com/Masterminds/semver/v3"
)

const executableOutputLimit = 64 * 1024

// ExecutableVersion keeps execution evidence separate from package metadata.
type ExecutableVersion struct {
	Value      string
	Raw        string
	ExitCode   int
	TimedOut   bool
	Err        error
	Truncated  bool
	Executable string
}

// VersionRunner executes only the artifact's own --version command with a caller-owned context.
type VersionRunner struct {
	Run func(context.Context, string) (ExecutableVersion, error)
}

// DefaultVersionRunner uses a process group, bounded output, and one cmd.Run/Wait owner.
func DefaultVersionRunner() VersionRunner {
	return VersionRunner{Run: runVersion}
}

func runVersion(ctx context.Context, artifactPath string) (ExecutableVersion, error) {
	cmd := exec.CommandContext(ctx, artifactPath, "--version")
	cmd.Stdin = nil
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	stdout := &limitedBuffer{max: executableOutputLimit}
	stderr := &limitedBuffer{max: executableOutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	observation := ExecutableVersion{
		Raw:        strings.TrimSpace(stdout.String()),
		ExitCode:   exitCode(err),
		TimedOut:   ctx.Err() == context.DeadlineExceeded,
		Err:        err,
		Truncated:  stdout.truncated || stderr.truncated,
		Executable: artifactPath,
	}
	if observation.Raw != "" && observation.ExitCode == 0 {
		observation.Value = parseVersionOutput(observation.Raw)
	}
	return observation, nil
}

// CompareStrict compares valid semantic versions only. Build metadata is parsed and ignored
// for precedence, as required by SemVer.
func CompareStrict(installed, target string) (int, error) {
	left, err := semver.StrictNewVersion(strings.TrimPrefix(installed, "v"))
	if err != nil {
		return 0, fmt.Errorf("installed version %q: %w", installed, err)
	}
	right, err := semver.StrictNewVersion(strings.TrimPrefix(target, "v"))
	if err != nil {
		return 0, fmt.Errorf("target version %q: %w", target, err)
	}
	return left.Compare(right), nil
}

func parseVersionOutput(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v")
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

type limitedBuffer struct {
	max       int
	truncated bool
	data      bytes.Buffer
}

func (b *limitedBuffer) String() string { return b.data.String() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.data.Len()+len(p) > b.max {
		remaining := b.max - b.data.Len()
		if remaining > 0 {
			b.data.Write(p[:remaining])
		}
		b.truncated = true
		return len(p), nil
	}
	return b.data.Write(p)
}

var _ io.Writer = (*limitedBuffer)(nil)
