import AppKit
import Combine
import Foundation
import SwiftProtobuf
import SwiftUI

/// The panel shown when the menu-bar icon is clicked: connection status, a
/// repository switcher, a live view of the selected repository's worktrees,
/// and sync/quit actions. Implements the "Lumberjack popover redesign" from
/// Claude Design; the fixed light palette below (`Palette`) comes straight from
/// that spec, which is why the popover is pinned to the light appearance.
struct MenuBarView: View {
    @ObservedObject var state: AppState

    @State private var reposOpen = false
    @State private var quitOpen = false

    /// Search is view-local UI state, not daemon state: it never leaves this
    /// panel and has no business on `AppState` alongside the live worktree data.
    @State private var searchOpen = false
    @State private var searchQuery = ""
    @FocusState private var searchFocused: Bool

    /// One short easing curve for the whole search transition — the field's
    /// width, the summary's cross-fade, and the list's height change all ride
    /// it, so the panel resizes as a single motion. Deliberately not a spring:
    /// overshoot looks wrong at this panel size.
    private static let searchAnimation: Animation = .easeInOut(duration: 0.18)

    /// The spyglass, built once. A template image so it takes the tint applied
    /// to the button rather than rendering as flat black like the row actions'
    /// application icons.
    private static let searchIcon: NSImage? = {
        let image = NSImage(
            systemSymbolName: "magnifyingglass",
            accessibilityDescription: "Search worktrees"
        )?
        .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 12, weight: .medium))
        image?.isTemplate = true
        return image
    }()

    /// The worktrees actually listed: everything when search is closed or the
    /// query is empty, otherwise the matches.
    private var visibleWorktrees: [Lumberjack_V1_Worktree] {
        WorktreeSearch.filter(state.worktrees, query: searchOpen ? searchQuery : "")
    }

    private func openSearch() {
        // Opening always starts from empty. Belt and braces alongside the
        // `.disabled` on the collapsed field: search must never open onto a
        // query the user cannot remember typing.
        searchQuery = ""
        withAnimation(Self.searchAnimation) { searchOpen = true }
        // Deliberately not in the same transaction. The field is `.disabled`
        // while collapsed and SwiftUI resolves a focus request against the tree
        // as it stands when the request is made, so asking here — before the
        // update that enables the field has been applied — can be dropped, and
        // search would open without a caret. One turn of the main queue later
        // the field is enabled and the request lands.
        DispatchQueue.main.async { searchFocused = true }
    }

    /// Collapses search and clears the query. Clearing inside the same
    /// transaction as the collapse is what makes the list grow back to its full
    /// height as part of one motion. Pass `animated: false` when the panel is
    /// already hidden, where animating would only be work nobody sees.
    private func closeSearch(animated: Bool = true) {
        searchFocused = false
        withAnimation(animated ? Self.searchAnimation : nil) {
            searchOpen = false
            searchQuery = ""
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            hairline

            switch state.connectionState {
            case .connected where !state.repositories.isEmpty:
                repositorySection
                hairline
                worktreesHeader
                // On the list itself, not inside it: dropping to zero matches
                // swaps the ScrollView out for the no-match message, so an
                // animation attached in there goes with it and the resize
                // becomes a jump cut. Here it survives the branch change.
                worktreesList
                    .animation(Self.searchAnimation, value: visibleWorktrees.count)
            default:
                statusArea
            }

            hairline
            footer

            if quitOpen {
                hairline
                quitConfirmation
            }
        }
        .frame(width: 360)
        .background(Palette.card)
        .onAppear { state.start() }
        // The popover reuses one hosting controller for the app's lifetime, so
        // `@State` survives a dismiss and `onDisappear` never fires for it —
        // the popover's own notification is what tells us the panel went away.
        // Without this, reopening the panel could show a filtered list the user
        // had forgotten they were filtering.
        .onReceive(NotificationCenter.default.publisher(for: NSPopover.didCloseNotification)) { _ in
            closeSearch(animated: false)
        }
        // A branch or PR query from one repository rarely means anything in
        // another, and a stale filter could hide every worktree in the one just
        // selected.
        .onChange(of: state.selectedRepository) { _, _ in
            closeSearch()
        }
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 10) {
            RoundedRectangle(cornerRadius: 7)
                .fill(Palette.iconBoxFill)
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Palette.iconBoxBorder, lineWidth: 1))
                .frame(width: 26, height: 26)
                .overlay(
                    Image(systemName: "tree.fill")
                        .font(.system(size: 13))
                        .foregroundStyle(Palette.treeGreen)
                )

            VStack(alignment: .leading, spacing: 1) {
                Text("Lumberjack")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Palette.titleText)
                Text(headerSubtitle)
                    .font(.system(size: 11))
                    .foregroundStyle(Palette.subtleText)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            connectionPill
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }

    private var connectionPill: some View {
        let p = connectionPillStyle
        return HStack(spacing: 6) {
            Circle()
                .fill(p.dot)
                .frame(width: 6, height: 6)
                .overlay(Circle().stroke(p.dot.opacity(0.18), lineWidth: 2).scaleEffect(1.6))
            Text(p.label)
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(p.text)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(Capsule().fill(p.background))
        .overlay(Capsule().stroke(p.border, lineWidth: 1))
        .fixedSize()
    }

    // MARK: Repository switcher

    private var repositorySection: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                sectionLabel("Repository")
                Spacer()
                Text("\(state.repositories.count) connected")
                    .font(.system(size: 10))
                    .foregroundStyle(Palette.faintText)
            }
            .padding(.bottom, 6)

            repositoryButton

            if reposOpen {
                repositoryDropdown
                    .padding(.top, 6)
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 12)
        .padding(.bottom, 10)
        .background(Palette.sectionFill)
    }

    private var repositoryButton: some View {
        Button {
            withAnimation(.easeOut(duration: 0.12)) { reposOpen.toggle() }
        } label: {
            HStack(spacing: 9) {
                Image(systemName: "folder.fill")
                    .font(.system(size: 12))
                    .foregroundStyle(Palette.subtleText)
                Text(selectedRepositoryName)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Palette.titleText)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Image(systemName: "chevron.down")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(Palette.faintText)
                    .rotationEffect(.degrees(reposOpen ? 180 : 0))
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 8)
            .contentShape(Rectangle())
            .modifier(FillOnHover(base: Palette.card, hover: Palette.buttonHover, cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Palette.controlBorder, lineWidth: 1))
        }
        .buttonStyle(.plain)
    }

    private var repositoryDropdown: some View {
        VStack(spacing: 0) {
            ForEach(Array(state.repositories.enumerated()), id: \.element.localPath) { index, repo in
                Button {
                    state.selectedRepository = repo.localPath
                    withAnimation(.easeOut(duration: 0.12)) { reposOpen = false }
                } label: {
                    HStack(spacing: 9) {
                        Text(repo.localPath == state.selectedRepository ? "✓" : "")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundStyle(Palette.treeGreen)
                            .frame(width: 14, alignment: .leading)
                        Text(repo.githubName)
                            .font(.system(size: 12.5, weight: .medium))
                            .foregroundStyle(Palette.titleText)
                            .lineLimit(1)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        if let count = state.worktreeCountsByRepo[repo.localPath] {
                            Text("\(count) branch\(count == 1 ? "" : "es")")
                                .font(.system(size: 10.5))
                                .foregroundStyle(Palette.faintText)
                        }
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 8)
                    .contentShape(Rectangle())
                    .modifier(FillOnHover(base: .clear, hover: Palette.rowHover, cornerRadius: 0))
                }
                .buttonStyle(.plain)
                .overlay(alignment: .bottom) {
                    if index < state.repositories.count - 1 {
                        Rectangle().fill(Palette.rowDivider).frame(height: 1)
                    }
                }
            }
        }
        .background(Palette.card)
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Palette.controlBorder, lineWidth: 1))
        .shadow(color: Color.black.opacity(0.10), radius: 10, y: 8)
    }

    // MARK: Worktrees

    private var worktreesHeader: some View {
        HStack(spacing: 6) {
            sectionLabel("Worktrees")
            Spacer(minLength: 6)

            // The summary and the search field share the header's right-hand
            // slot: the field stays in the hierarchy but is clipped to zero
            // width and disabled while closed, and the two cross-fade. Keeping
            // it mounted is what lets the width animate rather than the field
            // popping into existence at full size; `openSearch` focuses it a
            // tick later, once it is enabled. A fixed height keeps the header
            // from changing size between the two states, which would make the
            // panel jitter as search opens.
            ZStack(alignment: .trailing) {
                Text(worktreeSummary)
                    .font(.system(size: 10))
                    .foregroundStyle(Palette.faintText)
                    .opacity(searchOpen ? 0 : 1)

                searchField
                    .frame(width: searchOpen ? 170 : 0)
                    .opacity(searchOpen ? 1 : 0)
                    .clipped()
                    // Zero width and zero opacity hide the field but leave it in
                    // the key-view loop, so Tab could focus it while collapsed
                    // and text typed there would be silently swallowed. Disabled
                    // takes it out of the loop as well as out of sight.
                    .disabled(!searchOpen)
            }
            .frame(height: 22)

            IconButton(image: Self.searchIcon, help: "Search worktrees") {
                if searchOpen { closeSearch() } else { openSearch() }
            }
            .foregroundStyle(searchOpen ? Palette.primary : Palette.subtleText)
        }
        .padding(.horizontal, 14)
        .padding(.top, 6)
        .padding(.bottom, 4)
        // Losing the connection (or the repository list) swaps this whole
        // section out for the status area, destroying the field and with it the
        // focus state — while `searchOpen` and the query would survive. Without
        // this, reconnecting restored a field that looked open and still
        // filtered the list but could not be typed into.
        .onDisappear { closeSearch(animated: false) }
    }

    private var searchField: some View {
        HStack(spacing: 4) {
            TextField("Branch or #PR", text: $searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 11))
                .foregroundStyle(Palette.titleText)
                .focused($searchFocused)
                // Escape closes search rather than only clearing it, matching
                // the expectation set by the toggle button.
                .onExitCommand { closeSearch() }

            if !searchQuery.isEmpty {
                Button {
                    searchQuery = ""
                    searchFocused = true
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 10))
                        .foregroundStyle(Palette.faintText)
                }
                .buttonStyle(.plain)
                .help("Clear search")
            }
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 3)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(Palette.iconBoxFill)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Palette.iconBoxBorder, lineWidth: 1))
        )
    }

    @ViewBuilder
    private var worktreesList: some View {
        if !state.worktreesLoaded {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Loading worktrees…").foregroundStyle(Palette.subtleText)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 16)
        } else if state.worktrees.isEmpty {
            Text("No worktrees")
                .font(.system(size: 12))
                .foregroundStyle(Palette.subtleText)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 16)
        } else if visibleWorktrees.isEmpty {
            // Distinct from "No worktrees" above: the repository does have
            // worktrees, the query just excluded all of them. Naming the query
            // makes it obvious the list is filtered rather than empty.
            Text("No worktrees match “\(searchQuery.trimmingCharacters(in: .whitespacesAndNewlines))”")
                .font(.system(size: 12))
                .foregroundStyle(Palette.subtleText)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 16)
        } else {
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(visibleWorktrees, id: \.directoryPath) { worktree in
                        WorktreeRow(
                            worktree: worktree,
                            prURL: prURL(for: worktree),
                            isSyncing: state.syncing
                        )
                    }
                }
                .padding(.horizontal, 6)
            }
            // ~46pt per row, capped so the panel never grows without bound;
            // beyond ~8 rows the list scrolls (matching the design's 372px cap).
            // Sized to the *matching* rows so a narrowed list doesn't leave a
            // panel of dead space below it.
            .frame(height: min(CGFloat(visibleWorktrees.count) * 46, 372))
            .padding(.bottom, 6)
        }
    }

    // MARK: Footer

    private var footer: some View {
        HStack(spacing: 6) {
            Button {
                state.syncAll()
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "arrow.triangle.2.circlepath")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Palette.subtleText)
                    Text(state.syncing ? "Syncing…" : "Sync all")
                        .font(.system(size: 11.5, weight: .semibold))
                        .foregroundStyle(Palette.bodyText)
                }
                .padding(.horizontal, 9)
                .padding(.vertical, 5)
                .contentShape(Rectangle())
                .modifier(FillOnHover(base: Palette.card, hover: Palette.buttonHover, cornerRadius: 7))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Palette.controlBorder, lineWidth: 1))
            }
            .buttonStyle(.plain)
            .disabled(state.syncing || state.connectionState != .connected)

            Spacer()

            Button {
                withAnimation(.easeOut(duration: 0.12)) {
                    quitOpen = true
                    reposOpen = false
                }
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: "rectangle.portrait.and.arrow.right")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Palette.destructive)
                    Text("Quit")
                        .font(.system(size: 11.5, weight: .semibold))
                        .foregroundStyle(Palette.destructive)
                    Text("⌘Q")
                        .font(.system(size: 10, weight: .medium, design: .monospaced))
                        .foregroundStyle(Palette.destructive.opacity(0.6))
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .contentShape(Rectangle())
                .modifier(FillOnHover(base: Palette.card, hover: Palette.destructiveHover, cornerRadius: 7))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Palette.destructiveBorder, lineWidth: 1))
            }
            .buttonStyle(.plain)
            .keyboardShortcut("q", modifiers: .command)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 9)
        .background(Palette.sectionFill)
    }

    private var quitConfirmation: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Quit Lumberjack?")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Palette.titleText)
                .padding(.bottom, 3)
            Text("Branches stop syncing until you reopen it.")
                .font(.system(size: 11))
                .foregroundStyle(Palette.subtleText)
                .padding(.bottom, 9)
            HStack(spacing: 7) {
                Button {
                    withAnimation(.easeOut(duration: 0.12)) { quitOpen = false }
                } label: {
                    Text("Cancel")
                        .font(.system(size: 11.5, weight: .semibold))
                        .foregroundStyle(Palette.bodyText)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .contentShape(Rectangle())
                        .modifier(FillOnHover(base: Palette.card, hover: Palette.buttonHover, cornerRadius: 7))
                        .overlay(RoundedRectangle(cornerRadius: 7).stroke(Palette.controlBorder, lineWidth: 1))
                }
                .buttonStyle(.plain)

                Button {
                    NSApplication.shared.terminate(nil)
                } label: {
                    Text("Quit now")
                        .font(.system(size: 11.5, weight: .semibold))
                        .foregroundStyle(.white)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .contentShape(Rectangle())
                        .modifier(FillOnHover(base: Palette.destructive, hover: Palette.destructiveDeep, cornerRadius: 7))
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 11)
        .padding(.bottom, 13)
        .background(Palette.destructiveTint)
    }

    // MARK: Shared pieces

    private var hairline: some View {
        Rectangle().fill(Palette.hairline).frame(height: 1)
    }

    private func sectionLabel(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 10, weight: .semibold))
            .tracking(0.6)
            .textCase(.uppercase)
            .foregroundStyle(Palette.faintText)
    }

    @ViewBuilder
    private var statusArea: some View {
        let (text, symbol): (String, String) = {
            switch state.connectionState {
            case .connecting: return ("Connecting to daemon…", "arrow.triangle.2.circlepath")
            case .daemonNotRunning: return ("Daemon not running", "exclamationmark.triangle")
            case .connected: return ("No repositories tracked", "tray")
            }
        }()
        Label(text, systemImage: symbol)
            .font(.system(size: 12))
            .foregroundStyle(Palette.subtleText)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 22)
            .padding(.horizontal, 14)
    }

    // MARK: Derived values

    private var selectedRepositoryName: String {
        state.selectedRepositoryObject?.githubName ?? "Select repository"
    }

    private var headerSubtitle: String {
        switch state.connectionState {
        case .connecting:
            return "Connecting to daemon…"
        case .daemonNotRunning:
            return "Daemon not running"
        case .connected:
            let n = state.repositories.count
            var s = "Watching \(n) \(n == 1 ? "repository" : "repositories")"
            if let synced = latestSyncText { s += " · synced \(synced)" }
            return s
        }
    }

    private var latestSyncText: String? {
        let dates = state.repositories.compactMap { repo -> Date? in
            guard repo.hasLastSyncedAt else { return nil }
            return date(from: repo.lastSyncedAt)
        }
        guard let newest = dates.max() else { return nil }
        return newest.formatted(.relative(presentation: .named))
    }

    private var worktreeSummary: String {
        let inSync = state.worktrees.filter { !$0.needsReconciliation && !$0.orphaned }.count
        let disparity = state.worktrees.filter { $0.branchDisparity }.count
        let attention = state.worktrees.filter { $0.needsReconciliation && !$0.branchDisparity }.count
        let orphaned = state.worktrees.filter { $0.orphaned && !$0.needsReconciliation }.count
        var parts: [String] = []
        if inSync > 0 { parts.append("\(inSync) in sync") }
        if attention > 0 { parts.append("\(attention) needs attention") }
        if disparity > 0 { parts.append("\(disparity) branch disparity") }
        if orphaned > 0 { parts.append("\(orphaned) orphaned") }
        return parts.joined(separator: " · ")
    }

    private var connectionPillStyle: (dot: Color, text: Color, background: Color, border: Color, label: String) {
        switch state.connectionState {
        case .connected:
            return (Palette.treeGreen, Palette.treeGreen, Palette.pillGreenBg, Palette.pillGreenBorder, "Connected")
        case .connecting:
            return (Palette.warnDot, Palette.warnText, Palette.pillWarnBg, Palette.pillWarnBorder, "Connecting")
        case .daemonNotRunning:
            return (Palette.destructive, Palette.destructive, Palette.destructiveTint, Palette.destructiveBorder, "Offline")
        }
    }

    private func prURL(for worktree: Lumberjack_V1_Worktree) -> URL? {
        guard worktree.hasGithubPrNumber, let repo = state.selectedRepositoryObject else { return nil }
        let host = repo.host.isEmpty ? "github.com" : repo.host
        return URL(string: "https://\(host)/\(repo.githubOwner)/\(repo.githubName)/pull/\(worktree.githubPrNumber)")
    }

    private func date(from ts: Google_Protobuf_Timestamp) -> Date {
        Date(timeIntervalSince1970: TimeInterval(ts.seconds) + TimeInterval(ts.nanos) / 1e9)
    }
}

