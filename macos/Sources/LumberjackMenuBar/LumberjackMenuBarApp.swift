import AppKit
import SwiftUI

/// Entry point. The app is menu-bar-only (see Info.plist's `LSUIElement`): no
/// Dock icon, no regular window.
///
/// We drive the status item and its panel through AppKit (`NSStatusItem` +
/// `NSPopover`) rather than SwiftUI's `MenuBarExtra`. `MenuBarExtra(.window)`
/// gives no control over where the panel appears — it anchors to one edge of
/// the icon — whereas an `NSPopover` shown from the status button is centred
/// under the icon (its arrow points at the button's midpoint), which is the
/// placement we want. The panel content itself is still the SwiftUI
/// `MenuBarView`, hosted inside the popover.
@main
struct LumberjackMenuBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        // No visible scene; the UI lives entirely in the status-item popover.
        // A `Settings` scene keeps SwiftUI happy without showing a window.
        Settings { EmptyView() }
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem?
    private let popover = NSPopover()
    private let state = AppState()

    func applicationDidFinishLaunching(_ notification: Notification) {
        let hosting = NSHostingController(rootView: MenuBarView(state: state))
        // Let the popover track the SwiftUI view's own size, so it grows and
        // shrinks with the content (spinner vs. worktree list) instead of a
        // fixed guess.
        hosting.sizingOptions = .preferredContentSize
        popover.contentViewController = hosting
        popover.behavior = .transient

        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.image = NSImage(systemSymbolName: "tree", accessibilityDescription: "Lumberjack")
        item.button?.action = #selector(togglePopover(_:))
        item.button?.target = self
        statusItem = item
    }

    @objc private func togglePopover(_ sender: NSStatusBarButton) {
        if popover.isShown {
            popover.performClose(sender)
            return
        }
        // `.minY` shows the panel below the icon; the popover centres itself
        // horizontally on the button.
        popover.show(relativeTo: sender.bounds, of: sender, preferredEdge: .minY)
        popover.contentViewController?.view.window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
}
