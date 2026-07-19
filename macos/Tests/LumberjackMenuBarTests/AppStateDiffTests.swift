import Testing
@testable import LumberjackMenuBar

@MainActor
struct AppStateDiffTests {
    private func worktree(branch: String, path: String) -> Lumberjack_V1_Worktree {
        var wt = Lumberjack_V1_Worktree()
        wt.branchName = branch
        wt.directoryPath = path
        return wt
    }

    @Test
    func firstObservationEstablishesBaselineWithoutNotifying() {
        var posted: [(String, String)] = []
        Notifier.postOverride = { title, body in posted.append((title, body)) }
        defer { Notifier.postOverride = nil }

        let state = AppState()
        state.diffAndNotify(
            repository: "repo",
            worktrees: [worktree(branch: "main", path: "/tmp/repo/main")],
            key: "/tmp/repo"
        )

        #expect(posted.isEmpty)
    }

    @Test
    func createdWorktreeNotifiesWithBranchName() {
        var posted: [(String, String)] = []
        Notifier.postOverride = { title, body in posted.append((title, body)) }
        defer { Notifier.postOverride = nil }

        let state = AppState()
        state.diffAndNotify(repository: "repo", worktrees: [], key: "/tmp/repo")
        state.diffAndNotify(
            repository: "repo",
            worktrees: [worktree(branch: "feature-x", path: "/tmp/repo/feature-x")],
            key: "/tmp/repo"
        )

        #expect(posted.count == 1)
        #expect(posted.first?.0 == "Worktree cloned")
        #expect(posted.first?.1 == "repo: feature-x")
    }

    /// Regression test for the bug where a deleted worktree's notification
    /// showed the raw directory path instead of its branch name, because the
    /// snapshot used to diff across polls only remembered paths.
    @Test
    func deletedWorktreeNotifiesWithBranchNameNotPath() {
        var posted: [(String, String)] = []
        Notifier.postOverride = { title, body in posted.append((title, body)) }
        defer { Notifier.postOverride = nil }

        let state = AppState()
        state.diffAndNotify(
            repository: "repo",
            worktrees: [worktree(branch: "feature-x", path: "/tmp/repo/feature-x")],
            key: "/tmp/repo"
        )
        state.diffAndNotify(repository: "repo", worktrees: [], key: "/tmp/repo")

        #expect(posted.count == 1)
        #expect(posted.first?.0 == "Worktree deleted")
        #expect(posted.first?.1 == "repo: feature-x")
    }
}
