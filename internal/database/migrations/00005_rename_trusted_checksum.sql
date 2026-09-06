-- +goose Up
ALTER TABLE repositories RENAME COLUMN setup_consent_fingerprint TO trusted_checksum;

-- +goose Down
ALTER TABLE repositories RENAME COLUMN trusted_checksum TO setup_consent_fingerprint;
