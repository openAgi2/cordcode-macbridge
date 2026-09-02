import Foundation
import TOML

// MARK: - 路径解析（§3.5 职责①；T9：与 leader_subscriber.go resolveLeaderSocket 逐分支一致）

struct GrokLeaderPaths: Equatable {
    let grokHome: String
    let configPath: String
    let socketPath: String

    /// 链：GROK_LEADER_SOCKET env → $GROK_HOME/leader.sock → ~/.grok/leader.sock。
    /// GROK_HOME：env 优先否则 ~/.grok。只反映 Link 进程实际继承的 env，不做任何探测。
    static func resolve(environment: [String: String]) -> GrokLeaderPaths {
        func trimmedEnv(_ key: String) -> String {
            (environment[key] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        }
        let grokHomeEnv = trimmedEnv("GROK_HOME")
        let home = !grokHomeEnv.isEmpty ? grokHomeEnv : NSHomeDirectory() + "/.grok"
        let socketEnv = trimmedEnv("GROK_LEADER_SOCKET")
        let socket = !socketEnv.isEmpty ? socketEnv : home + "/leader.sock"
        return GrokLeaderPaths(
            grokHome: home,
            configPath: home + "/config.toml",
            socketPath: socket
        )
    }
}

// MARK: - 状态模型（§3.4；T20）

enum GrokLeaderConfigValue: Equatable {
    case absent
    case explicitFalse
    case explicitTrue
}

enum GrokLeaderReadError: Error, Equatable {
    /// F1：读取失败 / TOML 非法 / 类型非法（r8-M7）/ symlink 悬空、循环或非普通目标
    case f1(reason: String)
    /// F2：交叉裁决矩阵判等价形态（§3.3-3/6）
    case f2
}

struct GrokLeaderModeStatus: Equatable {
    var value: GrokLeaderConfigValue?
    var readError: GrokLeaderReadError?
    var socketPresent: Bool
    var paths: GrokLeaderPaths

    var isON: Bool { value == .explicitTrue }
    var isDisabled: Bool { readError != nil }
}

/// agentRow 行内副文案状态机（§3.4：核心三态 / 扩展观察态 / 失败态）。
/// 纯数据——渲染（颜色/hover/alert）在 WorkspaceView，推导在 Manager.rowState。
enum GrokLeaderRowState: Equatable {
    /// #1：OFF（absent），无副文案
    case coreOff
    /// #2：ON + 未检测到 socket——橙色「已开启，重启 grok 后生效」
    case coreOnPendingRestart
    /// #3：ON + 检测到 socket——次级色「检测到 Leader socket，实时推送以运行日志为准」
    case coreOnSocketDetected
    /// #4：无键/false + 检测到 socket——次级色运行痕迹提示
    case observeSocketTrace
    /// #5：explicit false——次级色「已显式关闭（会屏蔽服务器推荐开启）」
    case observeExplicitOff
    /// #6：Link 继承的 env 解析出非默认 socket 路径——中性提示
    case observeCustomSocket(path: String)
    /// F1：config 读取/解析/symlink 失败——开关禁用 + 错误提示
    case failedRead(reason: String)
    /// F2：交叉裁决矩阵判等价形态——开关禁用 + 提示手工处理
    case failedUnsafeForm

    var disablesToggle: Bool {
        switch self {
        case .failedRead, .failedUnsafeForm: return true
        default: return false
        }
    }
}

// MARK: - 语义 parser（§3.3-3：整文档合法性 + cli.use_leader 语义 oracle）

enum GrokLeaderSemanticParser {
    private struct Root: Codable {
        struct CLI: Codable {
            let use_leader: Bool?
        }
        let cli: CLI?
    }

    /// TOMLDecoder 解码只含 `cli.use_leader` 的 struct，多余键忽略（Codable 默认）。
    /// parse 抛错 → F1；`use_leader` 类型非法（字符串/整数/数组）→ DecodingError → F1（T33，
    /// 禁止误判 absent 后追加第二语义键）；键不存在 → nil → absent。
    static func value(in text: String) -> Result<GrokLeaderConfigValue, GrokLeaderReadError> {
        do {
            let root = try TOMLDecoder().decode(Root.self, from: text)
            switch root.cli?.use_leader {
            case .none: return .success(.absent)
            case .some(false): return .success(.explicitFalse)
            case .some(true): return .success(.explicitTrue)
            }
        } catch {
            return .failure(.f1(reason: "TOML 解析失败: \(describeDecodingError(error))"))
        }
    }

    private static func describeDecodingError(_ error: Error) -> String {
        if let decoding = error as? DecodingError,
           case .typeMismatch(_, let ctx) = decoding,
           let path = ctx.codingPath.last?.stringValue, path == "use_leader" {
            return "use_leader 类型非法（应为 Bool）"
        }
        return error.localizedDescription
    }
}

// MARK: - 词法行扫描器（T27–T30：多行字符串/跨行数组/注释状态跟踪）

enum TOMLMultilineKind {
    case basic
    case literal
}

struct TOMLLexicalState: Equatable {
    var multilineString: TOMLMultilineKind?
    var arrayDepth: Int = 0

    var isPlain: Bool { multilineString == nil && arrayDepth == 0 }
}

/// 字符级扫描一行，返回行末词法状态。调用方用「行首状态」决定该行是否可识别为
/// 节头/键值行（在多行字符串或数组中的行内容一律不参与识别，伪 token 不会误判）。
enum TOMLLineScanner {
    static func scanEndState<S: StringProtocol>(_ line: S, initial: TOMLLexicalState) -> TOMLLexicalState {
        var state = initial
        let chars = Array(line.unicodeScalars)
        var i = 0
        while i < chars.count {
            switch state.multilineString {
            case .some(.basic):
                if matchesAt(chars, i, "\"\"\"") {
                    i += 3
                    state.multilineString = nil
                } else if chars[i] == "\\" {
                    i += 2
                } else {
                    i += 1
                }
            case .some(.literal):
                if matchesAt(chars, i, "'''") {
                    i += 3
                    state.multilineString = nil
                } else {
                    i += 1
                }
            case .none:
                let c = chars[i]
                if c == "\"" {
                    if matchesAt(chars, i, "\"\"\"") {
                        state.multilineString = .basic
                        i += 3
                    } else {
                        i = skipBasicString(chars, from: i + 1)
                    }
                } else if c == "'" {
                    if matchesAt(chars, i, "'''") {
                        state.multilineString = .literal
                        i += 3
                    } else {
                        i = skipLiteralString(chars, from: i + 1)
                    }
                } else if c == "#" {
                    return state
                } else if c == "[" {
                    state.arrayDepth += 1
                    i += 1
                } else if c == "]" {
                    if state.arrayDepth > 0 { state.arrayDepth -= 1 }
                    i += 1
                } else {
                    i += 1
                }
            }
        }
        return state
    }

    private static func matchesAt(_ chars: [Unicode.Scalar], _ index: Int, _ token: String) -> Bool {
        let tokenScalars = Array(token.unicodeScalars)
        guard index + tokenScalars.count <= chars.count else { return false }
        for (offset, tc) in tokenScalars.enumerated() where chars[index + offset] != tc {
            return false
        }
        return true
    }