/// One worktree's row: status dot, branch name, PR link, subtitle, and — on
/// the right — a status pill that swaps for Finder/Terminal/VS Code actions
/// while the pointer is over the row (mirroring the design's hover behaviour).
private struct WorktreeRow: View {
    let worktree: Lumberjack_V1_Worktree
    let prURL: URL?
    let isSyncing: Bool

    @State private var hovered = false

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(style.dot)
                .frame(width: 5, height: 5)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(worktree.branchName)
                        .font(.system(size: 11.5, weight: .medium, design: .monospaced))
                        .foregroundStyle(Palette.titleText)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if worktree.hasGithubPrNumber {
                        prLink
                    }
                }
                if let sub = subtitle {
                    Text(sub)
                        .font(.system(size: 10.5))
                        .foregroundStyle(style.subColor)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            if hovered {
                actions
            } else {
                statusPill
            }
        }
        .padding(.horizontal, 8)
        .frame(height: 46)
        .background(RoundedRectangle(cornerRadius: 7).fill(hovered ? Palette.rowHover : .clear))
        .contentShape(Rectangle())
        .onHover { hovered = $0 }
        .onTapGesture(count: 2) { WorktreeActions.openInFinder(worktree.directoryPath) }
        .help("Double-click to reveal in Finder")
    }

    @ViewBuilder
    private var prLink: some View {
        let label = Text("#\(worktree.githubPrNumber)")
            .font(.system(size: 10, design: .monospaced))
            .foregroundStyle(Palette.subtleText)
        if let prURL {
            Link(destination: prURL) { label }
                .help("View pull request on GitHub")
        } else {
            label
        }
    }

    private var statusPill: some View {
        Text(style.label)
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(style.pillFg)
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .background(Capsule().fill(style.pillBg))
            .fixedSize()
    }

    private var actions: some View {
        HStack(spacing: 3) {
            IconButton(image: WorktreeActions.finderIcon, help: "Reveal in Finder") {
                WorktreeActions.openInFinder(worktree.directoryPath)
            }
            IconButton(image: WorktreeActions.terminalIcon, help: "Open in Terminal") {
                WorktreeActions.openInTerminal(worktree.directoryPath)
            }
            if WorktreeActions.vscodeIcon != nil {
                IconButton(image: WorktreeActions.vscodeIcon, help: "Open in VS Code") {
                    WorktreeActions.openInVSCode(worktree.directoryPath)
                }
            }
        }
        .fixedSize()
    }

    private var subtitle: String? {
        if worktree.needsReconciliation {
            if !worktree.reconciliationNote.isEmpty { return worktree.reconciliationNote }
            if worktree.branchDisparity {
                return "On \(worktree.checkedOutBranch), not the PR branch \(worktree.branchName)"
            }
            return "Needs reconciliation"
        }
        guard worktree.hasLastSyncedAt else { return nil }
        let ts = worktree.lastSyncedAt
        let date = Date(timeIntervalSince1970: TimeInterval(ts.seconds) + TimeInterval(ts.nanos) / 1e9)
        return "Synced \(date.formatted(.relative(presentation: .named)))"
    }

    private var style: StatusStyle {
        if isSyncing { return .syncing }
        if worktree.branchDisparity { return .disparity }
        if worktree.needsReconciliation { return .attention }
        if worktree.orphaned { return .orphaned }
        return .inSync
    }
}

