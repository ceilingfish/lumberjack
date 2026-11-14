import SwiftProtobuf
import Testing

@testable import LumberjackMenuBar

@MainActor
struct AppStateTabTests {
    private func waitUntil(_ condition: () -> Bool) async {
        for _ in 0..<1000 where !condition() {
            await Task.yield()
        }
        #expect(condition())
    }

    private func syncedRepository(_ name: String, seconds: Int64) -> Lumberjack_V1_Repository {
        var repo = stubRepository(name: name, path: "/tmp/\(name)")
        var ts = Google_Protobuf_Timestamp()
        ts.seconds = seconds
        repo.lastSyncedAt = ts
        return repo
    }

    private func splitDaemon() -> StubDaemon {
        let daemon = StubDaemon()
        daemon.repositories = [syncedRepository("repo", seconds: 100), stubRepository(name: "other", path: "/tmp/other")]
        daemon.worktreesByRepository = [
            "/tmp/repo": [
                stubWorktree(branch: "mine", path: "/tmp/repo/mine", authorship: .mine),
                stubWorktree(branch: "theirs", path: "/tmp/repo/theirs", authorship: .theirs),
            ]
        ]
        return daemon
    }

    @Test("the selected tab filters the worktrees")
    func tabFiltersWorktrees() async {
        let state = AppState(connect: { self.splitDaemon() })
        state.selectedRepository = "/tmp/repo"
        await state.refreshOnce()

        #expect(state.tabWorktrees.map(\.branchName) == ["mine"])
        state.selectedTab = .theirs
        #expect(state.tabWorktrees.map(\.branchName) == ["theirs"])
    }

    @Test("leaving a tab marks it viewed through the repository's last sync")
    func leavingATabMarksItViewed() async {
        let daemon = splitDaemon()
        let state = AppState(connect: { daemon })
        state.selectedRepository = "/tmp/repo"
        await state.refreshOnce()

        state.selectedTab = .theirs
        state.selectedTab = .theirs
        await waitUntil { daemon.viewed.count == 1 }
        #expect(daemon.viewed.first?.repository == "/tmp/repo")
        #expect(daemon.viewed.first?.authorship == .mine)
        #expect(daemon.viewed.first?.seenThrough?.seconds == 100)

        state.panelClosed()
        await waitUntil { daemon.viewed.count == 2 }
        #expect(daemon.viewed.last?.authorship == .theirs)

        state.selectedRepository = "/tmp/other"
        await waitUntil { daemon.viewed.count == 3 }
        #expect(daemon.viewed.last?.repository == "/tmp/repo")
    }

    @Test("a repository never synced is marked with no time, leaving the daemon to choose")
    func unsyncedRepositorySendsNoTime() async {
        let daemon = splitDaemon()
        daemon.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        let state = AppState(connect: { daemon })
        await state.refreshOnce()

        state.panelClosed()
        await waitUntil { daemon.viewed.count == 1 }
        #expect(daemon.viewed.first?.seenThrough == nil)
    }

    @Test("nothing is marked viewed against an older daemon, before connecting, or with no repository")
    func nothingMarkedWithoutSplitData() async {
        let daemon = StubDaemon()
        daemon.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        daemon.worktreesByRepository = ["/tmp/repo": [stubWorktree(branch: "main", path: "/tmp/repo/main")]]
        let state = AppState(connect: { daemon })

        state.panelClosed()
        await state.refreshOnce()
        state.selectedTab = .theirs
        state.panelClosed()
        #expect(state.tabWorktrees.map(\.branchName) == ["main"])

        let empty = AppState(connect: { StubDaemon() })
        await empty.refreshOnce()
        empty.panelClosed()

        for _ in 0..<100 { await Task.yield() }
        #expect(daemon.viewed.isEmpty)
    }
}
