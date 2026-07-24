import Foundation

// Wire DTOs for /api/v1 (snake_case mapped via .convertFromSnakeCase).
// Timestamps stay Strings: the server emits RFC3339 with optional fractional
// seconds, and the app only displays them.

struct Meta: Decodable {
    var serverVersion: String
    var apiVersion: Int
    var authRequired: Bool
    var features: [String]
}

struct Me: Decodable {
    struct User: Decodable {
        var id: Int64
        var username: String
        var role: String
        var allowAdult: Bool
    }
    var user: User
    var permissions: [String]

    func can(_ perm: String) -> Bool { permissions.contains(perm) }
}

struct LoginResponse: Decodable {
    var token: String
    var me: Me
}

struct Title: Decodable, Identifiable, Hashable {
    var id: Int64
    var displayTitle: String
    var coverImage: String
    var monitored: Bool
    var isAdult: Bool
    var averageScore: Int64
    var contentTags: [String]
    var favourite: Bool
    var releaseStatus: String
    var discoveredCount: Int64
    var missingCount: Int64
    var readCount: Int64
    var completedCount: Int64
    var volumeCount: Int64
    var updatedAt: String
}

struct TitlePage: Decodable {
    var items: [Title]
    var nextCursor: String
    var total: Int
}

struct ChapterProgress: Decodable, Identifiable, Hashable {
    var id: Int64
    var titleId: Int64
    var label: String
    var title: String
    var downloaded: Bool
    var pages: Int
    var totalPages: Int
    var readPages: Int
    var completed: Bool
    var manual: Bool
    var firstUnreadPage: Int
    var lastReadAt: String?
}

struct TitleReadProgress: Decodable {
    var title: Title
    var chapters: [ChapterProgress]
    var readChapters: Int
    var totalChapters: Int
    var nextChapterId: Int64
    var nextPage: Int
}

struct Manifest: Decodable {
    struct Chapter: Decodable, Identifiable {
        struct Page: Decodable {
            var page: Int
            var url: String
            var read: Bool
        }
        var id: Int64
        var label: String
        var pageCount: Int
        var readPages: Int
        var completed: Bool
        var pages: [Page]
    }
    var titleId: Int64
    var title: String
    var resumeChapterId: Int64
    var resumePage: Int
    var markBase: String
    var extendBase: String
    var chapters: [Chapter]
}

struct APIErrorBody: Decodable {
    var error: String
    var code: String?
}

struct UserSettings: Codable {
    var readerMode: String?
    var readerDir: String?
}

struct Manga: Decodable, Identifiable, Hashable {
    var catalogId: Int64
    var providerId: String
    var titleRomaji: String
    var titleEnglish: String
    var description: String?
    var coverImage: String
    var status: String?
    var format: String?
    var chapters: Int?
    var averageScore: Int?
    var isAdult: Bool
    var titleId: Int64?

    // Browse results aren't DB-cached (catalog id 0), so identity is the
    // stable AniList provider id.
    var id: String { providerId }
    var name: String { titleEnglish.isEmpty ? titleRomaji : titleEnglish }

    enum CodingKeys: String, CodingKey {
        case catalogId = "id"
        case providerId, titleRomaji, titleEnglish, description, coverImage
        case status, format, chapters, averageScore, isAdult, titleId
    }

    // caption mirrors the web card: FORMAT · STATUS · N ch · ★ N%
    var caption: String {
        var parts: [String] = []
        if let format, !format.isEmpty { parts.append(format) }
        if let status, !status.isEmpty { parts.append(status) }
        if let chapters, chapters > 0 { parts.append("\(chapters) ch") }
        if let averageScore, averageScore > 0 { parts.append("★ \(averageScore)%") }
        return parts.joined(separator: " · ")
    }
}

struct SearchPage: Decodable {
    var items: [Manga]
    var hasMore: Bool?
    var page: Int?
}

struct TagOption: Decodable, Identifiable, Hashable {
    var name: String
    var kind: String
    var id: String { name }
}

struct Match: Decodable, Identifiable, Hashable {
    var id: Int64
    var sourceId: String
    var title: String
    var confidence: Double
    var chaptersFound: Int
    var error: String?
    var verifiedAt: String?
}

struct MatchList: Decodable {
    var items: [Match]
}
