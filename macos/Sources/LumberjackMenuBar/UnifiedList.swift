import Foundation

struct UnifiedRow: Identifiable, Equatable {
    let repository: Lumberjack_V1_Repository
    let worktree: Worktree

    var id: String { worktree.directoryPath }
}

enum StatusFilter: String, CaseIterable, Hashable, Codable {
    case blocked
    case failingChecks
    case conflicts
    case changesRequested
    case comments
    case approved
    case needsReview

    var label: String {
        switch self {
        case .blocked: return "Blocked"
        case .failingChecks: return "Failing checks"
        case .conflicts: return "Merge conflicts"
        case .changesRequested: return "Changes requested"
        case .comments: return "Unresolved comments"
        case .approved: return "Approved"
        case .needsReview: return "Needs review"
        }
    }

    static func available(in tab: Authorship) -> [StatusFilter] {
        tab == .mine ? allCases.filter { $0 != .needsReview } : allCases
    }

    func matches(_ worktree: Worktree) -> Bool {
        guard worktree.hasPullRequest else { return false }
        let pr = worktree.pullRequest
        switch self {
        case .blocked: return PullRequestPresentation.problems(pr).count > 1
        case .failingChecks: return pr.checks.failing > 0
        case .conflicts: return pr.mergeStateStatus == .dirty
        case .changesRequested: return pr.reviewDecision == .changesRequested
        case .comments: return pr.unresolvedThreadCount > 0
        case .approved:
            if case .approved = PullRequestPresentation.badge(worktree) { return true }
            return false
        case .needsReview: return PullRequestPresentation.badge(worktree) == .review
        }
    }
}

struct PullRequestFilter: Equatable, Codable {
    enum Facet {
        case repository
        case size
        case status
    }

    static let sizes = ["XS", "S", "M", "L", "XL"]

    var repositories: Set<String> = []
    var sizes: Set<String> = []
    var statuses: Set<StatusFilter> = []

    var activeCount: Int { repositories.count + sizes.count + statuses.count }
    var isActive: Bool { activeCount > 0 }

    static func size(of worktree: Worktree) -> String? {
        guard worktree.hasPullRequest else { return nil }
        return PullRequestPresentation.sizeLabel(lines: PullRequestPresentation.lines(worktree.pullRequest))
    }

    func matches(_ row: UnifiedRow, ignoring facet: Facet? = nil) -> Bool {
        if facet != .repository, !repositories.isEmpty, !repositories.contains(row.repository.localPath) {
            return false
        }
        if facet != .size, !sizes.isEmpty {
            guard let size = Self.size(of: row.worktree), sizes.contains(size) else { return false }
        }
        if facet != .status, !statuses.isEmpty, !statuses.contains(where: { $0.matches(row.worktree) }) {
            return false
        }
        return true
    }

    func apply(_ rows: [UnifiedRow]) -> [UnifiedRow] {
        rows.filter { matches($0) }
    }

    func count(_ rows: [UnifiedRow], repository: String) -> Int {
        rows.filter { $0.repository.localPath == repository && matches($0, ignoring: .repository) }.count
    }

    func count(_ rows: [UnifiedRow], size: String) -> Int {
        rows.filter { Self.size(of: $0.worktree) == size && matches($0, ignoring: .size) }.count
    }

    func count(_ rows: [UnifiedRow], status: StatusFilter) -> Int {
        rows.filter { status.matches($0.worktree) && matches($0, ignoring: .status) }.count
    }

    mutating func toggle(repository: String) { repositories.formSymmetricDifference([repository]) }
    mutating func toggle(size: String) { sizes.formSymmetricDifference([size]) }
    mutating func toggle(status: StatusFilter) { statuses.formSymmetricDifference([status]) }

    func keeping(repositories tracked: [String]) -> PullRequestFilter {
        var out = self
        out.repositories.formIntersection(tracked)
        return out
    }

    func summary(repositories all: [Lumberjack_V1_Repository]) -> String {
        var parts: [String] = []
        if repositories.count == 1, let name = all.first(where: { repositories.contains($0.localPath) })?.githubName {
            parts.append(name)
        } else if !repositories.isEmpty {
            parts.append("\(repositories.count) repos")
        }
        if !sizes.isEmpty {
            parts.append(Self.sizes.filter(sizes.contains).joined(separator: "/"))
        }
        if !statuses.isEmpty {
            parts.append(StatusFilter.allCases.filter(statuses.contains).map(\.label).joined(separator: ", "))
        }
        return parts.joined(separator: " · ")
    }
}

enum FilterStore {
    static func key(for tab: Authorship) -> String {
        tab == .theirs ? "filter.incoming" : "filter.mine"
    }

    static func load(_ tab: Authorship, from defaults: UserDefaults) -> PullRequestFilter {
        guard let data = defaults.data(forKey: key(for: tab)),
            let filter = try? JSONDecoder().decode(PullRequestFilter.self, from: data)
        else { return PullRequestFilter() }
        return filter
    }

    static func save(_ filter: PullRequestFilter, for tab: Authorship, to defaults: UserDefaults) {
        guard let data = try? JSONEncoder().encode(filter) else { return }
        defaults.set(data, forKey: key(for: tab))
    }
}

enum UnifiedList {
    static func merge(
        repositories: [Lumberjack_V1_Repository], worktrees: [String: [Worktree]]
    ) -> [UnifiedRow] {
        let rows = repositories.flatMap { repo in
            (worktrees[repo.localPath] ?? []).map { UnifiedRow(repository: repo, worktree: $0) }
        }
        return rows.enumerated()
            .sorted { a, b in
                let order = compare(a.element.worktree, b.element.worktree)
                return order == 0 ? a.offset < b.offset : order < 0
            }
            .map(\.element)
    }

    static func compare(_ a: Worktree, _ b: Worktree) -> Int {
        if a.authorship != b.authorship {
            if a.authorship == .mine { return -1 }
            if b.authorship == .mine { return 1 }
            return a.authorship == .theirs ? -1 : 1
        }
        let (sa, sb) = (a.pullRequest.score, b.pullRequest.score)
        if sa != sb {
            let higherFirst = a.authorship != .theirs
            return (sa > sb) == higherFirst ? -1 : 1
        }
        let (ua, ub) = (updatedAt(a), updatedAt(b))
        if ua != ub { return ua > ub ? -1 : 1 }
        return 0
    }

    static func rows(in tab: Authorship, of rows: [UnifiedRow]) -> [UnifiedRow] {
        guard isSplit(rows) else { return rows }
        return rows.filter { $0.worktree.authorship == tab }
    }

    static func isSplit(_ rows: [UnifiedRow]) -> Bool {
        PullRequestPresentation.isSplit(rows.map(\.worktree))
    }

    private static func updatedAt(_ worktree: Worktree) -> Date {
        guard worktree.hasPullRequest, worktree.pullRequest.hasUpdatedAt else { return .distantPast }
        return MenuBarPresentation.date(from: worktree.pullRequest.updatedAt)
    }
}
