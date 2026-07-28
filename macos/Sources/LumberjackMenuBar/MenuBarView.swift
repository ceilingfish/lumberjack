import Foundation
import SwiftUI

/// The panel shown when the menu-bar icon is clicked: a repository switcher
/// plus a live view of the selected repository's worktrees, equivalent to
/// `lumberjack repositories NAME worktrees` on the CLI.
struct MenuBarView: View {
    @ObservedObject var state: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            content
                .padding(12)
        }
        .frame(width: 320)
        .onAppear { state.start() }
    }

    private var header: some View {
        HStack(spacing: 6) {
            Image(systemName: "tree.fill")
                .foregroundStyle(.green)
            Spacer()
            Circle()
                .fill(connectionColor)
                .frame(width: 7, height: 7)
                .help(connectionHelp)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }

    @ViewBuilder
    private var content: some View {
        switch state.connectionState {
        case .connecting:
            statusLine("Connecting to daemon…", systemImage: "arrow.triangle.2.circlepath")
        case .daemonNotRunning:
            statusLine("Daemon not running", systemImage: "exclamationmark.triangle")
        case .connected:
            connectedBody
        }
    }

    @ViewBuilder
    private var connectedBody: some View {
        if state.repositories.isEmpty {
            statusLine("No repositories tracked", systemImage: "tray")
        } else {
            repositoryPicker
            Divider()
                .padding(.vertical, 6)
            worktreeSection
        }
    }

    /// A centred "Repository" label paired with a chip-styled switcher. The
    /// default `Picker` pop-up looked cramped and out of place in the panel; a
    /// borderless `Menu` with an explicit chevron reads as part of the panel's
    /// layout.
    private var repositoryPicker: some View {
        HStack(spacing: 8) {
            Text("Repository")
                .fontWeight(.medium)
            Menu {
                ForEach(state.repositories, id: \.localPath) { repo in
                    Button {
                        state.selectedRepository = repo.localPath
                    } label: {
                        if repo.localPath == state.selectedRepository {
                            Label(repo.githubName, systemImage: "checkmark")
                        } else {
                            Text(repo.githubName)
                        }
                    }
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "folder.fill")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text(selectedRepositoryName)
                        .fontWeight(.medium)
                        .lineLimit(1)
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 7)
                .background(
                    RoundedRectangle(cornerRadius: 7)
                        .fill(Color.primary.opacity(0.06))
                )
                .contentShape(Rectangle())
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
        }
        .frame(maxWidth: .infinity, alignment: .center)
    }

    @ViewBuilder
    private var worktreeSection: some View {
        if !state.worktreesLoaded {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Loading worktrees…").foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.vertical, 12)
        } else if state.worktrees.isEmpty {
            statusLine("No worktrees", systemImage: "folder")
        } else {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(state.worktrees, id: \.directoryPath) { worktree in
                    WorktreeRow(worktree: worktree)
                }
            }
        }
    }

    private var selectedRepositoryName: String {
        state.repositories
            .first { $0.localPath == state.selectedRepository }?
            .githubName ?? "Select repository"
    }

    private var connectionColor: Color {
        switch state.connectionState {
        case .connecting: return .yellow
        case .daemonNotRunning: return .red
        case .connected: return .green
        }
    }

    private var connectionHelp: String {
        switch state.connectionState {
        case .connecting: return "Connecting to daemon…"
        case .daemonNotRunning: return "Daemon not running"
        case .connected: return "Connected to daemon"
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
                if let icon = WorktreeActions.vscodeIcon {
                    Button {
                        WorktreeActions.openInVSCode(worktree.directoryPath)
                    } label: {
                        Image(nsImage: icon)
                            .resizable()
                            .frame(width: 16, height: 16)
                    }
                    .buttonStyle(.plain)
                    .help("Open in VS Code")
                }
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
        .contentShape(Rectangle())
        .onTapGesture(count: 2) {
            WorktreeActions.openInFinder(worktree.directoryPath)
        }
        .help("Double-click to open in Finder")
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
