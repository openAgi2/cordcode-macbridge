import Foundation
import Security

private let algorithmName = "ecdsa_p256_sha256"
private let keyPrefix = "org.openagi.cordcode.codex-remote.phase0."

private func emit(_ value: [String: Any]) {
    let data = try! JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([0x0a]))
}

private func fail(_ message: String) -> Never {
    emit(["ok": false, "error": message])
    exit(1)
}

private func secError(_ error: Unmanaged<CFError>?) -> String {
    guard let error else { return "unknown Security.framework error" }
    return CFErrorCopyDescription(error.takeRetainedValue()) as String
}

private func tagData(_ keyID: String) -> Data {
    Data(keyID.utf8)
}

private func lookupPrivateKey(_ keyID: String) -> SecKey {
    var item: CFTypeRef?
    let status = SecItemCopyMatching([
        kSecClass: kSecClassKey,
        kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
        kSecAttrApplicationTag: tagData(keyID),
        kSecReturnRef: true,
    ] as CFDictionary, &item)
    guard status == errSecSuccess, let key = item as! SecKey? else {
        fail("device key lookup failed with OSStatus \(status)")
    }
    return key
}

private func spkiDER(_ publicKey: SecKey) -> Data {
    var error: Unmanaged<CFError>?
    guard let raw = SecKeyCopyExternalRepresentation(publicKey, &error) as Data? else {
        fail("public key export failed: \(secError(error))")
    }
    guard raw.count == 65, raw.first == 0x04 else {
        fail("unexpected P-256 public key representation")
    }
    let prefix: [UInt8] = [
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce,
        0x3d, 0x02, 0x01, 0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d,
        0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
    ]
    return Data(prefix) + raw
}

private func createKey() {
    let keyID = keyPrefix + UUID().uuidString.lowercased()
    let access = SecAccessControlCreateWithFlags(
        nil,
        kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        [.privateKeyUsage],
        nil
    )!
    func attributes(secureEnclave: Bool) -> CFDictionary {
        var privateAttributes: [CFString: Any] = [
            kSecAttrIsPermanent: true,
            kSecAttrApplicationTag: tagData(keyID),
            kSecAttrIsExtractable: false,
        ]
        if secureEnclave {
            privateAttributes[kSecAttrAccessControl] = access
        } else {
            // The probe helper is not an App Store bundle and therefore cannot use the
            // data-protection keychain access group expected by the ChatGPT addon.
            // A nonextractable permanent key in the user's login keychain is the
            // bounded Phase 0 fallback; production policy remains a later Gate item.
            privateAttributes[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlock
        }
        var result: [CFString: Any] = [
            kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits: 256,
            kSecPrivateKeyAttrs: privateAttributes,
        ]
        if secureEnclave { result[kSecAttrTokenID] = kSecAttrTokenIDSecureEnclave }
        return result as CFDictionary
    }
    var enclaveError: Unmanaged<CFError>?
    var key = SecKeyCreateRandomKey(attributes(secureEnclave: true), &enclaveError)
    var protectionClass = "hardware_secure_enclave"
    if key == nil {
        var fallbackError: Unmanaged<CFError>?
        key = SecKeyCreateRandomKey(attributes(secureEnclave: false), &fallbackError)
        protectionClass = "os_protected_nonextractable"
        if key == nil {
            fail("key creation failed; enclave=\(secError(enclaveError)); fallback=\(secError(fallbackError))")
        }
    }
    let publicKey = SecKeyCopyPublicKey(key!)!
    emit([
        "ok": true,
        "keyId": keyID,
        "publicKeySpkiDerBase64": spkiDER(publicKey).base64EncodedString(),
        "algorithm": algorithmName,
        "protectionClass": protectionClass,
    ])
}

private func sign(_ request: [String: Any]) {
    guard let keyID = request["keyId"] as? String,
          keyID.hasPrefix(keyPrefix),
          let payloadBase64 = request["payloadBase64"] as? String,
          let payload = Data(base64Encoded: payloadBase64) else {
        fail("invalid sign request")
    }
    let key = lookupPrivateKey(keyID)
    var error: Unmanaged<CFError>?
    guard let signature = SecKeyCreateSignature(
        key,
        .ecdsaSignatureMessageX962SHA256,
        payload as CFData,
        &error
    ) as Data? else {
        fail("device key sign failed: \(secError(error))")
    }
    emit(["ok": true, "algorithm": algorithmName, "signatureDerBase64": signature.base64EncodedString()])
}

private func deleteKey(_ request: [String: Any]) {
    guard let keyID = request["keyId"] as? String, keyID.hasPrefix(keyPrefix) else {
        fail("invalid delete request")
    }
    let status = SecItemDelete([
        kSecClass: kSecClassKey,
        kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
        kSecAttrApplicationTag: tagData(keyID),
    ] as CFDictionary)
    guard status == errSecSuccess || status == errSecItemNotFound else {
        fail("device key delete failed with OSStatus \(status)")
    }
    emit(["ok": true, "deleted": status == errSecSuccess])
}

guard let line = readLine(),
      let data = line.data(using: .utf8),
      let request = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
      let operation = request["op"] as? String else {
    fail("invalid request")
}

switch operation {
case "create": createKey()
case "sign": sign(request)
case "delete": deleteKey(request)
default: fail("unknown operation")
}
