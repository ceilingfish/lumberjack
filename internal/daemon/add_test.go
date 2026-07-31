package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/setup"
)

func TestAddWorktreeChecksOutExistingBranch(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/#325080-accept-suggestions")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	want := filepath.Join(h.parent, "n-accept-suggestions")
	if res.DirectoryPath != want {
		t.Errorf("DirectoryPath = %q, want %q", res.DirectoryPath, want)
	}
	if res.BranchCreated {
		t.Error("BranchCreated = true, want false when git could check the branch out")
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("worktree directory not created: %v", err)
	}

	wts, err := h.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil || len(wts) != 1 {
		t.Fatalf("ListWorktrees: %v (%d)", err, len(wts))
	}
	if wts[0].BranchName != "feature/#325080-accept-suggestions" {
		t.Errorf("BranchName = %q", wts[0].BranchName)
	}
	if wts[0].GithubPRNumber != nil {
		t.Errorf("GithubPRNumber = %d, want nil (sync links a PR later)", *wts[0].GithubPRNumber)
	}
}

func TestAddWorktreeCreatesBranchOffDefaultBranch(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// git can neither track a remote branch nor check out a local one.
	h.git.addErr["feature/new"] = errors.New("invalid reference: origin/feature/new")

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/new")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if !res.BranchCreated {
		t.Error("BranchCreated = false, want true for a branch that exists nowhere")
	}
	if got := h.git.newBranches["feature/new"]; got != "origin/main" {
		t.Errorf("branched off %q, want origin/main", got)
	}
	if _, err := os.Stat(res.DirectoryPath); err != nil {
		t.Errorf("worktree directory not created: %v", err)
	}
}

func TestAddWorktreeRejectsBranchAlreadyCheckedOut(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if _, err := h.svc.AddWorktree(context.Background(), repo, "feature/a"); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}

	_, err := h.svc.AddWorktree(context.Background(), repo, "feature/a")
	if err == nil || !strings.Contains(err.Error(), "already checked out") {
		t.Fatalf("second AddWorktree error = %v, want an already-checked-out error", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Errorf("tracked worktrees = %d, want 1", len(wts))
	}
}

func TestAddWorktreeFailsWhenGitCannotCreateIt(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.addErr["feature/a"] = errors.New("no such branch")
	h.git.newBranchErr["feature/a"] = errors.New("permission denied")

	if _, err := h.svc.AddWorktree(context.Background(), repo, "feature/a"); err == nil {
		t.Fatal("AddWorktree succeeded, want the git failure surfaced")
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 0 {
		t.Errorf("tracked worktrees = %d, want 0 when creation failed", len(wts))
	}
}

func TestAddWorktreeRunsSetupStepsAndReportsFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if err := os.MkdirAll(repo.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env
      destination: .env
`),
	}

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/a")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	// The source file is absent, so the copy-file step fails — the worktree is
	// still created and tracked, with the failure reported inline.
	if res.SetupError == "" {
		t.Error("SetupError is empty, want the failing step reported")
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("tracked worktrees = %d, want 1 (a setup failure keeps the worktree)", len(wts))
	}
	if wts[0].SetupError == nil {
		t.Error("worktree row SetupError = nil, want the failure recorded")
	}
}

func TestAddWorktreeRunsSetupStepsCopyingFiles(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if err := os.MkdirAll(repo.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.LocalPath, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env
      destination: .env
`),
	}

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/a")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if res.SetupError != "" {
		t.Fatalf("SetupError = %q, want none", res.SetupError)
	}
	got, err := os.ReadFile(filepath.Join(res.DirectoryPath, ".env"))
	if err != nil {
		t.Fatalf("reading copied file: %v", err)
	}
	if string(got) != "SECRET=1\n" {
		t.Errorf("copied content = %q", got)
	}
}

func TestAddWorktreeRejectsExistingDirectory(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if err := os.MkdirAll(filepath.Join(h.parent, "n-a"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := h.svc.AddWorktree(context.Background(), repo, "feature/a")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("AddWorktree error = %v, want an already-exists error", err)
	}
}

func TestAddWorktreePublishesChange(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	events, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	if _, err := h.svc.AddWorktree(context.Background(), repo, "feature/a"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	ev := <-events
	if ev.Type != EventWorktreeChanged {
		t.Fatalf("event type = %v, want EventWorktreeChanged", ev.Type)
	}
	if ev.Change == nil || ev.Change.Action != ActionCheckedOut || ev.Change.Branch != "feature/a" {
		t.Errorf("change = %+v", ev.Change)
	}
}

func TestAddWorktreeReportsBothFailuresWhenTheBaseIsUnknown(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.addErr["feature/x"] = errors.New("fatal: invalid reference")
	h.git.defaultBranchErr = errors.New("fatal: no upstream configured")

	_, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err == nil {
		t.Fatal("expected an error when neither checkout nor branching is possible")
	}
	if !strings.Contains(err.Error(), "checking out feature/x") ||
		!strings.Contains(err.Error(), "determining default branch") {
		t.Errorf("err = %v, want both attempts reported", err)
	}
}

func TestAddWorktreeFetchFailureAborts(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.fetchErr = errors.New("network is unreachable")

	if _, err := h.svc.AddWorktree(context.Background(), repo, "feature/x"); err == nil {
		t.Error("expected an error when the fetch fails")
	}
	if wts, _ := h.db.ListWorktrees(context.Background(), repo.ID); len(wts) != 0 {
		t.Errorf("worktrees = %d, want none recorded", len(wts))
	}
}
