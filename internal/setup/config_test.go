package setup

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cfg, err := Parse([]byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env
      destination: .env
  - type: run-command
    run_command:
      command: go mod download
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(cfg.Steps))
	}
	if !cfg.HasRunCommands() {
		t.Fatal("HasRunCommands() = false, want true")
	}
	if got, want := cfg.RunCommands(), []string{"go mod download"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("RunCommands() = %v, want %v", got, want)
	}
}

func TestParseEmpty(t *testing.T) {
	cfg, err := Parse([]byte(``))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Steps) != 0 {
		t.Fatalf("got %d steps, want 0", len(cfg.Steps))
	}
	if cfg.HasRunCommands() {
		t.Fatal("HasRunCommands() = true, want false")
	}
}

func TestParseRejectsUnknownType(t *testing.T) {
	_, err := Parse([]byte(`
steps:
  - type: symlink
`))
	if err == nil {
		t.Fatal("Parse: want error for unknown step type, got nil")
	}
}

func TestParseRejectsIncompleteStep(t *testing.T) {
	cases := []string{
		`steps:
  - type: copy-file
    copy_file:
      source: .env
`,
		`steps:
  - type: run-command
`,
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c)); err == nil {
			t.Fatalf("Parse(%q): want error, got nil", c)
		}
	}
}

func TestParseRejectsMalformedYAML(t *testing.T) {
	_, err := Parse([]byte("steps:\n  - type: copy-file\n   bad indentation:\n"))
	if err == nil {
		t.Fatal("Parse: want error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing "+ConfigFileName) {
		t.Fatalf("Parse error = %q, want it to name the file it failed to parse", err)
	}
}

func TestParseRejectsMissingStepType(t *testing.T) {
	_, err := Parse([]byte(`
steps:
  - type: copy-file
    copy_file:
      source: .env
      destination: .env
  - copy_file:
      source: other
      destination: other
`))
	if err == nil {
		t.Fatal("Parse: want error for a step with no type, got nil")
	}
	if !strings.Contains(err.Error(), "missing step type") {
		t.Fatalf("Parse error = %q, want it to say the step type is missing", err)
	}
	if !strings.Contains(err.Error(), "step 2") {
		t.Fatalf("Parse error = %q, want it to name step 2", err)
	}
}

func TestFingerprintStable(t *testing.T) {
	a := Fingerprint([]byte("steps: []\n"))
	b := Fingerprint([]byte("steps: []\n"))
	if a != b {
		t.Fatalf("Fingerprint not stable: %q != %q", a, b)
	}
	c := Fingerprint([]byte("steps: [{type: copy-file}]\n"))
	if a == c {
		t.Fatal("Fingerprint did not change with content")
	}
}
