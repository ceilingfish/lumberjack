import AppKit
import Combine
import Foundation
import SwiftProtobuf
import SwiftUI

/// The panel shown when the menu-bar icon is clicked: connection status, a
/// live view of every tracked repository's worktrees in one filterable list,
/// and sync/quit actions. Implements the "Lumberjack popover redesign" from
/// Claude Design; the fixed light palette below (`Palette`) comes straight from
/// that spec, which is why the popover is pinned to the light appearance.
struct MenuBarView: View {
    @ObservedObject var state: AppState

    @State private var quitOpen = false
    @State private var filterOpen = false
    @State private var listHeight: CGFloat = 0

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

    private var filter: PullRequestFilter { state.filter }

    private func rows(in tab: Authorship) -> [UnifiedRow] {
        let filter = state.filters[tab] ?? PullRequestFilter()
        return filter.apply(UnifiedList.rows(in: tab, of: state.rows))
    }

    private var filteredTabRows: [UnifiedRow] {
        filter.apply(state.tabRows)
    }

    private var visibleRows: [UnifiedRow] {
        let query = searchOpen ? searchQuery : ""
        let matching = Set(WorktreeSearch.filter(filteredTabRows.map(\.worktree), query: query).map(\.directoryPath))
        return filteredTabRows.filter { matching.contains($0.id) }
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
                controls
                hairline.padding(.top, 4)
                worktreesList
                    .animation(Self.searchAnimation, value: visibleRows.count)
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
            state.panelClosed()
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

    // MARK: Tabs and filters

    private var controls: some View {
        VStack(alignment: .leading, spacing: 8) {
            if UnifiedList.isSplit(state.rows) {
                tabBar
            }
            filterBar
            if filterOpen {
                filterPanel
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 10)
        .padding(.bottom, 6)
    }

    private var tabBar: some View {
        HStack(spacing: 2) {
            tabButton(.mine, title: "Mine") {
                let n = PullRequestPresentation.mineBadge(rows(in: .mine).map(\.worktree))
                if n > 0 {
                    HStack(spacing: 4) {
                        Circle().fill(Palette.destructive).frame(width: 6, height: 6)
                        Text("\(n)")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(Palette.chipRed.fg)
                    }
                }
            }
            tabButton(.theirs, title: "Incoming") {
                Text("\(rows(in: .theirs).count)")
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(Palette.subtleText)
                if PullRequestPresentation.incomingUnread(state.worktrees) {
                    Circle()
                        .fill(Palette.unreadDot)
                        .frame(width: 7, height: 7)
                        .overlay(Circle().stroke(Palette.card, lineWidth: 2).frame(width: 11, height: 11))
                        .accessibilityLabel("New pull requests since you last looked")
                }
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 8).fill(Palette.pillGreyBg))
    }

    private func tabButton<Accessory: View>(
        _ tab: Authorship, title: String, @ViewBuilder accessory: () -> Accessory
    ) -> some View {
        let selected = state.selectedTab == tab
        return Button {
            closeSearch()
            state.selectedTab = tab
        } label: {
            HStack(spacing: 6) {
                Text(title)
                    .font(.system(size: 12, weight: selected ? .semibold : .medium))
                    .foregroundStyle(selected ? Palette.titleText : Palette.chipSlate.fg)
                accessory()
            }
            .frame(maxWidth: .infinity)
            .frame(height: 28)
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(selected ? Palette.card : .clear)
                    .shadow(color: selected ? .black.opacity(0.12) : .clear, radius: 1, y: 1)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private var filterBar: some View {
        HStack(spacing: 6) {
            filterButton

            ZStack(alignment: .leading) {
                Text(filter.summary(repositories: state.repositories))
                    .font(.system(size: 10.5))
                    .foregroundStyle(Palette.subtleText)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .opacity(searchOpen ? 0 : 1)

                searchField
                    .opacity(searchOpen ? 1 : 0)
                    // Zero opacity hides the field but leaves it in the
                    // key-view loop, so Tab could focus it while collapsed and
                    // text typed there would be silently swallowed. Disabled
                    // takes it out of the loop as well as out of sight.
                    .disabled(!searchOpen)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(height: 22)

            if filter.isActive && !searchOpen {
                Button("Clear") {
                    state.filter = PullRequestFilter()
                }
                .buttonStyle(.plain)
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(Palette.primary)
            }

            IconButton(image: Self.searchIcon, help: "Search worktrees") {
                if searchOpen { closeSearch() } else { openSearch() }
            }
            .foregroundStyle(searchOpen ? Palette.primary : Palette.subtleText)
        }
        // Losing the connection (or the repository list) swaps this whole
        // section out for the status area, destroying the field and with it the
        // focus state — while `searchOpen` and the query would survive. Without
        // this, reconnecting restored a field that looked open and still
        // filtered the list but could not be typed into.
        .onDisappear { closeSearch(animated: false) }
    }

    private var filterButton: some View {
        let lit = filter.isActive || filterOpen
        return Button {
            withAnimation(.easeOut(duration: 0.15)) { filterOpen.toggle() }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "line.3.horizontal.decrease")
                    .font(.system(size: 10, weight: .semibold))
                Text("Filter")
                    .font(.system(size: 11, weight: .semibold))
                if filter.isActive {
                    Text("\(filter.activeCount)")
                        .font(.system(size: 9.5, weight: .bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 4)
                        .frame(minWidth: 15, minHeight: 15)
                        .background(Capsule().fill(Palette.treeGreen))
                }
                Image(systemName: "chevron.down")
                    .font(.system(size: 8, weight: .bold))
                    .rotationEffect(.degrees(filterOpen ? 180 : 0))
            }
            .foregroundStyle(lit ? Palette.chipGreen.fg : Palette.chipSlate.fg)
            .padding(.horizontal, 8)
            .frame(height: 24)
            .background(RoundedRectangle(cornerRadius: 6).fill(lit ? Palette.iconBoxFill : Palette.card))
            .overlay(
                RoundedRectangle(cornerRadius: 6).stroke(lit ? Palette.filterLitBorder : Palette.controlBorder)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .fixedSize()
        .accessibilityValue(filterOpen ? "Expanded" : "Collapsed")
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

    private var filterPanel: some View {
        let pool = state.tabRows
        return VStack(alignment: .leading, spacing: 12) {
            filterSection("Repository") {
                ForEach(state.repositories, id: \.localPath) { repo in
                    FilterChip(
                        label: repo.githubName,
                        count: filter.count(pool, repository: repo.localPath),
                        on: filter.repositories.contains(repo.localPath),
                        tone: Palette.chipSlate
                    ) { state.filter.toggle(repository: repo.localPath) }
                }
            }
            filterSection("Size") {
                ForEach(PullRequestFilter.sizes, id: \.self) { size in
                    FilterChip(
                        label: size,
                        count: filter.count(pool, size: size),
                        on: filter.sizes.contains(size),
                        tone: Palette.sizeTone(size)
                    ) { state.filter.toggle(size: size) }
                }
            }
            filterSection("Status") {
                ForEach(StatusFilter.available(in: state.selectedTab), id: \.self) { status in
                    FilterChip(
                        label: status.label,
                        count: filter.count(pool, status: status),
                        on: filter.statuses.contains(status),
                        tone: Palette.statusTone(status)
                    ) { state.filter.toggle(status: status) }
                }
            }
        }
        .padding(.horizontal, 11)
        .padding(.top, 11)
        .padding(.bottom, 13)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 9).fill(Palette.filterPanelFill))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Palette.filterPanelBorder, lineWidth: 1))
    }

    private func filterSection<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            sectionLabel(title)
            FlowLayout(spacing: 5) {
                content()
            }
        }
    }

    // MARK: Worktrees

    @ViewBuilder
    private var worktreesList: some View {
        if !state.worktreesLoaded {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Loading worktrees…").foregroundStyle(Palette.subtleText)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 16)
        } else if state.tabRows.isEmpty {
            emptyMessage(
                state.selectedTab == .theirs && UnifiedList.isSplit(state.rows) ? "Nothing incoming" : "No worktrees"
            )
        } else if filteredTabRows.isEmpty {
            emptyMessage("No pull requests match these filters")
        } else if visibleRows.isEmpty {
            // Distinct from "No worktrees" above: the repository does have
            // worktrees, the query just excluded all of them. Naming the query
            // makes it obvious the list is filtered rather than empty.
            emptyMessage("No worktrees match “\(searchQuery.trimmingCharacters(in: .whitespacesAndNewlines))”")
        } else {
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(visibleRows) { row in
                        WorktreeRow(
                            row: row,
                            prURL: MenuBarPresentation.prURL(repository: row.repository, worktree: row.worktree),
                            isSyncing: state.syncing
                        )
                    }
                }
                .padding(6)
                .onGeometryChange(for: CGFloat.self) {
                    $0.size.height
                } action: {
                    listHeight = $0
                }
            }
            .frame(height: min(max(listHeight, 44), 480))
        }
    }

    private func emptyMessage(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 12))
            .foregroundStyle(Palette.subtleText)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 28)
            .padding(.horizontal, 8)
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
                    filterOpen = false
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
            .foregroundStyle(Palette.subtleText)
    }

    @ViewBuilder
    private var statusArea: some View {
        let status = MenuBarPresentation.statusMessage(state.connectionState)
        Label(status.text, systemImage: status.symbol)
            .font(.system(size: 12))
            .foregroundStyle(Palette.subtleText)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 22)
            .padding(.horizontal, 14)
    }

    // MARK: Derived values

    private var headerSubtitle: String {
        MenuBarPresentation.headerSubtitle(state.connectionState, repositories: state.repositories)
    }

    private var connectionPillStyle: (dot: Color, text: Color, background: Color, border: Color, label: String) {
        let label = MenuBarPresentation.pillLabel(state.connectionState)
        switch state.connectionState {
        case .connected:
            return (Palette.treeGreen, Palette.treeGreen, Palette.pillGreenBg, Palette.pillGreenBorder, label)
        case .connecting:
            return (Palette.warnDot, Palette.warnText, Palette.pillWarnBg, Palette.pillWarnBorder, label)
        case .daemonNotRunning:
            return (Palette.destructive, Palette.destructive, Palette.destructiveTint, Palette.destructiveBorder, label)
        }
    }
}

