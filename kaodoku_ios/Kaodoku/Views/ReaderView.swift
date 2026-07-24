import SwiftUI

// ReaderView shows pages in paged (LTR/RTL) or vertical strip mode. Online it
// follows the server manifest window; with localChapters it reads device CBZs
// fully offline. Pages downloaded to the device always load locally first.
// Each settled page is marked; marks made offline queue for batch replay.
struct ReaderView: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    let titleID: Int64
    let startChapter: Int64
    var volumes = false
    var localChapters: [LocalStore.Entry]? = nil

    struct PageRef: Hashable {
        var chapterID: Int64
        var label: String
        var page: Int
        var total: Int
        var url: String
        var localURL: URL?
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
                        ReaderPage(ref: pages[i], active: abs(i - index) <= 3).tag(i)
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
                            ReaderPage(ref: pages[i], strip: true, active: abs(i - index) <= 4)
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
        .onDisappear {
            guard let api = app.api else { return }
            Task { await app.store.flush(api) }
        }
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
        if let localChapters {
            markBase = "/api/v1/reader/chapters/"
            noMore = true
            pages = localChapters.compactMap { e -> [PageRef]? in
                guard let local = app.store.url(for: e.id), e.pages > 0 else { return nil }
                return (1...e.pages).map {
                    PageRef(chapterID: e.id, label: e.label, page: $0, total: e.pages,
                            url: "", localURL: local)
                }
            }.flatMap { $0 }
            if let i = pages.firstIndex(where: { $0.chapterID == chapter }) { index = i }
            if pages.indices.contains(index) { mark(pages[index]) }
            return
        }
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
            let local = volumes ? nil : app.store.url(for: ch.id)
            pages.append(contentsOf: ch.pages.map {
                PageRef(chapterID: ch.id, label: ch.label, page: $0.page, total: ch.pageCount, url: $0.url, localURL: local)
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
        if !volumes { app.store.markLocal(chapterID: p.chapterID, page: p.page, total: p.total) }
        Task {
            do {
                guard let api = app.api else { throw APIError.badURL }
                _ = try await api.data("POST", markBase + "\(p.chapterID)/pages",
                                       body: ["page": p.page, "total_pages": p.total])
            } catch {
                app.store.queueMark(id: p.chapterID, volume: volumes, page: p.page, totalPages: p.total)
            }
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

// ReaderPage shows one page image; fit-to-screen when paged, full-width in
// strip. A device CBZ copy is preferred over the network. Pages outside the
// active window release their decoded bitmap so long sessions stay bounded.
struct ReaderPage: View {
    @Environment(AppState.self) private var app
    let ref: ReaderView.PageRef
    var strip = false
    var active = true
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
        .onChange(of: active) { _, a in
            if !a { image = nil; failed = false }
        }
        .task(id: active) {
            guard active, image == nil else { return }
            if let local = ref.localURL, let img = await LocalStore.pageImage(at: local, page: ref.page) {
                image = img
                return
            }
            if let api = app.api, !ref.url.isEmpty,
               let data = try? await api.data("GET", ref.url),
               let img = await Task.detached(priority: .userInitiated, operation: { UIImage.downsampled(data) }).value {
                image = img
            } else {
                failed = true
            }
        }
    }
}
