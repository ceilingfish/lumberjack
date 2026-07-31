import Testing

@testable import LumberjackMenuBar

@MainActor
struct AppStateRefreshTests {
    private func waitUntil(_ condition: () -> Bool) async {
        for _ in 0..<1000 where !condition() {
            await Task.yield()
        }
        #expect(condition())
    }

    @Test("a daemon that cannot be dialled at all reads as not running")
    func connectFailureReportsDaemonNotRunning() async {
        let state = AppState(connect: { throw StubDaemonError() })

        await state.refreshOnce()

        #expect(state.connectionState == .daemonNotRunning)
        #expect(state.repositories.isEmpty)
    }

    @Test("a failing health probe drops the connection so the next tick dials fresh")
    func healthFailureClosesTheConnectionAndRedials() async {
        let first = StubDaemon()
        first.healthError = StubDaemonError()
        let second = StubDaemon()
        second.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        var dialled: [StubDaemon] = [first, second]
        let state = AppState(connect: { dialled.removeFirst() })

        await state.refreshOnce()

        #expect(state.connectionState == .daemonNotRunning)
        #expect(first.closeCount == 1)

        await state.refreshOnce()

        #expect(state.connectionState == .connected)
        #expect(state.repositories.map(\.githubName) == ["repo"])
    }

    @Test("a refresh sorts repositories by name, selects the daemon's first, and counts worktrees")
    func refreshPopulatesRepositoriesAndWorktrees() async {
        let daemon = StubDaemon()
        daemon.repositories = [
            stubRepository(name: "zebra", path: "/tmp/zebra"),
            stubRepository(name: "apple", path: "/tmp/apple"),
        ]
        daemon.worktreesByRepository = [
            "/tmp/zebra": [
                stubWorktree(branch: "main", path: "/tmp/zebra/main"),
                stubWorktree(branch: "alpha", path: "/tmp/zebra/alpha"),
            ],
            "/tmp/apple": [stubWorktree(branch: "main", path: "/tmp/apple/main")],
        ]
        let state = AppState(connect: { daemon })

        await state.refreshOnce()

        #expect(state.repositories.map(\.githubName) == ["apple", "zebra"])
        #expect(state.selectedRepository == "/tmp/zebra")
        #expect(state.selectedRepositoryObject?.githubName == "zebra")
        #expect(state.worktrees.map(\.branchName) == ["alpha", "main"])
        #expect(state.worktreesLoaded)
        #expect(state.worktreeCountsByRepo == ["/tmp/zebra": 2, "/tmp/apple": 1])
        #expect(state.knownWorktreeKeys == ["/tmp/zebra", "/tmp/apple"])
    }

    @Test("an already-selected repository survives a refresh")
    func refreshKeepsAnExistingSelection() async {
        let daemon = StubDaemon()
        daemon.repositories = [
            stubRepository(name: "zebra", path: "/tmp/zebra"),
            stubRepository(name: "apple", path: "/tmp/apple"),
        ]
        daemon.worktreesByRepository = ["/tmp/apple": [stubWorktree(branch: "main", path: "/tmp/apple/main")]]
        let state = AppState(connect: { daemon })
        state.selectedRepository = "/tmp/apple"

        await state.refreshOnce()

        #expect(state.selectedRepository == "/tmp/apple")
        #expect(state.worktrees.map(\.branchName) == ["main"])
    }

    @Test("a worktree listing that fails mid-refresh leaves the last known good data on screen")
    func worktreeFailureKeepsTheLastKnownGoodData() async {
        let daemon = StubDaemon()
        daemon.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        daemon.worktreesByRepository = ["/tmp/repo": [stubWorktree(branch: "main", path: "/tmp/repo/main")]]
        let state = AppState(connect: { daemon })
        await state.refreshOnce()

        daemon.listWorktreesError = StubDaemonError()
        await state.refreshOnce()

        #expect(state.connectionState == .connected)
        #expect(state.worktrees.map(\.branchName) == ["main"])
        #expect(state.worktreeCountsByRepo == ["/tmp/repo": 1])
    }

    @Test("a repository listing that fails leaves the connection reported as up")
    func repositoryFailureKeepsTheConnectionUp() async {
        let daemon = StubDaemon()
        daemon.listRepositoriesError = StubDaemonError()
        let state = AppState(connect: { daemon })

        await state.refreshOnce()

        #expect(state.connectionState == .connected)
        #expect(state.repositories.isEmpty)
    }

    @Test("switching repository clears the previous one's worktrees, reselecting the same one does not")
    func selectionClearsWorktreesOnlyWhenItChanges() async {
        let daemon = StubDaemon()
        daemon.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        daemon.worktreesByRepository = ["/tmp/repo": [stubWorktree(branch: "main", path: "/tmp/repo/main")]]
        let state = AppState(connect: { daemon })
        await state.refreshOnce()

        state.selectedRepository = "/tmp/repo"

        #expect(state.worktrees.count == 1)
        #expect(state.worktreesLoaded)

        state.selectedRepository = "/tmp/other"

        #expect(state.worktrees.isEmpty)
        #expect(!state.worktreesLoaded)
        #expect(state.selectedRepositoryObject == nil)
    }

    @Test("start polls until stopped, and starting twice keeps one loop")
    func startPollsUntilStopped() async {
        Notifier.postOverride = { _, _ in }
        defer { Notifier.postOverride = nil }
        let daemon = StubDaemon()
        daemon.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        let state = AppState(pollIntervalSeconds: 3600, connect: { daemon })

        state.start()
        state.start()
        await waitUntil { state.connectionState == .connected }

        state.stop()
        await waitUntil { daemon.closeCount == 1 }
    }

    @Test("sync all reconciles every repository once, then refreshes")
    func syncAllReconcilesEveryRepository() async {
        let daemon = StubDaemon()
        daemon.repositories = [
            stubRepository(name: "apple", path: "/tmp/apple"),
            stubRepository(name: "zebra", path: "/tmp/zebra"),
        ]
        let state = AppState(connect: { daemon })
        await state.refreshOnce()

        state.syncAll()
        #expect(state.syncing)
        state.syncAll()

        await waitUntil { !state.syncing }
        #expect(daemon.syncedRepositories.sorted() == ["/tmp/apple", "/tmp/zebra"])
    }

    @Test("sync all does nothing before the first connection")
    func syncAllWithoutAConnectionDoesNothing() {
        let daemon = StubDaemon()
        let state = AppState(connect: { daemon })

        state.syncAll()

        #expect(!state.syncing)
        #expect(daemon.syncedRepositories.isEmpty)
    }
}
