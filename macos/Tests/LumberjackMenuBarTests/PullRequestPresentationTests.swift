import Foundation
import SwiftProtobuf
import Testing

@testable import LumberjackMenuBar

struct PullRequestPresentationTests {
    private func pr(
        score: Int64 = 0, new: Bool = false, draft: Bool = false,
        merge: Lumberjack_V1_MergeStateStatus = .clean, decision: Lumberjack_V1_ReviewDecision = .unspecified,
        failing: Int32 = 0, passing: Int32 = 0, pending: Int32 = 0, unresolved: Int32 = 0,
        reviews: [(String, Lumberjack_V1_ReviewState, Bool)] = [], requested: Bool = false,
        author: String = "dmitri", additions: Int32 = 0, deletions: Int32 = 0
    ) -> Lumberjack_V1_PullRequestState {
        var p = Lumberjack_V1_PullRequestState()
        p.score = score
        p.hasNewActivity_p = new
        p.isDraft = draft
        p.mergeStateStatus = merge
        p.reviewDecision = decision
        p.checks.failing = failing
        p.checks.passing = passing
        p.checks.pending = pending
        p.unresolvedThreadCount = unresolved
        p.latestReviews = reviews.map { login, state, isMe in
            var r = Lumberjack_V1_Review()
            r.login = login
            r.state = state
            r.isMe = isMe
            return r
        }
        p.reviewRequestedFromMe = requested
        p.authorLogin = author
        p.additions = additions
        p.deletions = deletions
        return p
    }

    private func mine(_ p: Lumberjack_V1_PullRequestState? = nil, needsReconciliation: Bool = false) -> Worktree {
        stubWorktree(branch: "m", path: "/m", needsReconciliation: needsReconciliation, authorship: .mine, pullRequest: p)
    }

    private func theirs(_ p: Lumberjack_V1_PullRequestState? = nil) -> Worktree {
        stubWorktree(branch: "t", path: "/t", authorship: .theirs, pullRequest: p)
    }

    @Test("worktrees split into tabs, or stay together from a daemon that does not classify")
    func tabs() {
        let all = [mine(), theirs()]
        #expect(PullRequestPresentation.isSplit(all))
        #expect(PullRequestPresentation.worktrees(in: .mine, of: all).map(\.branchName) == ["m"])
        #expect(PullRequestPresentation.worktrees(in: .theirs, of: all).map(\.branchName) == ["t"])

        let old = [stubWorktree(branch: "a", path: "/a")]
        #expect(!PullRequestPresentation.isSplit(old))
        #expect(PullRequestPresentation.worktrees(in: .theirs, of: old).map(\.branchName) == ["a"])
    }

    @Test("the Mine badge counts PRs that need the user, and Incoming shows unread activity")
    func tabIndicators() {
        let all = [mine(pr(score: 30)), mine(pr(score: 0)), mine(), theirs(pr(score: 900))]
        #expect(PullRequestPresentation.mineBadge(all) == 1)
        #expect(!PullRequestPresentation.incomingUnread(all))
        #expect(PullRequestPresentation.incomingUnread(all + [theirs(pr(new: true))]))
        #expect(!PullRequestPresentation.incomingUnread([mine(pr(new: true))]))
    }

