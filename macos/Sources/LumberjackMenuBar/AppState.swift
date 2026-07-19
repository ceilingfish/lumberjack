import Foundation
import GRPC

/// Daemon reachability, as surfaced to the UI. Mirrors the CLI's handling of
/// `ErrDaemonNotRunning` (see pkg/client): the app degrades to a clear state
/// rather than crashing or showing stale data as if it were live.
enum ConnectionState: Equatable {
    case connecting
    case connected
    case daemonNotRunning
}

/// Drives the menu bar UI: connects to the daemon, keeps the repository list
/// and the selected repository's worktrees up to date, and derives
/// clone/delete notifications from what changed between refreshes.
///
/// This polls on an interval rather than consuming a server-streamed feed.
/// Issue #13 (depended on by #9) adds a `Watch` RPC that streams repository
/// and worktree events from the daemon as they happen; once it lands, this
/// class's refresh loop should be replaced by consuming that stream directly
/// instead of re-polling `ListRepositories`/`ListWorktrees` on a timer. The
/// public surface (`connectionState`, `repositories`, `worktrees`,
/// `selectedRepository`) is written so that swap only touches `refreshLoop()`.
@MainActor
final class AppState: ObservableObject {
    @Published private(set) var connectionState: ConnectionState = .connecting
    @Published private(set) var repositories: [Lumberjack_V1_Repository] = []
    @Published private(set) var worktrees: [Lumberjack_V1_Worktree] = []
    @Published var selectedRepository: String? {
        didSet {
            guard selectedRepository != oldValue else { return }
            worktrees = []
        }
    }

    private let pollInterval: UInt64
    private var client: DaemonClient?
    /// Last-seen worktrees per repository, keyed by directory path, used to
    /// detect clone/delete since the previous refresh. Storing the branch
    /// name (not just the path) means a deleted worktree's branch is still
    /// known once it disappears from the current listing.
    private var knownWorktrees: [String: [String: String]] = [:]
    private var loopTask: Task<Void, Never>?

    init(pollIntervalSeconds: UInt64 = 2) {
        self.pollInterval = pollIntervalSeconds
    }

    func start() {
        guard loopTask == nil else { return }
        Notifier.requestAuthorization()
        loopTask = Task { await refreshLoop() }
    }

    func stop() {
        loopTask?.cancel()
        loopTask = nil
        client?.close()
        client = nil
    }

    private func refreshLoop() async {
        while !Task.isCancelled {
            await refreshOnce()
            try? await Task.sleep(nanoseconds: pollInterval * 1_000_000_000)
        }
    }

    private func refreshOnce() async {
        if client == nil {
            client = try? DaemonClient(socketPath: SocketPath.resolve())
        }
        guard let client else {
            connectionState = .daemonNotRunning
            return
        }

        do {
            _ = try await client.health()
        } catch {
            connectionState = .daemonNotRunning
            // Drop the connection so the next tick dials fresh once the
            // daemon comes back, rather than reusing a channel wedged
            // against a process that no longer exists.
            client.close()
            self.client = nil
            return
        }

        connectionState = .connected

        do {
            let repos = try await client.listRepositories()
            repositories = repos.sorted { $0.githubName < $1.githubName }
            if selectedRepository == nil {
                selectedRepository = repos.first?.localPath
            }

            pruneKnownWorktrees(tracking: repos.map(\.localPath))

            for repo in repos {
                let current = try await client.listWorktrees(repository: repo.localPath)
                diffAndNotify(repository: repo.githubName, worktrees: current, key: repo.localPath)
                if repo.localPath == selectedRepository {
                    worktrees = current.sorted { $0.branchName < $1.branchName }
                }
            }
        } catch {
            // A transient RPC failure between two successful health checks;
            // leave the last-known-good data on screen and retry next tick.
        }
    }

    /// Drops cached diff state (`knownWorktrees`) for any repository no
    /// longer in `paths`, so the dictionary doesn't grow unboundedly as
    /// repositories are added and removed over the app's lifetime.
    func pruneKnownWorktrees(tracking paths: [String]) {
        let trackedPaths = Set(paths)
        knownWorktrees = knownWorktrees.filter { trackedPaths.contains($0.key) }
    }

    /// Test-only accessor for `knownWorktrees`'s keys.
    var knownWorktreeKeys: Set<String> { Set(knownWorktrees.keys) }

    func diffAndNotify(repository: String, worktrees current: [Lumberjack_V1_Worktree], key: String) {
        let currentByPath = Dictionary(uniqueKeysWithValues: current.map { ($0.directoryPath, $0.branchName) })
        defer { knownWorktrees[key] = currentByPath }

        guard let previousByPath = knownWorktrees[key] else {
            // First observation of this repository: nothing to diff against,
            // so establish the baseline without notifying for pre-existing
            // worktrees.
            return
        }

        let currentPaths = Set(currentByPath.keys)
        let previousPaths = Set(previousByPath.keys)
        for created in currentPaths.subtracting(previousPaths) {
            Notifier.worktreeCreated(repository: repository, branch: currentByPath[created] ?? created)
        }
        for deleted in previousPaths.subtracting(currentPaths) {
            Notifier.worktreeDeleted(repository: repository, branch: previousByPath[deleted] ?? deleted)
        }
    }
}
