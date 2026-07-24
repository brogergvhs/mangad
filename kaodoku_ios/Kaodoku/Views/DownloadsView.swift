import SwiftUI

// DownloadsView mirrors the Library grid over local content: cover cards with
// read progress of the downloaded chapters, sizes, then a title page identical
// to the online one and the offline reader.
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
            .nordScreen()
            .navigationTitle("Downloads")
            .navigationDestination(for: Int64.self) { LocalTitleView(titleId: $0) }
            .onAppear { app.store.prune() }
        }
    }
}

// LocalTitleCard matches TitleCard's web-card layout. The bar covers only the
// downloaded chapters: green = read share of what's on the device.
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
                Text("\(title.readCount)/\(title.entries.count) read").lineLimit(1)
                Spacer(minLength: 8)
                Text(humanBytes(title.size))
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            BarView(read: pct(Int64(title.readCount), Int64(title.entries.count)), full: 1)
        }
    }
}

extension LocalStore.LocalTitle {
    var readCount: Int { entries.count { $0.isRead } }
}

// LocalTitleView is the offline title page, identical in layout to
// TitleDetailView: header, content header with the Read button, chapter rows.
struct LocalTitleView: View {
    @Environment(AppState.self) private var app
    let titleId: Int64
    @State private var reading: LocalStore.Entry?
    @State private var showRemoveRange = false

    private var title: LocalStore.LocalTitle? { app.store.titles.first { $0.id == titleId } }

    var body: some View {
        List {
            if let title {
                Section { header(title) }
                    .nordRows()
                Section { contentHeader(title) }
                    .listRowBackground(Color.clear)
                Section("Chapters") {
                    ForEach(title.entries) { entry in
                        Button {
                            reading = entry
                        } label: {
                            ChapterRow(chapter: chapterProgress(entry), local: true)
                        }
                    }
                    .onDelete { offsets in
                        for i in offsets { app.store.delete(title.entries[i].id) }
                    }
                }
                .nordRows()
            }
        }
        .nordScreen()
        .navigationTitle(title?.info.name ?? "")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            Menu {
                Menu {
                    Button("All chapters", role: .destructive) { app.store.deleteTitle(titleId) }
                    Button("Range…") { showRemoveRange = true }
                } label: {
                    Label("Remove downloads", systemImage: "trash")
                }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
        }
        .sheet(isPresented: $showRemoveRange) {
            RangeSheet(title: "Remove range", action: "Remove from device") { from, to in
                for e in title?.entries ?? [] {
                    if let n = Double(e.label), Int(n) >= from, Int(n) <= to {
                        app.store.delete(e.id)
                    }
                }
            }
        }
        .fullScreenCover(item: $reading) { entry in
            ReaderView(titleID: titleId, startChapter: entry.id,
                       localChapters: title?.entries ?? [entry])
        }
    }

    private func header(_ title: LocalStore.LocalTitle) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Spacer()
                Cover(path: "", adult: title.info.isAdult, local: title.coverURL).frame(width: 170)
                Spacer()
            }
            Text(title.info.name).font(.title2.bold())
            VStack(alignment: .leading, spacing: 4) {
                BarView(read: pct(Int64(title.readCount), Int64(title.entries.count)), full: 1)
                Text("\(title.readCount)/\(title.entries.count) read · \(humanBytes(title.size)) on device")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let detail = title.info.detail {
                MangaDetailBlock(manga: detail)
            }
        }
    }

    private func contentHeader(_ title: LocalStore.LocalTitle) -> some View {
        VStack(spacing: 10) {
            if let next = title.entries.first(where: { !$0.isRead }) ?? title.entries.last {
                Button {
                    reading = next
                } label: {
                    Text(title.entries.contains { ($0.readPages ?? 0) > 0 || $0.isRead } ? "Continue reading" : "Read")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
            }
        }
    }

    // chapterProgress adapts a local entry to the shared ChapterRow.
    private func chapterProgress(_ e: LocalStore.Entry) -> ChapterProgress {
        ChapterProgress(id: e.id, titleId: e.titleId, label: e.label, title: "", numberMain: 0,
                        downloaded: true, bytes: e.size, pages: e.pages, totalPages: e.pages,
                        readPages: e.readPages ?? 0, completed: e.isRead, manual: false,
                        firstUnreadPage: 0, lastReadAt: nil)
    }
}
