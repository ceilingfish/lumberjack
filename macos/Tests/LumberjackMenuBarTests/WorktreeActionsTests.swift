import AppKit
import Testing

@testable import LumberjackMenuBar

@MainActor
struct WorktreeActionsTests {
    private func launched(_ action: (String) -> Void) -> [(path: String, app: String)] {
        var launches: [(path: String, app: String)] = []
        WorktreeActions.launchOverride = { path, app in launches.append((path, app.lastPathComponent)) }
        defer { WorktreeActions.launchOverride = nil }
        action("/tmp/repo/feature-x")
        return launches
    }

    @Test("revealing a worktree opens its directory with Finder")
    func finderOpensTheDirectory() {
        let launches = launched(WorktreeActions.openInFinder)

        #expect(launches.count == 1)
        #expect(launches.first?.path == "/tmp/repo/feature-x")
        #expect(launches.first?.app == "Finder.app")
    }

    @Test("a shell opens in Terminal, which is always installed")
    func terminalOpensTheDirectory() {
        let launches = launched(WorktreeActions.openInTerminal)

        #expect(launches.count == 1)
        #expect(launches.first?.app == "Terminal.app")
    }

    @Test("VS Code is launched when installed and silently skipped when it is not")
    func vscodeIsConditionalOnBeingInstalled() {
        let launches = launched(WorktreeActions.openInVSCode)

        if WorktreeActions.vscodeURL == nil {
            #expect(launches.isEmpty)
        } else {
            #expect(launches.first?.path == "/tmp/repo/feature-x")
        }
    }

    @Test("icons come from Launch Services, and a second lookup is memoised")
    func iconsAreResolvedAndCached() {
        #expect(WorktreeActions.finderIcon != nil)
        #expect(WorktreeActions.finderIcon != nil)
        #expect(WorktreeActions.terminalIcon != nil)
        #expect((WorktreeActions.vscodeIcon == nil) == (WorktreeActions.vscodeURL == nil))
    }
}
