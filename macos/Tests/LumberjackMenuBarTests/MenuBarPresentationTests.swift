import Foundation
import SwiftProtobuf
import Testing

@testable import LumberjackMenuBar

struct MenuBarPresentationTests {
    private func timestamp(seconds: Int64, nanos: Int32 = 0) -> Google_Protobuf_Timestamp {
        var ts = Google_Protobuf_Timestamp()
        ts.seconds = seconds
        ts.nanos = nanos
        return ts
    }

    private func syncedRepository(name: String, path: String, at seconds: Int64) -> Lumberjack_V1_Repository {
        var repo = stubRepository(name: name, path: path)
        repo.lastSyncedAt = timestamp(seconds: seconds)
        return repo
    }

    @Test("the switcher prompts for a repository until one is selected")
    func selectedRepositoryName() {
        #expect(MenuBarPresentation.selectedRepositoryName(nil) == "Select repository")
        #expect(
            MenuBarPresentation.selectedRepositoryName(stubRepository(name: "repo", path: "/tmp/repo")) == "repo")
    }

    @Test("the status area names why there is nothing to list")
    func statusMessages() {
        #expect(MenuBarPresentation.statusMessage(.connecting).text == "Connecting to daemon…")
        #expect(MenuBarPresentation.statusMessage(.daemonNotRunning).text == "Daemon not running")
        #expect(MenuBarPresentation.statusMessage(.connected).text == "No repositories tracked")
        #expect(MenuBarPresentation.statusMessage(.daemonNotRunning).symbol == "exclamationmark.triangle")
        #expect(MenuBarPresentation.statusMessage(.connecting).symbol == "arrow.triangle.2.circlepath")
        #expect(MenuBarPresentation.statusMessage(.connected).symbol == "tray")
    }

    @Test("the pill labels each connection state")
    func pillLabels() {
        #expect(MenuBarPresentation.pillLabel(.connected) == "Connected")
        #expect(MenuBarPresentation.pillLabel(.connecting) == "Connecting")
        #expect(MenuBarPresentation.pillLabel(.daemonNotRunning) == "Offline")
    }

    @Test("a disconnected header repeats the status message")
    func headerSubtitleWhenDisconnected() {
        #expect(MenuBarPresentation.headerSubtitle(.connecting, repositories: []) == "Connecting to daemon…")
        #expect(MenuBarPresentation.headerSubtitle(.daemonNotRunning, repositories: []) == "Daemon not running")
    }

    @Test("a connected header counts the repositories being watched, singular for one")
    func headerSubtitleCountsRepositories() {
        #expect(MenuBarPresentation.headerSubtitle(.connected, repositories: []) == "Watching 0 repositories")
        #expect(
            MenuBarPresentation.headerSubtitle(
                .connected, repositories: [stubRepository(name: "repo", path: "/tmp/repo")]
            ) == "Watching 1 repository")
    }

    @Test("a connected header appends the newest sync across every repository")
    func headerSubtitleAppendsTheNewestSync() {
        let repositories = [
            syncedRepository(name: "old", path: "/tmp/old", at: 1_700_000_000),
            syncedRepository(name: "new", path: "/tmp/new", at: 1_800_000_000),
            stubRepository(name: "never", path: "/tmp/never"),
        ]
        let newest = Date(timeIntervalSince1970: 1_800_000_000)

        #expect(
            MenuBarPresentation.headerSubtitle(.connected, repositories: repositories)
                == "Watching 3 repositories · synced \(MenuBarPresentation.relative(newest))")
    }

    @Test("the newest sync ignores repositories that have never synced")
    func latestSyncIgnoresUnsyncedRepositories() {
        #expect(MenuBarPresentation.latestSync(of: [stubRepository(name: "repo", path: "/tmp/repo")]) == nil)
        #expect(
            MenuBarPresentation.latestSync(of: [
                syncedRepository(name: "old", path: "/tmp/old", at: 10),
                syncedRepository(name: "new", path: "/tmp/new", at: 20),
            ]) == Date(timeIntervalSince1970: 20))
    }

    @Test("a timestamp's nanoseconds survive the conversion to a date")
    func timestampsConvertToDates() {
        #expect(
            MenuBarPresentation.date(from: timestamp(seconds: 10, nanos: 500_000_000))
                == Date(timeIntervalSince1970: 10.5))
    }

    @Test("the summary lists only the non-empty categories")
    func worktreeSummaryListsNonEmptyCategories() {
        let worktrees = [
            stubWorktree(branch: "a", path: "/tmp/a"),
            stubWorktree(branch: "b", path: "/tmp/b"),
            stubWorktree(branch: "c", path: "/tmp/c", needsReconciliation: true),
            stubWorktree(branch: "d", path: "/tmp/d", orphaned: true),
        ]

        #expect(MenuBarPresentation.worktreeSummary(worktrees) == "2 in sync · 1 needs attention · 1 orphaned")
        #expect(MenuBarPresentation.worktreeSummary([]) == "")
        #expect(MenuBarPresentation.worktreeSummary([worktrees[0]]) == "1 in sync")
    }

    @Test("a worktree needing reconciliation is not also counted as orphaned")
    func reconciliationOutranksOrphaned() {
        let worktree = stubWorktree(branch: "a", path: "/tmp/a", needsReconciliation: true, orphaned: true)

        #expect(MenuBarPresentation.worktreeSummary([worktree]) == "1 needs attention")
        #expect(MenuBarPresentation.status(of: worktree, isSyncing: false) == .attention)
    }

    @Test("an in-flight sync outranks every other status")
    func syncingOutranksEverything() {
        let worktree = stubWorktree(branch: "a", path: "/tmp/a", needsReconciliation: true)

        #expect(MenuBarPresentation.status(of: worktree, isSyncing: true) == .syncing)
        #expect(MenuBarPresentation.status(of: stubWorktree(branch: "a", path: "/tmp/a"), isSyncing: false) == .inSync)
        #expect(
            MenuBarPresentation.status(of: stubWorktree(branch: "a", path: "/tmp/a", orphaned: true), isSyncing: false)
                == .orphaned)
    }

    @Test("a row's subtitle explains reconciliation, else reports the last sync, else nothing")
    func rowSubtitles() {
        #expect(
            MenuBarPresentation.rowSubtitle(
                stubWorktree(branch: "a", path: "/tmp/a", needsReconciliation: true, reconciliationNote: "diverged"))
                == "diverged")
        #expect(
            MenuBarPresentation.rowSubtitle(stubWorktree(branch: "a", path: "/tmp/a", needsReconciliation: true))
                == "Needs reconciliation")
        #expect(MenuBarPresentation.rowSubtitle(stubWorktree(branch: "a", path: "/tmp/a")) == nil)

        var synced = stubWorktree(branch: "a", path: "/tmp/a")
        synced.lastSyncedAt = timestamp(seconds: 1_700_000_000)
        #expect(
            MenuBarPresentation.rowSubtitle(synced)
                == "Synced \(MenuBarPresentation.relative(Date(timeIntervalSince1970: 1_700_000_000)))")
    }

    @Test("a pull-request link points at the repository's own host")
    func prURLUsesTheRepositoryHost() {
        var worktree = stubWorktree(branch: "a", path: "/tmp/a")
        worktree.githubPrNumber = 42
        let repo = stubRepository(name: "lumberjack", path: "/tmp/repo", owner: "ceilingfish")
        let enterprise = stubRepository(
            name: "lumberjack", path: "/tmp/repo", owner: "ceilingfish", host: "github.example.com")

        #expect(
            MenuBarPresentation.prURL(repository: repo, worktree: worktree)
                == URL(string: "https://github.com/ceilingfish/lumberjack/pull/42"))
        #expect(
            MenuBarPresentation.prURL(repository: enterprise, worktree: worktree)
                == URL(string: "https://github.example.com/ceilingfish/lumberjack/pull/42"))
    }

    @Test("a worktree with no pull request, or no repository, has no link")
    func prURLIsNilWithoutAPullRequest() {
        var worktree = stubWorktree(branch: "a", path: "/tmp/a")
        worktree.githubPrNumber = 42

        #expect(MenuBarPresentation.prURL(repository: nil, worktree: worktree) == nil)
        #expect(
            MenuBarPresentation.prURL(
                repository: stubRepository(name: "repo", path: "/tmp/repo"),
                worktree: stubWorktree(branch: "a", path: "/tmp/a")) == nil)
    }
}
