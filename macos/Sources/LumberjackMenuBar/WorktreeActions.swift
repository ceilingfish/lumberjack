import AppKit

/// Opening a worktree's directory from the UI: reveal in Finder, open a shell
/// in Terminal, or open the folder in VS Code. Finder and Terminal are always
/// present on macOS; VS Code is conditional on being installed (resolved once
/// via Launch Services) so its button only appears when there's an editor to
/// launch. Each action's button uses the real application icon, fetched from
/// Launch Services rather than bundled bitmaps, so it stays crisp and matches
/// whatever the user actually has installed.
@MainActor
enum WorktreeActions {
    private static let finderURL = URL(fileURLWithPath: "/System/Library/CoreServices/Finder.app")
    private static let terminalURL: URL? =
        NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.Terminal")
    static let vscodeURL: URL? =
        NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.microsoft.VSCode")

    private static var iconCache: [String: NSImage] = [:]

    /// The application icon at `url`, memoised — Launch Services' answer
    /// doesn't change while we run.
    private static func icon(for url: URL?) -> NSImage? {
        guard let url else { return nil }
        if let cached = iconCache[url.path] { return cached }
        let icon = NSWorkspace.shared.icon(forFile: url.path)
        iconCache[url.path] = icon
        return icon
    }

    static var finderIcon: NSImage? { icon(for: finderURL) }
    static var terminalIcon: NSImage? { icon(for: terminalURL) }
    static var vscodeIcon: NSImage? { icon(for: vscodeURL) }

    static var launchOverride: ((String, URL) -> Void)?

    static func openInFinder(_ path: String) {
        open(path, with: finderURL)
    }

    static func openInTerminal(_ path: String) {
        open(path, with: terminalURL)
    }

    static func openInVSCode(_ path: String) {
        open(path, with: vscodeURL)
    }

    private static func open(_ path: String, with app: URL?) {
        guard let app else { return }
        if let launchOverride {
            launchOverride(path, app)
            return
        }
        NSWorkspace.shared.open(
            [URL(fileURLWithPath: path)],
            withApplicationAt: app,
            configuration: NSWorkspace.OpenConfiguration()
        )
    }
}
