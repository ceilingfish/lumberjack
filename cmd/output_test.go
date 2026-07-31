package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type flakyWriter struct {
	succeed int
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

func (f *flakyWriter) Write(p []byte) (int, error) {
	if f.succeed == 0 {
		return 0, errWrite
	}
	f.succeed--
	return len(p), nil
}

func TestCmdDelete(t *testing.T) {
	serveService(t, &coverStub{deleteRepo: &lumberjackv1.DeleteRepositoryResponse{
		WorktreesRemoved: 2, Message: "stopped tracking n (2 worktree(s) removed)",
	}})

	out, err := run(t, "", "delete", "n")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "stopped tracking n") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdDeleteJSON(t *testing.T) {
	serveService(t, &coverStub{deleteRepo: &lumberjackv1.DeleteRepositoryResponse{
		WorktreesRemoved: 2, Message: "stopped tracking n",
	}})

	out, err := run(t, "", "--format", "json", "delete", "n")
	if err != nil {
		t.Fatalf("delete --format json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded["worktreesRemoved"] != "2" {
		t.Errorf("decoded = %+v, want the removed-worktree count", decoded)
	}
}

func TestCmdsSurfaceAnRPCFailure(t *testing.T) {
	for _, args := range [][]string{
		{"delete", "n"},
		{"init", "."},
		{"list"},
		{"status", "--repository", "n"},
		{"sync", "--repository", "n"},
		{"sync-all"},
		{"tidy", "--repository", "n"},
		{"worktrees", "--repository", "n"},
		{"worktree", "add", "feature/x", "--repository", "n"},
		{"worktree", "delete", "feature/x", "--repository", "n"},
		{"set-login", "work", "--repository", "n"},
		{"set-login", "--repository", "n"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			serveService(t, &coverStub{err: errors.New("boom")})
			if _, err := run(t, "", args...); err == nil {
				t.Errorf("%v succeeded against a failing daemon", args)
			}
		})
	}
}

func TestCmdsSurfaceAFailedWrite(t *testing.T) {
	stub := func() *coverStub {
		return &coverStub{
			repos:     []*lumberjackv1.Repository{{DirPrefix: "a", LocalPath: "/p/a"}},
			repo:      &lumberjackv1.Repository{DirPrefix: "n", LocalPath: "/p/n"},
			worktrees: []*lumberjackv1.Worktree{{DirectoryPath: "/p/n-x", BranchName: "feature/x"}},
			logins:    []string{"work"},
			addWT:     &lumberjackv1.AddWorktreeResponse{DirectoryPath: "/p/n-x", Branch: "feature/x"},
			deleteWT: []*lumberjackv1.DeleteWorktreeResponse{
				{Deleted: true, Message: "deleted n-x"},
			},
			deleteRepo: &lumberjackv1.DeleteRepositoryResponse{Message: "stopped tracking n"},
			tidyMoves: []*lumberjackv1.TidyMove{
				{Branch: "feature/x", From: "/elsewhere/x", To: "/p/n-x", Moved: true},
			},
			syncEvents: []*lumberjackv1.SyncResponse{
				{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{}},
			},
		}
	}

	for _, args := range [][]string{
		{"delete", "n"},
		{"init", "."},
		{"list"},
		{"status", "--repository", "n"},
		{"sync", "--repository", "n"},
		{"tidy", "--repository", "n"},
		{"worktrees", "--repository", "n"},
		{"worktree", "add", "feature/x", "--repository", "n"},
		{"worktree", "delete", "feature/x", "--repository", "n"},
		{"set-login", "work", "--repository", "n"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			serveService(t, stub())
			var errOut bytes.Buffer
			if err := runCmd(t, "", failWriter{}, &errOut, args...); !errors.Is(err, errWrite) {
				t.Errorf("%v: err = %v, want the failed write", args, err)
			}
		})
	}
}

func TestCmdSyncSurfacesAFailedProgressWrite(t *testing.T) {
	serveService(t, &coverStub{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Message: "creating worktree for PR #1"},
	}})

	var errOut bytes.Buffer
	err := runCmd(t, "", failWriter{}, &errOut, "sync", "--repository", "n")
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed progress write", err)
	}
}

