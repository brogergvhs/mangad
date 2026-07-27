import SwiftUI

@main
struct KaodokuApp: App {
    @State private var app = AppState()

    var body: some Scene {
        WindowGroup {
            Group {
                if app.connected {
                    @Bindable var app = app
                    TabView(selection: $app.tab) {
                        LibraryView().tabItem { Label("Library", systemImage: "books.vertical") }.tag(0)
                        SearchView().tabItem { Label("Search", systemImage: "magnifyingglass") }.tag(1)
                        DownloadsView().tabItem { Label("Downloads", systemImage: "arrow.down.circle") }.tag(2)
                        SettingsView().tabItem { Label("Settings", systemImage: "gearshape") }.tag(3)
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
