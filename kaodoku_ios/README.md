# Kaodoku iOS

Native SwiftUI client for a self-hosted Kaodoku server (`/api/v1`).

- iOS 17+, no dependencies. Open `Kaodoku.xcodeproj` in Xcode 16+.
- Sources live in `Kaodoku/` as a file-system-synchronized group — add files to
  the folder and Xcode picks them up; no project-file surgery.
- Build from CLI: `xcodebuild -project Kaodoku.xcodeproj -target Kaodoku -sdk iphonesimulator build`

## Phase 1: online read-only MVP
Connect (single-user auto-detect via `/meta`, or username/password → device
API token in the Keychain) → library grid → title detail → streaming reader
with progress marking and manifest window extension. Images cache via
URLCache honoring the server's ETags. ATS allows plain HTTP for LAN servers.

## Phase 2 (current): user actions
Library/Search tabs. Search mirrors the web app: personalized "For you"
suggestions with a guarded browse fallback, 400ms-debounced live search,
tag include/exclude filters + sort (`/api/v1/tags`), one-tap add
(`/api/v1/library/add`), in-library checkmarks, adult outlines, load-more
paging; a manual source picker remains as the advanced path. Title detail:
favourite, monitor toggle, refresh/download-missing jobs, per-title AniList
sync, volumes (list + reading). Reader: paged LTR/RTL and long-strip modes,
synced across devices via `/me/settings`. The API token is only ever sent to
the configured server (absolute URLs like AniList covers go unauthenticated).

## Next phases (see ../IOS_APP_PLAN.md)
Offline (CBZ archives, batch progress replay, delta sync) → polish (real
zoom-pan, collections UI, iPad layouts, push).
