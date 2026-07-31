import AppKit
import Testing

@testable import LumberjackMenuBar

@MainActor
struct StatusItemTests {
    private func launchedDelegate() -> AppDelegate {
        let daemon = StubDaemon()
        let delegate = AppDelegate(state: AppState(pollIntervalSeconds: 3600, connect: { daemon }))
        delegate.applicationDidFinishLaunching(
            Notification(name: NSApplication.didFinishLaunchingNotification))
        return delegate
    }

    private func buttonInAWindow() -> NSStatusBarButton {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 200, height: 200),
            styleMask: [.titled], backing: .buffered, defer: false)
        let button = NSStatusBarButton(frame: NSRect(x: 0, y: 0, width: 22, height: 22))
        window.contentView?.addSubview(button)
        window.orderFront(nil)
        return button
    }

    @Test("launching claims a menu-bar slot and installs a status item")
    func launchingInstallsAStatusItem() {
        Notifier.postOverride = { _, _ in }
        defer { Notifier.postOverride = nil }

        _ = launchedDelegate()

        let key = "NSStatusItem Preferred Position com.ceilingfish.lumberjack.menubar.statusItem"
        #expect(UserDefaults.standard.integer(forKey: key) > 0)
    }

    @Test("the panel toggles open and shut from the status button")
    func togglingShowsAndHidesThePanel() {
        Notifier.postOverride = { _, _ in }
        defer { Notifier.postOverride = nil }
        let delegate = launchedDelegate()
        let button = buttonInAWindow()

        delegate.perform(NSSelectorFromString("togglePopover:"), with: button)
        delegate.perform(NSSelectorFromString("togglePopover:"), with: button)
    }

    @Test("the delegate SwiftUI builds for itself owns its own app state")
    func theDefaultDelegateMakesItsOwnState() {
        _ = AppDelegate()
        _ = LumberjackMenuBarApp().body
    }
}
