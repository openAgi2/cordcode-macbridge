import XCTest
@testable import CordCodeLink

final class ProductAgentCatalogTests: XCTestCase {
    func testProductLineupMatchesReviewedContract() {
        XCTAssertEqual(ProductAgentCatalog.ids, ["claude", "codex-remote", "dsh-web", "grokbuild", "opencode-web"])
        XCTAssertEqual(Set(ProductAgentCatalog.products.map(\.kind)), Set(["claude_code", "codex-remote", "deepseek-web", "grokbuild", "opencode-web"]))
        XCTAssertTrue(ProductAgentCatalog.products.allSatisfy { $0.documentationURL.scheme == "https" })
    }
}
