package daemon

import (
	"context"
	"fmt"
	"slices"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/setup"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// runSetupSteps runs repo's trusted `.lumberjack.yml` setup steps against the
// worktree at dir, recording the failing step — or the reason no steps ran at
// all — on the worktree row so it surfaces on its reconciliation status. It never returns an error: per the
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
	cfg, raw, err := s.loadSetupConfig(ctx, repo)
	if err != nil {
		msg := fmt.Sprintf("loading %s: %v", setup.ConfigFileName, err)
		s.recordSetupError(ctx, worktreeID, &msg)
		return msg
	}
	if cfg == nil || len(cfg.Steps) == 0 {
		s.recordSetupError(ctx, worktreeID, nil)
		return ""
	}

	consented := false
	if cfg.HasRunCommands() {
		trusted, err := s.db.TrustedSetupChecksums(ctx, repo.ID)
		if err != nil {
			msg := fmt.Sprintf("reading trusted %s checksums: %v", setup.ConfigFileName, err)
			s.recordSetupError(ctx, worktreeID, &msg)
			return msg
		}
		consented = slices.Contains(trusted, setup.Fingerprint(raw))
	}

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
		st.Note = "setup: " + *setupErr
		return
	}
	st.Note += "; setup: " + *setupErr
}

// loadSetupConfig reads and parses the `.lumberjack.yml` governing repo. The
// main checkout's own file wins when it has one — it is the user's working
// tree, so steps they are still iterating on run without having to push them
// first — and the repository's trusted default-branch tip is the fallback.
// Neither source is ever the branch being cloned, so a PR author cannot use
// this to run arbitrary code on the user's machine; run-commands from either
// source still need the local user's consent. It returns (nil, nil, nil) when
// there is no config in either place.
func (s *Service) loadSetupConfig(ctx context.Context, repo *schema.Repository) (*setup.Config, []byte, error) {
	if local, err := setup.Resolve(repo.LocalPath); err == nil && local.ConfigPath != "" {
		return local.Config, local.Raw, nil
	}
	ref, err := s.trustedRef(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
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

// trustedRef is the remote-tracking ref `.lumberjack.yml` is trusted from:
// repo's default remote's default-branch tip.
func (s *Service) trustedRef(ctx context.Context, repo *schema.Repository) (string, error) {
	branch, err := s.git.DefaultBranch(ctx, repo.LocalPath, repo.DefaultRemote)
	if err != nil {
		return "", fmt.Errorf("determining default branch: %w", err)
	}
	return repo.DefaultRemote + "/" + branch, nil
}

type SetupSteps struct {
	IsDefined        bool
	TrustedChecksums []string
	CurrentChecksum  string
	IsTrusted        bool
	Steps            []string
}

func (s *Service) GetSetupSteps(ctx context.Context, repo *schema.Repository) (SetupSteps, error) {
	cfg, raw, err := s.loadSetupConfig(ctx, repo)
	if err != nil {
		return SetupSteps{}, err
	}
	trusted, err := s.db.TrustedSetupChecksums(ctx, repo.ID)
	if err != nil {
		return SetupSteps{}, err
	}
	steps := SetupSteps{TrustedChecksums: trusted}
	if cfg == nil {
		steps.IsTrusted = true
		return steps, nil
	}
	steps.IsDefined = true
	steps.CurrentChecksum = setup.Fingerprint(raw)
	steps.Steps = cfg.RunCommands()
	steps.IsTrusted = slices.Contains(trusted, steps.CurrentChecksum)
	return steps, nil
}

func (s *Service) TrustSetupSteps(ctx context.Context, repo *schema.Repository, checksum string) error {
	return s.db.TrustSetupSteps(ctx, repo.ID, checksum)
}

func (s *Service) SetSetupConsent(
	ctx context.Context, repo *schema.Repository, checksum string,
) (bool, error) {
	_, raw, err := s.loadSetupConfig(ctx, repo)
	if err != nil {
		return false, err
	}
	current := ""
	if raw != nil {
		current = setup.Fingerprint(raw)
	}
	if checksum != current || current == "" {
		return false, nil
	}
	if err := s.db.TrustSetupSteps(ctx, repo.ID, current); err != nil {
		return false, err
	}
	return true, nil
}
