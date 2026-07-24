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
    }

    struct QueuedMark: Codable, Equatable {
        var chapterId: Int64
        var volumeId: Int64?
        var page: Int
        var totalPages: Int
        var readAt: Date
    }

    private(set) var chapters: [Int64: Entry] = [:]
    private var queue: [QueuedMark] = []
    private var flushing = false

    nonisolated static let root = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
    private static let indexURL = root.appendingPathComponent(".index.json")
    private static let queueURL = root.appendingPathComponent(".marks.json")

    init() {
        if let data = try? Data(contentsOf: Self.indexURL),
           let list = try? JSONDecoder().decode([Entry].self, from: data) {
            chapters = Dictionary(uniqueKeysWithValues: list.map { ($0.id, $0) })
        }
        if let data = try? Data(contentsOf: Self.queueURL) {
            queue = (try? JSONDecoder().decode([QueuedMark].self, from: data)) ?? []
        }
        prune()
    }

    // prune drops index entries whose file was deleted through the Files app.
    func prune() {
        let dead = chapters.values.filter {
            !FileManager.default.fileExists(atPath: Self.root.appendingPathComponent($0.path).path)
        }
        guard !dead.isEmpty else { return }
        for e in dead { chapters.removeValue(forKey: e.id) }
        persistIndex()
    }

    func isDownloaded(_ chapterID: Int64) -> Bool { url(for: chapterID) != nil }

    // url returns the local CBZ if the entry exists and the file survived
    // (users can delete files through the Files app).
    func url(for chapterID: Int64) -> URL? {
        guard let e = chapters[chapterID] else { return nil }
        let u = Self.root.appendingPathComponent(e.path)
        return FileManager.default.fileExists(atPath: u.path) ? u : nil
    }

    func entries(titleId: Int64) -> [Entry] {
        chapters.values.filter { $0.titleId == titleId }
            .sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending }
    }

    var titles: [(id: Int64, name: String, entries: [Entry])] {
        Dictionary(grouping: chapters.values, by: \.titleId)
            .map { id, list in (id, list[0].titleName, list.sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending }) }
            .sorted { $0.1.localizedStandardCompare($1.1) == .orderedAscending }
    }

    // save moves a downloaded temp file into place; sanitized names that would
    // collide across titles or chapters get an id suffix.
    func save(file src: URL, chapterID: Int64, titleId: Int64, titleName: String, label: String) throws {
        let dirName = dirName(titleId: titleId, titleName: titleName)
        try FileManager.default.createDirectory(at: Self.root.appendingPathComponent(dirName, isDirectory: true),
                                                withIntermediateDirectories: true)
        var path = "\(dirName)/Chapter \(Self.safe(label)).cbz"
        if chapters.values.contains(where: { $0.path == path && $0.id != chapterID }) {
            path = "\(dirName)/Chapter \(Self.safe(label)) (\(chapterID)).cbz"
        }
        let dest = Self.root.appendingPathComponent(path)
        try? FileManager.default.removeItem(at: dest)
        try FileManager.default.moveItem(at: src, to: dest)
        let pages = ZipArchive(url: dest)?.imageEntries.count ?? 0
        chapters[chapterID] = Entry(id: chapterID, titleId: titleId, titleName: titleName,
                                    label: label, path: path, pages: pages)
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

    func delete(_ chapterID: Int64) {
        guard let e = chapters.removeValue(forKey: chapterID) else { return }
        let file = Self.root.appendingPathComponent(e.path)
        try? FileManager.default.removeItem(at: file)
        let dir = file.deletingLastPathComponent()
        if (try? FileManager.default.contentsOfDirectory(atPath: dir.path))?.isEmpty == true {
            try? FileManager.default.removeItem(at: dir)
        }
        persistIndex()
    }

    func deleteTitle(_ titleId: Int64) {
        for e in chapters.values where e.titleId == titleId { delete(e.id) }
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

    private func persistIndex() { persist(Array(chapters.values), to: Self.indexURL) }

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
    nonisolated static func downsampled(_ data: Data, maxDimension: CGFloat = 2600) -> UIImage? {
        guard let src = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
              let cg = CGImageSourceCreateThumbnailAtIndex(src, 0, [
                  kCGImageSourceCreateThumbnailFromImageAlways: true,
                  kCGImageSourceShouldCacheImmediately: true,
                  kCGImageSourceCreateThumbnailWithTransform: true,
                  kCGImageSourceThumbnailMaxPixelSize: maxDimension,
              ] as CFDictionary) else { return UIImage(data: data) }
        return UIImage(cgImage: cg)
    }
}
