import SwiftUI

// ReaderView streams pages online in paged (LTR/RTL) or vertical strip mode,
// following the server manifest window that extends as the reader approaches
// the end. Mode/direction sync via /me/settings. Each settled page is marked.
struct ReaderView: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    let titleID: Int64
    let startChapter: Int64
    var volumes = false

    struct PageRef: Hashable {
        var chapterID: Int64
        var label: String
        var page: Int
        var total: Int
        var url: String
    }

    @State private var pages: [PageRef] = []
    @State private var index = 0
    @State private var markBase = ""
    @State private var extendBase = ""
    @State private var lastChapterID: Int64 = 0
    @State private var noMore = false
    @State private var showBar = true
    @State private var showSettings = false

    private var paged: Bool { app.settings.readerMode != "strip" }
    private var rtl: Bool { app.settings.readerDir == "rtl" }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if pages.isEmpty {
                ProgressView().tint(.white)
            } else if paged {
                TabView(selection: $index) {
                    ForEach(pages.indices, id: \.self) { i in
                        ReaderPage(path: pages[i].url).tag(i)
                    }
                }
                .tabViewStyle(.page(indexDisplayMode: .never))
                .environment(\.layoutDirection, rtl ? .rightToLeft : .leftToRight)
                .ignoresSafeArea()
                .onChange(of: index) { onSettle(index) }
            } else {
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(pages.indices, id: \.self) { i in
                            ReaderPage(path: pages[i].url, strip: true)
                                .onAppear { onSettle(i) }
                        }
                    }
                }
                .ignoresSafeArea()
            }
            if showBar { bar }
        }
        .statusBarHidden(!showBar)
        .onTapGesture { showBar.toggle() }
        .task { await load(chapter: startChapter, resume: true) }
        .sheet(isPresented: $showSettings) { ReaderSettingsSheet() }
    }

    private var bar: some View {
        VStack {
            HStack {
                Button("Close") { dismiss() }
                Spacer()
                if pages.indices.contains(index) {
                    Text("\(volumes ? "Vol" : "Ch") \(pages[index].label) · \(pages[index].page)/\(pages[index].total)")
                }
                Spacer()
                Button {
                    showSettings = true
                } label: {
                    Image(systemName: "gearshape")
                }
            }
            .padding()
            .background(.black.opacity(0.6))
            .foregroundStyle(.white)
            Spacer()
        }
    }

    private func onSettle(_ i: Int) {
        index = i
        mark(pages[i])
        extendIfNeeded()
    }

    private func load(chapter: Int64, resume: Bool) async {
        guard let api = app.api else { return }
        let mode = volumes ? "&mode=volumes" : ""
        do {
            let m: Manifest = try await api.get("/api/v1/reader/titles/\(titleID)/manifest?chapter=\(chapter)\(mode)")
            markBase = m.markBase
            extendBase = m.extendBase
            append(m.chapters)
            if resume {
                if let i = pages.firstIndex(where: { $0.chapterID == m.resumeChapterId && $0.page == m.resumePage }) {
                    index = i
                }
                if pages.indices.contains(index) { mark(pages[index]) }
            }
        } catch {
            app.errorMessage = error.localizedDescription
        }
    }

    private func append(_ chapters: [Manifest.Chapter]) {
        var added = false
        for ch in chapters where !pages.contains(where: { $0.chapterID == ch.id }) {
            pages.append(contentsOf: ch.pages.map {
                PageRef(chapterID: ch.id, label: ch.label, page: $0.page, total: ch.pageCount, url: $0.url)
            })
            lastChapterID = ch.id
            added = true
        }
        if !added { noMore = true }
    }

    private func extendIfNeeded() {
        guard !noMore, pages.count - index < 5, lastChapterID > 0 else { return }
        let last = lastChapterID
        Task {
            guard let api = app.api,
                  let m: Manifest = try? await api.get(extendBase + String(last)) else { return }
            append(m.chapters)
        }
    }

    private func mark(_ p: PageRef) {
        guard let api = app.api else { return }
        Task {
            _ = try? await api.data("POST", markBase + "\(p.chapterID)/pages",
                                    body: ["page": p.page, "total_pages": p.total])
        }
    }
}

struct ReaderSettingsSheet: View {
    @Environment(AppState.self) private var app

    var body: some View {
        @Bindable var app = app
        NavigationStack {
            Form {
                Picker("Layout", selection: Binding(
                    get: { app.settings.readerMode ?? "paged" },
                    set: { app.settings.readerMode = $0; app.saveSettings() }
                )) {
                    Text("Paged").tag("paged")
                    Text("Long strip").tag("strip")
                }
                Picker("Direction", selection: Binding(
                    get: { app.settings.readerDir ?? "ltr" },
                    set: { app.settings.readerDir = $0; app.saveSettings() }
                )) {
                    Text("Left to right").tag("ltr")
                    Text("Right to left").tag("rtl")
                }
            }
            .navigationTitle("Reader")
            .navigationBarTitleDisplayMode(.inline)
        }
        .presentationDetents([.height(220)])
    }
}

// ReaderPage shows one page image; fit-to-screen when paged, full-width in strip.
struct ReaderPage: View {
    @Environment(AppState.self) private var app
    let path: String
    var strip = false
    @State private var image: UIImage?
    @State private var failed = false
    @State private var zoom: CGFloat = 1

    var body: some View {
        Group {
            if let image {
                if strip {
                    Image(uiImage: image).resizable().scaledToFit()
                } else {
                    ScrollView([.horizontal, .vertical], showsIndicators: false) {
                        Image(uiImage: image)
                            .resizable()
                            .scaledToFit()
                            .containerRelativeFrame([.horizontal, .vertical])
                            .scaleEffect(zoom)
                    }
                    .gesture(
                        MagnifyGesture().onChanged { zoom = max(1, min(3, $0.magnification)) }
                    )
                }
            } else if failed {
                Label("Page failed to load", systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.white)
                    .frame(minHeight: strip ? 200 : 0)
            } else {
                ProgressView().tint(.white)
                    .frame(maxWidth: .infinity, minHeight: strip ? 400 : 0)
            }
        }
        .task(id: path) {
            guard let api = app.api, image == nil else { return }
            if let data = try? await api.data("GET", path), let img = UIImage(data: data) {
                image = img
            } else {
                failed = true
            }
        }
    }
}
