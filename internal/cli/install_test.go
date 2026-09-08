package cli

import (
	"path/filepath"
	"testing"
)

func TestDefaultBinDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := DefaultBinDir()
	if err != nil {
		t.Fatalf("DefaultBinDir: %v", err)
	}
	if want := filepath.Join(home, ".local", "bin"); got != want {
		t.Errorf("DefaultBinDir = %q, want %q", got, want)
	}

	t.Setenv("HOME", "")
	if _, err := DefaultBinDir(); err == nil {
		t.Error("expected DefaultBinDir to fail with no home directory")
	}
}
