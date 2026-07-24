import SwiftUI

// ServerImage loads an image through the authed client (AsyncImage can't send
// the X-API-Key header); URLCache handles the caching.
struct ServerImage: View {
    @Environment(AppState.self) private var app
    let path: String
    @State private var image: UIImage?

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                Rectangle().fill(.quaternary)
            }
        }
        .task(id: path) {
            guard let api = app.api, image == nil else { return }
            image = (try? await api.data("GET", path)).flatMap(UIImage.init)
        }
    }
}

// Cover renders a uniform 5:7 cover cell (the web card's aspect) regardless
// of the image's own size: the clear frame owns the layout, the image only
// fills and clips. Adult titles get the web's red outline.
struct Cover: View {
    let path: String
    var adult = false
    var local: URL? = nil

    var body: some View {
        Color.clear
            .aspectRatio(5 / 7, contentMode: .fit)
            .overlay {
                if let local {
                    LocalImage(url: local)
                } else {
                    ServerImage(path: path)
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.red, lineWidth: adult ? 2 : 0))
    }
}

// LocalImage loads a file-backed image (offline covers).
struct LocalImage: View {
    let url: URL
    @State private var image: UIImage?

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                Rectangle().fill(.quaternary)
            }
        }
        .task(id: url) { image = UIImage(contentsOfFile: url.path) }
    }
}

func humanBytes(_ n: Int64) -> String {
    n > 0 ? ByteCountFormatter.string(fromByteCount: n, countStyle: .file) : "0 B"
}

// BarView is the web "bar" partial: green read share, primary completed share.
struct BarView: View {
    let read: Double
    let full: Double

    var body: some View {
        GeometryReader { geo in
            HStack(spacing: 0) {
                Rectangle().fill(.green).frame(width: geo.size.width * min(read, 1))
                Rectangle().fill(.blue).frame(width: geo.size.width * max(min(full, 1) - min(read, 1), 0))
                Color.clear
            }
        }
        .frame(height: 6)
        .background(Color(.systemFill))
        .clipShape(Capsule())
    }
}

// Badge mirrors the web badge variants (soft / ghost / outline / warning).
struct Badge: View {
    enum Style { case soft, ghost, outline, warning }
    let text: String
    var style: Style = .ghost

    var body: some View {
        Text(text)
            .font(.caption2)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background {
                switch style {
                case .soft: Capsule().fill(.blue.opacity(0.15))
                case .ghost: Capsule().fill(Color(.systemFill))
                case .warning: Capsule().fill(.yellow.opacity(0.2))
                case .outline: Capsule().strokeBorder(Color(.separator))
                }
            }
            .foregroundStyle(style == .soft ? .blue : style == .warning ? .orange : .primary)
    }
}

// WrapLayout flows badge chips onto multiple lines like the web's flex-wrap.
struct WrapLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        rows(in: proposal.width ?? .infinity, subviews: subviews).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        for (i, origin) in rows(in: bounds.width, subviews: subviews).origins.enumerated() {
            subviews[i].place(at: CGPoint(x: bounds.minX + origin.x, y: bounds.minY + origin.y), proposal: .unspecified)
        }
    }

    private func rows(in width: CGFloat, subviews: Subviews) -> (origins: [CGPoint], size: CGSize) {
        var origins: [CGPoint] = []
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0, maxX: CGFloat = 0
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x > 0, x + size.width > width {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            origins.append(CGPoint(x: x, y: y))
            x += size.width + spacing
            maxX = max(maxX, x - spacing)
            rowHeight = max(rowHeight, size.height)
        }
        return (origins, CGSize(width: width.isFinite ? width : maxX, height: y + rowHeight))
    }
}

// TitleStats is the web card footer: counts + size line, then the bar.
struct TitleStats: View {
    let title: Title

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                if volumesOnly {
                    Text("\(title.volumeReadCount)/\(title.volumeCount) vols").lineLimit(1)
                    Spacer(minLength: 8)
                    Text(humanBytes(title.volumeBytes))
                } else {
                    Text("\(title.completedCount)/\(title.discoveredCount)").lineLimit(1)
                    Spacer(minLength: 8)
                    Text((title.missingCount > 0 ? "\(title.missingCount) missing · " : "") + humanBytes(title.sizeBytes))
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            if volumesOnly {
                BarView(read: pct(title.volumeReadCount, title.volumeCount), full: 1)
            } else {
                BarView(read: pct(title.readCount, title.discoveredCount),
                        full: pct(title.completedCount, title.discoveredCount))
            }
        }
    }

    private var volumesOnly: Bool { title.discoveredCount == 0 && title.volumeCount > 0 }
}

func pct(_ a: Int64, _ b: Int64) -> Double { b > 0 ? Double(a) / Double(b) : 0 }

struct LibraryView: View {
    @Environment(AppState.self) private var app
    @State private var titles: [Title] = []
    @State private var query = ""
    @State private var loading = true

    private var shown: [Title] {
        query.isEmpty ? titles : titles.filter { $0.displayTitle.localizedCaseInsensitiveContains(query) }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                if loading {
                    ProgressView().padding(.top, 80)
                } else if shown.isEmpty {
                    Text(titles.isEmpty ? "No titles in the library yet." : "No matches.")
                        .foregroundStyle(.secondary)
                        .padding(.top, 80)
                }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 110), spacing: 12)], spacing: 16) {
                    ForEach(shown) { title in
                        NavigationLink(value: title) {
                            TitleCard(title: title)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal)
            }
            .navigationTitle("Library")
            .navigationDestination(for: Title.self) { TitleDetailView(titleID: $0.id) }
            .searchable(text: $query)
            .refreshable { await load() }
            .task { await load() }
            .toolbar {
                Menu {
                    if let me = app.me {
                        Text(me.user.username)
                    }
                    Button("Sign out", role: .destructive) { app.signOut() }
                } label: {
                    Image(systemName: "person.circle")
                }
            }
        }
    }

    private func load() async {
        guard let api = app.api else { return }
        do {
            let page: TitlePage = try await api.get("/api/v1/library?limit=200&sort=title")
            titles = page.items
        } catch APIError.unauthorized {
            app.signOut()
        } catch {
            app.errorMessage = error.localizedDescription
        }
        loading = false
    }
}

// TitleCard is the web libraryCard: cover, 2-line title, counts + size, bar.
struct TitleCard: View {
    let title: Title

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Cover(path: title.coverImage, adult: title.isAdult)
            Text(title.displayTitle)
                .font(.caption)
                .lineLimit(2, reservesSpace: true)
                .foregroundStyle(.primary)
            TitleStats(title: title)
        }
    }
}
