import Foundation
import Observation

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

    private static let serverKey = "server_url"
    private static let settingsKey = "user_settings"
    private static let tokenAccount = "api_token"
    private var settingsDirty = false
    private var settingsTask: Task<Void, Never>?

    var connected: Bool { api != nil }

    init() {
        if let raw = UserDefaults.standard.string(forKey: Self.serverKey), let url = URL(string: raw) {
            api = APIClient(baseURL: url, token: Keychain.load(account: Self.tokenAccount))
        }
        if let data = UserDefaults.standard.data(forKey: Self.settingsKey),
           let cached = try? JSONDecoder().decode(UserSettings.self, from: data) {
            settings = cached
        }
    }

    // connect probes the server, logs in when it requires auth, and persists
    // the session. Returns true when fully connected.
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
            UserDefaults.standard.set(url.absoluteString, forKey: Self.serverKey)
            api = client
            return true
        } catch {
            errorMessage = error.localizedDescription
            return false
        }
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
        if let api, api.token != nil {
            Task { _ = try? await api.data("DELETE", "/api/v1/auth/token") }
        }
        Keychain.delete(account: Self.tokenAccount)
        UserDefaults.standard.removeObject(forKey: Self.serverKey)
        UserDefaults.standard.removeObject(forKey: Self.settingsKey)
        APIClient.clearCache()
        api = nil
        me = nil
    }
}
