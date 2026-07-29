import Testing
@testable import LumberjackMenuBar

struct WorktreeSearchTests {
    private func worktree(branch: String, pr: Int64? = nil) -> Lumberjack_V1_Worktree {
        var wt = Lumberjack_V1_Worktree()
        wt.branchName = branch
        wt.directoryPath = "/tmp/repo/\(branch)"
        if let pr { wt.githubPrNumber = pr }
        return wt
    }

    // MARK: Branch matching

    @Test
    func matchesBranchSubstring() {
        let wt = worktree(branch: "feature/issue-26-menu-bar-search")
        #expect(WorktreeSearch.matches(wt, query: "menu-bar"))
        #expect(WorktreeSearch.matches(wt, query: "feature"))
        #expect(!WorktreeSearch.matches(wt, query: "nothing-like-this"))
    }

    @Test
    func branchMatchingIgnoresCase() {
        let wt = worktree(branch: "Feature/Issue-26-Menu-Bar")
        #expect(WorktreeSearch.matches(wt, query: "menu-bar"))
        #expect(WorktreeSearch.matches(wt, query: "MENU-BAR"))
        #expect(WorktreeSearch.matches(wt, query: "mEnU-bAr"))
    }

    // MARK: PR-number matching

    @Test
    func matchesPRNumber() {
        let wt = worktree(branch: "some-branch", pr: 123)
        #expect(WorktreeSearch.matches(wt, query: "123"))
        #expect(!WorktreeSearch.matches(wt, query: "999"))
    }

    @Test
    func hashPrefixIsStrippedSoItBehavesLikeThePlainNumber() {
        let wt = worktree(branch: "some-branch", pr: 123)
        #expect(WorktreeSearch.matches(wt, query: "#123"))
        #expect(WorktreeSearch.matches(wt, query: "123"))
        #expect(!WorktreeSearch.matches(wt, query: "#999"))
    }

    /// The accepted consequence of one uniform substring rule: a partial number
    /// matches any PR containing those digits, not just the PR it prefixes.
    @Test
    func partialNumberMatchesAnyPRContainingThoseDigits() {
        #expect(WorktreeSearch.matches(worktree(branch: "a", pr: 12), query: "12"))
        #expect(WorktreeSearch.matches(worktree(branch: "b", pr: 123), query: "12"))
        #expect(WorktreeSearch.matches(worktree(branch: "c", pr: 412), query: "12"))
        #expect(!WorktreeSearch.matches(worktree(branch: "d", pr: 99), query: "12"))
    }

    @Test
    func digitsAlsoMatchABranchNameContainingThem() {
        #expect(WorktreeSearch.matches(worktree(branch: "fix-120"), query: "12"))
    }

    // MARK: Worktrees without a PR

    @Test
    func worktreeWithoutPRCannotMatchDigitsButStillMatchesItsBranch() {
        let orphan = worktree(branch: "orphaned-work")
        #expect(!orphan.hasGithubPrNumber)
        #expect(!WorktreeSearch.matches(orphan, query: "26"))
        #expect(WorktreeSearch.matches(orphan, query: "orphan"))
    }

    // MARK: Empty queries

    @Test
    func emptyOrWhitespaceQueryMatchesEverything() {
        let wt = worktree(branch: "anything", pr: 7)
        #expect(WorktreeSearch.matches(wt, query: ""))
        #expect(WorktreeSearch.matches(wt, query: "   "))
        #expect(WorktreeSearch.matches(wt, query: "\n"))
    }

    @Test
    func queryIsTrimmedBeforeMatching() {
        let wt = worktree(branch: "feature/search", pr: 26)
        #expect(WorktreeSearch.matches(wt, query: "  search  "))
        #expect(WorktreeSearch.matches(wt, query: " #26 "))
    }

    /// Normalization must not be applied twice, or a second `#` would be eaten
    /// and `##12` would wrongly match PR 12.
    @Test
    func onlyOneLeadingHashIsStripped() {
        #expect(WorktreeSearch.normalize("##12") == "#12")
        #expect(!WorktreeSearch.matches(worktree(branch: "a", pr: 12), query: "##12"))
    }

    // MARK: Filtering a list

    @Test
    func filterPreservesTheGivenOrder() {
        let worktrees = [
            worktree(branch: "search-c", pr: 3),
            worktree(branch: "search-a", pr: 1),
            worktree(branch: "search-b", pr: 2),
        ]
        let filtered = WorktreeSearch.filter(worktrees, query: "search")
        #expect(filtered.map(\.branchName) == ["search-c", "search-a", "search-b"])
    }

    @Test
    func filterNarrowsToMatchesAndReturnsEverythingForAnEmptyQuery() {
        let worktrees = [
            worktree(branch: "main"),
            worktree(branch: "feature/search", pr: 26),
            worktree(branch: "feature/other", pr: 31),
        ]
        #expect(WorktreeSearch.filter(worktrees, query: "search").map(\.branchName) == ["feature/search"])
        #expect(WorktreeSearch.filter(worktrees, query: "#31").map(\.branchName) == ["feature/other"])
        #expect(WorktreeSearch.filter(worktrees, query: "").count == 3)
        #expect(WorktreeSearch.filter(worktrees, query: "zzz").isEmpty)
    }
}
