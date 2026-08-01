import Foundation
import Network
import Observation

// ServerEndpoints stores up to two addresses for the same server: a LAN one
// and a public one. instanceID  proves they are the same installation.
struct ServerEndpoints: Codable, Equatable {
    enum Mode: String, Codable { case auto, local, external }
    var localURL: URL? = nil
    var publicURL: URL? = nil
    var instanceID: String
    var mode: Mode = .auto

    var verifiable: Bool {
        guard let l = localURL, let p = publicURL else { return false }
        return !sameOrigin(l, p)
    }

    var wantsProbe: Bool { verifiable && mode != .external }

    func activeURL(localVerified: Bool) -> URL? {
        switch mode {
        case .external:
            return publicURL ?? localURL
        case .local:
            if verifiable && !localVerified { return publicURL }
            return localURL ?? publicURL
        case .auto:
            return localVerified ? (localURL ?? publicURL) : (publicURL ?? localURL)
        }
    }
}

// AppState holds the connection to one server: URL in UserDefaults, token in
// the Keychain. connected == usable client (token present or single-user).
@Observable @MainActor
final class AppState {
    enum LibraryNav: Equatable {
        case title(Int64)
        case root
    }

    var api: APIClient?
    var me: Me?
    var settings = UserSettings()
    var errorMessage: String?
    var tab = 0
    var libraryNav: LibraryNav?
    let store = LocalStore()
    var endpoints: ServerEndpoints? {
        didSet {
            if let endpoints {
                UserDefaults.standard.set(try? JSONEncoder().encode(endpoints), forKey: Self.endpointsKey)
            } else {
                UserDefaults.standard.removeObject(forKey: Self.endpointsKey)
            }
        }
    }

    private static let endpointsKey = "server_endpoints"
    private static let settingsKey = "user_settings"
    private static let tokenAccount = "api_token"
    private var settingsDirty = false
    private var settingsTask: Task<Void, Never>?
    private var reselectTask: Task<Void, Never>?
    private let pathMonitor = NWPathMonitor()

    var connected: Bool { api != nil }

    init() {
        if let data = UserDefaults.standard.data(forKey: Self.endpointsKey),
           let stored = try? JSONDecoder().decode(ServerEndpoints.self, from: data) {
            endpoints = stored
        }
        if let url = endpoints?.activeURL(localVerified: false) {
            api = APIClient(baseURL: url, token: Keychain.load(account: Self.tokenAccount))
        }
        if let data = UserDefaults.standard.data(forKey: Self.settingsKey),
           let cached = try? JSONDecoder().decode(UserSettings.self, from: data) {
            settings = cached
        }
        pathMonitor.pathUpdateHandler = { [weak self] _ in
            Task { @MainActor in self?.scheduleReselect() }
        }
        pathMonitor.start(queue: .global(qos: .utility))
        Task { [weak self] in await self?.reselect() }
    }

    func scheduleReselect() {
        reselectTask?.cancel()
        reselectTask = Task {
            try? await Task.sleep(for: .seconds(1))
            guard !Task.isCancelled else { return }
            await reselect()
        }
    }

    // reselect probes the local address and switches the client.
    func reselect() async {
        guard let e = endpoints, api != nil else { return }
        var localVerified = false
        if e.wantsProbe, let local = e.localURL {
            localVerified = await Self.fetchInstanceID(local) == e.instanceID
        }
        guard !Task.isCancelled, e == endpoints else { return }
        guard let url = e.activeURL(localVerified: localVerified),
              let current = api, !sameOrigin(current.baseURL, url) else { return }
        api = APIClient(baseURL: url, token: current.token)
    }

    nonisolated static func fetchInstanceID(_ base: URL) async -> String? {
        guard let url = URL(string: "/api/v1/meta", relativeTo: base), isAllowedServerURL(url) else { return nil }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        guard let (data, resp) = try? await URLSession.shared.data(for: req, delegate: NoRedirect.shared),
              let http = resp as? HTTPURLResponse, http.statusCode == 200,
              let respURL = http.url, sameOrigin(respURL, url) else { return nil }
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return (try? decoder.decode(Meta.self, from: data))?.instanceId
    }

