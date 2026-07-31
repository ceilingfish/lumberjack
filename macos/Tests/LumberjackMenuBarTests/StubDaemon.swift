@testable import LumberjackMenuBar

struct StubDaemonError: Error {}

final class StubDaemon: DaemonConnection, @unchecked Sendable {
    var repositories: [Lumberjack_V1_Repository] = []
    var worktreesByRepository: [String: [Lumberjack_V1_Worktree]] = [:]
    var healthError: (any Error)?
    var listRepositoriesError: (any Error)?
    var listWorktreesError: (any Error)?
    var syncedRepositories: [String] = []
    var closeCount = 0

    func health() async throws -> Lumberjack_V1_HealthResponse {
        if let healthError { throw healthError }
        return Lumberjack_V1_HealthResponse()
    }

    func listRepositories() async throws -> [Lumberjack_V1_Repository] {
        if let listRepositoriesError { throw listRepositoriesError }
        return repositories
    }

    func listWorktrees(repository: String) async throws -> [Lumberjack_V1_Worktree] {
        if let listWorktreesError { throw listWorktreesError }
        return worktreesByRepository[repository] ?? []
    }

    func sync(repository: String) async throws {
        syncedRepositories.append(repository)
    }

    func close() async {
        closeCount += 1
    }
}

func stubRepository(name: String, path: String, owner: String = "ceilingfish", host: String = "") -> Lumberjack_V1_Repository {
    var repo = Lumberjack_V1_Repository()
    repo.githubName = name
    repo.localPath = path
    repo.githubOwner = owner
    repo.host = host
    return repo
}

func stubWorktree(
    branch: String,
    path: String,
    needsReconciliation: Bool = false,
    reconciliationNote: String = "",
    orphaned: Bool = false,
    branchDisparity: Bool = false,
    checkedOutBranch: String = ""
) -> Lumberjack_V1_Worktree {
    var worktree = Lumberjack_V1_Worktree()
    worktree.branchName = branch
    worktree.directoryPath = path
    worktree.needsReconciliation = needsReconciliation
    worktree.reconciliationNote = reconciliationNote
    worktree.orphaned = orphaned
    worktree.branchDisparity = branchDisparity
    worktree.checkedOutBranch = checkedOutBranch
    return worktree
}
