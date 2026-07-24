import SwiftUI

@main
struct KaodokuApp: App {
    @State private var app = AppState()

    var body: some Scene {
        WindowGroup {
            Group {
                if app.connected {
                    TabView {
                        LibraryView().tabItem { Label("Library", systemImage: "books.vertical") }
                        SearchView().tabItem { Label("Search", systemImage: "magnifyingglass") }
                        DownloadsView().tabItem { Label("Downloads", systemImage: "arrow.down.circle") }
                    }
                    .task { await app.loadMe() }
                } else {
                    ConnectView()
                }
            }
            .environment(app)
            .tint(Theme.primary)
            .preferredColorScheme(.dark)
        }
    }
}
