import SwiftUI

@main
struct KaodokuApp: App {
    @State private var app = AppState()

    var body: some Scene {
        WindowGroup {
            if app.connected {
                LibraryView()
                    .environment(app)
                    .task { await app.loadMe() }
            } else {
                ConnectView()
                    .environment(app)
            }
        }
    }
}
