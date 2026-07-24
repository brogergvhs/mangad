import SwiftUI

// DownloadsView mirrors the Library grid over local content: cover cards with
// chapter counts, sizes, and progress, then an offline title page and reader.
struct DownloadsView: View {
    @Environment(AppState.self) private var app

    var body: some View {
        NavigationStack {
            ScrollView {
                if app.store.chapters.isEmpty {
                    Text("Nothing downloaded yet. Use a title's ⋯ menu to download chapters to this device.")
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 32)
                        .padding(.top, 80)
                }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 110), spacing: 12)], spacing: 16) {
                    ForEach(app.store.titles) { title in
                        NavigationLink(value: title.id) {
                            LocalTitleCard(title: title)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal)
            }
            .navigationTitle("Downloads")
            .navigationDestination(for: Int64.self) { LocalTitleView(titleId: $0) }
            .onAppear { app.store.prune() }
        }
    }
}

// LocalTitleCard matches TitleCard's web-card layout with local data:
// downloaded-chapter count and on-device size in the stats line.
struct LocalTitleCard: View {
    let title: LocalStore.LocalTitle

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Cover(path: "", adult: title.info.isAdult, local: title.coverURL)
            Text(title.info.name)
                .font(.caption)
                .lineLimit(2, reservesSpace: true)
                .foregroundStyle(.primary)
            HStack(alignment: .firstTextBaseline) {
                Text("\(title.entries.count) ch").lineLimit(1)
                Spacer(minLength: 8)
                Text(humanBytes(title.size))
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            BarView(read: pct(title.info.readCount, title.info.discoveredCount),
                    full: pct(title.info.completedCount, title.info.discoveredCount))
        }
    }
}

// LocalTitleView is the offline title page: web-style header from the stored
// snapshot, then chapter rows with pages and file sizes.
struct LocalTitleView: View {
    @Environment(AppState.self) private var app
    let titleId: Int64
    @State private var reading: LocalStore.Entry?

    private var title: LocalStore.LocalTitle? { app.store.titles.first { $0.id == titleId } }

    var body: some View {
        List {
            if let title {
                Section {
                    VStack(alignment: .leading, spacing: 12) {
                        HStack(alignment: .top, spacing: 12) {
                            Cover(path: "", adult: title.info.isAdult, local: title.coverURL)
                                .frame(width: 110)
                            VStack(alignment: .leading, spacing: 6) {
                                Text(title.info.name).font(.title3.bold())
                                Text("\(title.entries.count) chapters · \(humanBytes(title.size)) on device")
                                    .font(.caption).foregroundStyle(.secondary)
                                if title.info.discoveredCount > 0 {
                                    BarView(read: pct(title.info.readCount, title.info.discoveredCount),
                                            full: pct(title.info.completedCount, title.info.discoveredCount))
                                    Text(progressLine(title.info)).font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                        if let detail = title.info.detail {
                            MangaDetailBlock(manga: detail)
                        }
                    }
                }
                Section("Chapters") {
                    ForEach(title.entries) { entry in
                        Button {
                            reading = entry
                        } label: {
                            HStack {
                                Text("Chapter \(entry.label)")
                                Spacer()
                                Text("\(entry.pages) pages · \(humanBytes(entry.size))")
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
        }
        .navigationTitle(title?.info.name ?? "")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            Menu {
                Button("Remove all downloads", systemImage: "trash", role: .destructive) {
                    app.store.deleteTitle(titleId)
                }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
        }
        .fullScreenCover(item: $reading) { entry in
            ReaderView(titleID: titleId, startChapter: entry.id,
                       localChapters: title?.entries ?? [entry])
        }
    }

    private func progressLine(_ info: LocalStore.TitleInfo) -> String {
        var parts = ["\(info.readCount)/\(info.discoveredCount) read"]
        if info.missingCount > 0 { parts.append("\(info.missingCount) missing") }
        return parts.joined(separator: " · ") + " · last sync"
    }
}
