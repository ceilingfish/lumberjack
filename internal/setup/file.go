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

// Load reads and parses the `.lumberjack.yml` at dir's repository root. A
// missing file is not an error — it returns an empty Config so `setup-steps add` can
// create the file — but a malformed one is, so authoring commands never
// silently overwrite a config they could not understand.
func Load(dir string) (*Config, error) {
	data, err := os.ReadFile(configPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ConfigFileName, err)
	}
	return Parse(data)
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
