import Foundation
import ImageIO
import UIKit

// LocalStore owns offline content in Documents (visible in the Files app as
// the "Kaodoku" folder): one directory per title with CBZs inside, an index
// at .index.json, and a queue of progress marks made while offline.
@Observable @MainActor
final class LocalStore {
    struct Entry: Codable, Identifiable {
        var id: Int64
        var titleId: Int64
        var titleName: String
        var label: String
        var path: String
        var pages: Int
        var size: Int64 = 0
        // Local read state, kept current offline (optional: decode-tolerant).
        var readPages: Int?
        var completed: Bool?
        var pageAspects: [Double]?
        var volume: Bool?

        var isRead: Bool { completed ?? false }
        var isVolume: Bool { volume ?? false }
    }

    // TitleInfo snapshots the server title at download time so the Downloads
    // grid and offline title page can render the web card/detail layout.
    struct TitleInfo: Codable, Identifiable {
        var id: Int64
        var name: String
        var cover: String?
        var isAdult: Bool = false
        var detail: MangaDetail?
        var readCount: Int64 = 0
        var completedCount: Int64 = 0
        var discoveredCount: Int64 = 0
        var missingCount: Int64 = 0
    }

    private struct Index: Codable {
        var titles: [TitleInfo] = []
        var chapters: [Entry] = []
    }

    struct QueuedMark: Codable, Equatable {
        var chapterId: Int64
        var volumeId: Int64?
        var page: Int
        var totalPages: Int
        var readAt: Date
    }

    struct Pending: Identifiable {
        var id: Int64
        var titleId: Int64
        var label: String
        var pages: Int
        var volume: Bool
    }

    private(set) var chapters: [Int64: Entry] = [:]
    private(set) var titleInfo: [Int64: TitleInfo] = [:]
    private(set) var pending: [Int64: Pending] = [:]
    private var queue: [QueuedMark] = []
    private var flushing = false

    nonisolated static let root = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
    private static let indexURL = root.appendingPathComponent(".index.json")
    private static let queueURL = root.appendingPathComponent(".marks.json")

    // Legacy phase-3 index shape: a flat [Entry] array without sizes.
    private struct LegacyEntry: Decodable {
        var id: Int64
        var titleId: Int64
        var titleName: String
        var label: String
        var path: String
        var pages: Int
    }

    init() {
        if let data = try? Data(contentsOf: Self.indexURL) {
            if let idx = try? JSONDecoder().decode(Index.self, from: data) {
                chapters = Dictionary(idx.chapters.map { (key($0.id, volume: $0.isVolume), $0) },
                                      uniquingKeysWith: { a, _ in a })
                titleInfo = Dictionary(uniqueKeysWithValues: idx.titles.map { ($0.id, $0) })
            } else if let legacy = try? JSONDecoder().decode([LegacyEntry].self, from: data) {
                chapters = Dictionary(uniqueKeysWithValues: legacy.map { e in
                    let size = (try? FileManager.default.attributesOfItem(
                        atPath: Self.root.appendingPathComponent(e.path).path))?[.size] as? Int64 ?? 0
                    return (e.id, Entry(id: e.id, titleId: e.titleId, titleName: e.titleName,
                                        label: e.label, path: e.path, pages: e.pages, size: size))
                })
                persistIndex()
            }
        }
        if let data = try? Data(contentsOf: Self.queueURL) {
            queue = (try? JSONDecoder().decode([QueuedMark].self, from: data)) ?? []
        }
        prune()
    }

    // prune drops index entries whose file was deleted through the Files app,
    // and title records left without chapters.
    func prune() {
        let dead = chapters.values.filter {
            $0.pages == 0 || !FileManager.default.fileExists(atPath: Self.root.appendingPathComponent($0.path).path)
        }
        for e in dead { chapters.removeValue(forKey: key(e.id, volume: e.isVolume)) }
        let orphans = titleInfo.keys.filter { id in
            !chapters.values.contains { $0.titleId == id } && !pending.values.contains { $0.titleId == id }
        }
        for id in orphans { titleInfo.removeValue(forKey: id) }
        if !dead.isEmpty || !orphans.isEmpty { persistIndex() }
    }

    private func key(_ id: Int64, volume: Bool) -> Int64 { volume ? -id : id }

    func isDownloaded(_ id: Int64, volume: Bool = false) -> Bool { url(for: id, volume: volume) != nil }

    // url returns the local CBZ if the entry exists and the file survived
    // (users can delete files through the Files app).
    func url(for id: Int64, volume: Bool = false) -> URL? {
        guard let e = chapters[key(id, volume: volume)] else { return nil }
        let u = Self.root.appendingPathComponent(e.path)
        return FileManager.default.fileExists(atPath: u.path) ? u : nil
    }

