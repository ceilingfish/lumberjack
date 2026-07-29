import Foundation

/// The matching rule behind the worktrees list's search field (issue #26).
///
/// Deliberately a plain, case-insensitive substring test over two fields
/// rather than fuzzy or prefix matching: the request specified substring
/// matching as sufficient, and one uniform rule is the least surprising to
/// explain. The consequence is accepted, not accidental — `12` matches PR
/// `#123` and `#412` as well as `#12`, and a branch named `fix-120`.
///
/// Kept free of any SwiftUI so it can be unit-tested without standing up a
/// view.
enum WorktreeSearch {
    /// Trims surrounding whitespace and drops a single leading `#`, so `#123`
    /// and `123` behave identically — rows render the PR as `#123`, so that is
    /// what users type.
    static func normalize(_ query: String) -> String {
        var q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        if q.hasPrefix("#") { q.removeFirst() }
        return q
    }

    /// True when the normalized `query` is a case-insensitive substring of
    /// `worktree`'s branch name, or of the decimal digits of its PR number.
    ///
    /// An empty (or whitespace-only) query matches everything — it is not a
    /// filter at all. A worktree with no PR number, which is the case for an
    /// orphan that outlived its PR, simply cannot match on that field; it
    /// remains matchable by branch name.
    static func matches(_ worktree: Lumberjack_V1_Worktree, query: String) -> Bool {
        matches(worktree, normalized: normalize(query))
    }

    /// The worktrees matching `query`, in the order given.
    static func filter(
        _ worktrees: [Lumberjack_V1_Worktree],
        query: String
    ) -> [Lumberjack_V1_Worktree] {
        let q = normalize(query)
        guard !q.isEmpty else { return worktrees }
        return worktrees.filter { matches($0, normalized: q) }
    }

    /// Takes an already-normalized query so a `filter` over many worktrees
    /// normalizes once, and so normalization can never be applied twice (which
    /// would strip two `#` from `##12`).
    private static func matches(_ worktree: Lumberjack_V1_Worktree, normalized q: String) -> Bool {
        guard !q.isEmpty else { return true }
        if worktree.branchName.range(of: q, options: .caseInsensitive) != nil { return true }
        return worktree.hasGithubPrNumber && String(worktree.githubPrNumber).contains(q)
    }
}
