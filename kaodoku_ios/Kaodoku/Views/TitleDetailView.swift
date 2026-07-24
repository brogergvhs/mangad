import SwiftUI

struct TitleDetailView: View {
    @Environment(AppState.self) private var app
    let titleID: Int64
    @State private var progress: TitleReadProgress?
    @State private var volumes = false
    @State private var readerChapter: ChapterProgress?
    @State private var note: String?
    @State private var downloading = false

    private var canManage: Bool { app.me?.can("library.manage") == true }

    var body: some View {
        List {
            if let p = progress {
                Section {
                    header(p)
                }
                if p.title.volumeCount > 0 {
                    Picker("Content", selection: $volumes) {
                        Text("Chapters").tag(false)
                        Text("Volumes").tag(true)
                    }
                    .pickerStyle(.segmented)
                    .listRowBackground(Color.clear)
                }
                Section(volumes ? "Volumes" : "Chapters") {
                    ForEach(p.chapters) { ch in
                        Button {
                            readerChapter = ch
                        } label: {
                            ChapterRow(chapter: ch, volumes: volumes,
                                       local: !volumes && app.store.isDownloaded(ch.id))
                        }
                        .disabled(!volumes && !ch.downloaded && !app.store.isDownloaded(ch.id))
                    }
                }
            } else {
                ProgressView()
            }
        }
        .navigationTitle(progress?.title.displayTitle ?? "")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: volumes) { await load() }
        .fullScreenCover(item: $readerChapter, onDismiss: { Task { await load() } }) { ch in
            ReaderView(titleID: titleID, startChapter: ch.id, volumes: volumes)
        }
        .toolbar {
            if let p = progress {
                Button {
                    toggleFavourite(p)
                } label: {
                    Image(systemName: p.title.favourite ? "heart.fill" : "heart")
                }
                actionsMenu(p)
            }
        }
        .overlay(alignment: .bottom) {
            if let note {
                Text(note)
                    .font(.footnote)
                    .padding(.horizontal, 12).padding(.vertical, 8)
                    .background(.thinMaterial, in: Capsule())
                    .padding(.bottom, 12)
                    .task { try? await Task.sleep(for: .seconds(2)); self.note = nil }
            }
        }
    }

    // header mirrors the web title page: cover, name, progress bar block,
    // then the mangaDetail badges/description/genres.
    private func header(_ p: TitleReadProgress) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                Cover(path: p.title.coverImage, adult: p.title.isAdult)
                    .frame(width: 110)
                VStack(alignment: .leading, spacing: 6) {
                    Text(p.title.displayTitle).font(.title3.bold())
                    if !p.title.monitored {
                        Label("Not monitored", systemImage: "bell.slash").font(.caption).foregroundStyle(.secondary)
                    }
                    TitleProgressBlock(title: p.title)
                    if let next = p.chapters.first(where: { $0.id == p.nextChapterId }) {
                        Button(p.readChapters > 0 ? "Continue reading" : "Start reading") {
                            readerChapter = next
                        }
                        .buttonStyle(.borderedProminent)
                    }
                }
            }
            if let manga = p.manga {
                MangaDetailBlock(manga: manga)
            }
        }
    }

    private func actionsMenu(_ p: TitleReadProgress) -> some View {
        Menu {
            if !volumes {
                Button("Download to device", systemImage: "iphone.and.arrow.down") {
                    downloadAll(p)
                }
                .disabled(downloading || !p.chapters.contains { $0.downloaded && !app.store.isDownloaded($0.id) })
                if !app.store.entries(titleId: titleID).isEmpty {
                    Button("Remove device downloads", systemImage: "trash", role: .destructive) {
                        app.store.deleteTitle(titleID)
                        note = "Device downloads removed"
                    }
                }
            }
            if canManage {
                Divider()
                Button(p.title.monitored ? "Stop monitoring" : "Monitor", systemImage: p.title.monitored ? "bell.slash" : "bell") {
                    patch(["monitored": !p.title.monitored], done: p.title.monitored ? "Monitoring off" : "Monitoring on")
                }
                Button("Refresh chapters", systemImage: "arrow.clockwise") {
                    enqueue("refresh_title", done: "Refresh queued")
                }
                Button("Download missing", systemImage: "arrow.down.circle") {
                    enqueue("download_missing", done: "Download queued")
                }
                Button("Sync AniList", systemImage: "arrow.triangle.2.circlepath") {
                    post("/api/v1/anilist/sync", body: ["title_id": titleID], done: "AniList synced")
                }
            }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
    }

    // downloadAll pulls every server-downloaded chapter not yet on the device.
    private func downloadAll(_ p: TitleReadProgress) {
        guard let api = app.api else { return }
        let todo = p.chapters.filter { $0.downloaded && !app.store.isDownloaded($0.id) }
        downloading = true
        Task {
            defer { downloading = false }
            for (i, ch) in todo.enumerated() {
                note = "Downloading \(i + 1)/\(todo.count)…"
                do {
                    let tmp = try await api.download("/api/v1/reader/chapters/\(ch.id)/archive")
                    try app.store.save(file: tmp, chapterID: ch.id, titleId: titleID,
                                       titleName: p.title.displayTitle, label: ch.label)
                } catch {
                    note = error.localizedDescription
                    return
                }
            }
            let cover = try? await api.data("GET", p.title.coverImage)
            app.store.saveTitle(LocalStore.TitleInfo(
                id: titleID, name: p.title.displayTitle, isAdult: p.title.isAdult, detail: p.manga,
                readCount: p.title.readCount, completedCount: p.title.completedCount,
                discoveredCount: p.title.discoveredCount, missingCount: p.title.missingCount
            ), coverData: cover)
            note = "Downloaded \(todo.count) chapters"
        }
    }

    private func load() async {
        guard let api = app.api else { return }
        let mode = volumes ? "?mode=volumes" : ""
        progress = try? await api.get("/api/v1/reader/titles/\(titleID)\(mode)")
    }

    private func toggleFavourite(_ p: TitleReadProgress) {
        guard let api = app.api else { return }
        let method = p.title.favourite ? "DELETE" : "PUT"
        Task {
            _ = try? await api.data(method, "/api/v1/library/\(titleID)/favourite")
            await load()
        }
    }

    private func patch(_ body: [String: Bool], done: String) {
        guard let api = app.api else { return }
        Task {
            do {
                _ = try await api.data("PATCH", "/api/v1/library/\(titleID)", body: body)
                note = done
                await load()
            } catch { note = error.localizedDescription }
        }
    }

    private func enqueue(_ type: String, done: String) {
        let body: [String: JSONValue] = ["type": .string(type), "title_id": .int(titleID)]
        post("/api/v1/jobs/enqueue", body: body, done: done)
    }

    private func post(_ path: String, body: some Encodable, done: String) {
        guard let api = app.api else { return }
        Task {
            do {
                _ = try await api.data("POST", path, body: body)
                note = done
            } catch { note = error.localizedDescription }
        }
    }
}

