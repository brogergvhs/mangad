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

struct TitleCard: View {
    let title: Title

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ServerImage(path: title.coverImage)
                .aspectRatio(2 / 3, contentMode: .fit)
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .overlay(alignment: .topTrailing) {
                    let unread = title.completedCount - title.readCount
                    if unread > 0 {
                        Text("\(unread)")
                            .font(.caption2.bold())
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(.blue, in: Capsule())
                            .foregroundStyle(.white)
                            .padding(4)
                    }
                }
            Text(title.displayTitle)
                .font(.caption)
                .lineLimit(2)
                .foregroundStyle(.primary)
        }
    }
}