    /// basic string 内容：处理 \\ 与 \" 转义。返回闭合引号后的位置（行尾未闭合则 count，
    /// 该情形属非法 TOML，由语义 parser 兜底 F1）。
    private static func skipBasicString(_ chars: [Unicode.Scalar], from start: Int) -> Int {
        var i = start
        while i < chars.count {
            if chars[i] == "\\" {
                i += 2
            } else if chars[i] == "\"" {
                return i + 1
            } else {
                i += 1
            }
        }
        return i
    }

    /// literal string 内容：无转义。
    private static func skipLiteralString(_ chars: [Unicode.Scalar], from start: Int) -> Int {
        var i = start
        while i < chars.count {
            if chars[i] == "'" {
                return i + 1
            }
            i += 1
        }
        return i
    }

    /// 同一遍 Character 级扫描，返回 plain 状态下首个 `=` 与首个 `#` 的索引。
    /// 越过字符串内的 `=`/`#`、三引号与转义；键帽组合等 Unicode 簇天然安全。
    static func plainMarkers<S: StringProtocol>(in line: S) -> (equals: String.Index?, comment: String.Index?) {
        var state = TOMLLexicalState()
        var i = line.startIndex
        var eq: String.Index? = nil
        while i < line.endIndex {
            let c = line[i]
            switch state.multilineString {
            case .some(.basic):
                if c == "\"" {
                    let run = quoteRun(at: i, in: line, quote: "\"")
                    if run >= 3 { state.multilineString = nil; i = advance(i, in: line, by: 3) }
                    else { i = advance(i, in: line, by: max(run, 1)) }
                } else if c == "\\" {
                    i = advance(i, in: line, by: 2)
                } else {
                    i = line.index(after: i)
                }
            case .some(.literal):
                if c == "'" {
                    let run = quoteRun(at: i, in: line, quote: "'")
                    if run >= 3 { state.multilineString = nil; i = advance(i, in: line, by: 3) }
                    else { i = advance(i, in: line, by: max(run, 1)) }
                } else {
                    i = line.index(after: i)
                }
            case .none:
                if c == "\"" {
                    let run = quoteRun(at: i, in: line, quote: "\"")
                    if run >= 3 { state.multilineString = .basic; i = advance(i, in: line, by: 3) }
                    else if run >= 2 { i = advance(i, in: line, by: run) }
                    else { i = endOfBasicString(from: i, in: line) }
                } else if c == "'" {
                    let run = quoteRun(at: i, in: line, quote: "'")
                    if run >= 3 { state.multilineString = .literal; i = advance(i, in: line, by: 3) }
                    else if run >= 2 { i = advance(i, in: line, by: run) }
                    else { i = endOfLiteralString(from: i, in: line) }
                } else if c == "#" {
                    return (eq, i)
                } else {
                    if eq == nil, c == "=" { eq = i }
                    if c == "[" { state.arrayDepth += 1 }
                    else if c == "]", state.arrayDepth > 0 { state.arrayDepth -= 1 }
                    i = line.index(after: i)
                }
            }
        }
        return (eq, nil)
    }

    /// 在 plain 状态下定位首个 `#`（行内注释起点）；返回 nil = 无注释。
    static func firstPlainComment<S: StringProtocol>(in line: S) -> String.Index? {
        plainMarkers(in: line).comment
    }

    private static func quoteRun<S: StringProtocol>(at i: String.Index, in line: S, quote: Character) -> Int {
        var j = i
        var n = 0
        while j < line.endIndex, line[j] == quote {
            n += 1
            j = line.index(after: j)
        }
        return n
    }

    private static func advance<S: StringProtocol>(_ i: String.Index, in line: S, by count: Int) -> String.Index {
        var j = i
        var left = count
        while left > 0, j < line.endIndex {
            j = line.index(after: j)
            left -= 1
        }
        return j
    }

    private static func endOfBasicString<S: StringProtocol>(from open: String.Index, in line: S) -> String.Index {
        var i = line.index(after: open)
        while i < line.endIndex {
            if line[i] == "\\" {
                i = advance(i, in: line, by: 2)
            } else if line[i] == "\"" {
                return line.index(after: i)
            } else {
                i = line.index(after: i)
            }
        }
        return i
    }

    private static func endOfLiteralString<S: StringProtocol>(from open: String.Index, in line: S) -> String.Index {
        var i = line.index(after: open)
        while i < line.endIndex {
            if line[i] == "'" { return line.index(after: i) }
            i = line.index(after: i)
        }
        return i
    }
}

// MARK: - canonical locator（§3.3-3：保守词法扫描，供外科编辑与删键定位）

enum GrokLeaderLocatorResult: Equatable {
    /// 无 [cli] 节：开 = 文件末尾追加节头+键
    case noSection
    /// 有 [cli] 节、节内无 canonical use_leader 行：开 = 节内（下一节头行前）追加；
    /// insertBeforeLine = nil 表示 [cli] 是文档最后一节（追加到文档末尾）
    case sectionWithoutKey(sectionHeaderLine: Int, insertBeforeLine: Int?)
    /// 恰一 canonical 行：开 = 原位替换值，关 = 删该行
    case singleKey(line: Int)
    /// 多个 canonical 行：TOML 重复键非法（parser F1 兜底）；矩阵内按歧义 F2 处理
    case multipleKeys
    /// 等价形态（如 `[ cli ]` 带空白节头）——拒绝写入
    case ambiguous
}

enum GrokLeaderLocator {

    struct LineInfo {
        let content: Substring
        let start: String.Index
        let end: String.Index
        /// 换行符长度（2 = \r\n，1 = \n，0 = 文件末行无换行）
        let newlineLength: Int
        var lineEnd: String.Index { end }
    }

    static func splitLines(_ text: String) -> [LineInfo] {
        var result: [LineInfo] = []
        var lineStart = text.startIndex
        var index = text.startIndex
        while index < text.endIndex {
            let ch = text[index]
            // Character 是字素簇：CRLF 是单个 Character "\r\n"，不等于 "\r" 或 "\n"
            if ch == "\r" || ch == "\n" || ch == "\r\n" {
                let newlineLength = ch == "\r\n" ? 2 : 1
                let next = text.index(after: index)
                result.append(LineInfo(content: text[lineStart..<index], start: lineStart, end: index, newlineLength: newlineLength))
                index = next
                lineStart = next
            } else {
                index = text.index(after: index)
            }
        }
        if lineStart < text.endIndex {
            result.append(LineInfo(content: text[lineStart...], start: lineStart, end: text.endIndex, newlineLength: 0))
        }
        return result
    }

