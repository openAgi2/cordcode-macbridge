package toolinventory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// CommandRecipe is a structured command intended for user-initiated copying.
// CordCode never executes it as part of version checking.
type CommandRecipe struct {
	Executable  string               `json:"executable"`
	Args        []string             `json:"args,omitempty"`
	Environment []CommandEnvironment `json:"environment,omitempty"`
	Display     string               `json:"display"`
}

type CommandEnvironment struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type InstallGuidance struct {
	DocumentationURL string         `json:"documentationUrl"`
	Recipe           *CommandRecipe `json:"recipe,omitempty"`
	Reason           string         `json:"reason,omitempty"`
}

func (r CommandRecipe) withDisplay() CommandRecipe {
	quotedCommand := append([]string{ShellQuote(r.Executable)}, r.Args...)
	for i := 1; i < len(quotedCommand); i++ {
		quotedCommand[i] = ShellQuote(quotedCommand[i])
	}
	quoted := quotedCommand
	for _, environment := range r.Environment {
		quoted = append([]string{environment.Name + "=" + ShellQuote(environment.Value)}, quoted...)
	}
	r.Display = strings.Join(quoted, " ")
	return r
}

// ShellQuote quotes every byte for a POSIX shell. It deliberately does not try
// to decide that a "simple-looking" value can remain unquoted.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// BuildNPMUpdateRecipe keeps the exact npm binary, Node PATH, and prefix that
// own the installed artifact. A target version is mandatory.
func BuildNPMUpdateRecipe(origin InstallOrigin, targetVersion, scope string) (CommandRecipe, error) {
	if origin.Method != InstallMethodNPM || origin.ManagerPath == "" || origin.PackageID == "" {
		return CommandRecipe{}, fmt.Errorf("npm update requires a positively identified npm install")
	}
	if err := validateTargetVersion(targetVersion); err != nil {
		return CommandRecipe{}, err
	}
	spec := origin.PackageID + "@" + targetVersion
	args := []string{"install"}
	if scope == "cordcode-prefix" {
		if origin.Prefix == "" {
			return CommandRecipe{}, fmt.Errorf("npm prefix install requires its exact prefix")
		}
		args = append(args, "--prefix", origin.Prefix)
	} else {
		args = append(args, "-g")
	}
	args = append(args, spec)
	return CommandRecipe{
		Executable:  origin.ManagerPath,
		Args:        args,
		Environment: pathEnvironment(origin.NodeBinDir),
	}.withDisplay(), nil
}

// BuildGrokUpdateRecipe uses the already-located official Grok executable. The
// command is only displayed for copying; it is never run by this service.
func BuildGrokUpdateRecipe(artifactPath, targetVersion string) (CommandRecipe, error) {
	if artifactPath == "" {
		return CommandRecipe{}, fmt.Errorf("grok update requires the located grok executable")
	}
	if err := validateTargetVersion(targetVersion); err != nil {
		return CommandRecipe{}, err
	}
	return CommandRecipe{
		Executable: artifactPath,
		Args:       []string{"update", "--version", targetVersion},
	}.withDisplay(), nil
}

func BuildInstallGuidance(product Product, npmPath string) InstallGuidance {
	guidance := InstallGuidance{DocumentationURL: product.DocumentationURL}
	switch product.ID {
	case "dsh-web", "opencode-web":
		if npmPath == "" {
			guidance.Reason = "node/npm was not found in the runtime search path"
			return guidance
		}
		recipe := CommandRecipe{
			Executable: npmPath,
			Args:       []string{"install", "-g", product.NPMPackage},
			Environment: []CommandEnvironment{{
				Name: "PATH", Value: filepath.Dir(npmPath),
			}},
		}.withDisplay()
		guidance.Recipe = &recipe
	case "grokbuild":
		recipe := CommandRecipe{
			Executable: "/bin/sh",
			Args:       []string{"-c", "curl -fsSL https://x.ai/cli/install.sh | bash"},
		}.withDisplay()
		guidance.Recipe = &recipe
	default:
		guidance.Reason = "use the product's official installer or documentation"
	}
	return guidance
}

func validateTargetVersion(value string) error {
	if _, err := semver.StrictNewVersion(strings.TrimPrefix(value, "v")); err != nil {
		return fmt.Errorf("target version %q: %w", value, err)
	}
	return nil
}

func pathEnvironment(binDir string) []CommandEnvironment {
	if binDir == "" {
		return nil
	}
	return []CommandEnvironment{{Name: "PATH", Value: binDir}}
}

// SortedRecipeEnvironment is used by tests and future serializers when a
// deterministic environment order is required.
func SortedRecipeEnvironment(recipe CommandRecipe) []CommandEnvironment {
	out := append([]CommandEnvironment(nil), recipe.Environment...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
