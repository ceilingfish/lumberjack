package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/present"
	"google.golang.org/protobuf/types/known/timestamppb"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

var errWrite = errors.New("write failed")

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errWrite }

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

func TestActionVerb(t *testing.T) {
	cases := map[lumberjackv1.WorktreeAction]string{
		lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT: "checked out",
		lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED:     "adopted",
		lumberjackv1.WorktreeAction_WORKTREE_ACTION_UPDATED:     "updated",
		lumberjackv1.WorktreeAction_WORKTREE_ACTION_DELETED:     "deleted",
		lumberjackv1.WorktreeAction_WORKTREE_ACTION_RETAINED:    "retained",
		lumberjackv1.WorktreeAction_WORKTREE_ACTION_UNSPECIFIED: "unknown",
	}
	for action, want := range cases {
		if got := actionVerb(action); got != want {
			t.Errorf("actionVerb(%v) = %q, want %q", action, got, want)
		}
	}
}

func TestChangeActionIncludesItsDetail(t *testing.T) {
	got := changeAction(&lumberjackv1.WorktreeChange{
		Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_RETAINED,
		Detail: "uncommitted changes",
	}, false)
	if got != "retained (uncommitted changes)" {
		t.Errorf("changeAction = %q", got)
	}
}

func TestTabWStopsAtTheFirstWriteError(t *testing.T) {
	tw := newTabW(failWriter{})
	tw.row("a\f")
	if !errors.Is(tw.err, errWrite) {
		t.Fatalf("tabW.err = %v, want the failed write recorded", tw.err)
	}
	tw.row("b\f")
	if err := tw.flush(); !errors.Is(err, errWrite) {
		t.Errorf("flush = %v, want the first error", err)
	}
}

func TestReadLockAnswerIgnoresAnEmptyRead(t *testing.T) {
	got, err := readLockAnswer(emptyReader{})
	if err != nil {
		t.Fatalf("readLockAnswer: %v", err)
	}
	if got != lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED {
		t.Errorf("readLockAnswer = %v, want UNSPECIFIED for an empty read", got)
	}
}

func repoFixture() *lumberjackv1.Repository {
	return &lumberjackv1.Repository{
		DirPrefix: "n", LocalPath: "/repo", GithubOwner: "o", GithubName: "n",
		Host: "github.com", Login: "me", WorktreeParentDir: "/wt",
		LastSyncedAt:   timestamppb.New(time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)),
		LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_OK,
	}
}

func TestEmitRepositoriesRendersATableAndJSON(t *testing.T) {
	repos := []*lumberjackv1.Repository{repoFixture()}

	var table bytes.Buffer
	if err := EmitRepositories(&table, present.Structured, repos); err != nil {
		t.Fatalf("EmitRepositories: %v", err)
	}
	for _, want := range []string{"NAME", "PATH", "LAST SYNCED", "STATUS", "n", "/repo", "ok"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("table = %q, want it to contain %q", table.String(), want)
		}
	}

	var raw bytes.Buffer
	if err := EmitRepositories(&raw, present.JSON, repos); err != nil {
		t.Fatalf("EmitRepositories json: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw.Bytes(), &decoded); err != nil {
		t.Fatalf("json = %q: %v", raw.String(), err)
	}
	if len(decoded) != 1 {
		t.Errorf("decoded %d repositories, want 1", len(decoded))
	}
}

func TestEmitRepositoriesWithNoneExplainsHowToAddOne(t *testing.T) {
	var out bytes.Buffer
	if err := EmitRepositories(&out, present.Structured, nil); err != nil {
		t.Fatalf("EmitRepositories: %v", err)
	}
	if !strings.Contains(out.String(), "lumberjack init .") {
		t.Errorf("out = %q, want the empty-state hint", out.String())
	}
}

func TestEmitRepositoryDetailCoversEveryOptionalField(t *testing.T) {
	r := repoFixture()
	r.LastSyncStatus = lumberjackv1.SyncStatus_SYNC_STATUS_ERROR
	boom := "boom"
	r.LastSyncError = &boom
	r.SetupSteps = &lumberjackv1.SetupSteps{IsDefined: true, Steps: []string{"make"}}

	var out bytes.Buffer
	if err := EmitRepositoryDetail(&out, present.Color, r); err != nil {
		t.Fatalf("EmitRepositoryDetail: %v", err)
	}
	for _, want := range []string{"Name:", "Login:", "Last error:", "boom", "consent pending"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}

	var raw bytes.Buffer
	if err := EmitRepositoryDetail(&raw, present.JSON, r); err != nil {
		t.Fatalf("EmitRepositoryDetail json: %v", err)
	}
	if !strings.Contains(raw.String(), `"dirPrefix"`) {
		t.Errorf("json = %q, want the proto rendering", raw.String())
	}
}

