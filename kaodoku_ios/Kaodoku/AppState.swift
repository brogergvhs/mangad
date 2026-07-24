import Foundation
import Observation

// AppState holds the connection to one server: URL in UserDefaults, token in
// the Keychain. connected == usable client (token present or single-user).
@Observable @MainActor
final class AppState {
    var api: APIClient?
    var me: Me?
    var settings = UserSettings()
    var errorMessage: String?

    private static let serverKey = "server_url"
    private static let tokenAccount = "api_token"

    var connected: Bool { api != nil }

    init() {
        if let raw = UserDefaults.standard.string(forKey: Self.serverKey), let url = URL(string: raw) {
            api = APIClient(baseURL: url, token: Keychain.load(account: Self.tokenAccount))
        }
    }

    // connect probes the server, logs in when it requires auth, and persists
    // the session. Returns true when fully connected.
    func connect(server: String, username: String, password: String) async -> Bool {
        errorMessage = nil
        guard var url = URL(string: server.trimmingCharacters(in: .whitespaces)) else {
            errorMessage = APIError.badURL.localizedDescription
            return false
        }
        if url.scheme == nil { url = URL(string: "http://\(server)") ?? url }
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

    func loadMe() async {
        guard let api, me == nil else { return }
        do {
            me = try await api.get("/api/v1/me")
            settings = try await api.get("/api/v1/me/settings")
        } catch APIError.unauthorized {
            signOut()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func saveSettings() {
        guard let api else { return }
        let s = settings
        Task { _ = try? await api.data("PUT", "/api/v1/me/settings", body: s) }
    }

    func signOut() {
        if let api, api.token != nil {
            Task { _ = try? await api.data("DELETE", "/api/v1/auth/token") }
        }
        Keychain.delete(account: Self.tokenAccount)
        UserDefaults.standard.removeObject(forKey: Self.serverKey)
        api = nil
        me = nil
    }
}