    /// 逐行扫描，跟踪多行字符串/数组状态；仅在 plain 状态识别节头与 canonical 键值行。
    /// "其他形式定义了 cli 表"（`[ cli ]` 空白节头、`[cli.child]`、`[[cli]]`、顶层
    /// `cli = …` / `cli.x = …` 点键或 inline table）→ 无严格 `[cli]` 节头时判 ambiguous，
    /// 因为追加新 `[cli]` 节会与既有定义冲突（等价形态 F2，不猜测）。
    static func locate(text: String) -> GrokLeaderLocatorResult {
        let lines = splitLines(text)
        var state = TOMLLexicalState()
        var currentSection: String? = nil
        var strictCliHeader: Int? = nil
        var canonicalLines: [Int] = []
        /// [cli] 节边界：终止该节的下一节头行号（nil = 尚未遇到或 [cli] 是最后一节）
        var cliBoundaryLine: Int? = nil
        var cliDefinedByOtherForm = false

        for (index, info) in lines.enumerated() {
            let lineStartState = state
            state = TOMLLineScanner.scanEndState(info.content, initial: state)
            guard lineStartState.isPlain else { continue }

            let trimmed = info.content.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }

            if trimmed.hasPrefix("[") {
                guard let header = parseSectionHeader(trimmed) else { continue }
                if strictCliHeader != nil, cliBoundaryLine == nil {
                    // 任何节头（顶层表/子表/数组表）都终止 [cli] 节（r8-M4）
                    cliBoundaryLine = index
                }
                currentSection = header.canonicalName
                if !header.isStrictCLI, header.definesCLITable {
                    cliDefinedByOtherForm = true
                }
                if header.isStrictCLI, strictCliHeader == nil {
                    strictCliHeader = index
                    cliBoundaryLine = nil
                }
                continue
            }

            if strictCliHeader != nil, currentSection == "cli",
               cliBoundaryLine == nil || index < cliBoundaryLine!,
               let key = parseCanonicalKey(trimmed), key == "use_leader" {
                canonicalLines.append(index)
            } else if strictCliHeader == nil, currentSection == nil,
                      keyDefinesCLITable(trimmed) {
                cliDefinedByOtherForm = true
            }
        }

        if strictCliHeader == nil {
            return cliDefinedByOtherForm ? .ambiguous : .noSection
        }
        switch canonicalLines.count {
        case 0:
            return .sectionWithoutKey(sectionHeaderLine: strictCliHeader!, insertBeforeLine: cliBoundaryLine)
        case 1:
            return .singleKey(line: canonicalLines[0])
        default:
            return .multipleKeys
        }
    }

    private struct SectionHeader {
        /// 节名（点路径段以 "." 连接；数组表与同名表不区分——重复定义由语义 parser F1 兜底）
        let canonicalName: String
        /// 仅 `[cli]`（内部无空白、无引号、非数组表）为严格 canonical
        let isStrictCLI: Bool
        /// 语义上定义/参与了 cli 表（含子表头、数组表头、quoted 段）
        let definesCLITable: Bool
    }

    /// 节头解析：`[name]` / `[[name]]`，余下部分必须为空或注释（否则无法分类，交 parser 兜底）。
    private static func parseSectionHeader(_ trimmed: String) -> SectionHeader? {
        var rest = Substring(trimmed).dropFirst()
        var isArrayTable = false
        if rest.hasPrefix("[") {
            isArrayTable = true
            rest = rest.dropFirst()
        }
        guard let close = rest.firstIndex(of: "]") else { return nil }
        let inner = String(rest[..<close])
        var after = rest[rest.index(after: close)...]
        if isArrayTable {
            guard after.hasPrefix("]") else { return nil }
            after = after.dropFirst()
        }
        let afterTrimmed = after.trimmingCharacters(in: .whitespaces)
        if !afterTrimmed.isEmpty && !afterTrimmed.hasPrefix("#") { return nil }

        let innerTrimmed = inner.trimmingCharacters(in: .whitespaces)
        let segments = innerTrimmed
            .split(separator: ".", omittingEmptySubsequences: false)
            .map { $0.trimmingCharacters(in: .whitespaces) }
        let firstName = stripQuotes(segments.first ?? "")
        return SectionHeader(
            canonicalName: segments.joined(separator: "."),
            isStrictCLI: inner == "cli" && !isArrayTable,
            definesCLITable: firstName == "cli"
        )
    }

    /// 顶层键行是否定义了 cli 表：首段为 `cli` 的点键（`cli.x = …`，含 quoted 段），
    /// 或裸键 `cli = …`（值可为 inline table——`[cli]` 节头不能再追加定义）。
    private static func keyDefinesCLITable(_ trimmed: String) -> Bool {
        guard let eq = TOMLLineScanner.plainMarkers(in: trimmed).equals else { return false }
        let keyPart = String(trimmed[trimmed.startIndex..<eq])
        let first = stripQuotes(
            keyPart.split(separator: ".", omittingEmptySubsequences: false).first.map(String.init) ?? ""
        )
        return first == "cli"
    }

    private static func stripQuotes(_ s: String) -> String {
        if s.count >= 2, let f = s.first, let l = s.last, f == l, f == "\"" || f == "'" {
            return String(s.dropFirst().dropLast())
        }
        return s
    }

    /// canonical 赋值行：裸键 `use_leader`（无引号无点）后仅空白接 `=`，且有值。
    /// 返回 nil = 非 canonical（点键/quoted key/其他键），语义等价形态由矩阵 F2 拒绝。
    private static func parseCanonicalKey<S: StringProtocol>(_ trimmed: S) -> String? {
        var chars = Substring(trimmed)
        while let first = chars.first, first.isWhitespace { chars = chars.dropFirst() }
        var key = ""
        while let first = chars.first,
              first.isLetter || first.isNumber || first == "_" || first == "-" {
            key.append(first)
            chars = chars.dropFirst()
        }
        guard !key.isEmpty else { return nil }
        while let first = chars.first, first.isWhitespace { chars = chars.dropFirst() }
        guard chars.first == "=" else { return nil }
        let after = chars.dropFirst().drop { $0.isWhitespace }
        guard !after.isEmpty, !after.hasPrefix("#") else { return nil }
        return key
    }
}

// MARK: - 交叉裁决矩阵（§3.3-3；编辑前与写后校验共用）

enum GrokLeaderCrossVerdict: Equatable {
    /// 语义 absent + 无 canonical 行：安全（节内追加或新建节）
    case safeAppend
    /// 语义 absent + 有 canonical 行：矛盾（语法异常）→ F1
    case contradiction
    /// 语义有值 + 恰一 canonical 行：原位编辑
    case inPlaceEdit
    /// 语义有值 + 无/多/歧义：等价形态 → F2
    case equivalentForm
}

enum GrokLeaderCrossMatrix {
    /// §3.3-3 四格矩阵（歧义形态按"无法完整分类 → 拒绝"一律 F2，不猜测）。
    static func verdict(
        semantic: GrokLeaderConfigValue,
        locator: GrokLeaderLocatorResult
    ) -> GrokLeaderCrossVerdict {
        switch locator {
        case .ambiguous:
            return .equivalentForm
        case .noSection, .sectionWithoutKey:
            return semantic == .absent ? .safeAppend : .equivalentForm
        case .singleKey:
            return semantic == .absent ? .contradiction : .inPlaceEdit
        case .multipleKeys:
            return semantic == .absent ? .contradiction : .equivalentForm
        }
    }
}

// MARK: - 外科文本编辑（§3.3-1/2：禁止 parse→serialize 往返）

enum GrokLeaderConfigEditError: Error, Equatable {
    case f1(reason: String)
    case f2
}

enum GrokLeaderConfigFileEditor {

    /// 开 = 写 `use_leader = true`（原位替换 / 节内追加 / 新建节）。
    /// 返回 nil 表示已处于目标状态（幂等无操作，不产生写入）。
    static func enabledContent(from original: String) throws -> String? {
        try edit(original: original, target: .explicitTrue)
    }

