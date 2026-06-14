import Foundation

enum OpenCodeCredentialsGenerator {
    static func generatePassword() -> String {
        UUID().uuidString.lowercased()
    }
}
