package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/setup"
)

// writeLocalConfig makes repo.LocalPath look like a checkout (setup.Resolve
// walks up to a `.git` entry) holding an uncommitted `.lumberjack.yml`.
func writeLocalConfig(t *testing.T, repo *schema.Repository, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo.LocalPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.LocalPath, setup.ConfigFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const localRunCommandConfig = `
steps:
  - type: run-command
    run_command:
      command: touch ran-from-local
`

func TestAddWorktreeRunsATrustedUnpushedLocalConfig(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, localRunCommandConfig)
	if err := h.db.TrustSetupSteps(context.Background(), repo.ID, setup.Fingerprint([]byte(localRunCommandConfig))); err != nil {
		t.Fatal(err)
	}

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if res.SetupError != "" {
		t.Fatalf("SetupError = %q, want none", res.SetupError)
	}
	if _, err := os.Stat(filepath.Join(res.DirectoryPath, "ran-from-local")); err != nil {
		t.Errorf("trusted local run-command did not run: %v", err)
	}
}

func TestAddWorktreeSkipsAnUntrustedLocalConfig(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, localRunCommandConfig)

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if res.SetupError != "" {
		t.Fatalf("SetupError = %q, want none", res.SetupError)
	}
	if _, err := os.Stat(filepath.Join(res.DirectoryPath, "ran-from-local")); err == nil {
		t.Error("un-consented local run-command ran")
	}
}

func TestSyncRunsATrustedUnpushedLocalConfig(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, localRunCommandConfig)
	if err := h.db.TrustSetupSteps(context.Background(), repo.ID, setup.Fingerprint([]byte(localRunCommandConfig))); err != nil {
		t.Fatal(err)
	}
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("worktrees = %d, want 1", len(wts))
	}
	if wts[0].SetupError != nil {
		t.Fatalf("SetupError = %q, want none", *wts[0].SetupError)
	}
	if _, err := os.Stat(filepath.Join(wts[0].DirectoryPath, "ran-from-local")); err != nil {
		t.Errorf("trusted local run-command did not run: %v", err)
	}
}

func TestLocalConfigWithoutStepsNeedsNoExplanation(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, "steps: []\n")

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if res.SetupError != "" {
		t.Errorf("SetupError = %q, want none when the local config has no steps", res.SetupError)
	}
}

// A local config overrides the trusted default-branch one wholesale, matching
// setup.Resolve — what runs is always exactly one file's worth of steps.
func TestLocalConfigOverridesTheTrustedDefaultBranchConfig(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, `
steps:
  - type: copy-file
    copy_file:
      source: .env.local
      destination: .env
`)
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env.pushed
      destination: .env
`),
	}
	if err := os.WriteFile(filepath.Join(repo.LocalPath, ".env.local"), []byte("FROM=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if res.SetupError != "" {
		t.Fatalf("SetupError = %q, want none", res.SetupError)
	}
	got, err := os.ReadFile(filepath.Join(res.DirectoryPath, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FROM=local\n" {
		t.Errorf(".env = %q, want the local config's step to have won", got)
	}
}
