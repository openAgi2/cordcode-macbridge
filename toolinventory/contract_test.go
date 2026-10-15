package toolinventory

import "testing"

func TestProductCatalogMatchesMacUIContract(t *testing.T) {
	got := make([]string, 0, len(products))
	for _, product := range Products() {
		got = append(got, product.ID)
	}
	want := []string{"claude", "codex-remote", "dsh-web", "grokbuild", "opencode-web"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
