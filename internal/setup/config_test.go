package setup

import "testing"

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
