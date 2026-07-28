// Package setup parses and runs the `.lumberjack.yml` setup steps that the
// daemon executes against a freshly cloned worktree (see the Feature #5
// design notes). The config is always read from the repository's trusted
// default-branch tip — never the branch being cloned — so a PR author cannot
// smuggle arbitrary run-commands onto the user's machine; that trust decision
// is enforced by the caller (internal/daemon), not this package.
package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ConfigFileName is the committed, version-controlled file `.lumberjack.yml`
// setup steps are configured in, at the repository root.
const ConfigFileName = ".lumberjack.yml"

// Step type discriminators for Step.Type.
const (
	StepCopyFile   = "copy-file"
	StepRunCommand = "run-command"
)

// Config is the parsed `.lumberjack.yml`: an ordered list of typed setup
// steps run against a newly cloned worktree.
type Config struct {
	Steps []Step `yaml:"steps"`
}

// Step is one setup step. Exactly one of CopyFile or RunCommand is set,
// matching Type.
type Step struct {
	Type       string      `yaml:"type"`
	CopyFile   *CopyFile   `yaml:"copy_file,omitempty"`
	RunCommand *RunCommand `yaml:"run_command,omitempty"`
}

// CopyFile copies Source (resolved against the main checkout) to Destination
// (resolved against the new worktree). It runs unconditionally — copying a
// file executes no code, so it needs no consent.
type CopyFile struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
}

// RunCommand executes Command in the new worktree's root. It is off by
// default and only runs once the local user has consented to the trusted
// config (see internal/daemon's consent handling).
type RunCommand struct {
	Command string `yaml:"command"`
}

// Parse parses and validates a `.lumberjack.yml` document.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ConfigFileName, err)
	}
	for i, st := range cfg.Steps {
		if err := st.validate(); err != nil {
			return nil, fmt.Errorf("%s: step %d: %w", ConfigFileName, i+1, err)
		}
	}
	return &cfg, nil
}

// validate checks that a step carries the fields its declared Type requires.
func (st Step) validate() error {
	switch st.Type {
	case StepCopyFile:
		if st.CopyFile == nil || st.CopyFile.Source == "" || st.CopyFile.Destination == "" {
			return fmt.Errorf("%s requires source and destination", StepCopyFile)
		}
	case StepRunCommand:
		if st.RunCommand == nil || st.RunCommand.Command == "" {
			return fmt.Errorf("%s requires command", StepRunCommand)
		}
	case "":
		return fmt.Errorf("missing step type")
	default:
		return fmt.Errorf("unknown step type %q", st.Type)
	}
	return nil
}

// Label renders a step's position and type for a failure/consent message,
// e.g. "step 2 (run-command)".
func (st Step) Label(index int) string {
	return fmt.Sprintf("step %d (%s)", index+1, st.Type)
}

// HasRunCommands reports whether cfg declares any run-command steps — the
// steps that require consent before they can run.
func (c *Config) HasRunCommands() bool {
	return len(c.RunCommands()) > 0
}

// RunCommands returns the command strings of every run-command step, in
// declared order, for display in a consent prompt.
func (c *Config) RunCommands() []string {
	var cmds []string
	for _, st := range c.Steps {
		if st.Type == StepRunCommand {
			cmds = append(cmds, st.RunCommand.Command)
		}
	}
	return cmds
}

// AddCommand appends a run-command step running command. It reports false
// without changing the config if an identical run-command step already exists,
// so `setup-steps add` is idempotent and never records a duplicate.
func (c *Config) AddCommand(command string) bool {
	for _, cmd := range c.RunCommands() {
		if cmd == command {
			return false
		}
	}
	c.Steps = append(c.Steps, Step{
		Type:       StepRunCommand,
		RunCommand: &RunCommand{Command: command},
	})
	return true
}

// RemoveCommand deletes every run-command step running command, preserving all
// other steps in order. It reports whether it removed anything.
func (c *Config) RemoveCommand(command string) bool {
	kept := c.Steps[:0]
	removed := false
	for _, st := range c.Steps {
		if st.Type == StepRunCommand && st.RunCommand.Command == command {
			removed = true
			continue
		}
		kept = append(kept, st)
	}
	c.Steps = kept
	return removed
}

// Fingerprint hashes the raw config content. Consent is bound to this value:
// if `.lumberjack.yml` is added or its content changes, the fingerprint
// changes and previously-recorded consent no longer matches.
func Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
