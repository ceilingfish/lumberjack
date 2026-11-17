package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/ceilingfish/lumberjack/internal/present"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

var AuthorshipArgs = []string{"mine", "theirs", "auto"}

func ParseAuthorship(arg string) (*lumberjackv1.Authorship, error) {
	switch arg {
	case "mine":
		a := lumberjackv1.Authorship_AUTHORSHIP_MINE
		return &a, nil
	case "theirs":
		a := lumberjackv1.Authorship_AUTHORSHIP_THEIRS
		return &a, nil
	case "auto":
		return nil, nil
	}
	return nil, fmt.Errorf("authorship must be mine, theirs or auto, not %q", arg)
}

func authorshipName(wt *lumberjackv1.Worktree) string {
	switch wt.GetAuthorship() {
	case lumberjackv1.Authorship_AUTHORSHIP_MINE:
		return "mine"
	case lumberjackv1.Authorship_AUTHORSHIP_THEIRS:
		return "theirs"
	}
	return "-"
}

func authorshipLabel(wt *lumberjackv1.Worktree) string {
	label := authorshipName(wt)
	if wt.GetAuthorshipOverridden() {
		label += " (set)"
	}
	return label
}

func EmitAuthorship(w io.Writer, format present.Format, wt *lumberjackv1.Worktree) error {
	if format == present.JSON {
		return present.WriteJSONObject(w, wt)
	}
	how := "derived"
	if wt.GetAuthorshipOverridden() {
		how = "set by hand"
	}
	color := format == present.Color
	_, err := fmt.Fprintf(w, "%s is now %s (%s)\n",
		present.Branch(wt.GetBranchName(), color), authorshipName(wt), how)
	return err
}

func pullRequestSummary(pr *lumberjackv1.PullRequestState) string {
	if pr == nil {
		return "-"
	}
	var parts []string
	add := func(cond bool, s string) {
		if cond {
			parts = append(parts, s)
		}
	}
	add(pr.GetHasNewActivity(), "new")
	add(pr.GetIsDraft(), "draft")
	add(pr.GetMergeStateStatus() == lumberjackv1.MergeStateStatus_MERGE_STATE_STATUS_DIRTY, "conflicts")
	add(pr.GetChecks().GetFailing() > 0, fmt.Sprintf("%d failing", pr.GetChecks().GetFailing()))
	add(pr.GetReviewDecision() == lumberjackv1.ReviewDecision_REVIEW_DECISION_CHANGES_REQUESTED, "changes requested")
	add(pr.GetReviewDecision() == lumberjackv1.ReviewDecision_REVIEW_DECISION_APPROVED, "approved")
	add(pr.GetUnresolvedThreadCount() > 0, fmt.Sprintf("%d unresolved", pr.GetUnresolvedThreadCount()))
	add(pr.GetReviewRequestedFromMe(), "review requested")
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}