func TestEmitWorktrees(t *testing.T) {
	pr := int64(7)
	wts := []*lumberjackv1.Worktree{
		{DirectoryPath: "/wt/a", BranchName: "a", GithubPrNumber: &pr},
		{DirectoryPath: "/wt/b", BranchName: "b", ReconciliationNote: "drifted", NeedsReconciliation: true},
		{DirectoryPath: "/wt/c", BranchName: "c", ReconciliationNote: "noted"},
	}
	var out bytes.Buffer
	if err := EmitWorktrees(&out, present.Structured, wts); err != nil {
		t.Fatalf("EmitWorktrees: %v", err)
	}
	for _, want := range []string{"DIRECTORY", "#7", "-", "⚠ drifted", "noted", "ok"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}

	var empty bytes.Buffer
	if err := EmitWorktrees(&empty, present.Structured, nil); err != nil {
		t.Fatalf("EmitWorktrees empty: %v", err)
	}
	if !strings.Contains(empty.String(), "No worktrees tracked") {
		t.Errorf("out = %q, want the empty state", empty.String())
	}

	var raw bytes.Buffer
	if err := EmitWorktrees(&raw, present.JSON, wts); err != nil {
		t.Fatalf("EmitWorktrees json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(raw.String()), "[") {
		t.Errorf("json = %q, want a bare array", raw.String())
	}
}

func TestEmitTidyMovesRendersEveryResult(t *testing.T) {
	moves := []*lumberjackv1.TidyMove{
		{Branch: "a", From: "/x/a", To: "/y/a", Moved: true},
		{Branch: "b", From: "/x/b", To: "/y/b", Moved: true, Error: "relock failed"},
		{Branch: "c", From: "/x/c", To: "/y/c", Error: "locked"},
	}
	var out bytes.Buffer
	if err := EmitTidyMoves(&out, present.Structured, moves, false); err != nil {
		t.Fatalf("EmitTidyMoves: %v", err)
	}
	for _, want := range []string{"BRANCH", "moved", "⚠ moved: relock failed", "⚠ skipped: locked"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}

	var dry bytes.Buffer
	if err := EmitTidyMoves(&dry, present.Structured, moves[:1], true); err != nil {
		t.Fatalf("EmitTidyMoves dry run: %v", err)
	}
	if !strings.Contains(dry.String(), "would move") {
		t.Errorf("out = %q, want the dry-run verb", dry.String())
	}

	var empty bytes.Buffer
	if err := EmitTidyMoves(&empty, present.Structured, nil, false); err != nil {
		t.Fatalf("EmitTidyMoves empty: %v", err)
	}
	if !strings.Contains(empty.String(), "idiomatic locations") {
		t.Errorf("out = %q, want the empty state", empty.String())
	}

	var raw bytes.Buffer
	if err := EmitTidyMoves(&raw, present.JSON, moves, false); err != nil {
		t.Fatalf("EmitTidyMoves json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(raw.String()), "[") {
		t.Errorf("json = %q, want a bare array", raw.String())
	}
}

func TestRenderWorktreeChanges(t *testing.T) {
	pr := int64(3)
	changes := []*lumberjackv1.WorktreeChange{
		{Branch: "a", PrNumber: &pr, Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT},
		{Branch: "b", Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_DELETED},
	}
	var out bytes.Buffer
	if err := RenderWorktreeChanges(&out, changes, false); err != nil {
		t.Fatalf("RenderWorktreeChanges: %v", err)
	}
	for _, want := range []string{"BRANCH", "#3", "checked out", "deleted"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}

	var none bytes.Buffer
	if err := RenderWorktreeChanges(&none, nil, false); err != nil {
		t.Fatalf("RenderWorktreeChanges empty: %v", err)
	}
	if none.Len() != 0 {
		t.Errorf("out = %q, want nothing written for no changes", none.String())
	}
}

func TestSyncStatusCoversEveryState(t *testing.T) {
	cases := map[lumberjackv1.SyncStatus]string{
		lumberjackv1.SyncStatus_SYNC_STATUS_OK:          "ok",
		lumberjackv1.SyncStatus_SYNC_STATUS_ERROR:       "error",
		lumberjackv1.SyncStatus_SYNC_STATUS_UNSPECIFIED: "never synced",
	}
	for status, want := range cases {
		got := syncStatus(&lumberjackv1.Repository{LastSyncStatus: status}, false)
		if got != want {
			t.Errorf("syncStatus(%v) = %q, want %q", status, got, want)
		}
	}
}

func TestTimestampRendersNeverWhenUnset(t *testing.T) {
	if got := timestamp(nil, false); got != "never" {
		t.Errorf("timestamp(nil) = %q, want %q", got, "never")
	}
	ts := timestamppb.New(time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC))
	if got := timestamp(ts, false); !strings.Contains(got, "2026-03-01") {
		t.Errorf("timestamp = %q, want the local date", got)
	}
}

func TestRenderersSurfaceAFailedWrite(t *testing.T) {
	repos := []*lumberjackv1.Repository{repoFixture()}
	wts := []*lumberjackv1.Worktree{{DirectoryPath: "/wt/a", BranchName: "a"}}
	moves := []*lumberjackv1.TidyMove{{Branch: "a", From: "/x", To: "/y", Moved: true}}
	changes := []*lumberjackv1.WorktreeChange{{Branch: "a"}}

	cases := map[string]func(w io.Writer) error{
		"repositories":      func(w io.Writer) error { return EmitRepositories(w, present.Structured, repos) },
		"no repositories":   func(w io.Writer) error { return EmitRepositories(w, present.Structured, nil) },
		"repository detail": func(w io.Writer) error { return EmitRepositoryDetail(w, present.Structured, repoFixture()) },
		"worktrees":         func(w io.Writer) error { return EmitWorktrees(w, present.Structured, wts) },
		"no worktrees":      func(w io.Writer) error { return EmitWorktrees(w, present.Structured, nil) },
		"tidy moves":        func(w io.Writer) error { return EmitTidyMoves(w, present.Structured, moves, false) },
		"no tidy moves":     func(w io.Writer) error { return EmitTidyMoves(w, present.Structured, nil, false) },
		"changes":           func(w io.Writer) error { return RenderWorktreeChanges(w, changes, false) },
	}
	for name, render := range cases {
		if err := render(failWriter{}); !errors.Is(err, errWrite) {
			t.Errorf("%s: err = %v, want the failed write", name, err)
		}
	}
}

type flakyWriter struct {
	succeed int
}

func (f *flakyWriter) Write(p []byte) (int, error) {
	if f.succeed == 0 {
		return 0, errWrite
	}
	f.succeed--
	return len(p), nil
}
