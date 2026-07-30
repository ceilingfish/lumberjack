package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/scripts/covcheck"
)

// fixtureModule writes a throwaway Go module containing one package with a
// coverable statement and, optionally, a test file for it. Exercising run()
// against a fixture rather than this repository keeps the end-to-end test
// independent of whatever the repository's own coverage happens to be today.
func fixtureModule(t *testing.T, withTest bool) string {
	t.Helper()
	dir := t.TempDir()

	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module example.com/fixture\n\ngo 1.26\n")
	write("thing/thing.go", "package thing\n\nfunc Double(n int) int { return n * 2 }\n")
	if withTest {
		write("thing/thing_test.go",
			"package thing\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fail()\n\t}\n}\n")
	}
	write("coverage-exclude.txt", "# nothing excluded in the fixture\n")
	return dir
}

// A package with real code but no test file must fail the gate, whatever the
// profile says — that absence is precisely what the gate exists to catch.
func TestRunFailsOnPackageWithNoTests(t *testing.T) {
	dir := fixtureModule(t, false)
	t.Chdir(dir)

	profile := filepath.Join(t.TempDir(), "empty.out")
	if err := os.WriteFile(profile, []byte("mode: atomic\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{profile, "0", filepath.Join(dir, "coverage-exclude.txt")}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "thing") || !strings.Contains(stderr.String(), "no test files") {
		t.Errorf("expected the untested package reported as failing, got:\n%s", stderr.String())
	}
}

// The same package, once it has a test and a profile covering it, passes and
// reports its percentage.
func TestRunPassesWithCoveredPackage(t *testing.T) {
	dir := fixtureModule(t, true)
	t.Chdir(dir)

	profile := filepath.Join(t.TempDir(), "full.out")
	const content = "mode: atomic\nexample.com/fixture/thing/thing.go:3.32,3.51 1 1\n"
	if err := os.WriteFile(profile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{profile, "100", filepath.Join(dir, "coverage-exclude.txt")}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "100.0%") {
		t.Errorf("expected the covered package at 100.0%%, got:\n%s", stdout.String())
	}
}

// A malformed threshold is a usage error, distinct from a coverage failure.
func TestRunRejectsBadArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"only-one-arg"}, &stdout, &stderr); code != 2 {
		t.Errorf("wrong argument count: exit code = %d, want 2", code)
	}
	if code := run([]string{"p", "not-a-number", "e"}, &stdout, &stderr); code != 2 {
		t.Errorf("bad threshold: exit code = %d, want 2", code)
	}
}

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
