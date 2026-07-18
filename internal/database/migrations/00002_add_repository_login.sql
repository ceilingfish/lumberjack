-- +goose Up
-- login records which gh account the repository was registered under, so the
-- daemon can switch to it (`gh auth switch`) before any operation and restore
-- the previously-active account afterwards. Empty for repos tracked before this
-- column existed, in which case no switching is attempted.
ALTER TABLE repositories ADD COLUMN login TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE repositories DROP COLUMN login;
