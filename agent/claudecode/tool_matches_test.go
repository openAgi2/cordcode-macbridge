package claudecode

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestClaudeToolMatchesFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/tool-matches-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, ToolName, Content string
		Want                    *core.ToolMatches
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			got := parseClaudeToolMatches(fixture.ToolName, fixture.Content, false)
			if !reflect.DeepEqual(got, fixture.Want) {
				t.Fatalf("got=%#v want=%#v", got, fixture.Want)
			}
		})
	}
}

func TestClaudeToolMatchesNativeStructuredAndFailure(t *testing.T) {
	count := 2
	want := &core.ToolMatches{Kind: "count", Count: &count}
	got := parseClaudeToolMatches("Search", map[string]any{"matches": map[string]any{"kind": "count", "count": float64(2)}}, false)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
	if parseClaudeToolMatches("Glob", "src/app.ts", true) != nil {
		t.Fatal("failed result exposed matches")
	}
}