func TestCmdSyncSurfacesAFailedJSONWrite(t *testing.T) {
	serveService(t, &coverStub{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{}},
	}})

	var errOut bytes.Buffer
	err := runCmd(t, "", failWriter{}, &errOut, "--format", "json", "sync", "--repository", "n")
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed JSON write", err)
	}
}

func TestCmdSyncSurfacesAFailedChangeTableWrite(t *testing.T) {
	pr := int64(1)
	serveService(t, &coverStub{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Change: &lumberjackv1.WorktreeChange{Branch: "feature/x", PrNumber: &pr}},
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{}},
	}})

	var errOut bytes.Buffer
	err := runCmd(t, "", failWriter{}, &errOut, "sync", "--repository", "n")
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed table write", err)
	}
}

func TestCmdInitSurfacesAFailedAdoptedTableWrite(t *testing.T) {
	serveService(t, &coverStub{adopted: []*lumberjackv1.WorktreeChange{
		{Branch: "feature/x", Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED},
	}})

	var errOut bytes.Buffer
	err := runCmd(t, "", &flakyWriter{succeed: 1}, &errOut, "init", ".")
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed adopted-table write", err)
	}
}

func TestCmdTidyJSON(t *testing.T) {
	serveService(t, &coverStub{tidyMoves: []*lumberjackv1.TidyMove{
		{Branch: "feature/x", From: "/elsewhere/x", To: "/p/n-x", Moved: true},
	}})

	out, err := run(t, "", "--format", "json", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy --format json: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if len(decoded) != 1 || decoded[0]["branch"] != "feature/x" {
		t.Errorf("decoded = %+v, want one move", decoded)
	}
}

func TestCmdTidyReportsAMovedWorktreeThatCouldNotBeFinished(t *testing.T) {
	serveService(t, &coverStub{tidyMoves: []*lumberjackv1.TidyMove{
		{Branch: "feature/x", From: "/elsewhere/x", To: "/p/n-x", Moved: true, Error: "lock not restored"},
	}})

	out, err := run(t, "", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy: %v", err)
	}
	if !strings.Contains(out, "moved: lock not restored") {
		t.Errorf("out = %q, want the partial-move warning", out)
	}
}

func TestCmdTidySurfacesAFailedProbe(t *testing.T) {
	serveService(t, &coverStub{err: errors.New("boom")})
	onATerminal(t)
	scriptTerminal(t, "s")

	if _, err := run(t, "", "tidy", "--repository", "n"); err == nil {
		t.Error("expected the dry-run probe failure to surface")
	}
}

func TestCmdTidySurfacesAFailedPrompt(t *testing.T) {
	serveService(t, &coverStub{tidyMoves: []*lumberjackv1.TidyMove{{
		Branch: "feature/x", From: "/elsewhere/x", To: "/p/n-x", Locked: true,
	}}})
	onATerminal(t)
	scriptTerminal(t)

	if _, err := run(t, "", "tidy", "--repository", "n"); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want the prompt's read failure", err)
	}
}