    /// 关 = 删键（不写 false；§3.3-2；false→OFF 同样删键，T16）。
    /// 返回 nil = 幂等无操作（absent 时）。
    static func disabledContent(from original: String) throws -> String? {
        try edit(original: original, target: .absent)
    }

    private enum Target { case explicitTrue, absent }

    private static func edit(original: String, target: Target) throws -> String? {
        let semantic: GrokLeaderConfigValue
        switch GrokLeaderSemanticParser.value(in: original) {
        case .failure(let err):
            throw GrokLeaderConfigEditError.f1(reason: readableReadError(err))
        case .success(let value): semantic = value
        }
        let locator = GrokLeaderLocator.locate(text: original)
        // 矩阵先于幂等短路（§3.3-3：编辑前必须四格判定）——等价形态即使语义已达标也 F2 拒绝
        let verdict = GrokLeaderCrossMatrix.verdict(semantic: semantic, locator: locator)
        switch (target, verdict) {
        case (_, .contradiction):
            throw GrokLeaderConfigEditError.f1(reason: "语义与词法定位矛盾（语法异常）")
        case (_, .equivalentForm):
            throw GrokLeaderConfigEditError.f2
        case (.explicitTrue, .safeAppend):
            return appendTrue(original: original, locator: locator)
        case (.explicitTrue, .inPlaceEdit):
            if semantic == .explicitTrue { return nil }
            return try replaceInPlace(original: original, locator: locator, newValue: "true")
        case (.absent, .safeAppend):
            return nil
        case (.absent, .inPlaceEdit):
            return try removeKeyLine(original: original, locator: locator)
        }
    }

    static func readableReadError(_ err: GrokLeaderReadError) -> String {
        switch err {
        case .f1(let reason): return reason
        case .f2: return "配置含 CordCode 无法安全管理的 use_leader 写法"
        }
    }

    // MARK: 编辑原语

    private static func dominantNewline(in text: String) -> String {
        let crlfCount = text.components(separatedBy: "\r\n").count - 1
        let lfOnly = text.replacingOccurrences(of: "\r\n", with: "").components(separatedBy: "\n").count - 1
        return crlfCount > lfOnly ? "\r\n" : "\n"
    }

    private static func ensureTrailingNewline(_ text: String, _ newline: String) -> String {
        guard !text.isEmpty else { return text }
        // hasSuffix("\n") 对 CRLF 结尾为 false（"\r\n" 是单个字素簇），须按末字符判定
        if let last = text.last, last == "\n" || last == "\r" || last == "\r\n" { return text }
        return text + newline
    }

    private static func appendTrue(original: String, locator: GrokLeaderLocatorResult) -> String {
        let newline = dominantNewline(in: original)
        let entry = "use_leader = true"
        switch locator {
        case .sectionWithoutKey(_, let insertBeforeLine):
            let lines = GrokLeaderLocator.splitLines(original)
            guard let beforeLine = insertBeforeLine, beforeLine < lines.count else {
                // [cli] 是文档最后一节：末尾追加（保持换行完整，T32）
                return ensureTrailingNewline(original, newline) + entry + newline
            }
            let insertAt = lines[beforeLine].start
            let prefix = ensureTrailingNewline(String(original[..<insertAt]), newline)
            return prefix + entry + newline + String(original[insertAt...])
        case .noSection:
            var text = ensureTrailingNewline(original, newline)
            // 与既有内容之间补一个空行（无既有内容则不补）
            if !text.isEmpty && !text.hasSuffix(newline + newline) {
                text += newline
            }
            return text + "[cli]" + newline + entry + newline
        case .singleKey, .multipleKeys, .ambiguous:
            // 矩阵已在 edit() 拦截；防御性返回原文
            return original
        }
    }

    private static func replaceInPlace(original: String, locator: GrokLeaderLocatorResult, newValue: String) throws -> String {
        guard case .singleKey(let lineIndex) = locator else {
            throw GrokLeaderConfigEditError.f2
        }
        let lines = GrokLeaderLocator.splitLines(original)
        guard lineIndex < lines.count else {
            throw GrokLeaderConfigEditError.f1(reason: "定位越界")
        }
        let lineText = String(lines[lineIndex].content)
        guard let eqRange = plainEqualsRange(in: lineText) else {
            throw GrokLeaderConfigEditError.f1(reason: "canonical 行缺少 =")
        }
        let after = lineText[lineText.index(after: eqRange.lowerBound)...]
        // 值范围：首个非空白字符起，到行内注释（plain #）前、去尾随空白（T3/T14：注释保留）
        var valueStart = after.startIndex
        while valueStart < after.endIndex, after[valueStart].isWhitespace {
            valueStart = after.index(after: valueStart)
        }
        guard valueStart < after.endIndex else {
            throw GrokLeaderConfigEditError.f1(reason: "canonical 行缺少值")
        }
        let commentStart = TOMLLineScanner.firstPlainComment(in: after)
        var valueEnd = commentStart ?? after.endIndex
        while valueEnd > valueStart, after[after.index(before: valueEnd)].isWhitespace {
            valueEnd = after.index(before: valueEnd)
        }
        var newLine = String(lineText[..<valueStart])
        newLine += newValue
        if let cs = commentStart {
            // 保留注释前至少一个空白
            if valueEnd == cs, cs < after.endIndex, !after[cs].isWhitespace {
                newLine += " "
            } else if valueEnd < cs {
                newLine += String(after[valueEnd..<cs])
            }
            newLine += String(after[cs...])
        } else {
            newLine += String(after[valueEnd...])
        }
        let lineInfo = lines[lineIndex]
        var result = String(original[..<lineInfo.start])
        result += newLine
        result += String(original[lineInfo.end...])
        return result
    }

    private static func removeKeyLine(original: String, locator: GrokLeaderLocatorResult) throws -> String {
        guard case .singleKey(let lineIndex) = locator else {
            throw GrokLeaderConfigEditError.f2
        }
        let lines = GrokLeaderLocator.splitLines(original)
        guard lineIndex < lines.count else {
            throw GrokLeaderConfigEditError.f1(reason: "定位越界")
        }
        let info = lines[lineIndex]
        // 删整行含换行符（末行无换行则只删内容）；[cli] 节头一律保留（§3.3-2，T5）。
        // 换行符（含 CRLF）恰好占一个字素簇，index(after:) 步进 1 即越过
        var lineEnd = info.end
        if info.newlineLength > 0 {
            lineEnd = original.index(after: lineEnd)
        }
        var result = String(original[..<info.start])
        result += String(original[lineEnd...])
        return result
    }

    /// plain 状态下首个 `=` 的位置（越过字符串/注释；canonical 键部分不含引号）。
    private static func plainEqualsRange<S: StringProtocol>(in line: S) -> Range<String.Index>? {
        TOMLLineScanner.plainMarkers(in: line).equals.map { $0..<line.index(after: $0) }
    }
}

// MARK: - symlink 身份模型（§3.3-4）

struct GrokLeaderResolvedTarget {
    let canonicalPath: String
    let inode: UInt64
    let device: UInt32
    let mode: mode_t
}

enum GrokLeaderSymlinkResolver {
    enum ResolveError: Error, Equatable {
        case f1(reason: String)
    }

