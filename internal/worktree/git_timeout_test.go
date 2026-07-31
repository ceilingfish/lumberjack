package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/ghauth"
)

func stubGitWithTimeout(t *testing.T, timeout time.Duration, script string) *Git {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "git-stub")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Git{bin: bin, timeout: timeout}
}

func TestRunTimesOutWhenGitHangs(t *testing.T) {
	g := stubGitWithTimeout(t, 100*time.Millisecond, "exec sleep 30")

	start := time.Now()
	err := g.Fetch(context.Background(), t.TempDir(), "origin")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Fetch: expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("error should name the timeout, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Fetch took %s, expected it to be cut off promptly", elapsed)
	}
}

func TestShowFileTimesOutWhenGitHangs(t *testing.T) {
	g := stubGitWithTimeout(t, 100*time.Millisecond, "exec sleep 30")

	_, _, err := g.ShowFile(context.Background(), t.TempDir(), "origin/main", ".lumberjack.yml")
	if err == nil {
		t.Fatal("ShowFile: expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Errorf("error should name the timeout, got %v", err)
	}
}

func TestRunTimeoutRespectsCallerCancellation(t *testing.T) {
	g := stubGitWithTimeout(t, time.Minute, "exec sleep 30")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := g.Fetch(ctx, t.TempDir(), "origin"); err == nil {
		t.Fatal("Fetch: expected an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Fetch took %s, caller cancellation should still win", elapsed)
	}
}

func TestTimeoutFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
		raw  string
		want time.Duration
	}{
		{name: "unset", want: defaultCommandTimeout},
		{name: "valid", set: true, raw: "30s", want: 30 * time.Second},
		{name: "unparseable", set: true, raw: "soon", want: defaultCommandTimeout},
		{name: "zero", set: true, raw: "0s", want: defaultCommandTimeout},
		{name: "negative", set: true, raw: "-5s", want: defaultCommandTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envGitTimeout, tc.raw)
			if !tc.set {
				if err := os.Unsetenv(envGitTimeout); err != nil {
					t.Fatal(err)
				}
			}
			if got := timeoutFromEnv(); got != tc.want {
				t.Errorf("timeoutFromEnv() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRunPassesContextTokenToGit(t *testing.T) {
	g := stubGitWithTimeout(t, time.Minute, `printf '%s' "$GH_TOKEN"`)

	ctx := ghauth.WithToken(context.Background(), "github.com", "tok-work")
	out, err := g.run(ctx, t.TempDir(), "rev-parse")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "tok-work" {
		t.Errorf("git saw GH_TOKEN=%q, want tok-work", out)
	}
}

func TestRunPassesNoTokenWhenContextHasNone(t *testing.T) {
	g := stubGitWithTimeout(t, time.Minute, `printf '%s' "$GH_TOKEN"`)
	t.Setenv("GH_TOKEN", "")

	out, err := g.run(context.Background(), t.TempDir(), "rev-parse")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "" {
		t.Errorf("git saw GH_TOKEN=%q, want it unset", out)
	}
}
