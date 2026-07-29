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

    /// Where we would like to sit, in points from the right-hand edge of the
    /// menu bar: far enough left to clear the system items (Control Center is
    /// ~180, Now Playing ~330) and the apps that cluster near them.
    private static let preferredMenuBarPosition = 640

    /// Never sit further right than this, however narrow the bar: crowding the
    /// system items is recoverable with one ⌘-drag, and is the lesser evil.
    private static let minimumMenuBarPosition = 360

    /// Room for the item itself, so it is the *whole* icon that clears the
    /// notch rather than just its right-hand edge.
    private static let statusItemWidthAllowance = 32

    /// The slot to claim on first launch, before the user has ever dragged the
    /// icon.
    ///
    /// Not a fixed 640: on a notched display at a scaled resolution the usable
    /// strip to the right of the notch can be a good deal narrower than that
    /// (a 13" Air at ~1280pt logical width puts the notch around 555–725pt from
    /// the right edge), and a slot that lands under the notch reproduces the
    /// invisible icon this whole change exists to fix.
    ///
    /// `auxiliaryTopRightArea` is exactly that strip, and is nil on a display
    /// with no notch — where the whole bar is ours and the preferred slot
    /// stands.
    private static var defaultMenuBarPosition: Int {
        guard let rightOfNotch = NSScreen.main?.auxiliaryTopRightArea?.width else {
            return preferredMenuBarPosition
        }
        let clearOfNotch = Int(rightOfNotch.rounded(.down)) - statusItemWidthAllowance
        return max(min(preferredMenuBarPosition, clearOfNotch), minimumMenuBarPosition)
    }

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
        //
        // Naming the item does change which key AppKit persists under, so anyone
        // who had already dragged the icon — stored under the auto-generated
        // name — starts again from the slot below. Accepted rather than migrated:
        // one drag re-fixes it permanently, which is more than was possible
        // before, and the app has no released version whose placement we would
        // be breaking.
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