    @Test("problems name each blocker")
    func problems() {
        #expect(PullRequestPresentation.problems(pr()).isEmpty)
        let all = pr(merge: .dirty, decision: .changesRequested, failing: 2, passing: 6, pending: 1, unresolved: 3)
        #expect(
            PullRequestPresentation.problems(all)
                == ["2 of 9 checks failing", "Merge conflicts", "Changes requested", "3 unresolved comments"])
        #expect(PullRequestPresentation.problems(pr(unresolved: 1)) == ["1 unresolved comment"])
    }

    @Test("the badge shows one problem, or blocked with a count, or approval and review chips")
    func badges() {
        #expect(PullRequestPresentation.badge(mine()) == .none)
        #expect(PullRequestPresentation.badge(mine(pr(merge: .dirty))) == .problem("Merge conflicts"))
        #expect(
            PullRequestPresentation.badge(mine(pr(merge: .dirty, unresolved: 1)))
                == .blocked(count: 2, summary: "Merge conflicts, 1 unresolved comment"))
        #expect(PullRequestPresentation.badge(mine(pr())) == .none)
        #expect(
            PullRequestPresentation.badge(
                mine(pr(decision: .approved, reviews: [("me", .approved, true), ("dmitri", .approved, false)])))
                == .approved(tip: "Approved by dmitri"))
        #expect(PullRequestPresentation.badge(mine(pr(decision: .approved))) == .approved(tip: "Approved by a reviewer"))
        #expect(PullRequestPresentation.badge(theirs(pr())) == .review)
        #expect(
            PullRequestPresentation.badge(theirs(pr(reviews: [("me", .approved, true)])))
                == .approved(tip: "You approved this"))
    }

    @Test("the dot reflects what the row needs")
    func dots() {
        #expect(PullRequestPresentation.dot(mine(pr())) == .ok)
        #expect(PullRequestPresentation.dot(mine(pr(failing: 1))) == .blocked)
        #expect(PullRequestPresentation.dot(mine(pr(decision: .changesRequested))) == .blocked)
        #expect(PullRequestPresentation.dot(mine(pr(new: true))) == .new)
        #expect(PullRequestPresentation.dot(mine(pr(draft: true))) == .draft)
        #expect(PullRequestPresentation.dot(theirs(pr())) == .ok)
        #expect(PullRequestPresentation.dot(theirs(pr(unresolved: 1))) == .attention)
        #expect(PullRequestPresentation.dot(theirs(pr(merge: .dirty))) == .attention)
    }

    @Test("size labels, line counts and rank text")
    func sizes() {
        let cases: [(Int, String)] = [
            (0, "XS"), (49, "XS"), (50, "S"), (249, "S"), (250, "M"), (999, "M"), (1000, "L"), (2499, "L"), (2500, "XL"),
        ]
        for (lines, label) in cases {
            #expect(PullRequestPresentation.sizeLabel(lines: lines) == label)
        }
        let p = pr(score: 1305, additions: 1240, deletions: 65)
        #expect(PullRequestPresentation.lines(p) == 1305)
        #expect(PullRequestPresentation.linesText(p) == "+1,240 −65")
        #expect(PullRequestPresentation.sizeTitle(p) == "1,305 lines changed · ranks as 1,305")
    }

    @Test("subtitles describe the PR from the user's side")
    func subtitles() {
        #expect(PullRequestPresentation.subtitle(theirs(pr(requested: true))) == "dmitri · requested your review")
        #expect(PullRequestPresentation.subtitle(theirs(pr(decision: .changesRequested))) == "dmitri · changes requested")
        #expect(PullRequestPresentation.subtitle(theirs(pr(reviews: [("me", .approved, true)]))) == "dmitri · you approved")
        #expect(PullRequestPresentation.subtitle(theirs(pr(author: ""))) == "Someone · opened")
        #expect(
            PullRequestPresentation.subtitle(mine(pr(decision: .approved, reviews: [("zoe", .approved, false)])))
                == "zoe approved")
        #expect(PullRequestPresentation.subtitle(mine(pr())) == nil)
        #expect(PullRequestPresentation.subtitle(mine()) == nil)
        #expect(PullRequestPresentation.subtitle(mine(pr(), needsReconciliation: true)) == "Needs reconciliation")
    }

    @Test("the age column is compact")
    func updated() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        func at(_ secondsAgo: Int64) -> Worktree {
            var p = pr()
            var ts = Google_Protobuf_Timestamp()
            ts.seconds = 1_000_000 - secondsAgo
            p.updatedAt = ts
            return mine(p)
        }
        #expect(PullRequestPresentation.updated(at(10), now: now) == "now")
        #expect(PullRequestPresentation.updated(at(12 * 60), now: now) == "12m")
        #expect(PullRequestPresentation.updated(at(3 * 3600), now: now) == "3h")
        #expect(PullRequestPresentation.updated(at(2 * 86400), now: now) == "2d")
        #expect(PullRequestPresentation.updated(mine(pr()), now: now) == nil)
        #expect(PullRequestPresentation.updated(mine(), now: now) == nil)
    }
}