    func entries(titleId: Int64) -> [Entry] {
        chapters.values.filter { $0.titleId == titleId }
            .sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending }
    }

    struct LocalTitle: Identifiable {
        var id: Int64
        var info: TitleInfo
        var entries: [Entry]
        var pending: [Pending] = []
        var chapterEntries: [Entry] { entries.filter { !$0.isVolume } }
        var volumeEntries: [Entry] { entries.filter(\.isVolume) }
        var pendingChapters: [Pending] { pending.filter { !$0.volume } }
        var pendingVolumes: [Pending] { pending.filter(\.volume) }
        var size: Int64 { entries.reduce(0) { $0 + $1.size } }
        var coverURL: URL? {
            info.cover.map(LocalStore.root.appendingPathComponent).flatMap {
                FileManager.default.fileExists(atPath: $0.path) ? $0 : nil
            }
        }
    }

    var titles: [LocalTitle] {
        var groups = Dictionary(grouping: chapters.values, by: \.titleId)
        for id in titleInfo.keys where groups[id] == nil { groups[id] = [] }
        for p in pending.values where groups[p.titleId] == nil { groups[p.titleId] = [] }
        return groups
            .map { id, list in
                LocalTitle(id: id,
                           info: titleInfo[id] ?? TitleInfo(id: id, name: list.first?.titleName ?? "…"),
                           entries: list.sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending },
                           pending: pending.values.filter { $0.titleId == id }
                               .sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending })
            }
            .sorted { $0.info.name.localizedStandardCompare($1.info.name) == .orderedAscending }
    }

    func beginPending(_ items: [Pending]) {
        for p in items { pending[key(p.id, volume: p.volume)] = p }
    }

    func clearPending(titleId: Int64) {
        pending = pending.filter { $0.value.titleId != titleId }
        prune()
    }

    // saveTitle snapshots the title card data and writes the cover image next
    // to the chapters, so Downloads renders the web layout fully offline.
    func saveTitle(_ info: TitleInfo, coverData: Data?) {
        var info = info
        let dir = dirName(titleId: info.id, titleName: info.name)
        if let coverData {
            let path = "\(dir)/cover.jpg"
            try? FileManager.default.createDirectory(at: Self.root.appendingPathComponent(dir, isDirectory: true),
                                                     withIntermediateDirectories: true)
            if (try? coverData.write(to: Self.root.appendingPathComponent(path))) != nil {
                info.cover = path
            }
        }
        if info.cover == nil { info.cover = titleInfo[info.id]?.cover }
        titleInfo[info.id] = info
        persistIndex()
    }

    // markLocal records read progress on a downloaded chapter/volume so
    // Downloads stays accurate offline.
    func markLocal(chapterID: Int64, page: Int, total: Int, volume: Bool = false) {
        guard var e = chapters[key(chapterID, volume: volume)] else { return }
        let read = max(e.readPages ?? 0, page)
        let done = total > 0 && read >= total
        guard read != e.readPages ?? 0 || done != e.isRead else { return }
        e.readPages = read
        e.completed = done
        chapters[key(chapterID, volume: volume)] = e
        persistIndex()
    }

    // syncRead refreshes local read state from the server's chapter list.
    // Chapters with queued (unflushed) offline marks are skipped — the local
    // state is newer than what the server knows.
    func syncRead(_ list: [ChapterProgress], volumes: Bool = false) {
        var changed = false
        let pending = volumes ? Set(queue.compactMap(\.volumeId)) : Set(queue.map(\.chapterId))
        for ch in list {
            guard !pending.contains(ch.id) else { continue }
            let k = key(ch.id, volume: volumes)
            guard var e = chapters[k], e.readPages ?? -1 != ch.readPages || e.isRead != ch.completed else { continue }
            e.readPages = ch.readPages
            e.completed = ch.completed
            chapters[k] = e
            changed = true
        }
        if changed { persistIndex() }
    }

    // save moves a downloaded temp file into place; sanitized names that would
    // collide across titles or chapters get an id suffix.
    func save(file src: URL, chapterID: Int64, titleId: Int64, titleName: String, label: String,
              readPages: Int = 0, completed: Bool = false, volume: Bool = false) throws {
        let dirName = dirName(titleId: titleId, titleName: titleName)
        try FileManager.default.createDirectory(at: Self.root.appendingPathComponent(dirName, isDirectory: true),
                                                withIntermediateDirectories: true)
        let base = volume
            ? (Double(label) == nil ? Self.safe(label) : "Volume \(Self.safe(label))")
            : "Chapter \(Self.safe(label))"
        var path = "\(dirName)/\(base).cbz"
        if chapters.values.contains(where: { $0.path == path && ($0.id != chapterID || $0.isVolume != volume) }) {
            path = "\(dirName)/\(base) (\(chapterID)).cbz"
        }
        let dest = Self.root.appendingPathComponent(path)
        try? FileManager.default.removeItem(at: dest)
        try FileManager.default.moveItem(at: src, to: dest)
        guard let zip = ZipArchive(url: dest), !zip.imageEntries.isEmpty else {
            try? FileManager.default.removeItem(at: dest)
            throw CocoaError(.fileReadCorruptFile)
        }
        let images = zip.imageEntries
        let aspects = images.map { entry -> Double in
            guard let data = zip.data(for: entry),
                  let src = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
                  let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any],
                  let w = props[kCGImagePropertyPixelWidth] as? Double,
                  let h = props[kCGImagePropertyPixelHeight] as? Double, w > 0, h > 0 else { return 0 }
            return w / h
        }
        let size = (try? FileManager.default.attributesOfItem(atPath: dest.path))?[.size] as? Int64 ?? 0
        chapters[key(chapterID, volume: volume)] = Entry(
            id: chapterID, titleId: titleId, titleName: titleName,
            label: label, path: path, pages: images.count, size: size,
            readPages: readPages, completed: completed, pageAspects: aspects,
            volume: volume ? true : nil)
        pending.removeValue(forKey: key(chapterID, volume: volume))
        persistIndex()
    }

    private func dirName(titleId: Int64, titleName: String) -> String {
        if let e = chapters.values.first(where: { $0.titleId == titleId }),
           let dir = e.path.split(separator: "/").first {
            return String(dir)
        }
        let base = Self.safe(titleName)
        let taken = chapters.values.contains { $0.titleId != titleId && $0.path.hasPrefix(base + "/") }
        return taken ? "\(base) (\(titleId))" : base
    }

    func delete(_ id: Int64, volume: Bool = false) {
        guard let e = chapters.removeValue(forKey: key(id, volume: volume)) else { return }
        let file = Self.root.appendingPathComponent(e.path)
        try? FileManager.default.removeItem(at: file)
        if !chapters.values.contains(where: { $0.titleId == e.titleId }) {
            titleInfo.removeValue(forKey: e.titleId)
            try? FileManager.default.removeItem(at: file.deletingLastPathComponent())
        }
        persistIndex()
    }

    func deleteTitle(_ titleId: Int64) {
        for e in chapters.values where e.titleId == titleId { delete(e.id, volume: e.isVolume) }
    }

    func queueMark(id: Int64, volume: Bool, page: Int, totalPages: Int) {
        let mark = QueuedMark(chapterId: volume ? 0 : id, volumeId: volume ? id : nil,
                              page: page, totalPages: totalPages, readAt: Date())
        queue.removeAll { $0.chapterId == mark.chapterId && $0.volumeId == mark.volumeId && $0.page == page }
        queue.append(mark)
        persist(queue, to: Self.queueURL)
    }

    // flush replays offline marks through the batch endpoint; keeps them on
    // failure. Only the sent snapshot is removed — marks queued during the
    // request survive (actor reentrancy), and concurrent flushes are skipped.
    func flush(_ api: APIClient) async {
        guard !queue.isEmpty, !flushing else { return }
        flushing = true
        defer { flushing = false }
        struct Batch: Encodable { var entries: [QueuedMark] }
        let sent = queue
        if (try? await api.data("POST", "/api/v1/reader/progress/batch", body: Batch(entries: sent))) != nil {
            queue.removeAll { sent.contains($0) }
            persist(queue, to: Self.queueURL)
        }
    }

    // pageImage extracts one page (1-based) from a local CBZ off the main actor.
    nonisolated static func pageImage(at url: URL, page: Int) async -> UIImage? {
        await Task.detached(priority: .userInitiated) {
            guard let zip = ZipArchive(url: url) else { return nil }
            let images = zip.imageEntries
            guard images.indices.contains(page - 1), let data = zip.data(for: images[page - 1]) else { return nil }
            return UIImage.downsampled(data)
        }.value
    }

    private func persistIndex() {
        persist(Index(titles: Array(titleInfo.values), chapters: Array(chapters.values)), to: Self.indexURL)
    }

    private func persist(_ value: some Encodable, to url: URL) {
        try? JSONEncoder().encode(value).write(to: url)
    }

    private nonisolated static func safe(_ name: String) -> String {
        let cleaned = name.components(separatedBy: CharacterSet(charactersIn: "/\\:?*\"<>|")).joined(separator: " ")
            .trimmingCharacters(in: .whitespaces)
        return cleaned.isEmpty ? "Untitled" : String(cleaned.prefix(100))
    }
}

extension UIImage {
    // downsampled decodes at a bounded pixel size so a reading session's pages
    // don't hold full-scan bitmaps in memory (2600px keeps 3x zoom sharp).
    // Long-strip segments are capped by their SHORT side instead — a
    // longest-side cap would crush an 800×12000 strip to ~170px wide (blurry).
    nonisolated static func downsampled(_ data: Data, maxDimension: CGFloat = 2600) -> UIImage? {
        guard let src = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary) else {
            return UIImage(data: data)
        }
        var maxPixel = maxDimension
        if let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any],
           let w = props[kCGImagePropertyPixelWidth] as? CGFloat,
           let h = props[kCGImagePropertyPixelHeight] as? CGFloat, w > 0, h > 0 {
            let short = min(w, h), long = max(w, h)
            if long > short * 2.2 {
                // 16000 stays under the GPU texture limit.
                maxPixel = long * min(1, 1600 / short, 16000 / long)
            } else {
                maxPixel = min(long, maxDimension)
            }
        }
        guard let cg = CGImageSourceCreateThumbnailAtIndex(src, 0, [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixel,
        ] as CFDictionary) else { return UIImage(data: data) }
        return UIImage(cgImage: cg)
    }
}