    /// readlink 沿链最多 8 级；相对链接按链接文件所在目录解析（POSIX 语义）；
    /// 最终目标必须是普通文件，否则（目录/FIFO/socket/悬空/循环/超深）→ F1。
    static func resolve(path: String) throws -> GrokLeaderResolvedTarget {
        var current = path
        var hops = 0
        while true {
            var st = stat()
            let lstatOK = current.withCString { lstat($0, &st) == 0 }
            guard lstatOK else {
                throw ResolveError.f1(reason: "目标不存在或不可访问")
            }
            if (st.st_mode & S_IFMT) == S_IFLNK {
                hops += 1
                if hops > 8 {
                    throw ResolveError.f1(reason: "symlink 链超过 8 级")
                }
                var buffer = [CChar](repeating: 0, count: Int(PATH_MAX) + 1)
                let length = current.withCString { cPath in
                    readlink(cPath, &buffer, buffer.count - 1)
                }
                guard length > 0 else {
                    throw ResolveError.f1(reason: "symlink 读取失败")
                }
                buffer[length] = 0
                let dest = String(cString: buffer)
                if dest.hasPrefix("/") {
                    current = dest
                } else {
                    let dir = (current as NSString).deletingLastPathComponent
                    current = dir.isEmpty ? dest : dir + "/" + dest
                }
                continue
            }
            guard (st.st_mode & S_IFMT) == S_IFREG else {
                throw ResolveError.f1(reason: "目标不是普通文件")
            }
            return GrokLeaderResolvedTarget(
                canonicalPath: current,
                inode: UInt64(st.st_ino),
                device: UInt32(st.st_dev),
                mode: mode_t(st.st_mode & 0o7777)
            )
        }
    }
}

/// 读路径的存在性分类：区分「文件确实不存在」（absent，安全新建）与
/// 「路径存在但无法解析为普通文件」（悬空/循环/非普通 → F1）。
/// 不做该区分会把悬空 symlink 当 absent，rename 时替换链接本身，违反 §3.3-4。
enum GrokLeaderConfigPresence: Equatable {
    case regularFile(canonicalPath: String)
    case absent
    case invalid(reason: String)
}

extension GrokLeaderSymlinkResolver {
    static func classify(path: String) -> GrokLeaderConfigPresence {
        do {
            let target = try resolve(path: path)
            return .regularFile(canonicalPath: target.canonicalPath)
        } catch {
            var st = stat()
            let rc = path.withCString { lstat($0, &st) }
            if rc != 0 && errno == ENOENT { return .absent }
            return .invalid(reason: "config 路径无法解析为普通文件（悬空/循环/非普通文件）")
        }
    }
}

// MARK: - 备份存储（§3.3-10：crash-safe 顺序 + exclusive create）

struct GrokConfigBackupStore {
    let directory: URL

    enum BackupError: Error, Equatable {
        case convergeFailed(reason: String)
        case createFailed(reason: String)
    }

    static func defaultDirectory(appSupport: URL) -> URL {
        appSupport.appendingPathComponent("GrokConfigBackups", isDirectory: true)
    }

    func ensureDirectory() throws {
        var isDir: ObjCBool = false
        if FileManager.default.fileExists(atPath: directory.path, isDirectory: &isDir) {
            guard isDir.boolValue else {
                throw BackupError.convergeFailed(reason: "备份路径被非目录占用")
            }
        } else {
            do {
                try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            } catch {
                throw BackupError.convergeFailed(reason: "备份目录创建失败: \(error.localizedDescription)")
            }
        }
    }

    /// 先收敛到 ≤2 份（删最旧）。失败 fail-closed：抛错，调用方不动原文件。
    func convergeToAtMostTwo() throws {
        try ensureDirectory()
        let entries = try existingBackups()
        guard entries.count > 2 else { return }
        let sorted = entries.sorted { $0.key < $1.key }
        for stale in sorted.prefix(entries.count - 2) {
            do {
                try FileManager.default.removeItem(at: stale.url)
            } catch {
                throw BackupError.convergeFailed(reason: "删除旧备份失败: \(stale.url.lastPathComponent)")
            }
        }
    }

    /// 创建本轮备份（0600，exclusive create：已存在即换新名重试，绝不覆盖既有备份）。
    func createBackup(
        data: Data,
        clock: () -> (seconds: Int64, nanoseconds: Int32) = defaultClock,
        uuid: () -> UUID = UUID.init
    ) throws -> URL {
        try ensureDirectory()
        for _ in 0..<8 {
            let ts = clock()
            let name = String(format: "%lld.%09d-%@.tomlbak", ts.seconds, ts.nanoseconds, uuid().uuidString)
            let target = directory.appendingPathComponent(name)
            let fd = target.path.withCString { cPath in
                open(cPath, O_WRONLY | O_CREAT | O_EXCL, 0o600)
            }
            guard fd >= 0 else {
                let errnoNow = errno
                if errnoNow == EEXIST { continue }
                throw BackupError.createFailed(reason: "创建备份失败 errno=\(errnoNow)")
            }
            defer { close(fd) }
            var offset = 0
            var writeFailed = false
            while offset < data.count {
                let written = data.withUnsafeBytes { raw -> Int in
                    guard let base = raw.bindMemory(to: UInt8.self).baseAddress else { return 0 }
                    return write(fd, base + offset, data.count - offset)
                }
                if written <= 0 {
                    writeFailed = true
                    break
                }
                offset += written
            }
            if writeFailed {
                try? FileManager.default.removeItem(at: target)
                throw BackupError.createFailed(reason: "备份写入失败 errno=\(errno)")
            }
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: target.path)
            return target
        }
        throw BackupError.createFailed(reason: "备份名连续碰撞（8 次重试后放弃）")
    }

    struct Entry {
        let key: String
        let url: URL
    }

    func existingBackups() throws -> [Entry] {
        let names = (try? FileManager.default.contentsOfDirectory(atPath: directory.path)) ?? []
        return names
            .filter { $0.hasSuffix(".tomlbak") }
            .map { Entry(key: $0, url: directory.appendingPathComponent($0)) }
    }

    private static func defaultClock() -> (seconds: Int64, nanoseconds: Int32) {
        var ts = timespec()
        clock_gettime(CLOCK_REALTIME, &ts)
        return (Int64(ts.tv_sec), Int32(ts.tv_nsec))
    }
}

// MARK: - 原子写入器（§3.3-1/5/7/8/10 编排）

struct GrokLeaderConfigWriter {

    enum WriterError: Error, Equatable {
        case f1(reason: String)
        case f2
        /// 并发修改：rename 前内容身份比较或身份钉扎复核失败；磁盘未被覆盖
        case concurrentModification(reason: String)
        /// IO 失败（目录只读等）：原文件不变
        case ioFailure(reason: String)
        /// 写后校验失败；rolledBack 表示是否已用备份恢复
        case postVerifyFailed(rolledBack: Bool, reason: String)
    }

    struct Outcome: Equatable {
        var backupPath: String?
        var bytesWritten: Int
    }

    let backupStore: GrokConfigBackupStore
    let fileManager: FileManager

    /// 测试注入口（生产恒 nil）：temp 写入后、身份复核前调用——模拟并发修改/link swap（T7/T23）。
    var testHookBeforeVerify: ((_ canonicalPath: String) -> Void)?
    /// 测试注入口（生产恒 nil）：rename 后、写后校验前调用——模拟写后磁盘损坏/消失（T17）。
    var testHookAfterRename: ((_ canonicalPath: String) -> Void)?

