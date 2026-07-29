package daemon

import (
	"context"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// runSetupSteps runs repo's trusted `.lumberjack.yml` setup steps against the
// worktree at dir, recording the failing step (if any) on the worktree row so
// it surfaces on its reconciliation status. It never returns an error: per the
// feature's fail-fast-but-keep design, a setup failure does not fail the clone
// or the sync, it is only surfaced. The recorded failure is returned (empty on
// success) so a caller driving one worktree — `worktree add` — can report it
// inline instead of waiting for a later status read.
//
// preserveExisting must be set when dir is a directory Lumberjack did not
// create, so copy-file steps cannot destroy the user's own files (see
// setup.Options.PreserveExisting).
func (s *Service) runSetupSteps(
	ctx context.Context, repo *schema.Repository, dir string, worktreeID int64,
	preserveExisting bool,
) string {
	cfg, raw, err := s.loadTrustedSetupConfig(ctx, repo)
	if err != nil {
		msg := fmt.Sprintf("loading %s: %v", setup.ConfigFileName, err)
		s.recordSetupError(ctx, worktreeID, &msg)
		return msg
	}
	if cfg == nil || len(cfg.Steps) == 0 {
		return ""
	}

	consented := cfg.HasRunCommands() && repo.SetupConsentFingerprint != "" &&
		repo.SetupConsentFingerprint == setup.Fingerprint(raw)

	failedStep, runErr := setup.Run(ctx, cfg, setup.Options{
		MainCheckout:     repo.LocalPath,
		WorktreeDir:      dir,
		Consented:        consented,
		PreserveExisting: preserveExisting,
	})
	if runErr != nil {
		msg := fmt.Sprintf("%s failed: %v", failedStep, runErr)
		s.recordSetupError(ctx, worktreeID, &msg)
		return msg
	}
	// Clear any stale failure from a previous attempt at this directory.
	s.recordSetupError(ctx, worktreeID, nil)
	return ""
}

// recordSetupError stores (or clears) a worktree's setup failure. It logs
// nothing on its own failure — this is best-effort bookkeeping, not something
// that should fail the sync it is called from.
func (s *Service) recordSetupError(ctx context.Context, worktreeID int64, msg *string) {
	_ = s.db.SetWorktreeSetupError(ctx, worktreeID, msg)
}

// applySetupError folds a persisted setup failure into a live reconciliation
// Status, so it surfaces through the same reconciliation-note/status field
// the CLI already renders. A setup failure always needs attention, regardless
// of the worktree's git-derived state.
func applySetupError(st *worktree.Status, setupErr *string) {
	if setupErr == nil || *setupErr == "" {
		return
	}
	st.NeedsReconciliation = true
	if st.Note == "" {
		st.Note = "setup failed: " + *setupErr
		return
	}
	st.Note += "; setup failed: " + *setupErr
}

// loadTrustedSetupConfig reads and parses `.lumberjack.yml` from repo's
// trusted default-branch tip — never the branch being cloned, so a PR author
// cannot use it to run arbitrary code on the user's machine. It returns
// (nil, nil, nil) when the repository has no such file there.
func (s *Service) loadTrustedSetupConfig(ctx context.Context, repo *schema.Repository) (*setup.Config, []byte, error) {
	branch, err := s.git.DefaultBranch(ctx, repo.LocalPath, repo.DefaultRemote)
	if err != nil {
		return nil, nil, fmt.Errorf("determining default branch: %w", err)
	}
	ref := repo.DefaultRemote + "/" + branch
	data, found, err := s.git.ShowFile(ctx, repo.LocalPath, ref, setup.ConfigFileName)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s from %s: %w", setup.ConfigFileName, ref, err)
	}
	if !found {
		return nil, nil, nil
	}
	cfg, err := setup.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	return cfg, data, nil
}

// SetupConsent is whether a repository's trusted run-command setup steps are
// pending the local user's consent, and the commands themselves — for the CLI
// to prompt with.
type SetupConsent struct {
	Pending  bool
	Commands []string
}

// GetSetupConsent reports repo's setup-consent status: pending when the
// trusted config declares run-commands and the local user has not consented
// to their current content (never, or the config has since changed).
func (s *Service) GetSetupConsent(ctx context.Context, repo *schema.Repository) (SetupConsent, error) {
	cfg, raw, err := s.loadTrustedSetupConfig(ctx, repo)
	if err != nil {
		return SetupConsent{}, err
	}
	if cfg == nil || !cfg.HasRunCommands() {
		return SetupConsent{}, nil
	}
	if repo.SetupConsentFingerprint == setup.Fingerprint(raw) {
		return SetupConsent{}, nil
	}
	return SetupConsent{Pending: true, Commands: cfg.RunCommands()}, nil
}

// SetSetupConsent records the local user's consent to run repo's current
// trusted run-command steps, binding it to the config's content fingerprint.
// Consent is local per machine (no remote or shared store).
func (s *Service) SetSetupConsent(ctx context.Context, repo *schema.Repository) (*schema.Repository, error) {
	cfg, raw, err := s.loadTrustedSetupConfig(ctx, repo)
	if err != nil {
		return nil, err
	}
	fingerprint := ""
	if cfg != nil {
		fingerprint = setup.Fingerprint(raw)
	}
	if err := s.db.UpdateSetupConsent(ctx, repo.ID, fingerprint); err != nil {
		return nil, err
	}
	updated := *repo
	updated.SetupConsentFingerprint = fingerprint
	return &updated, nil
}
