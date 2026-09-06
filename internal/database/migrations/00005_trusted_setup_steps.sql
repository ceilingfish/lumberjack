-- +goose Up
CREATE TABLE trusted_setup_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    checksum TEXT NOT NULL,
    trusted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (repository_id, checksum)
);

CREATE INDEX idx_trusted_setup_steps_repository_id ON trusted_setup_steps (
    repository_id
);

INSERT INTO trusted_setup_steps (repository_id, checksum)
SELECT
    id,
    setup_consent_fingerprint
FROM repositories
WHERE setup_consent_fingerprint != '';

ALTER TABLE repositories DROP COLUMN setup_consent_fingerprint;

-- +goose Down
ALTER TABLE repositories ADD COLUMN setup_consent_fingerprint TEXT NOT NULL DEFAULT '';

UPDATE repositories SET setup_consent_fingerprint = COALESCE((
    SELECT t.checksum FROM trusted_setup_steps AS t
    WHERE t.repository_id = repositories.id
    ORDER BY t.trusted_at DESC, t.id DESC
    LIMIT 1
), '');

DROP TABLE trusted_setup_steps;