private struct WorktreeRow: View {
    let row: UnifiedRow
    let prURL: URL?
    let isSyncing: Bool

    @State private var hovered = false

    private var worktree: Worktree { row.worktree }

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Circle()
                .fill(dotColor)
                .frame(width: 6, height: 6)
                .padding(.top, 5)

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(worktree.branchName)
                        .font(.system(size: 11.5, weight: .medium, design: .monospaced))
                        .foregroundStyle(Palette.titleText)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if worktree.hasGithubPrNumber {
                        prLink
                    }
                    if isNew {
                        Circle()
                            .fill(Palette.newActivity)
                            .frame(width: 6, height: 6)
                            .accessibilityLabel("New activity")
                    }
                }
                subtitleLine
                if hasChips {
                    FlowLayout(spacing: 4) { chips }
                        .padding(.top, 2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            if hovered {
                actions
            } else if let updated = PullRequestPresentation.updated(worktree) {
                Text(updated)
                    .font(.system(size: 10))
                    .foregroundStyle(Palette.subtleText)
                    .padding(.top, 2)
            }
        }
        .padding(.horizontal, 8)
        .padding(.top, 8)
        .padding(.bottom, 9)
        .background(RoundedRectangle(cornerRadius: 7).fill(hovered ? Palette.rowHover : .clear))
        .contentShape(Rectangle())
        .onHover { hovered = $0 }
        .onTapGesture(count: 2) { WorktreeActions.openInFinder(worktree.directoryPath) }
        .help("Double-click to reveal in Finder")
    }

    private var subtitleLine: some View {
        HStack(spacing: 5) {
            Text(row.repository.githubName)
                .fontWeight(.semibold)
                .foregroundStyle(Palette.chipSlate.fg)
            if let detail {
                Text("· \(detail)")
                    .foregroundStyle(status == .inSync ? Palette.subtleText : style.subColor)
            }
        }
        .font(.system(size: 10.5))
        .lineLimit(1)
        .truncationMode(.tail)
    }

    private var detail: String? {
        if worktree.authorship == .theirs && worktree.hasPullRequest && status == .inSync {
            return worktree.pullRequest.authorLogin.isEmpty ? nil : worktree.pullRequest.authorLogin
        }
        if status != .inSync || !worktree.hasPullRequest {
            return MenuBarPresentation.rowSubtitle(worktree)
        }
        return nil
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

    private var hasChips: Bool {
        if !worktree.hasPullRequest || status != .inSync { return true }
        return worktree.authorship == .theirs || badge != .none
    }

    @ViewBuilder
    private var chips: some View {
        if status != .inSync || !worktree.hasPullRequest {
            RowChip(text: style.label, tone: style.tone)
        }
        if worktree.hasPullRequest {
            let pr = worktree.pullRequest
            if worktree.authorship == .theirs {
                let size = PullRequestPresentation.sizeLabel(lines: PullRequestPresentation.lines(pr))
                RowChip(text: size, tone: Palette.sizeTone(size))
                    .help("\(PullRequestPresentation.linesText(pr)) · \(PullRequestPresentation.sizeTitle(pr))")
            }
            switch badge {
            case .none:
                EmptyView()
            case .problem(let text):
                RowChip(text: text, tone: Palette.chipRed)
            case .blocked(let count, let summary):
                RowChip(text: "Blocked · \(count)", symbol: "xmark.circle", tone: Palette.chipRed)
                    .help(summary)
                    .accessibilityLabel("Blocked: \(summary)")
            case .approved(let tip):
                RowChip(text: "Approved", symbol: "checkmark", tone: Palette.chipGreen)
                    .help(tip)
            case .review:
                RowChip(text: "Review", symbol: "eye", tone: Palette.chipYellow)
            }
        }
    }

    private var badge: RowBadge {
        PullRequestPresentation.badge(worktree)
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

    private var status: WorktreeStatus {
        MenuBarPresentation.status(of: worktree, isSyncing: isSyncing)
    }

    private var isNew: Bool {
        worktree.hasPullRequest && worktree.pullRequest.hasNewActivity_p
    }

    private var dotColor: Color {
        guard worktree.hasPullRequest && status == .inSync else { return style.dot }
        switch PullRequestPresentation.dot(worktree) {
        case .ok: return Palette.dotGreen
        case .new: return Palette.newActivity
        case .draft: return Palette.dotGrey
        case .blocked: return Palette.destructive
        case .attention: return Palette.dotAmber
        }
    }

    private var style: StatusStyle {
        switch status {
        case .inSync: return .inSync
        case .attention: return .attention
        case .disparity: return .disparity
        case .orphaned: return .orphaned
        case .syncing: return .syncing
        }
    }
}

private struct RowChip: View {
    let text: String
    var symbol: String? = nil
    let tone: ChipTone

    var body: some View {
        HStack(spacing: 4) {
            if let symbol {
                Image(systemName: symbol)
                    .font(.system(size: 8.5, weight: .bold))
            }
            Text(text)
                .lineLimit(1)
        }
        .font(.system(size: 10, weight: .semibold))
        .foregroundStyle(tone.fg)
        .padding(.horizontal, 6)
        .frame(height: 18)
        .background(RoundedRectangle(cornerRadius: 5).fill(tone.bg))
        .overlay(RoundedRectangle(cornerRadius: 5).stroke(tone.border, lineWidth: 1))
        .fixedSize()
    }
}

private struct FilterChip: View {
    let label: String
    let count: Int
    let on: Bool
    let tone: ChipTone
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Text(label)
                Text("\(count)")
                    .fontWeight(.medium)
                    .opacity(0.8)
            }
            .font(.system(size: 10.5, weight: .semibold))
            .foregroundStyle(on ? tone.solidText : tone.fg)
            .padding(.horizontal, 7)
            .frame(height: 22)
            .background(RoundedRectangle(cornerRadius: 5).fill(on ? tone.solid : tone.bg))
            .overlay(RoundedRectangle(cornerRadius: 5).stroke(on ? tone.solid : tone.border, lineWidth: 1))
            .opacity(count > 0 || on ? 1 : 0.4)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .fixedSize()
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

private struct FlowLayout: Layout {
    var spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        let frames = arrange(subviews, width: width)
        let height = frames.map(\.maxY).max() ?? 0
        let used = frames.map(\.maxX).max() ?? 0
        return CGSize(width: proposal.width ?? used, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        for (subview, frame) in zip(subviews, arrange(subviews, width: bounds.width)) {
            subview.place(
                at: CGPoint(x: bounds.minX + frame.minX, y: bounds.minY + frame.minY),
                proposal: ProposedViewSize(frame.size)
            )
        }
    }

    private func arrange(_ subviews: Subviews, width: CGFloat) -> [CGRect] {
        var frames: [CGRect] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var lineHeight: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > 0 && x + size.width > width {
                x = 0
                y += lineHeight + spacing
                lineHeight = 0
            }
            frames.append(CGRect(origin: CGPoint(x: x, y: y), size: size))
            x += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }
        return frames
    }
}

