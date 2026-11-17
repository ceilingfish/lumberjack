import Foundation

typealias Worktree = Lumberjack_V1_Worktree
typealias Authorship = Lumberjack_V1_Authorship

enum RowBadge: Equatable {
    case none
    case problem(String)
    case blocked(count: Int, summary: String)
    case approved(tip: String)
    case review
}

enum RowDot: Equatable {
    case ok
    case new
    case draft
    case blocked
    case attention
}

enum PullRequestPresentation {
    static func isSplit(_ worktrees: [Worktree]) -> Bool {
        worktrees.contains { $0.authorship != .unspecified }
    }

    static func worktrees(in tab: Authorship, of worktrees: [Worktree]) -> [Worktree] {
        guard isSplit(worktrees) else { return worktrees }
        return worktrees.filter { $0.authorship == tab }
    }

    static func mineBadge(_ worktrees: [Worktree]) -> Int {
        worktrees.filter { $0.authorship == .mine && $0.hasPullRequest && $0.pullRequest.score > 0 }.count
    }

    static func incomingUnread(_ worktrees: [Worktree]) -> Bool {
        worktrees.contains { $0.authorship == .theirs && $0.pullRequest.hasNewActivity_p }
    }

    static func problems(_ pr: Lumberjack_V1_PullRequestState) -> [String] {
        var out: [String] = []
        let checks = pr.checks
        if checks.failing > 0 {
            let total = checks.passing + checks.failing + checks.pending
            out.append("\(checks.failing) of \(total) checks failing")
        }
        if pr.mergeStateStatus == .dirty { out.append("Merge conflicts") }
        if pr.reviewDecision == .changesRequested { out.append("Changes requested") }
        if pr.unresolvedThreadCount > 0 {
            let n = pr.unresolvedThreadCount
            out.append("\(n) unresolved comment\(n == 1 ? "" : "s")")
        }
        return out
    }

    static func approvedByMe(_ pr: Lumberjack_V1_PullRequestState) -> Bool {
        pr.latestReviews.contains { $0.isMe && $0.state == .approved }
    }

    static func approver(_ pr: Lumberjack_V1_PullRequestState) -> String? {
        pr.latestReviews.first { !$0.isMe && $0.state == .approved }?.login
    }

    static func badge(_ worktree: Worktree) -> RowBadge {
        guard worktree.hasPullRequest else { return .none }
        let pr = worktree.pullRequest
        let problems = problems(pr)
        if problems.count == 1 { return .problem(problems[0]) }
        if problems.count > 1 { return .blocked(count: problems.count, summary: problems.joined(separator: ", ")) }
        if worktree.authorship == .theirs {
            return approvedByMe(pr) ? .approved(tip: "You approved this") : .review
        }
        if pr.reviewDecision == .approved {
            return .approved(tip: "Approved by \(approver(pr) ?? "a reviewer")")
        }
        return .none
    }

    static func dot(_ worktree: Worktree) -> RowDot {
        let pr = worktree.pullRequest
        let blocked = pr.checks.failing > 0 || pr.mergeStateStatus == .dirty || pr.reviewDecision == .changesRequested
        if worktree.authorship == .theirs {
            return blocked || pr.unresolvedThreadCount > 0 ? .attention : .ok
        }
        if blocked { return .blocked }
        if pr.hasNewActivity_p { return .new }
        if pr.isDraft { return .draft }
        return .ok
    }

    static func lines(_ pr: Lumberjack_V1_PullRequestState) -> Int {
        Int(pr.additions) + Int(pr.deletions)
    }

    static func sizeLabel(lines: Int) -> String {
        switch lines {
        case ..<50: return "XS"
        case ..<250: return "S"
        case ..<1000: return "M"
        case ..<2500: return "L"
        default: return "XL"
        }
    }

    static func linesText(_ pr: Lumberjack_V1_PullRequestState) -> String {
        "+\(grouped(Int(pr.additions))) −\(grouped(Int(pr.deletions)))"
    }

    static func sizeTitle(_ pr: Lumberjack_V1_PullRequestState) -> String {
        "\(grouped(lines(pr))) lines changed · ranks as \(grouped(Int(pr.score)))"
    }

    static func subtitle(_ worktree: Worktree) -> String? {
        if worktree.needsReconciliation || !worktree.hasPullRequest {
            return MenuBarPresentation.rowSubtitle(worktree)
        }
        let pr = worktree.pullRequest
        if worktree.authorship == .theirs {
            let author = pr.authorLogin.isEmpty ? "Someone" : pr.authorLogin
            if pr.reviewRequestedFromMe { return "\(author) · requested your review" }
            if pr.reviewDecision == .changesRequested { return "\(author) · changes requested" }
            if approvedByMe(pr) { return "\(author) · you approved" }
            return "\(author) · opened"
        }
        if pr.reviewDecision == .approved, let approver = approver(pr) {
            return "\(approver) approved"
        }
        return MenuBarPresentation.rowSubtitle(worktree)
    }

    static func updated(_ worktree: Worktree, now: Date = Date()) -> String? {
        guard worktree.hasPullRequest, worktree.pullRequest.hasUpdatedAt else { return nil }
        let seconds = max(0, now.timeIntervalSince(MenuBarPresentation.date(from: worktree.pullRequest.updatedAt)))
        switch seconds {
        case ..<60: return "now"
        case ..<3600: return "\(Int(seconds / 60))m"
        case ..<86400: return "\(Int(seconds / 3600))h"
        default: return "\(Int(seconds / 86400))d"
        }
    }

    private static func grouped(_ n: Int) -> String {
        n.formatted(.number.grouping(.automatic).locale(Locale(identifier: "en_GB")))
    }
}
