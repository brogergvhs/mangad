import SwiftUI

@main
struct KaodokuApp: App {
    @State private var app = AppState()

    var body: some Scene {
        WindowGroup {
            if app.connected {
                TabView {
                    LibraryView().tabItem { Label("Library", systemImage: "books.vertical") }
                    SearchView().tabItem { Label("Search", systemImage: "magnifyingglass") }
                }
                .environment(app)
                .task { await app.loadMe() }
            } else {
                ConnectView()
                    .environment(app)
            }
        }
    }
}
