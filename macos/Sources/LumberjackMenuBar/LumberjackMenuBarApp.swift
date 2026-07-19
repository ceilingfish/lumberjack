import SwiftUI

/// Entry point. `MenuBarExtra` gives us the `NSStatusItem` menu-bar icon (and
/// its window-management chrome) without hand-rolling AppKit status-item
/// plumbing; the app never shows a Dock icon or a regular window (see
/// Info.plist's `LSUIElement`).
@main
struct LumberjackMenuBarApp: App {
    @StateObject private var state = AppState()

    var body: some Scene {
        MenuBarExtra("Lumberjack", systemImage: "tree") {
            MenuBarView(state: state)
        }
        .menuBarExtraStyle(.window)
    }
}
