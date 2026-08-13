import Testing

@testable import LumberjackMenuBar

@MainActor
struct NotifierTests {
    @Test("clone and delete post distinct titles naming the repository and branch")
    func createdAndDeletedPostThroughTheSeam() {
        var posted: [(String, String)] = []
        Notifier.postOverride = { title, body in posted.append((title, body)) }
        defer { Notifier.postOverride = nil }

        Notifier.worktreeCreated(repository: "repo", branch: "feature-x")
        Notifier.worktreeDeleted(repository: "repo", branch: "feature-y")

        #expect(posted.map(\.0) == ["Worktree cloned", "Worktree deleted"])
        #expect(posted.map(\.1) == ["repo: feature-x", "repo: feature-y"])
    }

    @Test("a request carries the title and body, under an identifier of its own")
    func requestCarriesTheTitleAndBody() {
        let request = Notifier.request(title: "Worktree cloned", body: "repo: feature-x")

        #expect(request.content.title == "Worktree cloned")
        #expect(request.content.body == "repo: feature-x")
        #expect(!request.identifier.isEmpty)
        #expect(request.identifier != Notifier.request(title: "Worktree cloned", body: "repo: feature-x").identifier)
    }

    @Test("authorization is skipped while the test seam is installed")
    func authorizationIsSkippedUnderTheSeam() {
        Notifier.postOverride = { _, _ in }
        defer { Notifier.postOverride = nil }

        Notifier.requestAuthorization()
    }
}
