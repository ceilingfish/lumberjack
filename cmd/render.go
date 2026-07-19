package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// tabW wraps a tabwriter and records the first write error, so render helpers
// can stream rows without checking every Fprintf and still report failure.
type tabW struct {
	w   *tabwriter.Writer
	err error
}

func newTabW(w io.Writer) *tabW {
	return &tabW{w: tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
}

func (t *tabW) row(format string, args ...any) {
	if t.err != nil {
		return
	}
	_, t.err = fmt.Fprintf(t.w, format, args...)
}

// flush writes the buffered table and returns the first error seen.
func (t *tabW) flush() error {
	if t.err != nil {
		return t.err
	}
	return t.w.Flush()
}

// renderRepositories prints a table of repositories.
func renderRepositories(w io.Writer, repos []*lumberjackv1.Repository) error {
	if len(repos) == 0 {
		_, err := fmt.Fprintln(w, "No repositories tracked. Add one with `lumberjack init .`")
		return err
	}
	t := newTabW(w)
	t.row("NAME\tPATH\tLAST SYNCED\tSTATUS\n")
	for _, r := range repos {
		t.row("%s\t%s\t%s\t%s\n",
			r.GetDirPrefix(), r.GetLocalPath(),
			timestamp(r.GetLastSyncedAt()), syncStatus(r))
	}
	return t.flush()
}

// renderRepositoryDetail prints the last-sync detail for one repository.
func renderRepositoryDetail(w io.Writer, r *lumberjackv1.Repository) error {
	t := newTabW(w)
	t.row("Name:\t%s\n", r.GetDirPrefix())
	t.row("Path:\t%s\n", r.GetLocalPath())
	t.row("GitHub:\t%s/%s (%s)\n", r.GetGithubOwner(), r.GetGithubName(), r.GetHost())
	if r.GetLogin() != "" {
		t.row("Login:\t%s\n", r.GetLogin())
	}
	t.row("Worktrees dir:\t%s\n", r.GetWorktreeParentDir())
	t.row("Last synced:\t%s\n", timestamp(r.GetLastSyncedAt()))
	t.row("Status:\t%s\n", syncStatus(r))
	if r.GetLastSyncError() != "" {
		t.row("Last error:\t%s\n", r.GetLastSyncError())
	}
	if r.GetSetupConsentPending() {
		t.row("Setup steps:\t⚠ run-command consent pending\n")
	}
	return t.flush()
}

// renderWorktrees prints a table of worktrees with a reconciliation warning.
func renderWorktrees(w io.Writer, wts []*lumberjackv1.Worktree) error {
	if len(wts) == 0 {
		_, err := fmt.Fprintln(w, "No worktrees tracked for this repository.")
		return err
	}
	t := newTabW(w)
	t.row("DIRECTORY\tBRANCH\tPR\tLAST SYNCED\tSTATUS\n")
	for _, wt := range wts {
		pr := "-"
		if wt.GithubPrNumber != nil {
			pr = fmt.Sprintf("#%d", wt.GetGithubPrNumber())
		}
		t.row("%s\t%s\t%s\t%s\t%s\n",
			filepath.Base(wt.GetDirectoryPath()), wt.GetBranchName(), pr,
			timestamp(wt.GetLastSyncedAt()), worktreeStatus(wt))
	}
	return t.flush()
}

// renderWorktreeChanges prints a branch/PR/action table of the per-branch
// changes a sync or init made. It writes nothing when there are no changes.
func renderWorktreeChanges(w io.Writer, changes []*lumberjackv1.WorktreeChange) error {
	if len(changes) == 0 {
		return nil
	}
	t := newTabW(w)
	t.row("BRANCH\tPR\tACTION\n")
	for _, c := range changes {
		t.row("%s\t%s\t%s\n", c.GetBranch(), changePR(c), changeAction(c))
	}
	return t.flush()
}

// changePR renders a change's PR reference, "-" when it has none.
func changePR(c *lumberjackv1.WorktreeChange) string {
	if c.PrNumber == nil {
		return "-"
	}
	return fmt.Sprintf("#%d", c.GetPrNumber())
}

// changeAction renders the action verb, appending its detail in parentheses
// when present (e.g. why a worktree was retained).
func changeAction(c *lumberjackv1.WorktreeChange) string {
	verb := actionVerb(c.GetAction())
	if d := c.GetDetail(); d != "" {
		return verb + " (" + d + ")"
	}
	return verb
}

// actionVerb maps the wire action enum onto its display verb.
func actionVerb(a lumberjackv1.WorktreeAction) string {
	switch a {
	case lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT:
		return "checked out"
	case lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED:
		return "adopted"
	case lumberjackv1.WorktreeAction_WORKTREE_ACTION_UPDATED:
		return "updated"
	case lumberjackv1.WorktreeAction_WORKTREE_ACTION_DELETED:
		return "deleted"
	case lumberjackv1.WorktreeAction_WORKTREE_ACTION_RETAINED:
		return "retained"
	default:
		return "unknown"
	}
}

// worktreeStatus renders the reconciliation state, prefixing a warning marker
// when the worktree needs attention.
func worktreeStatus(wt *lumberjackv1.Worktree) string {
	note := wt.GetReconciliationNote()
	switch {
	case note == "":
		return "ok"
	case wt.GetNeedsReconciliation():
		return "⚠ " + note
	default:
		return note
	}
}

// syncStatus renders a repository's last sync status.
func syncStatus(r *lumberjackv1.Repository) string {
	switch r.GetLastSyncStatus() {
	case lumberjackv1.SyncStatus_SYNC_STATUS_OK:
		return "ok"
	case lumberjackv1.SyncStatus_SYNC_STATUS_ERROR:
		return "error"
	default:
		return "never synced"
	}
}

// timestamp renders a protobuf timestamp as a local time, or "never" when the
// timestamp is unset (a nil pointer).
func timestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return "never"
	}
	return ts.AsTime().Local().Format("2006-01-02 15:04")
}
