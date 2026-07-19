-- +goose Up
-- setup_consent_fingerprint records the content fingerprint of the
-- `.lumberjack.yml` run-command steps the local user has consented to run
-- for this repository (see internal/setup). Empty means no consent has been
-- given yet. A repository's run-commands are pending consent whenever this
-- does not match the fingerprint of the trusted (default-branch) config.
ALTER TABLE repositories ADD COLUMN setup_consent_fingerprint TEXT NOT NULL DEFAULT '';

-- setup_error records the setup step that failed when a worktree was cloned
-- (e.g. "step 2 (run-command): exit status 1: ..."), surfaced alongside the
-- worktree's live reconciliation status. NULL when setup succeeded or the
-- repository has no setup steps configured.
ALTER TABLE worktrees ADD COLUMN setup_error TEXT;

-- +goose Down
ALTER TABLE worktrees DROP COLUMN setup_error;
ALTER TABLE repositories DROP COLUMN setup_consent_fingerprint;