    init(backupDirectory: URL, fileManager: FileManager = .default) {
        self.backupStore = GrokConfigBackupStore(directory: backupDirectory)
        self.fileManager = fileManager
    }

    /// newContent 已由 Editor 产出（语义/词法/矩阵校验通过）；expected 为目标语义值。
    /// 编排：钉扎 → 目录创建 → 备份（收敛 ≤2 → 创建第 3 份）→ temp(mode 保留) →
    /// rename 前三重复核 → rename → 写后校验（字节 + 语义/矩阵，§3.3-8）→ 受限回滚。
    func apply(newContent: String, to configPath: String, expecting expected: GrokLeaderConfigValue) throws -> Outcome {
        let newData = Data(newContent.utf8)

        // symlink 解析与身份钉扎（§3.3-4）；悬空/循环/非普通文件 → F1，绝不 rename 到链接本身
        let pinned: GrokLeaderResolvedTarget?
        do {
            pinned = try GrokLeaderSymlinkResolver.resolve(path: configPath)
        } catch {
            var st = stat()
            let rc = configPath.withCString { lstat($0, &st) }
            if rc == 0 || errno != ENOENT {
                throw WriterError.f1(reason: "config 路径无法解析为普通文件（悬空/循环/非普通文件）")
            }
            // 原文件不存在：目标是尚未创建的普通文件路径（T11/T17）
            pinned = nil
        }
        let canonicalPath = pinned?.canonicalPath ?? configPath
        let originalData = readFileOrNil(canonicalPath)
        let mode: mode_t = pinned?.mode ?? 0o644
        if pinned != nil, originalData == nil {
            throw WriterError.f1(reason: "目标文件读取失败（权限）")
        }

        // 目录不存在 → 创建 $GROK_HOME 0700（§3.3-9 裁决）
        let targetDir = (canonicalPath as NSString).deletingLastPathComponent
        if !fileManager.fileExists(atPath: targetDir) {
            do {
                try fileManager.createDirectory(atPath: targetDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            } catch {
                throw WriterError.ioFailure(reason: "创建 GROK_HOME 失败: \(error.localizedDescription)")
            }
        }

        // 备份序列（crash-safe：先收敛 ≤2 → 创建第 3 份；无原文件无备份）
        var backupPath: URL? = nil
        if let original = originalData {
            do {
                try backupStore.convergeToAtMostTwo()
                backupPath = try backupStore.createBackup(data: original)
            } catch {
                throw WriterError.ioFailure(reason: "备份失败（fail-closed，未写原文件）: \(describeBackupError(error))")
            }
        }

        // temp 写入（同目标目录，保留原 mode；无原文件 0644）
        let tempPath = targetDir + "/.cordcode-grok-config-\(UUID().uuidString).tmp"
        do {
            try newData.write(to: URL(fileURLWithPath: tempPath))
            try fileManager.setAttributes([.posixPermissions: mode], ofItemAtPath: tempPath)
        } catch {
            try? fileManager.removeItem(atPath: tempPath)
            throw WriterError.ioFailure(reason: "临时文件写入失败: \(error.localizedDescription)")
        }

        // rename 前三重复核：链接链同 canonical 路径 + inode/device 一致 + 内容 == 快照
        testHookBeforeVerify?(canonicalPath)
        do {
            try verifyTargetIdentity(originalPath: configPath, pinned: pinned, canonicalPath: canonicalPath, originalData: originalData)
        } catch let error as WriterError {
            try? fileManager.removeItem(atPath: tempPath)
            throw error
        }

        // rename（原子；replaceItemAt 可能继承旧属性，显式设回 mode）
        do {
            if fileManager.fileExists(atPath: canonicalPath) {
                _ = try fileManager.replaceItemAt(
                    URL(fileURLWithPath: canonicalPath),
                    withItemAt: URL(fileURLWithPath: tempPath),
                    backupItemName: nil,
                    options: []
                )
            } else {
                try fileManager.moveItem(atPath: tempPath, toPath: canonicalPath)
            }
            try? fileManager.setAttributes([.posixPermissions: mode], ofItemAtPath: canonicalPath)
        } catch {
            try? fileManager.removeItem(atPath: tempPath)
            throw WriterError.ioFailure(reason: "rename 失败: \(error.localizedDescription)")
        }

        // 写后校验（§3.3-8）：磁盘内容逐字节等于本次写入 + 语义/矩阵复核（设计 :614）
        testHookAfterRename?(canonicalPath)
        let onDisk = readFileOrNil(canonicalPath)
        guard onDisk == newData else {
            return try rollback(
                canonicalPath: canonicalPath,
                expectedWritten: newData,
                onDisk: onDisk,
                backupPath: backupPath,
                originalMode: mode,
                failureReason: "磁盘字节与本次写入不一致"
            )
        }
        if let reason = Self.semanticVerifyFailure(newContent, expected: expected) {
            return try rollback(
                canonicalPath: canonicalPath,
                expectedWritten: newData,
                onDisk: onDisk,
                backupPath: backupPath,
                originalMode: mode,
                failureReason: reason
            )
        }
        return Outcome(backupPath: backupPath?.path, bytesWritten: newData.count)
    }

    /// 写后语义/矩阵校验：parser 语义值必须等于目标，且 locator 定位到恰一 canonical 行
    /// （absent 目标 → 无 canonical 行）。追加产生非法 TOML（如与既有 cli 定义冲突）在此
    /// 被捕获并触发回滚。
    static func semanticVerifyFailure(_ content: String, expected: GrokLeaderConfigValue) -> String? {
        let semantic: GrokLeaderConfigValue
        switch GrokLeaderSemanticParser.value(in: content) {
        case .failure(let e):
            return GrokLeaderConfigFileEditor.readableReadError(e)
        case .success(let v):
            semantic = v
        }
        guard semantic == expected else { return "写后语义值与目标不符" }
        let verdict = GrokLeaderCrossMatrix.verdict(
            semantic: semantic,
            locator: GrokLeaderLocator.locate(text: content)
        )
        let ok: Bool
        switch expected {
        case .explicitTrue: ok = verdict == .inPlaceEdit
        case .absent: ok = verdict == .safeAppend
        case .explicitFalse: ok = false
        }
        return ok ? nil : "写后 locator/矩阵校验未通过（\(verdict)）"
    }

    /// 受限回滚：仅当磁盘内容仍逐字节等于本次写入才用备份原子恢复（备份本体保留，
    /// 经临时副本恢复）；内容已被第三方再修改 → 保留现场并报告（不覆盖他人修改）。
    private func rollback(
        canonicalPath: String,
        expectedWritten: Data,
        onDisk: Data?,
        backupPath: URL?,
        originalMode: mode_t,
        failureReason: String
    ) throws -> Outcome {
        if let disk = onDisk, disk != expectedWritten {
            throw WriterError.postVerifyFailed(rolledBack: false, reason: "磁盘内容已被第三方修改（保留现场，不回滚覆盖）")
        }
        guard let backup = backupPath else {
            throw WriterError.postVerifyFailed(rolledBack: false, reason: "无备份可恢复（原文件不存在路径）")
        }
        guard let backupData = readFileOrNil(backup.path) else {
            throw WriterError.postVerifyFailed(rolledBack: false, reason: "备份读取失败")
        }
        let dir = (canonicalPath as NSString).deletingLastPathComponent
        let restoreTemp = dir + "/.cordcode-grok-restore-\(UUID().uuidString).tmp"
        do {
            try backupData.write(to: URL(fileURLWithPath: restoreTemp))
            try fileManager.setAttributes([.posixPermissions: originalMode], ofItemAtPath: restoreTemp)
            if fileManager.fileExists(atPath: canonicalPath) {
                _ = try fileManager.replaceItemAt(
                    URL(fileURLWithPath: canonicalPath),
                    withItemAt: URL(fileURLWithPath: restoreTemp),
                    backupItemName: nil,
                    options: []
                )
            } else {
                try fileManager.moveItem(atPath: restoreTemp, toPath: canonicalPath)
            }
            try? fileManager.setAttributes([.posixPermissions: originalMode], ofItemAtPath: canonicalPath)
            throw WriterError.postVerifyFailed(rolledBack: true, reason: "写后校验失败（\(failureReason)），已用备份恢复")
        } catch let e as WriterError {
            throw e
        } catch {
            try? fileManager.removeItem(atPath: restoreTemp)
            throw WriterError.postVerifyFailed(rolledBack: false, reason: "回滚失败: \(error.localizedDescription)")
        }
    }

    /// §3.3-4 双重复核 + §3.3-7 内容身份比较（残余竞态窗口见设计；best-effort）。
    private func verifyTargetIdentity(
        originalPath: String,
        pinned: GrokLeaderResolvedTarget?,
        canonicalPath: String,
        originalData: Data?
    ) throws {
        let repinned = try? GrokLeaderSymlinkResolver.resolve(path: originalPath)
        if let p = pinned {
            guard let r = repinned else {
                throw WriterError.concurrentModification(reason: "目标在写入前消失或链接被破坏")
            }
            guard r.canonicalPath == p.canonicalPath else {
                throw WriterError.concurrentModification(reason: "链接链被更换（link swap）")
            }
            guard r.inode == p.inode, r.device == p.device else {
                throw WriterError.concurrentModification(reason: "目标文件被替换（同路径不同 inode）")
            }
            guard readFileOrNil(r.canonicalPath) == originalData else {
                throw WriterError.concurrentModification(reason: "磁盘内容已被并发修改")
            }
        } else {
            if repinned != nil || fileManager.fileExists(atPath: canonicalPath) {
                throw WriterError.concurrentModification(reason: "目标在写入前被并发创建")
            }
        }
    }

    private func readFileOrNil(_ path: String) -> Data? {
        try? Data(contentsOf: URL(fileURLWithPath: path))
    }

    private func describeBackupError(_ error: Error) -> String {
        if let backup = error as? GrokConfigBackupStore.BackupError {
            switch backup {
            case .convergeFailed(let reason), .createFailed(let reason):
                return reason
            }
        }
        return error.localizedDescription
    }
}

// MARK: - Manager（§3.5 职责⑥：@Published 状态 + 开关操作）

@MainActor
final class GrokLeaderModeManager: ObservableObject {

