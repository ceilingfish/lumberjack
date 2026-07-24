import Foundation
import SwiftUI

/// The panel shown when the menu-bar icon is clicked: a repository switcher
/// plus a live view of the selected repository's worktrees, equivalent to
/// `lumberjack repositories NAME worktrees` on the CLI.
struct MenuBarView: View {
    @ObservedObject var state: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            switch state.connectionState {
            case .connecting:
                statusLine("Connecting to daemon…", systemImage: "arrow.triangle.2.circlepath")
            case .daemonNotRunning:
                statusLine("Daemon not running", systemImage: "exclamationmark.triangle")
            case .connected:
                connectedBody
            }
        }
        .padding(12)
        .frame(width: 420)
        .onAppear { state.start() }
    }

    @ViewBuilder
    private var connectedBody: some View {
        if state.repositories.isEmpty {
            statusLine("No repositories tracked", systemImage: "tray")
        } else {
            Picker("Repository", selection: Binding(
                get: { state.selectedRepository ?? "" },
                set: { state.selectedRepository = $0 }
            )) {
                ForEach(state.repositories, id: \.localPath) { repo in
                    Text(repo.githubName).tag(repo.localPath)
                }
            }
            .labelsHidden()

            Divider()

            if state.worktrees.isEmpty {
                statusLine("No worktrees", systemImage: "folder")
            } else {
                worktreeTable
            }
        }
    }

    private var worktreeTable: some View {
        VStack(alignment: .leading, spacing: 4) {
            ForEach(state.worktrees, id: \.directoryPath) { worktree in
                WorktreeRow(worktree: worktree)
            }
        }
    }

    private func statusLine(_ text: String, systemImage: String) -> some View {
        Label(text, systemImage: systemImage)
            .foregroundStyle(.secondary)
            .padding(.vertical, 8)
    }
}

/// One worktree's branch/PR/status/last-synced row, matching the CLI's
/// worktree listing columns.
private struct WorktreeRow: View {
    let worktree: Lumberjack_V1_Worktree

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(worktree.branchName).bold()
                if worktree.hasGithubPrNumber {
                    Text("#\(worktree.githubPrNumber)")
                        .foregroundStyle(.secondary)
                }
                Spacer()
                statusBadge
            }
            if !worktree.reconciliationNote.isEmpty {
                Text(worktree.reconciliationNote)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if worktree.hasLastSyncedAt {
                let ts = worktree.lastSyncedAt
                let date = Date(timeIntervalSince1970: TimeInterval(ts.seconds) + TimeInterval(ts.nanos) / 1e9)
                Text("Last synced \(date.formatted(.relative(presentation: .named)))")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder
    private var statusBadge: some View {
        if worktree.needsReconciliation {
            Text("needs attention").font(.caption).foregroundStyle(.orange)
        } else if worktree.orphaned {
            Text("orphaned").font(.caption).foregroundStyle(.secondary)
        } else {
            Text("in sync").font(.caption).foregroundStyle(.green)
        }
    }
}
