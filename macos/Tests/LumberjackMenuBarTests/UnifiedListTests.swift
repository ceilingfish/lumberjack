import Foundation
import SwiftProtobuf
import Testing

@testable import LumberjackMenuBar

private func pr(
    score: Int64 = 0, updated: Int64? = nil, failing: Int32 = 0, merge: Lumberjack_V1_MergeStateStatus = .clean,
    decision: Lumberjack_V1_ReviewDecision = .unspecified, unresolved: Int32 = 0,
    approvedByMe: Bool = false, additions: Int32 = 0
) -> Lumberjack_V1_PullRequestState {
    var p = Lumberjack_V1_PullRequestState()
    p.score = score
    if let updated {
        var ts = Google_Protobuf_Timestamp()
        ts.seconds = updated
        p.updatedAt = ts
    }
    p.checks.failing = failing
    p.mergeStateStatus = merge
    p.reviewDecision = decision
    p.unresolvedThreadCount = unresolved
    p.additions = additions
    if approvedByMe {
        var r = Lumberjack_V1_Review()
        r.isMe = true
        r.state = .approved
        p.latestReviews = [r]
    }
    return p
}

private func row(
    _ repo: String, _ branch: String, _ authorship: Authorship = .mine, _ p: Lumberjack_V1_PullRequestState? = nil
) -> UnifiedRow {
    UnifiedRow(
        repository: stubRepository(name: repo, path: "/tmp/\(repo)"),
        worktree: stubWorktree(branch: branch, path: "/tmp/\(repo)/\(branch)", authorship: authorship, pullRequest: p)
    )
}

private func isolatedDefaults() -> UserDefaults {
    let name = "lumberjack.tests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: name)!
    defaults.removePersistentDomain(forName: name)
    return defaults
}