    @Published private(set) var status: GrokLeaderModeStatus

    private let environment: [String: String]
    private let appSupportDirectory: URL

    /// Link 进程实际继承的 env 是否解析出非默认 socket 路径（§3.4 #6：只反映
    /// Link 继承的 env，不能发现任意 TUI 的 --leader-socket）。
    var hasCustomSocketPath: Bool {
        !(environment["GROK_LEADER_SOCKET"] ?? "").trimmingCharacters(in: .whitespaces).isEmpty
    }

    /// 失败回弹 alert 的备份路径段（§3.3 备份目录；固定目录而非单次文件名——
    /// Writer 错误链不携带 backupPath，目录足以让 owner 定位受限回滚材料）。
    var backupDirectoryPath: String {
        GrokConfigBackupStore.defaultDirectory(appSupport: appSupportDirectory).path
    }

    init(environment: [String: String] = ProcessInfo.processInfo.environment,
         appSupport: URL? = nil) {
        self.environment = environment
        let paths = GrokLeaderPaths.resolve(environment: environment)
        self.appSupportDirectory = appSupport
            ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
                .appendingPathComponent("CordCode Link", isDirectory: true)
        self.status = GrokLeaderModeStatus(
            value: nil,
            readError: nil,
            socketPresent: false,
            paths: paths
        )
    }

    /// agentRow 行内副文案状态机（§3.4）。纯函数（可单测）：
    /// 失败态 F1/F2 > 核心三态 #2/#3 > 观察态 #4 > #5 > #6（#6 仅在其余态无文案时
    /// 显示——核心态与 socket 观察态信息优先，完整路径信息由 Phase 4 DiagnosticsSheet
    /// 呈现）。#4 优先于 #5：explicit false + socket 痕迹时运行痕迹更值得注意。
    static func rowState(status: GrokLeaderModeStatus, customSocketPath: Bool) -> GrokLeaderRowState {
        if let err = status.readError {
            switch err {
            case .f1(let reason): return .failedRead(reason: reason)
            case .f2: return .failedUnsafeForm
            }
        }
        switch status.value {
        case .explicitTrue:
            return status.socketPresent ? .coreOnSocketDetected : .coreOnPendingRestart
        case .explicitFalse, .absent, .none:
            if status.socketPresent { return .observeSocketTrace }
            if status.value == .explicitFalse { return .observeExplicitOff }
            if customSocketPath { return .observeCustomSocket(path: status.paths.socketPath) }
            return .coreOff
        }
    }

    /// 状态刷新（§3.4：视图 appear / agents 刷新回调 / 开关操作完成后）。
    /// socket 存在性仅 stat，不引入常驻定时器。
    func refresh() {
        let paths = GrokLeaderPaths.resolve(environment: environment)
        var value: GrokLeaderConfigValue? = nil
        var readError: GrokLeaderReadError? = nil

        switch GrokLeaderSymlinkResolver.classify(path: paths.configPath) {
        case .invalid(let reason):
            readError = .f1(reason: reason)
        case .absent:
            value = .absent
        case .regularFile(let canonicalPath):
            let data = try? Data(contentsOf: URL(fileURLWithPath: canonicalPath))
            if let d = data, let text = String(data: d, encoding: .utf8) {
                switch GrokLeaderSemanticParser.value(in: text) {
                case .success(let v):
                    value = v
                    switch GrokLeaderCrossMatrix.verdict(semantic: v, locator: GrokLeaderLocator.locate(text: text)) {
                    case .safeAppend, .inPlaceEdit:
                        break
                    case .equivalentForm:
                        readError = .f2
                        value = nil
                    case .contradiction:
                        readError = .f1(reason: "语义与词法定位矛盾（语法异常）")
                        value = nil
                    }
                case .failure(let e):
                    readError = e
                }
            } else if data != nil {
                readError = .f1(reason: "config 编码非 UTF-8")
            } else {
                readError = .f1(reason: "config 读取失败（权限）")
            }
        }

        let socketPresent = FileManager.default.fileExists(atPath: paths.socketPath)
        status = GrokLeaderModeStatus(
            value: value,
            readError: readError,
            socketPresent: socketPresent,
            paths: paths
        )
    }

