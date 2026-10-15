package toolinventory

// Product is the Mac product lineup, deliberately independent from the Go plugin registry.
type Product struct {
	ID         string
	Kind       string
	NPMPackage string
}

var products = []Product{
	{ID: "claude", Kind: "claude_code", NPMPackage: "@anthropic-ai/claude-code"},
	{ID: "codex-remote", Kind: "codex-remote"},
	{ID: "dsh-web", Kind: "deepseek-web", NPMPackage: "@deepseek-ai/dsh"},
	{ID: "grokbuild", Kind: "grokbuild"},
	{ID: "opencode-web", Kind: "opencode-web", NPMPackage: "opencode-ai"},
}

// Products returns the supported product IDs in stable UI order.
func Products() []Product {
	out := make([]Product, len(products))
	copy(out, products)
	return out
}

// ProductByID returns the catalog entry and whether the ID belongs to the product lineup.
func ProductByID(id string) (Product, bool) {
	for _, product := range products {
		if product.ID == id {
			return product, true
		}
	}
	return Product{}, false
}
