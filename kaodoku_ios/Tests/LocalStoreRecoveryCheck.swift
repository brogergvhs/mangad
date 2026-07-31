import Foundation
import Testing
@testable import Kaodoku

@MainActor
@Test("Unreadable metadata can be reset without deleting CBZ files")
func localStoreRecovery() async throws {
    let files = FileManager.default
    let root = LocalStore.root
    let index = root.appendingPathComponent(".index.json")
    let queue = root.appendingPathComponent(".marks.json")
    let originalIndex = try? Data(contentsOf: index)
    let originalQueue = try? Data(contentsOf: queue)
    let originalNames = Set((try? files.contentsOfDirectory(atPath: root.path)) ?? [])
    let sentinel = root.appendingPathComponent("RecoveryCheck-\(UUID().uuidString)", isDirectory: true)
    let archive = sentinel.appendingPathComponent("chapter.cbz")

    defer {
        try? files.removeItem(at: sentinel)
        try? files.removeItem(at: index)
        try? files.removeItem(at: queue)
        if let originalIndex { try? originalIndex.write(to: index, options: .atomic) }
        if let originalQueue { try? originalQueue.write(to: queue, options: .atomic) }
        if let names = try? files.contentsOfDirectory(atPath: root.path) {
            for name in names where !originalNames.contains(name)
                && (name.hasPrefix(".index.json.corrupt-") || name.hasPrefix(".marks.json.corrupt-")) {
                try? files.removeItem(at: root.appendingPathComponent(name))
            }
        }
    }

    try files.createDirectory(at: sentinel, withIntermediateDirectories: true)
    try Data([0]).write(to: archive)
    try Data("{".utf8).write(to: index, options: .atomic)
    try Data("{".utf8).write(to: queue, options: .atomic)

    let store = LocalStore()
    await store.load()
    #expect(store.requiresRecovery)
    #expect(store.persistenceError != nil)

    await store.resetCorruptMetadata()

    #expect(!store.requiresRecovery)
    #expect(store.persistenceError == nil)
    #expect(files.fileExists(atPath: archive.path))
    let names = try files.contentsOfDirectory(atPath: root.path)
    #expect(names.contains { $0.hasPrefix(".index.json.corrupt-") })
    #expect(names.contains { $0.hasPrefix(".marks.json.corrupt-") })
}

@MainActor
@Test("Pending downloads are grouped once by title and sorted")
func pendingDownloadGrouping() throws {
    let store = LocalStore()
    store.beginPending([
        .init(id: 1, titleId: 10, label: "10", pages: 1, volume: false),
        .init(id: 2, titleId: 10, label: "2", pages: 1, volume: false),
        .init(id: 3, titleId: 20, label: "1", pages: 1, volume: true),
    ])

    let titles = store.titles
    #expect(titles.count == 2)
    let chapters = try #require(titles.first { $0.id == 10 })
    #expect(chapters.pending.map(\.label) == ["2", "10"])
    #expect(titles.first { $0.id == 20 }?.pendingVolumes.count == 1)
}