private struct ChipTone {
    let fg: Color
    let bg: Color
    let border: Color
    var solid: Color
    var solidText: Color = .white
}

/// The colour/label set for a worktree's status, keyed to the design's pills.
private struct StatusStyle {
    let label: String
    let dot: Color
    let tone: ChipTone
    let subColor: Color

    static let inSync = StatusStyle(
        label: "In sync", dot: Palette.dotGreen, tone: Palette.chipGreen, subColor: Palette.subtleText)
    static let attention = StatusStyle(
        label: "Needs attention", dot: Palette.dotAmber, tone: Palette.chipAmber, subColor: Palette.warnText)
    static let disparity = StatusStyle(
        label: "Branch disparity", dot: Palette.destructive, tone: Palette.chipRed, subColor: Palette.destructiveDeep)
    static let syncing = StatusStyle(
        label: "Syncing", dot: Palette.newActivity, tone: Palette.chipBlue, subColor: Palette.subtleText)
    static let orphaned = StatusStyle(
        label: "Orphaned", dot: Palette.dotGrey, tone: Palette.chipSlate, subColor: Palette.subtleText)
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

    static let newActivity = Color(hex: 0x2a5bd7)
    static let unreadDot = Color(hex: 0xe0a100)
    static let filterLitBorder = Color(hex: 0xc9dccd)
    static let filterPanelFill = Color(hex: 0xeef0f4)
    static let filterPanelBorder = Color(hex: 0xd5d8e0)

