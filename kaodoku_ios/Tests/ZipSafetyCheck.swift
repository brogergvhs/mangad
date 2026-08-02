import Compression
import Foundation
@testable import Kaodoku
import Testing
import UIKit

private extension Data {
    mutating func le16(_ value: Int) {
        append(contentsOf: [UInt8(value & 0xFF), UInt8(value >> 8 & 0xFF)])
    }

    mutating func le32(_ value: Int) {
        le16(value)
        le16(value >> 16)
    }
}

private func deflate(_ data: Data) -> Data {
    var output = Data(count: data.count + 64)
    let capacity = output.count
    let written = output.withUnsafeMutableBytes { destination in
        data.withUnsafeBytes { source in
            compression_encode_buffer(
                destination.bindMemory(to: UInt8.self).baseAddress!, capacity,
                source.bindMemory(to: UInt8.self).baseAddress!, data.count,
                nil, COMPRESSION_ZLIB
            )
        }
    }
    return Data(output.prefix(written))
}

private func archive(flags: Int = 0, method: Int = 0, payload: Data = Data([0x78]), size: Int? = nil,
                     nameLength: Int = 5, headerOffset: Int = 0) -> Data
{
    let name = Data("1.jpg".utf8)
    let compressed = method == 8 ? deflate(payload) : payload
    let declaredSize = size ?? payload.count
    var zip = Data()
    zip.le32(0x0403_4B50)
    zip.le16(20)
    zip.le16(flags)
    zip.le16(method)
    zip.append(Data(repeating: 0, count: 8))
    zip.le32(compressed.count)
    zip.le32(declaredSize)
    zip.le16(name.count)
    zip.le16(0)
    zip.append(name)
    zip.append(compressed)

    let directoryOffset = zip.count
    zip.le32(0x0201_4B50)
    zip.le16(20)
    zip.le16(20)
    zip.le16(flags)
    zip.le16(method)
    zip.append(Data(repeating: 0, count: 8))
    zip.le32(compressed.count)
    zip.le32(declaredSize)
    zip.le16(nameLength)
    zip.le16(0)
    zip.le16(0)
    zip.le16(0)
    zip.append(Data(repeating: 0, count: 6))
    zip.le32(headerOffset)
    zip.append(name)

    let directorySize = zip.count - directoryOffset
    zip.le32(0x0605_4B50)
    zip.append(Data(repeating: 0, count: 4))
    zip.le16(1)
    zip.le16(1)
    zip.le32(directorySize)
    zip.le32(directoryOffset)
    zip.le16(0)
    return zip
}

private func parse(_ data: Data, in directory: URL) throws -> ZipArchive {
    let url = directory.appendingPathComponent(UUID().uuidString)
    try data.write(to: url)
    return try ZipArchive(url: url)
}

@Test("ZIP parser accepts supported data and rejects unsafe metadata")
func zipSafety() throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    defer { try? FileManager.default.removeItem(at: directory) }

    let valid = try parse(archive(), in: directory)
    let stored = try #require(valid.imageEntries.first)
    #expect(valid.data(for: stored) == Data([0x78]))

    let payload = Data(repeating: 0x78, count: 1024)
    let deflated = try parse(archive(method: 8, payload: payload), in: directory)
    let compressed = try #require(deflated.imageEntries.first)
    #expect(deflated.data(for: compressed) == payload)

    for (data, expected) in [
        (archive(flags: 1), ZipArchive.ArchiveError.encrypted),
        (archive(method: 99), .unsupported),
        (archive(size: 129 << 20), .tooLarge),
        (Data(archive().dropLast()), .invalid),
        (archive(nameLength: 65535), .invalid),
        (archive(headerOffset: 0xFFFF_FFFE), .invalid),
        (archive(headerOffset: 0xFFFF_FFFF), .zip64),
    ] {
        do {
            _ = try parse(data, in: directory)
            Issue.record("Unsafe archive was accepted")
        } catch let error as ZipArchive.ArchiveError {
            #expect(error == expected)
        }
    }
}

@MainActor
@Test("Cancelled local page loads discard decoded images")
func cancelledPageLoad() async throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    defer { try? FileManager.default.removeItem(at: directory) }

    let png = UIGraphicsImageRenderer(size: CGSize(width: 1, height: 1)).pngData { _ in
        UIColor.black.setFill()
        UIRectFill(CGRect(x: 0, y: 0, width: 1, height: 1))
    }
    let url = directory.appendingPathComponent("page.cbz")
    try archive(payload: png).write(to: url)
    #expect(await LocalStore.pageImage(at: url, page: 1, maxPixelSize: CGSize(width: 1, height: 1)) != nil)

    let load = Task {
        withUnsafeCurrentTask { $0?.cancel() }
        return await LocalStore.pageImage(at: url, page: 1, maxPixelSize: CGSize(width: 1, height: 1))
    }
    #expect(await load.value == nil)
    await LocalStore.clearPageCache()
}
