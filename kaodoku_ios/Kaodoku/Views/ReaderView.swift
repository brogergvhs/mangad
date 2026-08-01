import SwiftUI

// ReaderView shows pages in paged (LTR/RTL) or vertical strip mode. Online it
// follows the server manifest window; with localChapters it reads device CBZs
// fully offline. Pages downloaded to the device always load locally first.
// Each settled page is marked locally and queued for a batched upload.
struct ReaderView: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    @Environment(\.displayScale) private var displayScale
    let titleID: Int64
    let startChapter: Int64
    var volumes = false
    var localChapters: [LocalStore.Entry]? = nil

    struct PageRef: Hashable, Sendable {
        var chapterID: Int64
        var label: String
        var page: Int
        var total: Int
        var url: String
        var localURL: URL?
        var volume = false
    }

    // Per-page width/height ratios, so the strip reader knows each page's exact
    // height up front. Seeded from downloads on load, cleared per session.
    @MainActor static var aspects: [String: CGFloat] = [:]

    @State private var pages: [PageRef] = []
    @State private var index = 0
    @State private var extendBase = ""
    @State private var lastChapterID: Int64 = 0
    @State private var noMore = false
    @State private var showBar = true
    @State private var showSettings = false
    @State private var scrollID: Int?
    @State private var loadFailed = false
    @State private var extendChapter: Int64?
    @State private var zoom: CGFloat = 1
    @State private var zoomBase: CGFloat = 1

    // stripLoader fetches a page for the UICollectionView reader: the device
    // CBZ first, then the network, downsampled off the main thread.
    private var stripLoader: (PageRef, CGSize) async -> UIImage? {
        let api = app.api
        return { ref, size in
            if let local = ref.localURL,
               let img = await LocalStore.pageImage(at: local, page: ref.page, maxPixelSize: size) {
                return img
            }
            guard let api, !ref.url.isEmpty, let data = try? await api.data("GET", ref.url) else { return nil }
            return await Task.detached(priority: .userInitiated) {
                UIImage.downsampled(data, maxPixelSize: size)
            }.value
        }
    }

    private var paged: Bool { app.settings.readerMode != "strip" }
    private var rtl: Bool { app.settings.readerDir == "rtl" }

    var body: some View {
        GeometryReader { geo in
            let pixels = CGSize(width: max(1, geo.size.width * displayScale),
                                height: max(1, geo.size.height * displayScale))
            ZStack {
                Color.black.ignoresSafeArea()
                if loadFailed {
                    Label("This chapter can't be opened", systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.white)
                } else if pages.isEmpty {
                    ProgressView().tint(.white)
                } else if paged {
                    ScrollView(.horizontal, showsIndicators: false) {
                        LazyHStack(spacing: 0) {
                            ForEach(pages.indices, id: \.self) { i in
                                ReaderPage(ref: pages[i], maxPixelSize: pixels,
                                           active: abs(i - index) <= 1,
                                           zoom: $zoom, zoomBase: $zoomBase)
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
                    StripReader(
                        pages: pages,
                        startIndex: index,
                        maxPixelSize: CGSize(width: pixels.width, height: 8_192),
                        estimateAspect: geo.size.width / max(geo.size.height, 1),
                        loadImage: stripLoader,
                        onPage: onSettle
                    )
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
        .task(id: extendChapter) {
            guard let last = extendChapter, let api = app.api else { return }
            defer { extendChapter = nil }
            guard let m: Manifest = try? await api.get(extendBase + String(last)) else { return }
            append(m.chapters)
        }
        .onDisappear {
            Self.aspects.removeAll()
            Task {
                await app.store.flush(app.api)
                await LocalStore.clearPageCache()
            }
        }
        .sheet(isPresented: $showSettings) { ReaderSettingsSheet() }
    }

    private var bar: some View {
        VStack {
            HStack {
                Button("Close") { dismiss() }
                Spacer()
                if pages.indices.contains(index) {
                    let p = pages[index]
                    let name = volumes && Double(p.label) == nil ? p.label : "\(volumes ? "Vol" : "Ch") \(p.label)"
                    Text("\(name) · \(p.page)/\(p.total)").lineLimit(1)
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

    private func onSettle(_ i: Int) {
        index = i
        mark(pages[i])
        extendIfNeeded()
    }

    private func load(chapter: Int64, resume: Bool) async {
        if let localChapters {
            noMore = true
            let volumeMode = volumes
            let build = Task.detached(priority: .userInitiated) {
                var refs: [PageRef] = []
                var aspects: [String: CGFloat] = [:]
                for e in localChapters where e.pages > 0 {
                    guard !Task.isCancelled else { break }
                    let local = LocalStore.root.appendingPathComponent(e.path)
                    guard FileManager.default.fileExists(atPath: local.path) else { continue }
                    for (pi, aspect) in e.pageAspects.enumerated() where aspect > 0 {
                        aspects["\(volumeMode ? "v" : "c")\(e.id)-\(pi + 1)"] = aspect
                    }
                    for page in 1...e.pages {
                        refs.append(PageRef(chapterID: e.id, label: e.label, page: page,
                                            total: e.pages, url: "", localURL: local,
                                            volume: volumeMode))
                    }
                }
                return (refs, aspects)
            }
            let loaded = await withTaskCancellationHandler {
                await build.value
            } onCancel: {
                build.cancel()
            }
            guard !Task.isCancelled else { return }
            Self.aspects.merge(loaded.1) { _, new in new }
            pages = loaded.0
            let entry = localChapters.first { $0.id == chapter }
            let resumePage = entry.map { $0.isRead ? 1 : min($0.readPages + 1, max($0.pages, 1)) } ?? 1
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
            let local = app.store.url(for: ch.id, volume: volumes)
            pages.append(contentsOf: ch.pages.map {
                PageRef(chapterID: ch.id, label: ch.label, page: $0.page, total: ch.pageCount,
                        url: $0.url, localURL: local, volume: volumes)
            })
            lastChapterID = ch.id
            added = true
        }
        if !added { noMore = true }
    }

    private func extendIfNeeded() {
        guard extendChapter == nil, !noMore, pages.count - index < 5, lastChapterID > 0 else { return }
        extendChapter = lastChapterID
    }

    private func mark(_ p: PageRef) {
        app.store.recordMark(id: p.chapterID, volume: volumes, page: p.page, totalPages: p.total)
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

// ReaderPage renders one page in paged mode: fit-to-screen with pinch zoom.
// A device CBZ copy is preferred over the network; the bitmap is released when
// the page leaves the active window so long sessions stay bounded.
struct ReaderPage: View {
    @Environment(AppState.self) private var app
    let ref: ReaderView.PageRef
    let maxPixelSize: CGSize
    var active = true
    @Binding var zoom: CGFloat
    @Binding var zoomBase: CGFloat
    @State private var image: UIImage?
    @State private var failed = false

    var body: some View {
        content
            .onChange(of: active) { _, a in
                if !a { image = nil; failed = false }
            }
            .task(id: active) { await loadIfNeeded() }
    }

    @ViewBuilder private var content: some View {
        if let image {
            GeometryReader { g in
                let fit = Self.fitted(image.size, in: g.size)
                ScrollView([.horizontal, .vertical], showsIndicators: false) {
                    Image(uiImage: image)
                        .resizable()
                        .frame(width: fit.width * zoom, height: fit.height * zoom)
                        .frame(width: max(fit.width * zoom, g.size.width),
                               height: max(fit.height * zoom, g.size.height))
                }
            }
            .gesture(MagnifyGesture()
                .onChanged { zoom = max(1, min(3, zoomBase * $0.magnification)) }
                .onEnded { _ in zoomBase = zoom })
        } else if failed {
            Label("Page failed to load", systemImage: "exclamationmark.triangle").foregroundStyle(.white)
        } else {
            ProgressView().tint(.white)
        }
    }

    nonisolated static func fitted(_ img: CGSize, in box: CGSize) -> CGSize {
        guard img.width > 0, img.height > 0, box.width > 0, box.height > 0 else { return box }
        let s = min(box.width / img.width, box.height / img.height)
        return CGSize(width: img.width * s, height: img.height * s)
    }

    private func loadIfNeeded() async {
        guard active, image == nil else { return }
        if let local = ref.localURL,
           let img = await LocalStore.pageImage(at: local, page: ref.page, maxPixelSize: maxPixelSize) {
            if !Task.isCancelled { image = img }
            return
        }
        guard let api = app.api, !ref.url.isEmpty, let data = try? await api.data("GET", ref.url) else {
            if !Task.isCancelled { failed = true }
            return
        }
        let decode = Task.detached(priority: .userInitiated) { UIImage.downsampled(data, maxPixelSize: maxPixelSize) }
        let img = await withTaskCancellationHandler { await decode.value } onCancel: { decode.cancel() }
        guard !Task.isCancelled else { return }
        if let img { image = img } else { failed = true }
    }
}
