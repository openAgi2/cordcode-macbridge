package toolinventory

import (
	"strings"
	"testing"
)

func TestShellQuoteAlwaysQuotesSpecialBytes(t *testing.T) {
	got := ShellQuote("a'b$HOME`cmd\n")
	want := "'a'\\''b$HOME`cmd\n'"
	if got != want {
		t.Fatalf("ShellQuote() = %q, want %q", got, want)
	}
}

func TestBuildNPMUpdateRecipeUsesExactGlobalScope(t *testing.T) {
	origin := InstallOrigin{
		Method: InstallMethodNPM, PackageID: "opencode-ai",
		Prefix: "/Users/example/tools", ManagerPath: "/Users/example/tools/bin/npm",
		NodeBinDir: "/Users/example/tools/bin",
	}
	recipe, err := BuildNPMUpdateRecipe(origin, "1.19.0", "user-global")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"install", "-g", "opencode-ai@1.19.0"}
	if strings.Join(recipe.Args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args = %v, want %v", recipe.Args, wantArgs)
	}
	if !strings.Contains(recipe.Display, "PATH='/Users/example/tools/bin' '/Users/example/tools/bin/npm'") {
		t.Fatalf("display omitted exact PATH/npm: %s", recipe.Display)
	}
}

func TestBuildNPMUpdateRecipePreservesCordcodePrefix(t *testing.T) {
	origin := InstallOrigin{
		Method: InstallMethodNPM, PackageID: "@deepseek-ai/dsh",
		Prefix: "/tmp/data dsh", ManagerPath: "/tmp/npm bin/npm", NodeBinDir: "/tmp/npm bin",
	}
	recipe, err := BuildNPMUpdateRecipe(origin, "0.2.0-rc.2", "cordcode-prefix")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"install", "--prefix", "/tmp/data dsh", "@deepseek-ai/dsh@0.2.0-rc.2"}
	if strings.Join(recipe.Args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args = %v, want %v", recipe.Args, wantArgs)
	}
	if !strings.Contains(recipe.Display, "'--prefix' '/tmp/data dsh'") {
		t.Fatalf("display omitted quoted prefix: %s", recipe.Display)
	}
}

func TestBuildRecipesRejectInvalidTarget(t *testing.T) {
	origin := InstallOrigin{Method: InstallMethodNPM, PackageID: "opencode-ai", ManagerPath: "/npm"}
	if _, err := BuildNPMUpdateRecipe(origin, "1.2", ""); err == nil {
		t.Fatal("expected strict SemVer rejection")
	}
	if _, err := BuildGrokUpdateRecipe("/grok", "latest"); err == nil {
		t.Fatal("expected strict SemVer rejection")
	}
}

func TestBuildInstallGuidanceUsesOfficialScopedCommands(t *testing.T) {
	dsh, _ := ProductByID("dsh-web")
	dshGuidance := BuildInstallGuidance(dsh, "/tools/bin/npm")
	if dshGuidance.Recipe == nil || dshGuidance.Recipe.Executable != "/tools/bin/npm" {
		t.Fatalf("dsh guidance: %+v", dshGuidance)
	}
	if strings.Join(dshGuidance.Recipe.Args, " ") != "install -g @deepseek-ai/dsh" {
		t.Fatalf("dsh args: %+v", dshGuidance.Recipe.Args)
	}
	if !strings.Contains(dshGuidance.Recipe.Display, "PATH='/tools/bin' '/tools/bin/npm'") {
		t.Fatalf("dsh display: %s", dshGuidance.Recipe.Display)
	}

	grok, _ := ProductByID("grokbuild")
	grokGuidance := BuildInstallGuidance(grok, "")
	if grokGuidance.Recipe == nil || !strings.Contains(grokGuidance.Recipe.Display, "'curl -fsSL https://x.ai/cli/install.sh | bash'") {
		t.Fatalf("grok guidance: %+v", grokGuidance)
	}

	opencode, _ := ProductByID("opencode-web")
	missingNPM := BuildInstallGuidance(opencode, "")
	if missingNPM.Recipe != nil || missingNPM.Reason == "" || missingNPM.DocumentationURL == "" {
		t.Fatalf("missing npm guidance: %+v", missingNPM)
	}

	claude, _ := ProductByID("claude")
	claudeGuidance := BuildInstallGuidance(claude, "/npm")
	if claudeGuidance.Recipe != nil || claudeGuidance.DocumentationURL == "" {
		t.Fatalf("Claude must use official guidance, not an unverified bootstrap: %+v", claudeGuidance)
	}
}
