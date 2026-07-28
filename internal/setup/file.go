package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// loadIfPresent parses the `.lumberjack.yml` at path, reporting whether it
// exists. A missing file is not an error — Resolve falls back to the main
// checkout's config, and `setup-steps add` creates the file — but a malformed
// one is, so authoring commands never silently overwrite a config they could
// not understand. Callers choosing between candidate paths need to tell "no
// file here" apart from "an empty config here".
func loadIfPresent(path string) (cfg *Config, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	cfg, err = Parse(data)
	if err != nil {
		return nil, false, err
	}
	return cfg, true, nil
}

// Save writes cfg back to the `.lumberjack.yml` at dir's repository root,
// indented to match the file's hand-authored convention. Marshalling does not
// preserve comments or key order from an existing file; the config is a small,
// tool-managed document where that trade-off is acceptable.
func Save(dir string, cfg *Config) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encoding %s: %w", ConfigFileName, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encoding %s: %w", ConfigFileName, err)
	}
	if err := os.WriteFile(configPath(dir), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", ConfigFileName, err)
	}
	return nil
}

// configPath is the `.lumberjack.yml` path at dir.
func configPath(dir string) string {
	return filepath.Join(dir, ConfigFileName)
}
