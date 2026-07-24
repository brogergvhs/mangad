import SwiftUI

// DownloadsView lists device downloads from the local index, grouped by
// title, and opens the reader fully offline.
struct DownloadsView: View {
    @Environment(AppState.self) private var app
    @State private var reading: LocalStore.Entry?

    var body: some View {
        NavigationStack {
            List {
                ForEach(app.store.titles, id: \.id) { title in
                    Section(title.name) {
                        ForEach(title.entries) { entry in
                            Button {
                                reading = entry
                            } label: {
                                HStack {
                                    Text("Chapter \(entry.label)")
                                    Spacer()
                                    Text("\(entry.pages) pages")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            .foregroundStyle(.primary)
                        }
                        .onDelete { offsets in
                            for i in offsets { app.store.delete(title.entries[i].id) }
                        }
                    }
                }
                if app.store.chapters.isEmpty {
                    Text("Nothing downloaded yet. Use a title's ⋯ menu to download chapters to this device.")
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Downloads")
            .onAppear { app.store.prune() }
            .fullScreenCover(item: $reading) { entry in
                ReaderView(titleID: entry.titleId, startChapter: entry.id,
                           localChapters: app.store.entries(titleId: entry.titleId))
            }
        }
    }
}
