import AppKit

/// Opening a worktree's directory from the UI: in Finder (double-click) or in
/// VS Code (the trailing button). VS Code support is conditional on it being
/// installed — resolved once via Launch Services — so the button only appears
/// when there's actually an editor to launch.
@MainActor
enum WorktreeActions {
    /// The installed VS Code application, or nil if it isn't installed.
    /// Resolved once: Launch Services' answer doesn't change while we run.
    static let vscodeURL: URL? =
        NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.microsoft.VSCode")

    private static var cachedIcon: NSImage?

    /// VS Code's own app icon, for the "open in VS Code" button. nil when VS
    /// Code isn't installed (so the button is hidden entirely).
    static var vscodeIcon: NSImage? {
        if let cachedIcon { return cachedIcon }
        guard let vscodeURL else { return nil }
        let icon = NSWorkspace.shared.icon(forFile: vscodeURL.path)
        cachedIcon = icon
        return icon
    }

    static func openInFinder(_ path: String) {
        NSWorkspace.shared.open(URL(fileURLWithPath: path))
    }

    static func openInVSCode(_ path: String) {
        guard let vscodeURL else { return }
        NSWorkspace.shared.open(
            [URL(fileURLWithPath: path)],
            withApplicationAt: vscodeURL,
            configuration: NSWorkspace.OpenConfiguration()
        )
    }
}
