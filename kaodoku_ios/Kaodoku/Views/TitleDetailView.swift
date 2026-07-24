import SwiftUI

struct TitleDetailView: View {
    @Environment(AppState.self) private var app
    let titleID: Int64
    @State private var progress: TitleReadProgress?
    @State private var readerChapter: ChapterProgress?

    var body: some View {
        List {
            if let p = progress {
                Section {
                    HStack(alignment: .top, spacing: 12) {
                        ServerImage(path: p.title.coverImage)
                            .frame(width: 90, height: 135)
                            .clipShape(RoundedRectangle(cornerRadius: 8))
                        VStack(alignment: .leading, spacing: 6) {
                            Text(p.title.displayTitle).font(.headline)
                            Text("\(p.readChapters)/\(p.totalChapters) chapters read")
                                .font(.subheadline).foregroundStyle(.secondary)
                            if p.title.averageScore > 0 {
                                Text("★ \(p.title.averageScore)%")
                                    .font(.subheadline).foregroundStyle(.secondary)
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
                Section("Chapters") {
                    ForEach(p.chapters) { ch in
                        Button {
                            readerChapter = ch
                        } label: {
                            ChapterRow(chapter: ch)
                        }
                        .disabled(!ch.downloaded)
                    }
                }
            } else {
                ProgressView()
            }
        }
        .navigationTitle(progress?.title.displayTitle ?? "")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .fullScreenCover(item: $readerChapter, onDismiss: { Task { await load() } }) { ch in
            ReaderView(titleID: titleID, startChapter: ch.id)
        }
    }

    private func load() async {
        guard let api = app.api else { return }
        progress = try? await api.get("/api/v1/reader/titles/\(titleID)")
    }
}

struct ChapterRow: View {
    let chapter: ChapterProgress

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text("Chapter \(chapter.label)").foregroundStyle(chapter.downloaded ? .primary : .secondary)
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
            } else if !chapter.downloaded {
                Image(systemName: "icloud.slash").foregroundStyle(.secondary)
            }
        }
    }
}