// TitleProgressBlock is the web progressBar partial: two-color bar plus the
// "X/Y read · N missing · size · vols" line (volumes fallback included).
struct TitleProgressBlock: View {
    let title: Title

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if title.discoveredCount > 0 {
                BarView(read: pct(title.readCount, title.discoveredCount),
                        full: pct(title.completedCount, title.discoveredCount))
                Text(line).font(.caption).foregroundStyle(.secondary)
            } else if title.volumeCount > 0 {
                BarView(read: pct(title.volumeReadCount, title.volumeCount), full: 1)
                Text("\(title.volumeReadCount)/\(title.volumeCount) volumes read · \(humanBytes(title.volumeBytes))")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private var line: String {
        var parts = ["\(title.readCount)/\(title.discoveredCount) read"]
        if title.missingCount > 0 { parts.append("\(title.missingCount) missing") }
        if title.failedCount > 0 { parts.append("\(title.failedCount) failed") }
        parts.append(humanBytes(title.sizeBytes))
        if title.volumeCount > 0 { parts.append("\(title.volumeReadCount)/\(title.volumeCount) vols") }
        return parts.joined(separator: " · ")
    }
}

// MangaDetailBlock is the web mangaDetail cell: badge row, description,
// authors/AniList-counts line, genre chips.
struct MangaDetailBlock: View {
    let manga: MangaDetail

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            WrapLayout {
                if let s = manga.status, !s.isEmpty { Badge(text: s, style: .soft) }
                if let f = manga.format, !f.isEmpty { Badge(text: f) }
                if let sc = manga.averageScore, sc > 0 { Badge(text: "★ \(sc)%", style: .warning) }
                if let y = manga.year, y > 0 { Badge(text: String(y)) }
            }
            if let d = manga.description, !d.isEmpty {
                Text(d.strippedHTML).font(.subheadline).foregroundStyle(.secondary)
            }
            if !metaLine.isEmpty {
                Text(metaLine).font(.caption).foregroundStyle(.tertiary)
            }
            if let genres = manga.genres, !genres.isEmpty {
                WrapLayout {
                    ForEach(genres, id: \.self) { Badge(text: $0, style: .outline) }
                }
            }
        }
    }

    private var metaLine: String {
        var parts: [String] = []
        if let a = manga.authors, !a.isEmpty { parts.append("By \(a.joined(separator: ", "))") }
        if let c = manga.chapters, c > 0 { parts.append("\(c) chapters (AniList)") }
        if let v = manga.volumes, v > 0 { parts.append("\(v) volumes (AniList)") }
        return parts.joined(separator: " · ")
    }
}

// JSONValue lets small mixed-type bodies stay one-liners.
enum JSONValue: Encodable {
    case string(String)
    case int(Int64)

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let v): try c.encode(v)
        case .int(let v): try c.encode(v)
        }
    }
}

struct ChapterRow: View {
    let chapter: ChapterProgress
    var volumes = false
    var local = false

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text("\(volumes ? "Volume" : "Chapter") \(chapter.label)")
                    .foregroundStyle(volumes || chapter.downloaded || local ? .primary : .secondary)
                if !chapter.title.isEmpty {
                    Text(chapter.title).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
            if chapter.bytes > 0 {
                Text(humanBytes(chapter.bytes)).font(.caption2).foregroundStyle(.tertiary)
            }
            if local {
                Image(systemName: "iphone").font(.caption).foregroundStyle(.secondary)
            }
            if chapter.completed {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            } else if chapter.readPages > 0 {
                Text("\(chapter.readPages)/\(chapter.totalPages)")
                    .font(.caption).foregroundStyle(.secondary)
            } else if !volumes && !chapter.downloaded {
                Image(systemName: "icloud.slash").foregroundStyle(.secondary)
            }
        }
    }
}
