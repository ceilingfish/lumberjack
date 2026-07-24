package cmd

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/color"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// withColorEnabled forces color.Enabled for the duration of the test.
func withColorEnabled(t *testing.T, v bool) {
	t.Helper()
	prev := color.Enabled
	color.Enabled = func() bool { return v }
	t.Cleanup(func() { color.Enabled = prev })
}

func sampleRepos() []*lumberjackv1.Repository {
	return []*lumberjackv1.Repository{
		{
			DirPrefix: "lumberjack", LocalPath: "/Users/x/code/lumberjack",
			LastSyncedAt: timestamppb.New(timestamppb.Now().AsTime()), LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_OK,
		},
		{
			DirPrefix: "a-much-longer-repo-name", LocalPath: "/x",
			LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_ERROR,
		},
		{
			DirPrefix: "n", LocalPath: "/y/z",
			LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_UNSPECIFIED,
		},
	}
}

func TestRenderRepositoriesColorDisabledUnchanged(t *testing.T) {
	withColorEnabled(t, false)
	repos := sampleRepos()

	var got bytes.Buffer
	if err := renderRepositories(&got, repos); err != nil {
		t.Fatalf("renderRepositories: %v", err)
	}
	if ansiRE.MatchString(got.String()) {
		t.Errorf("expected no ANSI codes when colour disabled, got %q", got.String())
	}
	for _, want := range []string{"lumberjack", "/Users/x/code/lumberjack", "ok", "error", "never synced"} {
		if !bytes.Contains(got.Bytes(), []byte(want)) {
			t.Errorf("expected output to contain %q, got %q", want, got.String())
		}
	}
}

func TestRenderRepositoriesColorEnabledStaysAligned(t *testing.T) {
	repos := sampleRepos()

	withColorEnabled(t, false)
	var plain bytes.Buffer
	if err := renderRepositories(&plain, repos); err != nil {
		t.Fatalf("renderRepositories (plain): %v", err)
	}

	withColorEnabled(t, true)
	var colored bytes.Buffer
	if err := renderRepositories(&colored, repos); err != nil {
		t.Fatalf("renderRepositories (colour): %v", err)
	}

	if !ansiRE.MatchString(colored.String()) {
		t.Fatalf("expected ANSI codes when colour enabled, got %q", colored.String())
	}
	stripped := ansiRE.ReplaceAllString(colored.String(), "")
	if stripped != plain.String() {
		t.Errorf("colourised table misaligned once ANSI codes are stripped:\ngot:  %q\nwant: %q", stripped, plain.String())
	}
}

func TestRenderWorktreesColorEnabledStaysAlignedWithMixedPRColumn(t *testing.T) {
	num1 := int64(42)
	wts := []*lumberjackv1.Worktree{
		{DirectoryPath: "/repo/wt/feature-a", BranchName: "feature-a", GithubPrNumber: &num1},
		{DirectoryPath: "/repo/wt/a-much-longer-branch-name", BranchName: "a-much-longer-branch-name"},
	}

	withColorEnabled(t, false)
	var plain bytes.Buffer
	if err := renderWorktrees(&plain, wts); err != nil {
		t.Fatalf("renderWorktrees (plain): %v", err)
	}

	withColorEnabled(t, true)
	var colored bytes.Buffer
	if err := renderWorktrees(&colored, wts); err != nil {
		t.Fatalf("renderWorktrees (colour): %v", err)
	}

	stripped := ansiRE.ReplaceAllString(colored.String(), "")
	if stripped != plain.String() {
		t.Errorf("colourised worktrees table misaligned once ANSI codes are stripped:\ngot:  %q\nwant: %q", stripped, plain.String())
	}
}
