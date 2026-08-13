import Foundation
import SwiftProtobuf

enum WorktreeStatus {
    case inSync
    case attention
    case disparity
    case orphaned
    case syncing
}

enum MenuBarPresentation {
    static func selectedRepositoryName(_ repository: Lumberjack_V1_Repository?) -> String {
        repository?.githubName ?? "Select repository"
    }

    static func statusMessage(_ connection: ConnectionState) -> (text: String, symbol: String) {
        switch connection {
        case .connecting: return ("Connecting to daemon…", "arrow.triangle.2.circlepath")
        case .daemonNotRunning: return ("Daemon not running", "exclamationmark.triangle")
        case .connected: return ("No repositories tracked", "tray")
        }
    }

    static func pillLabel(_ connection: ConnectionState) -> String {
        switch connection {
        case .connected: return "Connected"
        case .connecting: return "Connecting"
        case .daemonNotRunning: return "Offline"
        }
    }

    static func headerSubtitle(_ connection: ConnectionState, repositories: [Lumberjack_V1_Repository]) -> String {
        guard connection == .connected else { return statusMessage(connection).text }
        let n = repositories.count
        var subtitle = "Watching \(n) \(n == 1 ? "repository" : "repositories")"
        if let synced = latestSync(of: repositories) {
            subtitle += " · synced \(relative(synced))"
        }
        return subtitle
    }

    static func latestSync(of repositories: [Lumberjack_V1_Repository]) -> Date? {
        repositories.compactMap { $0.hasLastSyncedAt ? date(from: $0.lastSyncedAt) : nil }.max()
    }

    static func worktreeSummary(_ worktrees: [Lumberjack_V1_Worktree]) -> String {
        var counts: [WorktreeStatus: Int] = [:]
        for worktree in worktrees {
            counts[status(of: worktree, isSyncing: false), default: 0] += 1
        }
        let parts: [String] = [
            counts[.inSync].map { "\($0) in sync" },
            counts[.attention].map { "\($0) needs attention" },
            counts[.disparity].map { "\($0) branch disparity" },
            counts[.orphaned].map { "\($0) orphaned" },
        ].compactMap { $0 }
        return parts.joined(separator: " · ")
    }

    static func status(of worktree: Lumberjack_V1_Worktree, isSyncing: Bool) -> WorktreeStatus {
        if isSyncing { return .syncing }
        if worktree.branchDisparity { return .disparity }
        if worktree.needsReconciliation { return .attention }
        if worktree.orphaned { return .orphaned }
        return .inSync
    }

    static func rowSubtitle(_ worktree: Lumberjack_V1_Worktree) -> String? {
        if worktree.needsReconciliation {
            if !worktree.reconciliationNote.isEmpty { return worktree.reconciliationNote }
            if worktree.branchDisparity {
                return "On \(worktree.checkedOutBranch), not the PR branch \(worktree.branchName)"
            }
            return "Needs reconciliation"
        }
        guard worktree.hasLastSyncedAt else { return nil }
        return "Synced \(relative(date(from: worktree.lastSyncedAt)))"
    }

    static func prURL(repository: Lumberjack_V1_Repository?, worktree: Lumberjack_V1_Worktree) -> URL? {
        guard worktree.hasGithubPrNumber, let repository else { return nil }
        let host = repository.host.isEmpty ? "github.com" : repository.host
        return URL(
            string: "https://\(host)/\(repository.githubOwner)/\(repository.githubName)/pull/\(worktree.githubPrNumber)"
        )
    }

    static func date(from ts: Google_Protobuf_Timestamp) -> Date {
        Date(timeIntervalSince1970: TimeInterval(ts.seconds) + TimeInterval(ts.nanos) / 1e9)
    }

    static func relative(_ date: Date) -> String {
        date.formatted(.relative(presentation: .named))
    }
}
