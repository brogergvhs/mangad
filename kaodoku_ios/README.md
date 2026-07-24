# Kaodoku iOS

Native SwiftUI client for a self-hosted Kaodoku server (`/api/v1`).

- iOS 17+, no dependencies. Open `Kaodoku.xcodeproj` in Xcode 16+.
- Sources live in `Kaodoku/` as a file-system-synchronized group — add files to
  the folder and Xcode picks them up; no project-file surgery.
- Build from CLI: `xcodebuild -project Kaodoku.xcodeproj -target Kaodoku -sdk iphonesimulator build`

## Phase 1 (current): online read-only MVP
Connect (single-user auto-detect via `/meta`, or username/password → device
API token in the Keychain) → library grid → title detail → streaming paged
reader with progress marking and manifest window extension. Images cache via
URLCache honoring the server's ETags. ATS allows plain HTTP for LAN servers.

## Next phases (see ../IOS_APP_PLAN.md)
User actions (favourites, collections, downloads-trigger) → offline (CBZ
archives, batch progress replay, delta sync) → polish (strip/RTL reader modes
from server-synced prefs, real zoom/pan, iPad layouts, push).
