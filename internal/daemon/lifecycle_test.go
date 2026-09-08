package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
)

func TestServiceEnvCarriesPath(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin")
	if got := serviceEnv()["PATH"]; got != "/opt/homebrew/bin" {
		t.Errorf("serviceEnv PATH = %q, want the install-time PATH", got)
	}

	t.Setenv("PATH", "")
	if _, ok := serviceEnv()["PATH"]; ok {
		t.Error("serviceEnv should omit an empty PATH rather than pinning it")
	}
}

func TestProgramStartSurfacesAWiringFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUMBERJACK_DB_PATH", filepath.Join(blocked, "db.sqlite"))

	p := &program{socketPath: filepath.Join(t.TempDir(), "d.sock")}
	if err := p.Start(nil); err == nil {
		t.Error("expected Start to fail when the database cannot be opened")
		_ = p.Stop(nil)
	}
}

func TestProgramStopWithoutAStartedApp(t *testing.T) {
	p := &program{}
	if err := p.Stop(nil); err != nil {
		t.Errorf("Stop before Start = %v, want nil", err)
	}

	p.app = fx.New(fx.NopLogger)
	if err := p.Stop(nil); err != nil {
		t.Errorf("Stop = %v, want nil", err)
	}
}

func TestNewService(t *testing.T) {
	for _, socket := range []string{"", "/tmp/lj.sock"} {
		svc, err := NewPlatformService(socket, "", "test")
		if err != nil {
			t.Fatalf("NewPlatformService(%q): %v", socket, err)
		}
		if svc == nil {
			t.Fatalf("NewPlatformService(%q) returned nil service", socket)
		}
		if svc.Platform() == "" {
			t.Errorf("NewPlatformService(%q): empty platform", socket)
		}
	}
}