func TestCmdWorktreeAddJSON(t *testing.T) {
	serveService(t, &coverStub{addWT: &lumberjackv1.AddWorktreeResponse{
		DirectoryPath: "/p/n-x", Branch: "feature/x",
	}})

	out, err := run(t, "", "--format", "json", "worktree", "add", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("worktree add --format json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded["branch"] != "feature/x" {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestCmdWorktreeAddSurfacesAFailedWarningWrite(t *testing.T) {
	serveService(t, &coverStub{addWT: &lumberjackv1.AddWorktreeResponse{
		DirectoryPath: "/p/n-x", Branch: "feature/x", SetupError: "step 1 failed",
	}})

	var errOut bytes.Buffer
	err := runCmd(t, "", &flakyWriter{succeed: 1}, &errOut, "worktree", "add", "feature/x", "--repository", "n")
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed warning write", err)
	}
}

func TestCmdWorktreeDeleteReportsARefusalWithoutPrompting(t *testing.T) {
	serveService(t, &coverStub{deleteWT: []*lumberjackv1.DeleteWorktreeResponse{
		{Message: "worktree is not tracked here"},
	}})

	out, err := run(t, "", "worktree", "delete", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("worktree delete: %v", err)
	}
	if !strings.Contains(out, "not tracked") {
		t.Errorf("out = %q, want the daemon's message surfaced", out)
	}
}

func TestCmdWorktreeDeleteWithNoAnswerAborts(t *testing.T) {
	serveService(t, &coverStub{deleteWT: []*lumberjackv1.DeleteWorktreeResponse{
		{RequiresConfirmation: true, CommitsAtRisk: 3, Message: "3 local-only commit(s)"},
	}})

	out, err := run(t, "", "worktree", "delete", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("worktree delete: %v", err)
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("out = %q, want an abort when there is no answer to read", out)
	}
}

func TestCmdWorktreeDeleteSurfacesAFailedForcedRetry(t *testing.T) {
	serveService(t, &coverStub{deleteWT: []*lumberjackv1.DeleteWorktreeResponse{
		{RequiresConfirmation: true, CommitsAtRisk: 3, Message: "3 local-only commit(s)"},
	}})

	if _, err := run(t, "y\n", "worktree", "delete", "feature/x", "--repository", "n"); err == nil {
		t.Error("expected the forced retry's failure to surface")
	}
}

func TestCmdWorktreeDeleteSurfacesFailedConfirmationWrites(t *testing.T) {
	confirmed := []*lumberjackv1.DeleteWorktreeResponse{
		{RequiresConfirmation: true, CommitsAtRisk: 3, Message: "3 local-only commit(s)"},
		{Deleted: true, Message: "deleted n-x"},
	}
	cases := []struct {
		name    string
		stdin   string
		succeed int
	}{
		{name: "the warning", stdin: "", succeed: 0},
		{name: "the abort notice", stdin: "n\n", succeed: 2},
		{name: "the outcome", stdin: "y\n", succeed: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			serveService(t, &coverStub{deleteWT: confirmed})
			var errOut bytes.Buffer
			err := runCmd(t, c.stdin, &flakyWriter{succeed: c.succeed}, &errOut,
				"worktree", "delete", "feature/x", "--repository", "n")
			if !errors.Is(err, errWrite) {
				t.Errorf("err = %v, want the failed write", err)
			}
		})
	}
}

func TestCmdSetLoginJSON(t *testing.T) {
	serveService(t, &coverStub{})

	out, err := run(t, "", "--format", "json", "set-login", "work", "--repository", "n")
	if err != nil {
		t.Fatalf("set-login --format json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if !strings.Contains(decoded["message"].(string), "work") {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestCmdStatusSurfacesFailedConsentWrites(t *testing.T) {
	consent := &lumberjackv1.GetSetupConsentResponse{Pending: true, RunCommands: []string{"make setup"}}
	cases := []struct {
		name    string
		stdin   string
		succeed int
	}{
		{name: "the preamble", stdin: "", succeed: 1},
		{name: "the command list", stdin: "", succeed: 2},
		{name: "the refusal", stdin: "n\n", succeed: 4},
		{name: "the confirmation", stdin: "y\n", succeed: 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			serveService(t, &coverStub{
				repo:    &lumberjackv1.Repository{DirPrefix: "n", SetupConsentPending: true},
				consent: consent,
			})
			var errOut bytes.Buffer
			err := runCmd(t, c.stdin, &flakyWriter{succeed: c.succeed}, &errOut, "status", "--repository", "n")
			if !errors.Is(err, errWrite) {
				t.Errorf("err = %v, want the failed write", err)
			}
		})
	}
}

func TestCmdStatusSurfacesAFailedConsentLookup(t *testing.T) {
	serveService(t, &coverStub{
		repo:       &lumberjackv1.Repository{DirPrefix: "n", SetupConsentPending: true},
		consentErr: errors.New("boom"),
	})

	if _, err := run(t, "", "status", "--repository", "n"); err == nil {
		t.Error("expected the consent lookup failure to surface")
	}
}

func TestCmdStatusRendersEveryDetailField(t *testing.T) {
	syncedAt := time.Date(2024, 5, 6, 7, 8, 0, 0, time.Local)
	lastErr := "gh rate limited"
	serveService(t, &coverStub{repo: &lumberjackv1.Repository{
		DirPrefix: "n", LocalPath: "/p/n", GithubOwner: "o", GithubName: "n", Host: "github.com",
		Login: "work", LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_ERROR,
		LastSyncError: &lastErr, LastSyncedAt: timestamppb.New(syncedAt),
	}})

	out, err := run(t, "", "status", "--repository", "n")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"Login:", "work", "Last error:", "gh rate limited", "error", "2024-"} {
		if !strings.Contains(out, want) {
			t.Errorf("out %q missing %q", out, want)
		}
	}
}

