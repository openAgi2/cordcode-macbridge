import Foundation

/// Product lineup, not the Go plugin registry. Keep IDs in contract with toolinventory.Catalog.
enum ProductAgentCatalog {
    struct Product: Identifiable, Equatable {
        let id: String
        let name: String
        let kind: String
        let documentationURL: URL
    }

    static let products: [Product] = [
        Product(id: "claude", name: "Claude Code", kind: "claude_code", documentationURL: URL(string: "https://code.claude.com/docs/en/setup")!),
        Product(id: "codex-remote", name: "Codex Desktop", kind: "codex-remote", documentationURL: URL(string: "https://chatgpt.com/download")!),
        Product(id: "dsh-web", name: "DeepSeek Harness", kind: "deepseek-web", documentationURL: URL(string: "https://github.com/deepseek-ai/deepseek-harness")!),
        Product(id: "grokbuild", name: "Grok Build", kind: "grokbuild", documentationURL: URL(string: "https://github.com/xai-org/grok-build")!),
        Product(id: "opencode-web", name: "OpenCode Web", kind: "opencode-web", documentationURL: URL(string: "https://opencode.ai/docs/")!),
    ]
    static let ids = products.map(\.id)
}
