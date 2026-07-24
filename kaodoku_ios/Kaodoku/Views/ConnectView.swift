import SwiftUI

struct ConnectView: View {
    @Environment(AppState.self) private var app
    @State private var server = ""
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    TextField("http://192.168.1.10:8080", text: $server)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                Section("Account (if the server requires sign-in)") {
                    TextField("Username", text: $username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    SecureField("Password", text: $password)
                }
                if let msg = app.errorMessage {
                    Text(msg).foregroundStyle(.red)
                }
                Button {
                    busy = true
                    Task {
                        _ = await app.connect(server: server, username: username, password: password)
                        busy = false
                    }
                } label: {
                    if busy {
                        ProgressView()
                    } else {
                        Text("Connect")
                    }
                }
                .disabled(server.isEmpty || busy)
            }
            .navigationTitle("Kaodoku")
        }
    }
}