    static let chipRed = ChipTone(
        fg: Color(hex: 0xa32323), bg: Color(hex: 0xfdf1f1), border: Color(hex: 0xf0d2d2), solid: Color(hex: 0xb42d2d))
    static let chipGreen = ChipTone(
        fg: Color(hex: 0x2a6e3e), bg: Color(hex: 0xeef6f0), border: Color(hex: 0xd8e7dc), solid: Color(hex: 0x2f7d46))
    static let chipOlive = ChipTone(
        fg: Color(hex: 0x55621f), bg: Color(hex: 0xf4f6e6), border: Color(hex: 0xe1e6c4), solid: Color(hex: 0x5f6d22))
    static let chipOrange = ChipTone(
        fg: Color(hex: 0x8a4a00), bg: Color(hex: 0xfdf1e3), border: Color(hex: 0xf0d9bb), solid: Color(hex: 0xa35a00))
    static let chipAmber = ChipTone(
        fg: Color(hex: 0x7a4800), bg: Color(hex: 0xfdf3e3), border: Color(hex: 0xf0dcbb), solid: Color(hex: 0xa35a00))
    static let chipYellow = ChipTone(
        fg: Color(hex: 0x6b4e00), bg: Color(hex: 0xfdf6dc), border: Color(hex: 0xefe0a6), solid: Color(hex: 0xe0a100),
        solidText: Color(hex: 0x2b1f00))
    static let chipBlue = ChipTone(
        fg: Color(hex: 0x2443a8), bg: Color(hex: 0xeef1fb), border: Color(hex: 0xd6ddf3), solid: Color(hex: 0x2443a8))
    static let chipSlate = ChipTone(
        fg: Color(hex: 0x3f4553), bg: Color(hex: 0xf1f2f6), border: Color(hex: 0xe1e3ea), solid: Color(hex: 0x4b5160))

    static func sizeTone(_ size: String) -> ChipTone {
        switch size {
        case "M": return chipOlive
        case "L": return chipOrange
        case "XL": return chipRed
        default: return chipGreen
        }
    }

    static func statusTone(_ status: StatusFilter) -> ChipTone {
        switch status {
        case .approved: return chipGreen
        case .needsReview: return chipYellow
        default: return chipRed
        }
    }
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