    func connect(server: String, username: String, password: String) async -> Bool {
        errorMessage = nil
        let server = server.trimmingCharacters(in: .whitespaces)
        guard var url = URL(string: server) else {
            errorMessage = APIError.badURL.localizedDescription
            return false
        }
        if url.scheme == nil { url = URL(string: "https://\(server)") ?? url }
        guard isAllowedServerURL(url) else {
            errorMessage = "Use HTTPS, or HTTP only for a private local server."
            return false
        }
        var client = APIClient(baseURL: url, token: nil)
        do {
            let meta: Meta = try await client.get("/api/v1/meta")
            if meta.authRequired {
                guard !username.isEmpty else {
                    errorMessage = "This server requires a username and password"
                    return false
                }
                let login: LoginResponse = try await client.post("/api/v1/auth/login", body: [
                    "username": username, "password": password, "device_name": "iOS app",
                ])
                client.token = login.token
                me = login.me
                Keychain.save(login.token, account: Self.tokenAccount)
            } else {
                me = try await client.get("/api/v1/me")
            }
            guard let id = meta.instanceId, !id.isEmpty else {
                errorMessage = "This server doesn't report an instance ID — update the server."
                return false
            }
            var e = endpoints?.instanceID == id ? endpoints! : ServerEndpoints(instanceID: id)
            if isPrivateHost(url.host) { e.localURL = url } else { e.publicURL = url }
            endpoints = e
            api = client
            return true
        } catch {
            errorMessage = error.localizedDescription
            return false
        }
    }

    // updateEndpoints saves edited addresses from Settings.
    func updateEndpoints(local localRaw: String, external publicRaw: String) async -> String? {
        guard var e = endpoints else { return "Not connected" }
        func parse(_ raw: String) -> URL?? {
            let raw = raw.trimmingCharacters(in: .whitespaces)
            if raw.isEmpty { return .some(nil) }
            var url = URL(string: raw)
            if url?.scheme == nil { url = URL(string: "https://\(raw)") }
            guard let url, isAllowedServerURL(url) else { return nil }
            return url
        }
        guard let local = parse(localRaw), let pub = parse(publicRaw) else {
            return "Use HTTPS, or HTTP only for a private local address."
        }
        guard local != nil || pub != nil else { return "At least one address is required" }
        if let pub, isPrivateHost(pub.host) {
            return "The public address must be reachable from outside — a private IP only means something on its own network."
        }
        for (new, old) in [(local, e.localURL), (pub, e.publicURL)] where new != nil && new != old {
            guard let got = await Self.fetchInstanceID(new!) else {
                return "\(new!.host ?? "The address") didn't respond — check the address."
            }
            guard got == e.instanceID else {
                return "\(new!.host ?? "The address") responds, but it's a different server."
            }
        }
        e.localURL = local
        e.publicURL = pub
        reselectTask?.cancel()
        endpoints = e
        await reselect()
        return nil
    }

    func loadSession() async {
        guard let api else { return }
        do {
            if me == nil { me = try await api.get("/api/v1/me") }
            settings = try await api.get("/api/v1/me/settings")
            cacheSettings()
            await store.flush(api)
        } catch APIError.unauthorized {
            signOut()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func saveSettings() {
        cacheSettings()
        guard let api else { return }
        settingsDirty = true
        guard settingsTask == nil else { return }
        settingsTask = Task {
            while settingsDirty, !Task.isCancelled {
                settingsDirty = false
                let snapshot = settings
                _ = try? await api.data("PUT", "/api/v1/me/settings", body: snapshot)
            }
            settingsTask = nil
        }
    }

    private func cacheSettings() {
        UserDefaults.standard.set(try? JSONEncoder().encode(settings), forKey: Self.settingsKey)
    }

    func signOut() {
        settingsDirty = false
        settingsTask?.cancel()
        reselectTask?.cancel()
        if let api, api.token != nil {
            Task { _ = try? await api.data("DELETE", "/api/v1/auth/token") }
        }
        Keychain.delete(account: Self.tokenAccount)
        UserDefaults.standard.removeObject(forKey: Self.settingsKey)
        APIClient.clearCache()
        endpoints = nil
        api = nil
        me = nil
    }
}
