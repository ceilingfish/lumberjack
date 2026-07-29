package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/scripts/covcheck"
)

func TestReportPassAndFail(t *testing.T) {
	results := []covcheck.Result{
		{Dir: ".", Excluded: true, Pass: true},
		{Dir: "good", Percent: 90, Pass: true},
		{Dir: "bad", Percent: 20, Pass: false},
		{Dir: "untested", NoTests: true, Pass: false},
	}

	var stdout, stderr bytes.Buffer
	code := report(&stdout, &stderr, results, 55.5, 80)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if strings.Contains(stdout.String(), ".") && strings.Contains(stdout.String(), "  .\n") {
		t.Errorf("excluded package should not appear in the listing:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "good") || !strings.Contains(stdout.String(), "90.0%") {
		t.Errorf("expected passing package in output:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "TOTAL") || !strings.Contains(stdout.String(), "55.5%") {
		t.Errorf("expected global total in output:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "bad") || !strings.Contains(stderr.String(), "20.0%") {
		t.Errorf("expected bad package in failure output:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "untested") || !strings.Contains(stderr.String(), "no test files") {
		t.Errorf("expected untested package in failure output:\n%s", stderr.String())
	}
}

func TestReportAllPass(t *testing.T) {
	results := []covcheck.Result{
		{Dir: "good", Percent: 90, Pass: true},
	}
	var stdout, stderr bytes.Buffer
	if code := report(&stdout, &stderr, results, 90, 80); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr output, got %q", stderr.String())
	}
}

// TestRunAgainstRealRepo exercises run() end to end against this actual
// repository: real `go list` package discovery and the real committed
// exclusion list, with a fabricated near-empty coverage profile. At
// threshold 0, every package with tests passes trivially regardless of the
// profile's contents, so this isolates and proves the one gate that must
// hold independent of any profile: a package with real, non-excluded code
// but zero test files fails the run.
func TestRunAgainstRealRepo(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Skipf("could not locate repo root at %s: %v", repoRoot, err)
	}

	profile := filepath.Join(t.TempDir(), "empty.out")
	if err := os.WriteFile(profile, []byte("mode: atomic\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exclusions := filepath.Join(repoRoot, "coverage-exclude.txt")
	if _, err := os.Stat(exclusions); err != nil {
		t.Fatalf("expected committed exclusion list at %s: %v", exclusions, err)
	}

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{profile, "0", exclusions}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected the gate to fail because of a zero-test package, got exit %d\nstdout:\n%s\nstderr:\n%s",
			code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "database/schema") {
		t.Errorf("expected internal/database/schema (no test files) to be reported failing, got:\n%s", stderr.String())
	}
}
