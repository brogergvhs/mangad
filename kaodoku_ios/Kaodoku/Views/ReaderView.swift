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
    @State private var scrollID: Int?
    @State private var loadFailed = false
    @State private var settled = false

    private var paged: Bool { app.settings.readerMode != "strip" }
    private var rtl: Bool { app.settings.readerDir == "rtl" }

    var body: some View {
        GeometryReader { geo in
            ZStack {
                Color.black.ignoresSafeArea()
                if loadFailed {
                    Label("This chapter can't be opened", systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.white)
                } else if pages.isEmpty {
                    ProgressView().tint(.white)
                } else if paged {
                    // A paging ScrollView instead of TabView(.page): TabView can
                    // land between pages when the page array grows mid-swipe.
                    ScrollView(.horizontal, showsIndicators: false) {
                        LazyHStack(spacing: 0) {
                            ForEach(pages.indices, id: \.self) { i in
                                ReaderPage(ref: pages[i], active: abs(i - index) <= 3)
                                    .containerRelativeFrame([.horizontal, .vertical])
                            }
                        }
                        .scrollTargetLayout()
                    }
                    .scrollTargetBehavior(.paging)
                    .scrollPosition(id: $scrollID)
                    .environment(\.layoutDirection, rtl ? .rightToLeft : .leftToRight)
                    .ignoresSafeArea()
                    .onChange(of: scrollID) {
                        if let i = scrollID, pages.indices.contains(i) { onSettle(i) }
                    }
                } else {
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            ForEach(pages.indices, id: \.self) { i in
                                ReaderPage(ref: pages[i], strip: true, active: abs(i - index) <= 4)
                                    .onAppear { onSettle(i) }
                            }
                        }
                        .scrollTargetLayout()
                    }
                    .scrollPosition(id: $scrollID, anchor: settled ? nil : .top)
                    .ignoresSafeArea()
                }
                if showBar { bar }
            }
            .statusBarHidden(!showBar)
            .onTapGesture { point in
                handleTap(point, width: geo.size.width)
            }
        }
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

    // handleTap mirrors the web tap zones in paged mode: outer thirds page in
    // the tapped physical direction, the middle toggles the bar.
    private func handleTap(_ point: CGPoint, width: CGFloat) {
        guard paged, !pages.isEmpty, width > 0 else {
            showBar.toggle()
            return
        }
        let leftStep = rtl ? 1 : -1
        switch point.x {
        case ..<(width / 3): step(by: leftStep)
        case (width * 2 / 3)...: step(by: -leftStep)
        default: showBar.toggle()
        }
    }

    private func step(by delta: Int) {
        let target = min(max(index + delta, 0), pages.count - 1)
        guard target != index else { return }
        withAnimation { scrollID = target }
    }

    // onSettle ignores rows that appear before the initial resume jump lands
    // (a strip's top rows fire onAppear first) so they aren't marked read.
    private func onSettle(_ i: Int) {
        if !settled {
            guard abs(i - index) <= 4 else { return }
            settled = true
        }
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
            // Resume at the first unread page from the LOCAL read state —
            // offline progress must survive without a server. Completed
            // chapters restart at page 1, like the web. The settle path does
            // the marking; a missing/unreadable chapter is an error, never a
            // silent fallback that would mark some other chapter.
            let entry = localChapters.first { $0.id == chapter }
            let resumePage = entry.map { $0.isRead ? 1 : min(($0.readPages ?? 0) + 1, max($0.pages, 1)) } ?? 1
            guard let i = pages.firstIndex(where: { $0.chapterID == chapter && $0.page == resumePage }) else {
                loadFailed = true
                return
            }
            index = i
            scrollID = i
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
                scrollID = index
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
    // aspects remembers each decoded page's width/height ratio so placeholders
    // and evicted pages keep their true height — unstable strip row heights
    // cause scroll jumps when scrolling upward.
    @MainActor static var aspects: [String: CGFloat] = [:]

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
            } else if strip, let ratio = Self.aspects[aspectKey] {
                ZStack {
                    Color.clear
                    ProgressView().tint(.white)
                }
                .aspectRatio(ratio, contentMode: .fit)
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
                setImage(img)
                return
            }
            if let api = app.api, !ref.url.isEmpty,
               let data = try? await api.data("GET", ref.url),
               let img = await Task.detached(priority: .userInitiated, operation: { UIImage.downsampled(data) }).value {
                setImage(img)
            } else {
                failed = true
            }
        }
    }

    private var aspectKey: String { "\(ref.chapterID)-\(ref.page)" }

    private func setImage(_ img: UIImage) {
        if img.size.height > 0 { Self.aspects[aspectKey] = img.size.width / img.size.height }
        image = img
    }
}