    enum SetModeError: Error, Equatable {
        case f1(reason: String)
        case f2
        case concurrentModification(reason: String)
        case ioFailure(reason: String)
        case postVerifyFailed(rolledBack: Bool, reason: String)
    }

    /// 开 = 外科写入 true；关 = 删键。返回是否实际写入（false = 幂等无操作）。
    @discardableResult
    func setLeaderMode(_ enabled: Bool) throws -> Bool {
        let paths = GrokLeaderPaths.resolve(environment: environment)
        let writer = GrokLeaderConfigWriter(
            backupDirectory: GrokConfigBackupStore.defaultDirectory(appSupport: appSupportDirectory)
        )

        let original: String
        switch GrokLeaderSymlinkResolver.classify(path: paths.configPath) {
        case .invalid(let reason):
            refresh()
            throw SetModeError.f1(reason: reason)
        case .absent:
            original = ""
        case .regularFile(let canonicalPath):
            guard let data = try? Data(contentsOf: URL(fileURLWithPath: canonicalPath)),
                  let text = String(data: data, encoding: .utf8) else {
                refresh()
                throw SetModeError.f1(reason: "config 读取失败")
            }
            original = text
        }

        let newContent: String?
        do {
            newContent = enabled
                ? try GrokLeaderConfigFileEditor.enabledContent(from: original)
                : try GrokLeaderConfigFileEditor.disabledContent(from: original)
        } catch let e as GrokLeaderConfigEditError {
            refresh()
            switch e {
            case .f1(let reason): throw SetModeError.f1(reason: reason)
            case .f2: throw SetModeError.f2
            }
        }
        guard let content = newContent else {
            refresh()
            return false
        }

        do {
            _ = try writer.apply(
                newContent: content,
                to: paths.configPath,
                expecting: enabled ? .explicitTrue : .absent
            )
        } catch let e as GrokLeaderConfigWriter.WriterError {
            refresh()
            switch e {
            case .f1(let reason): throw SetModeError.f1(reason: reason)
            case .f2: throw SetModeError.f2
            case .concurrentModification(let reason): throw SetModeError.concurrentModification(reason: reason)
            case .ioFailure(let reason): throw SetModeError.ioFailure(reason: reason)
            case .postVerifyFailed(let rolledBack, let reason):
                throw SetModeError.postVerifyFailed(rolledBack: rolledBack, reason: reason)
            }
        }
        refresh()
        return true
    }

    // MARK: - 安装版 grok 版本（§3.2 DiagnosticsSheet grok 组；§4.10 版本漂移 fail-visible）

    /// `grok --version` 首行原文（含发行身份，如 "grok 1.0.12 (ece2b556c271)"）。
    /// 未探测/失败 = nil，UI 显示「未检测到」——不猜测、不 fallback。
    @Published private(set) var installedGrokVersion: String?

    /// 非阻塞探测：在 detached task 上执行（Process + 5s 上限），完成后回 MainActor。
    func probeGrokVersion() {
        let env = environment
        Task { [weak self] in
            let version = await Task.detached(priority: .utility) {
                GrokLeaderVersionProbe.installedVersion(environment: env)
            }.value
            self?.installedGrokVersion = version
        }
    }

    // MARK: - DiagnosticsSheet grok 组状态行（§3.2 必做：配置三分 + socket + 版本）

    struct GrokLeaderDiagnosticsSummary: Equatable {
        let configText: String
        let socketText: String
        let versionText: String

        var joined: String { [configText, socketText, versionText].joined(separator: " · ") }
    }

    /// 诊断行文案（纯函数可单测）：user 层配置值区分 absent / explicit false / true
    /// （读失败 F1/F2 优先呈现，fail-visible）、socket 路径与存在性、安装版本。
    static func diagnosticsSummary(
        status: GrokLeaderModeStatus,
        version: String?
    ) -> GrokLeaderDiagnosticsSummary {
        let config: String
        if let err = status.readError {
            switch err {
            case .f1(let reason): config = String(format: L10n.grokLeaderDiagReadFailedFmt, reason)
            case .f2: config = L10n.grokLeaderDiagUnsafeForm
            }
        } else {
            switch status.value {
            case .explicitTrue: config = L10n.grokLeaderDiagConfigTrue
            case .explicitFalse: config = L10n.grokLeaderDiagConfigFalse
            case .absent, .none: config = L10n.grokLeaderDiagConfigAbsent
            }
        }
        let socketState = status.socketPresent
            ? L10n.grokLeaderDiagSocketPresent
            : L10n.grokLeaderDiagSocketMissing
        return GrokLeaderDiagnosticsSummary(
            configText: String(format: L10n.grokLeaderDiagConfigFmt, config),
            socketText: String(
                format: L10n.grokLeaderDiagSocketFmt,
                status.paths.socketPath,
                socketState
            ),
            versionText: String(
                format: L10n.grokLeaderDiagVersionFmt,
                version ?? L10n.grokLeaderDiagVersionMissing
            )
        )
    }
}

/// 安装版 grok 的 `--version` 探测。搜索链镜像 RuntimeManager.defaultCLISearchPath
/// （GUI 进程不继承用户 shell PATH），再追加继承 PATH 中的目录；逐目录找可执行的
/// `grok`，取 stdout 首个非空行。找不到 / 启动失败 / 超时 / 空输出 → nil。
enum GrokLeaderVersionProbe {
    static let defaultSearchDirs: [String] = [
        "~/.bun/bin",
        "~/.local/bin",
        "~/.cargo/bin",
        "/opt/homebrew/bin",
        "/opt/homebrew/sbin",
        "/usr/local/bin",
        "/usr/local/sbin",
        "/usr/bin",
        "/bin",
        "/usr/sbin",
        "/sbin",
        "~/Library/pnpm",
        "~/.volta/bin",
        "~/.npm-global/bin",
    ]

    /// `searchDirs` 仅供测试注入；生产走 defaultSearchDirs + 继承 PATH。
    static func installedVersion(
        environment: [String: String],
        searchDirs: [String]? = nil
    ) -> String? {
        var dirs = (searchDirs ?? defaultSearchDirs).map { NSString(string: $0).expandingTildeInPath }
        if let path = environment["PATH"], !path.isEmpty {
            dirs.append(contentsOf: path.split(separator: ":").map(String.init))
        }
        for dir in dirs where !dir.isEmpty {
            let bin = (dir as NSString).appendingPathComponent("grok")
            guard FileManager.default.isExecutableFile(atPath: bin) else { continue }
            if let line = firstVersionLine(of: bin) { return line }
        }
        return nil
    }

    private static func firstVersionLine(of bin: String) -> String? {
        let process = Process()
        let pipe = Pipe()
        process.executableURL = URL(fileURLWithPath: bin)
        process.arguments = ["--version"]
        process.standardOutput = pipe
        process.standardError = Pipe()
        let done = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in done.signal() }
        do {
            try process.run()
        } catch {
            return nil
        }
        guard done.wait(timeout: .now() + 5) == .success else {
            process.terminate()
            return nil
        }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let text = String(data: data, encoding: .utf8) ?? ""
        return text
            .split(separator: "\n")
            .lazy
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .first { !$0.isEmpty }
    }
}
