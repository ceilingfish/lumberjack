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

    /// Key under which our menu-bar slot is remembered. Unique to us, so the
    /// stored position can't be confused with another app's.
    private static let statusItemAutosaveName = "com.ceilingfish.lumberjack.menubar.statusItem"

    /// Points from the right-hand edge of the menu bar. Far enough left to sit
    /// clear of the system items (Control Center is ~180, Now Playing ~330)
    /// without reaching the notch on built-in displays.
    private static let defaultMenuBarPosition = 640

    private static func claimDefaultMenuBarPosition() {
        UserDefaults.standard.register(defaults: [
            "NSStatusItem Preferred Position \(statusItemAutosaveName)": defaultMenuBarPosition
        ])
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let hosting = NSHostingController(rootView: MenuBarView(state: state))
        // Let the popover track the SwiftUI view's own size, so it grows and
        // shrinks with the content (spinner vs. worktree list) instead of a
        // fixed guess.
        hosting.sizingOptions = .preferredContentSize
        popover.contentViewController = hosting
        popover.behavior = .transient
        // The panel is designed as a light "card" with a fixed palette; pin it
        // to the light appearance so it reads the same whether or not the
        // system is in dark mode.
        popover.appearance = NSAppearance(named: .aqua)

        // AppKit remembers where the user dragged a status item under
        // "NSStatusItem Preferred Position <autosaveName>" — points from the
        // right-hand edge of the menu bar. An item that has never been dragged
        // has no stored value and lands in the same default slot every other
        // undragged item picks, so two such items get drawn on top of each
        // other. That is what hides our icon when Memtime is running: its tray
        // item takes the identical slot. Registering (rather than writing) a
        // default claims a slot of our own on first launch, while a real drag
        // still persists to the same key and takes precedence.
        Self.claimDefaultMenuBarPosition()

        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.autosaveName = Self.statusItemAutosaveName
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