func TestCmdWorktreesReportsANoteWithoutAWarning(t *testing.T) {
	serveService(t, &coverStub{worktrees: []*lumberjackv1.Worktree{{
		DirectoryPath: "/p/n-x", BranchName: "feature/x",
		ReconciliationNote: "branch merged", NeedsReconciliation: false,
	}}})

	out, err := run(t, "", "worktrees", "--repository", "n")
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	if !strings.Contains(out, "branch merged") {
		t.Errorf("out = %q, want the note", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("out = %q, want no warning marker for a note that needs no action", out)
	}
}

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

func TestRawTerminalWithoutATerminal(t *testing.T) {
	if _, _, err := rawTerminal(); !errors.Is(err, errNoTerminal) {
		t.Errorf("rawTerminal err = %v, want errNoTerminal under `go test`", err)
	}
}

func TestReinstallDaemonSurfacesAFailedInstall(t *testing.T) {
	err := reinstallDaemon(&fakeLifecycle{installErr: errors.New("boom")})
	if err == nil || !strings.Contains(err.Error(), "installing daemon") {
		t.Errorf("err = %v, want the wrapped install failure", err)
	}
}

func TestInstallCLISurfacesAnUnreadableDestination(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(io.Discard, blocked, blocked, false); err == nil ||
		!strings.Contains(err.Error(), "checking") {
		t.Errorf("err = %v, want the stat failure", err)
	}
}

func TestSetupStepsJSONAndFailedWrites(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "add", args: []string{"setup-steps", "add", "echo hi"}},
		{name: "add duplicate", args: []string{"setup-steps", "add", "true"}},
		{name: "remove", args: []string{"setup-steps", "remove", "true"}},
		{name: "run", args: []string{"setup-steps", "run"}},
	}
	for _, c := range cases {
		t.Run(c.name+" json", func(t *testing.T) {
			setupRepo(t)
			if _, err := run(t, "", "setup-steps", "add", "true"); err != nil {
				t.Fatal(err)
			}
			out, err := run(t, "", append([]string{"--format", "json"}, c.args...)...)
			if err != nil {
				t.Fatalf("%v: %v", c.args, err)
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(out), &decoded); err != nil {
				t.Fatalf("output is not valid JSON: %v (%q)", err, out)
			}
			if decoded["message"] == nil {
				t.Errorf("decoded = %+v, want a message", decoded)
			}
		})
		t.Run(c.name+" failed write", func(t *testing.T) {
			setupRepo(t)
			if _, err := run(t, "", "setup-steps", "add", "true"); err != nil {
				t.Fatal(err)
			}
			var errOut bytes.Buffer
			if err := runCmd(t, "", failWriter{}, &errOut, c.args...); !errors.Is(err, errWrite) {
				t.Errorf("%v: err = %v, want the failed write", c.args, err)
			}
		})
	}
}

func TestCmdSetupListSurfacesAFailedWrite(t *testing.T) {
	setupRepo(t)
	if _, err := run(t, "", "setup-steps", "add", "make setup"); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	if err := runCmd(t, "", failWriter{}, &errOut, "setup-steps", "list"); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}

func TestCmdSetupRunSurfacesAFailedInheritanceNotice(t *testing.T) {
	setupLinkedWorktree(t, "steps:\n  - type: run-command\n    run_command:\n      command: true\n")
	var out bytes.Buffer
	if err := runCmd(t, "", &out, failWriter{}, "setup-steps", "run"); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed progress write", err)
	}
}

