import SwiftUI

// TitleSourcesSheet mirrors the web title page's Sources section: linked
// sources, find + match candidates, and manual page-URL linking — the missing
// middle step between adding a title and downloading chapters.
struct TitleSourcesSheet: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    let titleID: Int64
    var onChanged: () -> Void = {}
    @State private var data: TitleSources?
    @State private var busy = false
    @State private var linkSource: SourcePick?
    @State private var linkURL = ""
    @State private var error: String?
    @State private var pollGen = 0

    var body: some View {
        NavigationStack {
            List {
                Group {
                    Section("Linked sources") {
                        if let data {
                            if data.linked.isEmpty {
                                Text("No source linked yet — chapters can't be found or downloaded until one is.")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            ForEach(data.linked) { link in
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack {
                                        Text(link.name)
                                        if link.active {
                                            Badge(text: "active", style: .soft)
                                        }
                                    }
                                    Text(link.url).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                                }
                                .swipeActions {
                                    Button("Unlink", systemImage: "link.badge.minus", role: .destructive) {
                                        unlink(link)
                                    }
                                }
                            }
                        } else {
                            ProgressView()
                        }
                    }
                    if let data {
                        Section("Found sources") {
                            Button(data.finding ? "Searching sources…" : "Find sources") { find() }
                                .disabled(data.finding || busy)
                            if data.failed, let msg = data.error, !msg.isEmpty {
                                Text(msg).font(.caption).foregroundStyle(Theme.error)
                            }
                            if data.matches.isEmpty && !data.finding {
                                Text("No candidates yet — run a search.")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            ForEach(data.matches) { match in
                                Button {
                                    link(match)
                                } label: {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(match.title.isEmpty ? match.sourceId : match.title)
                                        Text("\(match.sourceId) · \(match.chaptersFound) chapters · \(Int(match.confidence * 100))%")
                                            .font(.caption).foregroundStyle(.secondary)
                                    }
                                }
                                .disabled(busy || match.chaptersFound == 0)
                            }
                        }
                        Section("Link a page URL") {
                            Picker("Source", selection: $linkSource) {
                                Text("Choose…").tag(SourcePick?.none)
                                ForEach(data.sources) { Text($0.name).tag(Optional($0)) }
                            }
                            TextField("https://…", text: $linkURL)
                                .keyboardType(.URL)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                            Button("Link this page") { linkByURL() }
                                .disabled(linkSource == nil || linkURL.isEmpty || busy)
                        }
                    }
                    if let error {
                        Text(error).foregroundStyle(Theme.error)
                    }
                }
                .nordRows()
            }
            .nordScreen()
            .navigationTitle("Sources")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { Button("Close") { dismiss() } }
            .task(id: pollGen) { await refreshLoop() }
        }
    }

    private func load() async {
        guard let api = app.api else { return }
        data = try? await api.get("/api/v1/library/\(titleID)/sources")
    }

    // refreshLoop polls while a find job is running, like the web's HTMX poll.
    private func refreshLoop() async {
        await load()
        while !Task.isCancelled, data?.finding == true {
            try? await Task.sleep(for: .seconds(2))
            await load()
        }
    }

    // find kicks the match job, then bumps pollGen so the VIEW-LIFETIME task
    // restarts the poll loop — a detached poller would outlive the sheet, and
    // holding `busy` across the whole search would lock the manual-link form.
    private func find() {
        guard let api = app.api else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                _ = try await api.data("POST", "/api/v1/library/\(titleID)/sources/find")
                pollGen += 1
            } catch { self.error = error.localizedDescription }
        }
    }

    private func link(_ match: Match) {
        post("/api/v1/library/\(titleID)/sources/link", body: ["match_id": match.id]) {
            onChanged()
            dismiss()
        }
    }

    private func linkByURL() {
        guard let source = linkSource else { return }
        post("/api/v1/library/\(titleID)/sources/link-url",
             body: ["source_id": source.id, "url": linkURL.trimmingCharacters(in: .whitespaces)]) {
            onChanged()
            dismiss()
        }
    }

    private func unlink(_ link: LinkedSource) {
        post("/api/v1/library/\(titleID)/sources/unlink", body: ["url": link.url]) {
            Task { await load() }
            onChanged()
        }
    }

    private func post(_ path: String, body: some Encodable, then done: @escaping () -> Void) {
        guard let api = app.api else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                _ = try await api.data("POST", path, body: body)
                done()
            } catch { self.error = error.localizedDescription }
        }
    }
}

// SourcesManageView mirrors the web /sources page: health rows with enable
// toggles and re-verification (gated on sources.manage).
struct SourcesManageView: View {
    @Environment(AppState.self) private var app
    @State private var items: [SourceManageRow] = []
    @State private var note: String?

    var body: some View {
        List {
            ForEach(items) { src in
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Circle()
                            .fill(statusColor(src.status))
                            .frame(width: 8, height: 8)
                        Text(src.name).font(.subheadline.weight(.medium))
                        Spacer()
                        Toggle("", isOn: enabledBinding(src)).labelsHidden()
                    }
                    Text(meta(src)).font(.caption2).foregroundStyle(.secondary)
                    if let err = src.lastError, !err.isEmpty {
                        Text(err).font(.caption2).foregroundStyle(Theme.error).lineLimit(2)
                    }
                }
                .swipeActions {
                    Button("Verify", systemImage: "checkmark.shield") { verify(src) }
                        .tint(Theme.info)
                }
            }
            .nordRows()
        }
        .nordScreen()
        .navigationTitle("Sources")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
        .overlay(alignment: .bottom) {
            if let note {
                Text(note)
                    .font(.footnote)
                    .padding(.horizontal, 12).padding(.vertical, 8)
                    .background(.thinMaterial, in: Capsule())
                    .padding(.bottom, 12)
                    .task { try? await Task.sleep(for: .seconds(2)); self.note = nil }
            }
        }
    }

    private func statusColor(_ status: String) -> Color {
        switch status {
        case "healthy": Theme.success
        case "": Theme.mutedText
        default: Theme.error
        }
    }

    private func meta(_ src: SourceManageRow) -> String {
        var parts = [src.id]
        if !src.status.isEmpty { parts.append(src.status) }
        if src.chaptersFound > 0 { parts.append("\(src.chaptersFound) chapters found") }
        if src.nsfw { parts.append("NSFW") }
        return parts.joined(separator: " · ")
    }

    private func load() async {
        guard let api = app.api else { return }
        items = ((try? await api.get("/api/v1/sources/manage") as SourceManageList) ?? SourceManageList(items: [])).items
    }

    private func enabledBinding(_ src: SourceManageRow) -> Binding<Bool> {
        Binding(
            get: { items.first { $0.id == src.id }?.enabled ?? src.enabled },
            set: { on in
                guard let api = app.api else { return }
                if let i = items.firstIndex(where: { $0.id == src.id }) { items[i].enabled = on }
                Task {
                    do { _ = try await api.data("POST", "/api/v1/sources/\(src.id)/enabled", body: ["on": on]) }
                    catch {
                        note = error.localizedDescription
                        await load()
                    }
                }
            }
        )
    }

    private func verify(_ src: SourceManageRow) {
        guard let api = app.api else { return }
        Task {
            do {
                _ = try await api.data("POST", "/api/v1/sources/\(src.id)/verify")
                note = "Verification queued for \(src.name)"
            } catch { note = error.localizedDescription }
        }
    }
}
