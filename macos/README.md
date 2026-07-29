# Lumberjack menu-bar app

A native macOS menu-bar companion to the `lumberjack` CLI/daemon: a live view
of tracked repositories and their worktrees, with a native notification
whenever a worktree is cloned or deleted.

It is a separate, opt-in install from the Go CLI — see "Out of scope" in
issue #9. Installing the Go binary never installs this app, and vice versa.

## Architecture

- Swift Package Manager project (no Xcode project file to keep in sync;
  `swift build` and `Package.swift` are the source of truth — open the folder
  in Xcode directly, it understands `Package.swift` natively).
- `Sources/LumberjackMenuBar/Generated/` holds Swift protobuf/gRPC stubs
  generated from `proto/lumberjack/v1/lumberjack.proto`, the same contract the
  Go CLI and daemon use. **Never hand-edit these** — regenerate them (see
  below) whenever the proto changes.
- `DaemonClient` dials the daemon's Unix domain socket the same way the Go CLI
  does (`LUMBERJACK_SOCKET_PATH`, else `~/.lumberjack/daemon.sock` — see
  `SocketPath.swift`, which mirrors `pkg/client.DefaultSocketPath`).
- `AppState` refreshes repositories and the selected repository's worktrees on
  a short poll interval and diffs successive snapshots to detect worktree
  clone/delete, driving both the live UI and `Notifier`'s native notifications.

  **This is an interim implementation.** Issue #9 depends on #13, which adds a
  streaming `Watch` RPC and daemon-side event broadcasting; #13 is explicitly
  out of scope for #9 and had not landed at the time this app was built. Once
  it lands, `AppState.refreshLoop()` should be replaced with a subscriber to
  that stream instead of polling `ListRepositories`/`ListWorktrees` on a timer
  — everything else (`ConnectionState`, `repositories`, `worktrees`,
  `selectedRepository`, `Notifier`) was written to stay the same across that
  swap.
- `MenuBarView` mirrors the CLI's worktree listing columns (branch, PR,
  status/reconciliation note, last-synced) for the selected repository.

## Building

Requires Xcode (or the Xcode Command Line Tools with a full Xcode install for
`codesign`/app bundling) and Swift 6.0+, targeting macOS 14+. Swift 6.0 is
required so SPM auto-links the `Testing` framework used by
`Tests/LumberjackMenuBarTests` (Swift Testing requires swift-tools-version
6.0+; under 5.9 `swift test` fails with "no such module 'Testing'"). The
deployment target is macOS 14+ (not 13) because swift-testing's
`Testing.framework` itself requires a macOS 14 minimum — a `.v13` target
built fine but `swift test` failed to dlopen `Testing.framework` at run time.

```sh
cd macos
swift build                 # debug build, for development
open Package.swift          # or: open this folder in Xcode
```

### Regenerating the gRPC stubs

Only needed after `proto/lumberjack/v1/lumberjack.proto` changes. Requires
`buf` (`brew install buf`) and network access (the Swift plugins are fetched
remotely from buf.build, same as the Go codegen in `buf.gen.yaml`):

```sh
# from the repo root
buf generate --template buf.gen.swift.yaml
```

### Building a distributable `.app` / `.dmg`

```sh
macos/scripts/build-app.sh        # -> macos/.build/app/LumberjackMenuBar.app
macos/scripts/package-dmg.sh      # -> macos/.build/LumberjackMenuBar.dmg
```

The app is ad-hoc signed by `build-app.sh`; a real release build should sign
with a Developer ID certificate and notarize before distributing the `.dmg`.

## Installing and running

1. Build (above) or download a released `.dmg`, open it, and drag
   `LumberjackMenuBar.app` to `/Applications`.

   **Gatekeeper warning on a downloaded `.dmg`:** released builds are
   currently **ad-hoc signed only, not notarized** (see issue #11; Developer
   ID signing and notarization are a follow-up). macOS quarantines anything
   downloaded from a browser, so a plain double-click on the `.app` reports it
   as damaged or refuses to open it. To run it anyway:

   - **Right-click (or Control-click) `LumberjackMenuBar.app` → Open**, then
     confirm in the dialog that appears — this only needs to be done once.
   - Or clear the quarantine attribute from the Terminal before launching:
     ```sh
     xattr -dr com.apple.quarantine /Applications/LumberjackMenuBar.app
     ```
2. Launch it. It has no Dock icon (menu-bar-only, `LSUIElement`); look for the
   tree icon in the menu bar.
3. It connects to whatever `lumberjack daemon` is already running for the
   current user (start it the usual way, e.g. `lumberjack daemon start`). If
   no daemon is reachable, the menu shows "Daemon not running" and keeps
   retrying — no restart of the app needed once the daemon comes up.
4. Click the menu-bar icon to open the panel: pick a tracked repository from
   the switcher to see its live worktree list. A notification appears
   whenever a worktree is cloned or deleted for any tracked repository,
   regardless of which one is currently selected.

This app is read-only: it has no create/delete actions. Use the CLI
(`lumberjack sync`, `lumberjack repositories NAME worktree ... delete`, etc.)
to make changes.