/// The colour/label set for a worktree's status, keyed to the design's pills.
private struct StatusStyle {
    let label: String
    let dot: Color
    let pillFg: Color
    let pillBg: Color
    let subColor: Color

    static let inSync = StatusStyle(
        label: "In sync", dot: Palette.dotGreen,
        pillFg: Palette.treeGreen, pillBg: Palette.pillGreenBg, subColor: Palette.faintText)
    static let attention = StatusStyle(
        label: "Needs attention", dot: Palette.dotAmber,
        pillFg: Palette.warnText, pillBg: Palette.pillWarnBg, subColor: Palette.warnText)
    static let disparity = StatusStyle(
        label: "Branch disparity", dot: Palette.destructive,
        pillFg: Palette.destructiveDeep, pillBg: Palette.destructiveTint, subColor: Palette.destructiveDeep)
    static let syncing = StatusStyle(
        label: "Syncing", dot: Palette.dotBlue,
        pillFg: Palette.primary, pillBg: Palette.pillBlueBg, subColor: Palette.faintText)
    static let orphaned = StatusStyle(
        label: "Orphaned", dot: Palette.dotGrey,
        pillFg: Palette.subtleText, pillBg: Palette.pillGreyBg, subColor: Palette.faintText)
}

/// A 22×22 icon button that fills its background only while hovered — used for
/// the per-row Finder/Terminal/VS Code actions.
private struct IconButton: View {
    let image: NSImage?
    let help: String
    let action: () -> Void