struct UnifiedListTests {
    @Test("merging ranks across repositories: mine by need, incoming smallest first, ties newest first")
    func mergeRanksAcrossRepositories() {
        let a = stubRepository(name: "a", path: "/a")
        let b = stubRepository(name: "b", path: "/b")
        let rows = UnifiedList.merge(
            repositories: [a, b],
            worktrees: [
                "/a": [
                    stubWorktree(branch: "a-big", path: "/a/1", authorship: .theirs, pullRequest: pr(score: 900)),
                    stubWorktree(branch: "a-mine-low", path: "/a/2", authorship: .mine, pullRequest: pr(score: 5)),
                    stubWorktree(branch: "a-plain", path: "/a/3"),
                ],
                "/b": [
                    stubWorktree(branch: "b-small", path: "/b/1", authorship: .theirs, pullRequest: pr(score: 10)),
                    stubWorktree(
                        branch: "b-mine-old", path: "/b/2", authorship: .mine, pullRequest: pr(score: 40, updated: 1)),
                    stubWorktree(
                        branch: "b-mine-new", path: "/b/3", authorship: .mine, pullRequest: pr(score: 40, updated: 9)),
                ],
            ]
        )
        #expect(
            rows.map(\.worktree.branchName) == [
                "b-mine-new", "b-mine-old", "a-mine-low", "b-small", "a-big", "a-plain",
            ])
        #expect(rows.first?.repository.githubName == "b")
    }

    @Test("rows the daemon does not rank keep repository then daemon order")
    func unrankedRowsAreStable() {
        let rows = UnifiedList.merge(
            repositories: [stubRepository(name: "a", path: "/a"), stubRepository(name: "b", path: "/b")],
            worktrees: [
                "/a": [stubWorktree(branch: "z", path: "/a/z"), stubWorktree(branch: "y", path: "/a/y")],
                "/b": [stubWorktree(branch: "x", path: "/b/x")],
                "/gone": [stubWorktree(branch: "w", path: "/gone/w")],
            ]
        )
        #expect(rows.map(\.worktree.branchName) == ["z", "y", "x"])
    }

    @Test("tabs split the merged list unless the daemon does not classify")
    func tabs() {
        let split = [row("a", "m", .mine), row("b", "t", .theirs)]
        #expect(UnifiedList.isSplit(split))
        #expect(UnifiedList.rows(in: .theirs, of: split).map(\.worktree.branchName) == ["t"])
        let old = [row("a", "x", .unspecified)]
        #expect(UnifiedList.rows(in: .theirs, of: old).count == 1)
    }

    @Test("status filters test the PR's state, and match nothing without a PR")
    func statusFilters() {
        let failing = row("a", "f", .mine, pr(failing: 1)).worktree
        let blocked = row("a", "b", .mine, pr(failing: 1, merge: .dirty)).worktree
        let changes = row("a", "c", .mine, pr(decision: .changesRequested)).worktree
        let comments = row("a", "u", .mine, pr(unresolved: 2)).worktree
        let approved = row("a", "a", .mine, pr(decision: .approved)).worktree
        let review = row("a", "r", .theirs, pr()).worktree
        let mineApproved = row("a", "x", .theirs, pr(approvedByMe: true)).worktree

        #expect(StatusFilter.failingChecks.matches(failing))
        #expect(!StatusFilter.blocked.matches(failing))
        #expect(StatusFilter.blocked.matches(blocked))
        #expect(StatusFilter.conflicts.matches(blocked))
        #expect(StatusFilter.changesRequested.matches(changes))
        #expect(StatusFilter.comments.matches(comments))
        #expect(StatusFilter.approved.matches(approved))
        #expect(StatusFilter.approved.matches(mineApproved))
        #expect(!StatusFilter.approved.matches(review))
        #expect(StatusFilter.needsReview.matches(review))
        #expect(!StatusFilter.needsReview.matches(mineApproved))
        #expect(!StatusFilter.failingChecks.matches(row("a", "n").worktree))

        #expect(StatusFilter.allCases.allSatisfy { !$0.label.isEmpty })
        #expect(!StatusFilter.available(in: .mine).contains(.needsReview))
        #expect(StatusFilter.available(in: .theirs).contains(.needsReview))
    }

    @Test("facets combine with AND, values within a facet with OR, and counts ignore their own facet")
    func filterMatchingAndCounts() {
        let rows = [
            row("a", "small-failing", .mine, pr(failing: 1, additions: 10)),
            row("a", "big", .mine, pr(additions: 3000)),
            row("b", "small-comments", .mine, pr(unresolved: 1, additions: 20)),
            row("b", "no-pr"),
        ]
        var filter = PullRequestFilter()
        #expect(!filter.isActive)
        #expect(filter.apply(rows).count == 4)

        filter.toggle(size: "XS")
        filter.toggle(status: .failingChecks)
        filter.toggle(status: .comments)
        #expect(filter.activeCount == 3)
        #expect(filter.apply(rows).map(\.worktree.branchName) == ["small-failing", "small-comments"])

        filter.toggle(repository: "/tmp/b")
        #expect(filter.apply(rows).map(\.worktree.branchName) == ["small-comments"])
        #expect(filter.count(rows, repository: "/tmp/a") == 1)
        #expect(filter.count(rows, size: "XS") == 1)
        #expect(filter.count(rows, size: "XL") == 0)
        #expect(filter.count(rows, status: .failingChecks) == 0)
        #expect(filter.count(rows, status: .comments) == 1)

        filter.toggle(size: "XS")
        #expect(!filter.sizes.contains("XS"))
    }

    @Test("the summary names one repository, counts several, and lists sizes and statuses in display order")
    func summary() {
        let repos = [stubRepository(name: "alpha", path: "/a"), stubRepository(name: "beta", path: "/b")]
        var filter = PullRequestFilter()
        #expect(filter.summary(repositories: repos) == "")
        filter.repositories = ["/a"]
        filter.sizes = ["XL", "S"]
        filter.statuses = [.comments, .failingChecks]
        #expect(filter.summary(repositories: repos) == "alpha · S/XL · Failing checks, Unresolved comments")
        filter.repositories = ["/a", "/b"]
        #expect(filter.summary(repositories: repos) == "2 repos · S/XL · Failing checks, Unresolved comments")
    }

    @Test("a filter forgets repositories that are no longer tracked")
    func keepingRepositories() {
        var filter = PullRequestFilter()
        filter.repositories = ["/a", "/gone"]
        #expect(filter.keeping(repositories: ["/a", "/b"]).repositories == ["/a"])
    }

    @Test("each tab's filter round-trips through defaults under its own key")
    func filterStoreRoundTrips() {
        let defaults = isolatedDefaults()
        #expect(FilterStore.load(.mine, from: defaults) == PullRequestFilter())

        var mine = PullRequestFilter()
        mine.statuses = [.blocked]
        var incoming = PullRequestFilter()
        incoming.sizes = ["XS"]
        FilterStore.save(mine, for: .mine, to: defaults)
        FilterStore.save(incoming, for: .theirs, to: defaults)

        #expect(FilterStore.load(.mine, from: defaults) == mine)
        #expect(FilterStore.load(.theirs, from: defaults) == incoming)

        defaults.set(Data("nonsense".utf8), forKey: FilterStore.key(for: .mine))
        #expect(FilterStore.load(.mine, from: defaults) == PullRequestFilter())
    }
}

@MainActor
struct AppStateFilterTests {
    @Test("each tab keeps its own filter, and both survive a restart")
    func filtersArePerTabAndPersist() {
        let defaults = isolatedDefaults()
        let state = AppState(defaults: defaults, connect: { StubDaemon() })
        state.filter.toggle(status: .blocked)
        state.selectedTab = .theirs
        #expect(state.filter == PullRequestFilter())
        state.filter.toggle(size: "XS")

        let restarted = AppState(defaults: defaults, connect: { StubDaemon() })
        #expect(restarted.filter.statuses == [.blocked])
        restarted.selectedTab = .theirs
        #expect(restarted.filter.sizes == ["XS"])
    }

    @Test("a refresh drops untracked repositories from saved filters, but an empty listing does not")
    func refreshPrunesRepositories() async {
        let defaults = isolatedDefaults()
        var saved = PullRequestFilter()
        saved.repositories = ["/tmp/repo", "/tmp/gone"]
        FilterStore.save(saved, for: .theirs, to: defaults)

        let empty = AppState(defaults: defaults, connect: { StubDaemon() })
        await empty.refreshOnce()
        #expect(FilterStore.load(.theirs, from: defaults).repositories == ["/tmp/repo", "/tmp/gone"])

        let daemon = StubDaemon()
        daemon.repositories = [stubRepository(name: "repo", path: "/tmp/repo")]
        let state = AppState(defaults: defaults, connect: { daemon })
        await state.refreshOnce()

        #expect(state.filters[.theirs]?.repositories == ["/tmp/repo"])
        #expect(FilterStore.load(.theirs, from: defaults).repositories == ["/tmp/repo"])
    }
}
