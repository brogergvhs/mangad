import Foundation

enum APIError: LocalizedError {
    case badURL
    case unauthorized
    case server(String)

    var errorDescription: String? {
        switch self {
        case .badURL: return "Invalid server URL"
        case .unauthorized: return "Signed out by the server — reconnect"
        case .server(let msg): return msg
        }
    }
}

// APIClient talks to one Kaodoku server. URLSession's URLCache honors the
// server's ETag/Cache-Control, so page and cover images cache for free.
struct APIClient {
    var baseURL: URL
    var token: String?

    private static let session: URLSession = {
        let cfg = URLSessionConfiguration.default
        cfg.urlCache = URLCache(memoryCapacity: 64 << 20, diskCapacity: 512 << 20)
        cfg.requestCachePolicy = .useProtocolCachePolicy
        return URLSession(configuration: cfg)
    }()

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }()

    private func request(_ method: String, _ path: String, body: (any Encodable)? = nil) throws -> URLRequest {
        guard let url = URL(string: path, relativeTo: baseURL) else { throw APIError.badURL }
        var req = URLRequest(url: url)
        req.httpMethod = method
        if let token, url.host == baseURL.host {
            req.setValue(token, forHTTPHeaderField: "X-API-Key")
        }
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            let enc = JSONEncoder()
            enc.keyEncodingStrategy = .convertToSnakeCase
            enc.dateEncodingStrategy = .iso8601
            req.httpBody = try enc.encode(body)
        }
        return req
    }

    func data(_ method: String = "GET", _ path: String, body: (any Encodable)? = nil) async throws -> Data {
        let (data, resp) = try await Self.session.data(for: request(method, path, body: body))
        guard let http = resp as? HTTPURLResponse else { throw APIError.server("no response") }
        switch http.statusCode {
        case 200..<300: return data
        case 401: throw APIError.unauthorized
        default:
            let msg = (try? Self.decoder.decode(APIErrorBody.self, from: data))?.error
            throw APIError.server(msg ?? "HTTP \(http.statusCode)")
        }
    }

    // download streams a response to a temp file (large CBZs never sit in
    // memory), reporting byte progress when the length is known.
    func download(_ path: String, progress: @escaping @Sendable (Double) -> Void = { _ in }) async throws -> URL {
        let (bytes, resp) = try await Self.session.bytes(for: request("GET", path))
        guard let http = resp as? HTTPURLResponse else { throw APIError.server("no response") }
        switch http.statusCode {
        case 200..<300: break
        case 401: throw APIError.unauthorized
        default: throw APIError.server("HTTP \(http.statusCode)")
        }
        let total = resp.expectedContentLength
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        FileManager.default.createFile(atPath: tmp.path, contents: nil)
        let fh = try FileHandle(forWritingTo: tmp)
        defer { try? fh.close() }
        var buf = Data(capacity: 128 << 10)
        var written: Int64 = 0
        for try await b in bytes {
            buf.append(b)
            if buf.count >= 128 << 10 {
                try fh.write(contentsOf: buf)
                written += Int64(buf.count)
                buf.removeAll(keepingCapacity: true)
                if total > 0 { progress(Double(written) / Double(total)) }
            }
        }
        try fh.write(contentsOf: buf)
        progress(1)
        return tmp
    }

    func get<T: Decodable>(_ path: String) async throws -> T {
        try Self.decoder.decode(T.self, from: await data("GET", path))
    }

    func post<T: Decodable>(_ path: String, body: (any Encodable)? = nil) async throws -> T {
        try Self.decoder.decode(T.self, from: await data("POST", path, body: body))
    }
}

// Keychain stores the device API token.
enum Keychain {
    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: "com.kaodoku.ios",
         kSecAttrAccount as String: account]
    }

    static func save(_ value: String, account: String) {
        var q = query(account)
        SecItemDelete(q as CFDictionary)
        q[kSecValueData as String] = Data(value.utf8)
        SecItemAdd(q as CFDictionary, nil)
    }

    static func load(account: String) -> String? {
        var q = query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func delete(account: String) {
        SecItemDelete(query(account) as CFDictionary)
    }
}