func TestWriteSetupMessageJSON(t *testing.T) {
	setupRepo(t)
	out, err := run(t, "", "--format", "json", "setup-steps", "run")
	if err != nil {
		t.Fatalf("setup-steps run --format json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded["message"] != "No setup steps configured." {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestEmitTidyMovesFormats(t *testing.T) {
	moves := []*lumberjackv1.TidyMove{{Branch: "feature/x", From: "/a", To: "/b"}}

	var jsonOut bytes.Buffer
	if err := emitTidyMoves(&jsonOut, present.JSON, moves, true); err != nil {
		t.Fatalf("emitTidyMoves json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(jsonOut.String()), "[") {
		t.Errorf("out = %q, want a bare JSON array", jsonOut.String())
	}

	var textOut bytes.Buffer
	if err := emitTidyMoves(&textOut, present.Structured, moves, true); err != nil {
		t.Fatalf("emitTidyMoves structured: %v", err)
	}
	if !strings.Contains(textOut.String(), "would move") {
		t.Errorf("out = %q, want the dry-run verb", textOut.String())
	}
}

func dialStub(t *testing.T) *client.Client {
	t.Helper()
	cl, err := client.Dial()
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

func TestPromptSetupConsentSurfacesFailedWrites(t *testing.T) {
	cases := []struct {
		name    string
		succeed int
	}{
		{name: "the preamble", succeed: 0},
		{name: "the command list", succeed: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			serveService(t, &coverStub{consent: &lumberjackv1.GetSetupConsentResponse{
				Pending: true, RunCommands: []string{"make setup"},
			}})
			cmd := &cobra.Command{}
			cmd.SetOut(&flakyWriter{succeed: c.succeed})
			cmd.SetErr(io.Discard)
			cmd.SetIn(strings.NewReader("y\n"))

			err := promptSetupConsent(context.Background(), cmd, dialStub(t), "n")
			if !errors.Is(err, errWrite) {
				t.Errorf("err = %v, want the failed write", err)
			}
		})
	}
}

func TestPromptSetupConsentSurfacesAFailedRecord(t *testing.T) {
	serveService(t, &coverStub{
		consent: &lumberjackv1.GetSetupConsentResponse{
			Pending: true, RunCommands: []string{"make setup"},
		},
		setConsentErr: errors.New("boom"),
	})
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader("y\n"))

	if err := promptSetupConsent(context.Background(), cmd, dialStub(t), "n"); err == nil {
		t.Error("expected the failed consent record to surface")
	}
}

func TestRunInstallDefaultsTheBinDirToTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
	exe := filepath.Join(t.TempDir(), "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInstall(&out, installOptions{exe: exe, cliOnly: true}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	installed := filepath.Join(home, ".local", "bin", cliBinaryName)
	if _, err := os.Stat(installed); err != nil {
		t.Errorf("expected the CLI at %s: %v", installed, err)
	}

	out.Reset()
	if err := runUninstall(&out, uninstallOptions{daemonOnly: false, cliOnly: true}); err != nil {
		t.Fatalf("runUninstall: %v", err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("expected the CLI to be removed, got %v", err)
	}
}

func TestRunInstallWarnsWhenTheBinDirIsNotOnPath(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "lumberjack")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin")

	err := runInstall(&flakyWriter{succeed: 1}, installOptions{exe: exe, binDir: t.TempDir(), cliOnly: true})
	if !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed PATH-warning write", err)
	}
}

func TestInstallCLISurfacesAFailedCopy(t *testing.T) {
	_, err := installCLI(io.Discard, filepath.Join(t.TempDir(), "absent"), t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "copying CLI binary") {
		t.Errorf("err = %v, want the wrapped copy failure", err)
	}
}

func TestSetupStepsSurfaceAFailedSave(t *testing.T) {
	dir := setupRepo(t)
	if _, err := run(t, "", "setup-steps", "add", "true"); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, ".lumberjack.yml")
	if err := os.Chmod(config, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(config, 0o600) })

	if _, err := run(t, "", "setup-steps", "add", "echo hi"); err == nil {
		t.Error("expected setup-steps add to fail when the config cannot be written")
	}
	if _, err := run(t, "", "setup-steps", "remove", "true"); err == nil {
		t.Error("expected setup-steps remove to fail when the config cannot be written")
	}
}
