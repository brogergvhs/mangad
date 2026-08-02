import Foundation
@testable import Kaodoku
import Testing

@MainActor
@Test("Unreadable metadata can be reset without deleting CBZ files")
func localStoreRecovery() async throws {
    let files = FileManager.default
    let instance = "test-\(UUID().uuidString)"
    let dir = LocalStore.root.appendingPathComponent(instance, isDirectory: true)
    let index = LocalStore.indexURL(instance)
    let queue = LocalStore.queueURL(instance)
    let archive = dir.appendingPathComponent("Title/chapter.cbz")

    defer { try? files.removeItem(at: dir) }

    try files.createDirectory(at: archive.deletingLastPathComponent(), withIntermediateDirectories: true)
    try Data([0]).write(to: archive)
    try Data("{".utf8).write(to: index, options: .atomic)
    try Data("{".utf8).write(to: queue, options: .atomic)

    let store = LocalStore()
    await store.load(instance: instance)
    #expect(store.requiresRecovery)
    #expect(store.persistenceError != nil)

    await store.resetCorruptMetadata()

    #expect(!store.requiresRecovery)
    #expect(store.persistenceError == nil)
    #expect(files.fileExists(atPath: archive.path))
    let names = try files.contentsOfDirectory(atPath: dir.path)
    #expect(names.contains { $0.hasPrefix(".index.json.corrupt-") })
    #expect(names.contains { $0.hasPrefix(".marks.json.corrupt-") })
}

@MainActor
@Test("Downloads are namespaced per server and stay separated")
func perInstanceSeparation() async throws {
    let files = FileManager.default
    let instance = "sep-\(UUID().uuidString)"
    let dir = LocalStore.root.appendingPathComponent(instance, isDirectory: true)
    let cbz = dir.appendingPathComponent("MyTitle/Chapter 1.cbz")

    defer { try? files.removeItem(at: dir) }

    try files.createDirectory(at: cbz.deletingLastPathComponent(), withIntermediateDirectories: true)
    try Data([0]).write(to: cbz)
    let json = """
    {"titles":[],"chapters":[{"id":5,"titleId":1,"titleName":"MyTitle","label":"1",\
    "path":"\(instance)/MyTitle/Chapter 1.cbz","pages":1,"size":1,"readPages":0,\
    "completed":false,"pageAspects":[0.7],"volume":false}]}
    """
    try Data(json.utf8).write(to: LocalStore.indexURL(instance), options: .atomic)

    let store = LocalStore()
    await store.load(instance: instance)
    let url = try #require(store.url(for: 5))
    #expect(url.path.contains("/\(instance)/MyTitle/"))

    await store.activate("other-\(UUID().uuidString)")
    #expect(store.url(for: 5) == nil)
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
