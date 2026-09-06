package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/internal/worktree"
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
      command: npm ci
`

func TestAddWorktreeExplainsAnUnpushedLocalConfig(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, localRunCommandConfig)

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if !strings.Contains(res.SetupError, setup.ConfigFileName) ||
		!strings.Contains(res.SetupError, trustedRef("origin", "main")) {
		t.Errorf("SetupError = %q, want it to name the config and the trusted ref", res.SetupError)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("worktrees = %d, want 1", len(wts))
	}
	if wts[0].SetupError == nil || *wts[0].SetupError != res.SetupError {
		t.Errorf("recorded SetupError = %v, want it on the worktree row too", wts[0].SetupError)
	}
}

func TestSyncExplainsAnUnpushedLocalConfig(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, localRunCommandConfig)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	wts, _ := h.db.ListWorktrees(context.Background(), repo.ID)
	if len(wts) != 1 {
		t.Fatalf("worktrees = %d, want 1", len(wts))
	}
	if wts[0].SetupError == nil {
		t.Fatal("SetupError = nil, want the skip explained")
	}
	st := worktree.Status{}
	applySetupError(&st, wts[0].SetupError)
	if !st.NeedsReconciliation || !strings.Contains(st.Note, setup.ConfigFileName) {
		t.Errorf("status = %+v, want the skip surfaced as a reconciliation note", st)
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

func TestTrustedConfigSuppressesTheUnpushedExplanation(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	writeLocalConfig(t, repo, localRunCommandConfig)
	h.git.configFiles = map[string][]byte{
		trustedRef("origin", "main") + ":" + setup.ConfigFileName: []byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env
      destination: .env
`),
	}
	if err := os.WriteFile(filepath.Join(repo.LocalPath, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := h.svc.AddWorktree(context.Background(), repo, "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if res.SetupError != "" {
		t.Errorf("SetupError = %q, want none when trusted steps ran", res.SetupError)
	}
}