    @State private var hovered = false

    var body: some View {
        Button(action: action) {
            Group {
                if let image {
                    Image(nsImage: image).resizable().scaledToFit()
                }
            }
            .frame(width: 16, height: 16)
            .frame(width: 22, height: 22)
            .background(RoundedRectangle(cornerRadius: 6).fill(hovered ? Palette.iconHover : .clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 }
        .help(help)
    }
}

/// Fills a view's background, switching to `hover` while the pointer is over
/// it. Keeps the many hover-highlighted controls in the panel to one place.
private struct FillOnHover: ViewModifier {
    let base: Color
    let hover: Color
    let cornerRadius: CGFloat

    @State private var hovering = false

    func body(content: Content) -> some View {
        content
            .background(RoundedRectangle(cornerRadius: cornerRadius).fill(hovering ? hover : base))
            .onHover { hovering = $0 }
    }
}

/// The fixed light palette from the "Lumberjack popover redesign" spec.
private enum Palette {
    static let card = Color(hex: 0xffffff)
    static let sectionFill = Color(hex: 0xfbfbfd)
    static let hairline = Color(hex: 0xeceef2)
    static let rowDivider = Color(hex: 0xf1f2f6)

    static let titleText = Color(hex: 0x1f2430)
    static let bodyText = Color(hex: 0x333333)
    static let subtleText = Color(hex: 0x6b7280)
    static let faintText = Color(hex: 0x9aa0ae)

    static let controlBorder = Color(hex: 0xdfe2e9)
    static let buttonHover = Color(hex: 0xf4f5f9)
    static let rowHover = Color(hex: 0xf6f7fa)
    static let iconHover = Color(hex: 0xeceef3)

    static let iconBoxFill = Color(hex: 0xeef3ee)
    static let iconBoxBorder = Color(hex: 0xdbe6dc)
    static let treeGreen = Color(hex: 0x2f7d46)

    static let primary = Color(hex: 0x000081)

    static let dotGreen = Color(hex: 0x2f9e52)
    static let dotAmber = Color(hex: 0xd98b1f)
    static let dotBlue = Color(hex: 0x4b4bc8)
    static let dotGrey = Color(hex: 0x9aa0ae)

    static let pillGreenBg = Color(hex: 0xeef6f0)
    static let pillGreenBorder = Color(hex: 0xd8e7dc)
    static let pillWarnBg = Color(hex: 0xfdf3e3)
    static let pillWarnBorder = Color(hex: 0xf0dcbb)
    static let pillBlueBg = Color(hex: 0xeeeef8)
    static let pillGreyBg = Color(hex: 0xf1f2f6)

    static let warnText = Color(hex: 0x8a5200)
    static let warnDot = Color(hex: 0xd98b1f)

    static let destructive = Color(hex: 0xb42d2d)
    static let destructiveDeep = Color(hex: 0x9d2727)
    static let destructiveBorder = Color(hex: 0xe6d3d3)
    static let destructiveHover = Color(hex: 0xfdf5f5)
    static let destructiveTint = Color(hex: 0xfdf7f7)
}

extension Color {
    /// Builds a colour from a 24-bit `0xRRGGBB` literal — lets the palette read
    /// the same hex values the design spec uses.
    fileprivate init(hex: UInt32) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xff) / 255,
            green: Double((hex >> 8) & 0xff) / 255,
            blue: Double(hex & 0xff) / 255,
            opacity: 1
        )
    }
}
