import Compression
import Foundation

// ZipArchive is a minimal CBZ reader: central directory parsing plus stored
// and deflated entries — everything Go's archive/zip produces. No zip64.
struct ZipArchive {
    struct Entry {
        let name: String
        let method: Int
        let compressedSize: Int
        let uncompressedSize: Int
        let headerOffset: Int
    }

    let url: URL
    let entries: [Entry]

    private static let imageExts: Set<String> = ["jpg", "jpeg", "png", "webp", "gif", "avif"]

    // imageEntries are the page images in natural reading order.
    var imageEntries: [Entry] {
        entries
            .filter { Self.imageExts.contains(($0.name as NSString).pathExtension.lowercased()) }
            .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    init?(url: URL) {
        guard let fh = try? FileHandle(forReadingFrom: url),
              let size = try? fh.seekToEnd(), size > 22 else { return nil }
        defer { try? fh.close() }

        let tailLen = min(Int(size), 66_000)
        try? fh.seek(toOffset: size - UInt64(tailLen))
        guard let tail = try? fh.read(upToCount: tailLen) else { return nil }
        var eocd = -1
        var i = tail.count - 22
        while i >= 0 {
            if tail[tail.startIndex + i] == 0x50, tail[tail.startIndex + i + 1] == 0x4b,
               tail[tail.startIndex + i + 2] == 0x05, tail[tail.startIndex + i + 3] == 0x06 {
                eocd = i
                break
            }
            i -= 1
        }
        guard eocd >= 0 else { return nil }

        let count = Self.u16(tail, eocd + 10)
        let cdSize = Self.u32(tail, eocd + 12)
        let cdOffset = Self.u32(tail, eocd + 16)
        try? fh.seek(toOffset: UInt64(cdOffset))
        guard let cd = try? fh.read(upToCount: cdSize) else { return nil }

        var out: [Entry] = []
        var p = 0
        for _ in 0..<count {
            guard p + 46 <= cd.count, Self.u32(cd, p) == 0x0201_4b50 else { break }
            let nlen = Self.u16(cd, p + 28)
            let xlen = Self.u16(cd, p + 30)
            let clen = Self.u16(cd, p + 32)
            let name = String(data: cd.subdata(in: cd.startIndex + p + 46 ..< cd.startIndex + p + 46 + nlen), encoding: .utf8) ?? ""
            out.append(Entry(
                name: name,
                method: Self.u16(cd, p + 10),
                compressedSize: Self.u32(cd, p + 20),
                uncompressedSize: Self.u32(cd, p + 24),
                headerOffset: Self.u32(cd, p + 42)
            ))
            p += 46 + nlen + xlen + clen
        }
        guard !out.isEmpty else { return nil }
        self.url = url
        self.entries = out
    }

    // data extracts one entry (method 0 = stored, 8 = deflate).
    func data(for entry: Entry) -> Data? {
        guard let fh = try? FileHandle(forReadingFrom: url) else { return nil }
        defer { try? fh.close() }
        try? fh.seek(toOffset: UInt64(entry.headerOffset))
        guard let head = try? fh.read(upToCount: 30), head.count == 30,
              Self.u32(head, 0) == 0x0403_4b50 else { return nil }
        let nlen = Self.u16(head, 26)
        let xlen = Self.u16(head, 28)
        try? fh.seek(toOffset: UInt64(entry.headerOffset + 30 + nlen + xlen))
        guard let raw = try? fh.read(upToCount: entry.compressedSize) else { return nil }
        switch entry.method {
        case 0: return raw
        case 8: return Self.inflate(raw, to: entry.uncompressedSize)
        default: return nil
        }
    }

    private static func u16(_ d: Data, _ o: Int) -> Int {
        Int(d[d.startIndex + o]) | Int(d[d.startIndex + o + 1]) << 8
    }

    private static func u32(_ d: Data, _ o: Int) -> Int {
        u16(d, o) | u16(d, o + 2) << 16
    }

    private static func inflate(_ data: Data, to size: Int) -> Data? {
        guard size > 0 else { return Data() }
        var dst = Data(count: size)
        let written = dst.withUnsafeMutableBytes { db in
            data.withUnsafeBytes { sb in
                compression_decode_buffer(
                    db.bindMemory(to: UInt8.self).baseAddress!, size,
                    sb.bindMemory(to: UInt8.self).baseAddress!, data.count,
                    nil, COMPRESSION_ZLIB
                )
            }
        }
        return written == size ? dst : nil
    }
}
