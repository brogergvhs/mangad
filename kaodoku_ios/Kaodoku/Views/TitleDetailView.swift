import SwiftUI

struct TitleDetailView: View {
    @Environment(AppState.self) private var app
    let titleID: Int64
    @State private var progress: TitleReadProgress?
    @State private var volumes = false
    @State private var readerChapter: ChapterProgress?
    @State private var note: String?

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
                            ChapterRow(chapter: ch, volumes: volumes)
                        }
                        .disabled(!volumes && !ch.downloaded)
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
                if canManage {
                    actionsMenu(p)
                }
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

    private func header(_ p: TitleReadProgress) -> some View {
        HStack(alignment: .top, spacing: 12) {
            ServerImage(path: p.title.coverImage)
                .frame(width: 90, height: 135)
                .clipShape(RoundedRectangle(cornerRadius: 8))
            VStack(alignment: .leading, spacing: 6) {
                Text(p.title.displayTitle).font(.headline)
                Text("\(p.readChapters)/\(p.totalChapters) \(volumes ? "volumes" : "chapters") read")
                    .font(.subheadline).foregroundStyle(.secondary)
                if p.title.averageScore > 0 {
                    Text("★ \(p.title.averageScore)%").font(.subheadline).foregroundStyle(.secondary)
                }
                if !p.title.monitored {
                    Label("Not monitored", systemImage: "bell.slash").font(.caption).foregroundStyle(.secondary)
                }
                if let next = p.chapters.first(where: { $0.id == p.nextChapterId }) {
                    Button(p.readChapters > 0 ? "Continue reading" : "Start reading") {
                        readerChapter = next
                    }
                    .buttonStyle(.borderedProminent)
                }
            }
        }
    }

    private func actionsMenu(_ p: TitleReadProgress) -> some View {
        Menu {
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
        } label: {
            Image(systemName: "ellipsis.circle")
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

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text("\(volumes ? "Volume" : "Chapter") \(chapter.label)")
                    .foregroundStyle(volumes || chapter.downloaded ? .primary : .secondary)
                if !chapter.title.isEmpty {
                    Text(chapter.title).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
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
