package toolinventory

// Product is the Mac product lineup, deliberately independent from the Go plugin registry.
type Product struct {
	ID               string
	Kind             string
	NPMPackage       string
	DocumentationURL string
}

var products = []Product{
	{ID: "claude", Kind: "claude_code", NPMPackage: "@anthropic-ai/claude-code", DocumentationURL: "https://code.claude.com/docs/en/setup"},
	{ID: "codex-remote", Kind: "codex-remote", DocumentationURL: "https://chatgpt.com/download"},
	{ID: "dsh-web", Kind: "deepseek-web", NPMPackage: "@deepseek-ai/dsh", DocumentationURL: "https://github.com/deepseek-ai/deepseek-harness"},
	{ID: "grokbuild", Kind: "grokbuild", DocumentationURL: "https://github.com/xai-org/grok-build"},
	{ID: "opencode-web", Kind: "opencode-web", NPMPackage: "opencode-ai", DocumentationURL: "https://opencode.ai/docs/"},
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
