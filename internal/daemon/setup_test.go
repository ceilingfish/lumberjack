package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/internal/worktree"
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

// copyEnvConfig is the trusted-config fixture the adoption tests share: a
// single copy-file step lifting .env out of the main checkout.
func copyEnvConfig(t *testing.T, h *harness, repo *schema.Repository) {
	t.Helper()
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
}

// A directory checked out by hand and claimed by an open PR is adopted rather
// than created, but it is still newly tracked, so setup steps must run.
func TestAdoptedWorktreeRunsSetupSteps(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	copyEnvConfig(t, h, repo)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/x"}}

	existing := filepath.Join(h.parent, "hand-checkout")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{{Dir: existing, Branch: "feature/x"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(existing, ".env"))
	if err != nil {
		t.Fatalf("setup steps did not run on the adopted worktree: %v", err)
	}
	if string(got) != "SECRET=1\n" {
		t.Errorf("copied content = %q", got)
	}
}

// An orphan — checked out by hand with no open PR asking for its branch — is
// adopted too, and gets the same setup treatment. A second sync sees an
// already-tracked row and must not run the steps again.
func TestAdoptedOrphanRunsSetupStepsOnceOnly(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	copyEnvConfig(t, h, repo)

	existing := filepath.Join(h.parent, "orphan")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{{Dir: existing, Branch: "feature/orphan"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	dest := filepath.Join(existing, ".env")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("setup steps did not run on the adopted orphan: %v", err)
	}

	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("second SyncRepository: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("setup steps re-ran on an already-tracked worktree")
	}
}

// The whole point of the preserve-existing rule: the user checked this
// directory out by hand and tuned its (gitignored, so unrecoverable) .env.
// Adoption must not silently overwrite it with the main checkout's copy.
func TestAdoptionDoesNotOverwriteExistingFiles(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	copyEnvConfig(t, h, repo)

	existing := filepath.Join(h.parent, "hand-tuned")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(existing, ".env")
	const mine = "SECRET=my-own-value\n"
	if err := os.WriteFile(dest, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{{Dir: existing, Branch: "feature/tuned"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Errorf("adoption overwrote the user's file: got %q, want %q", got, mine)
	}
}

// A setup failure during adoption is surfaced on the worktree's reconciliation
// status, but the sync still succeeds and the worktree is kept — the same
// fail-fast-but-keep contract createWorktree has.
func TestAdoptionSetupFailureIsRecordedButKeepsWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// A copy-file step whose source does not exist in the main checkout, so the
	// step fails without needing consent or a subprocess.
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: copy-file
    copy_file:
      source: nonexistent.env
      destination: .env
`),
	}

	existing := filepath.Join(h.parent, "will-fail")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git.worktrees = []worktree.Ref{{Dir: existing, Branch: "feature/fails"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("a setup failure must not fail the sync: %v", err)
	}

	rows, err := h.svc.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("adopted worktree count = %d, want 1 (a setup failure must not drop it)", len(rows))
	}
	if rows[0].SetupError == nil || *rows[0].SetupError == "" {
		t.Error("expected the setup failure to be recorded on the adopted worktree row")
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
