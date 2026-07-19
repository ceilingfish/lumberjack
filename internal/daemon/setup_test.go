package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/setup"
)

// trustedRef mirrors loadTrustedSetupConfig's ref computation for tests.
func trustedRef(remote, branch string) string { return remote + "/" + branch }

func TestCreateWorktreeRunsCopyFileStep(t *testing.T) {
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
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, err := h.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil || len(wts) != 1 {
		t.Fatalf("ListWorktrees: %v (%d)", err, len(wts))
	}
	got, err := os.ReadFile(filepath.Join(wts[0].DirectoryPath, ".env"))
	if err != nil {
		t.Fatalf("reading copied file: %v", err)
	}
	if string(got) != "SECRET=1\n" {
		t.Errorf("copied content = %q", got)
	}
	if wts[0].SetupError != nil {
		t.Errorf("SetupError = %v, want nil", *wts[0].SetupError)
	}
}

func TestCreateWorktreeSkipsRunCommandWithoutConsent(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if err := os.MkdirAll(repo.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: run-command
    run_command:
      command: touch marker
`),
	}
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if _, err := os.Stat(filepath.Join(wts[0].DirectoryPath, "marker")); !os.IsNotExist(err) {
		t.Error("run-command executed without consent")
	}
	if wts[0].SetupError != nil {
		t.Errorf("SetupError = %v, want nil (skip is not a failure)", *wts[0].SetupError)
	}
}

func TestCreateWorktreeRunsRunCommandWithConsent(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	if err := os.MkdirAll(repo.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`
steps:
  - type: run-command
    run_command:
      command: touch marker
`)
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: raw,
	}
	if err := h.db.UpdateSetupConsent(context.Background(), repo.ID, setup.Fingerprint(raw)); err != nil {
		t.Fatal(err)
	}
	repo.SetupConsentFingerprint = setup.Fingerprint(raw)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if _, err := os.Stat(filepath.Join(wts[0].DirectoryPath, "marker")); err != nil {
		t.Error("run-command did not execute despite consent")
	}
}

func TestCreateWorktreeSetupFailureSurfacesOnStatus(t *testing.T) {
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
      source: does-not-exist
      destination: dest
`),
	}
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("expected worktree to be kept despite setup failure, got %d", len(wts))
	}
	if wts[0].SetupError == nil {
		t.Fatal("expected SetupError to be recorded")
	}

	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if !views[0].Status.NeedsReconciliation {
		t.Error("expected NeedsReconciliation = true after setup failure")
	}
	if views[0].Status.Note == "" {
		t.Error("expected a non-empty reconciliation note naming the failed step")
	}
}

func TestGetSetupConsentPendingWhenNeverConsented(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: run-command
    run_command:
      command: echo hi
`),
	}

	consent, err := h.svc.GetSetupConsent(context.Background(), repo)
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if !consent.Pending {
		t.Error("expected pending consent")
	}
	if len(consent.Commands) != 1 || consent.Commands[0] != "echo hi" {
		t.Errorf("Commands = %v", consent.Commands)
	}
}

func TestSetSetupConsentClearsAndConfigChangeRePends(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	raw := []byte(`
steps:
  - type: run-command
    run_command:
      command: echo hi
`)
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: raw,
	}

	updated, err := h.svc.SetSetupConsent(context.Background(), repo)
	if err != nil {
		t.Fatalf("SetSetupConsent: %v", err)
	}
	consent, err := h.svc.GetSetupConsent(context.Background(), updated)
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if consent.Pending {
		t.Error("expected consent no longer pending after SetSetupConsent")
	}

	// The trusted config changes: consent should become pending again.
	h.git.configFiles[trustedRef("origin", "main")+":"+setup.ConfigFileName] = []byte(`
steps:
  - type: run-command
    run_command:
      command: echo changed
`)
	consent, err = h.svc.GetSetupConsent(context.Background(), updated)
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if !consent.Pending {
		t.Error("expected consent to be pending again after config content changed")
	}
}

func TestGetSetupConsentNoRunCommandsNotPending(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: copy-file
    copy_file:
      source: a
      destination: b
`),
	}

	consent, err := h.svc.GetSetupConsent(context.Background(), repo)
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if consent.Pending {
		t.Error("copy-file-only config should never require consent")
	}
}

func TestGetSetupConsentNoConfigNotPending(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)

	consent, err := h.svc.GetSetupConsent(context.Background(), repo)
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if consent.Pending {
		t.Error("no .lumberjack.yml should never require consent")
	}
}
