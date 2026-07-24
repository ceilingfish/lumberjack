package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/color"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// renderRepositories prints a table of repositories.
func renderRepositories(w io.Writer, repos []*lumberjackv1.Repository) error {
	if len(repos) == 0 {
		_, err := fmt.Fprintln(w, "No repositories tracked. Add one with `lumberjack init .`")
		return err
	}
	t := color.NewTable(w)
	t.Row("NAME\tPATH\tLAST SYNCED\tSTATUS\n")
	for _, r := range repos {
		t.Row("%s\t%s\t%s\t%s\n",
			r.GetDirPrefix(), t.Paint(color.Path, r.GetLocalPath()),
			paintTimestamp(t, r.GetLastSyncedAt()), paintSyncStatus(t, r))
	}
	return t.Flush()
}

// renderRepositoryDetail prints the last-sync detail for one repository.
func renderRepositoryDetail(w io.Writer, r *lumberjackv1.Repository) error {
	t := color.NewTable(w)
	t.Row("Name:\t%s\n", r.GetDirPrefix())
	t.Row("Path:\t%s\n", t.Paint(color.Path, r.GetLocalPath()))
	t.Row("GitHub:\t%s/%s (%s)\n", r.GetGithubOwner(), r.GetGithubName(), r.GetHost())
	if r.GetLogin() != "" {
		t.Row("Login:\t%s\n", r.GetLogin())
	}
	t.Row("Worktrees dir:\t%s\n", t.Paint(color.Path, r.GetWorktreeParentDir()))
	t.Row("Last synced:\t%s\n", paintTimestamp(t, r.GetLastSyncedAt()))
	t.Row("Status:\t%s\n", paintSyncStatus(t, r))
	if r.GetLastSyncError() != "" {
		t.Row("Last error:\t%s\n", r.GetLastSyncError())
	}
	if r.GetSetupConsentPending() {
		t.Row("Setup steps:\t⚠ run-command consent pending\n")
	}
	return t.Flush()
}

// renderWorktrees prints a table of worktrees with a reconciliation warning.
func renderWorktrees(w io.Writer, wts []*lumberjackv1.Worktree) error {
	if len(wts) == 0 {
		_, err := fmt.Fprintln(w, "No worktrees tracked for this repository.")
		return err
	}
	t := color.NewTable(w)
	t.Row("DIRECTORY\tBRANCH\tPR\tLAST SYNCED\tSTATUS\n")
	for _, wt := range wts {
		pr := "-"
		if wt.GithubPrNumber != nil {
			pr = fmt.Sprintf("#%d", wt.GetGithubPrNumber())
		}
		t.Row("%s\t%s\t%s\t%s\t%s\n",
			t.Paint(color.Path, filepath.Base(wt.GetDirectoryPath())),
			t.Paint(color.Branch, wt.GetBranchName()),
			paintDash(t, pr),
			paintTimestamp(t, wt.GetLastSyncedAt()), paintWorktreeStatus(t, wt))
	}
	return t.Flush()
}

// renderWorktreeChanges prints a branch/PR/action table of the per-branch
// changes a sync or init made. It writes nothing when there are no changes.
func renderWorktreeChanges(w io.Writer, changes []*lumberjackv1.WorktreeChange) error {
	if len(changes) == 0 {
		return nil
	}
	t := color.NewTable(w)
	t.Row("BRANCH\tPR\tACTION\n")
	for _, c := range changes {
		t.Row("%s\t%s\t%s\n",
			t.Paint(color.Branch, c.GetBranch()),
			paintDash(t, changePR(c)),
			t.Paint(color.Action, changeAction(c)))
	}
	return t.Flush()
}

// paintDash dims s when it is the "-" placeholder, leaving any other value
// (e.g. a PR reference) unstyled.
func paintDash(t *color.Table, s string) string {
	if s != "-" {
		return s
	}
	return t.Paint(color.Dim, s)
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

// paintWorktreeStatus colourises worktreeStatus's definite states (ok green,
// ⚠ warning yellow); an informational note with neither marker is left
// unstyled since it's neither clearly ok nor clearly a problem.
func paintWorktreeStatus(t *color.Table, wt *lumberjackv1.Worktree) string {
	s := worktreeStatus(wt)
	switch {
	case s == "ok":
		return t.Paint(color.OK, s)
	case strings.HasPrefix(s, "⚠"):
		return t.Paint(color.Warning, s)
	default:
		return s
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

// paintSyncStatus colourises syncStatus: ok green, error red, and the
// de-emphasised "never synced" dim.
func paintSyncStatus(t *color.Table, r *lumberjackv1.Repository) string {
	s := syncStatus(r)
	switch s {
	case "ok":
		return t.Paint(color.OK, s)
	case "error":
		return t.Paint(color.Error, s)
	default:
		return t.Paint(color.Dim, s)
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

// paintTimestamp colourises timestamp's "never" placeholder as de-emphasised;
// an actual timestamp is left unstyled (ordinary text).
func paintTimestamp(t *color.Table, ts *timestamppb.Timestamp) string {
	s := timestamp(ts)
	if ts == nil {
		return t.Paint(color.Dim, s)
	}
	return s
}
