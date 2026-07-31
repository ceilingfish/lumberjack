package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/present"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing fake %s: %v", name, err)
	}
	return path
}

const (
	workingGit = `case "$1" in
--version) echo "git version 2.99.0" ;;
*) exit 1 ;;
esac
`
	workingGH = `case "$1" in
--version) echo "gh version 2.50.0 (2024-01-01)"; echo "https://github.com/cli/cli" ;;
auth) exit 0 ;;
*) exit 1 ;;
esac
`
	unauthenticatedGH = `case "$1" in
--version) echo "gh version 2.50.0 (2024-01-01)"; echo "https://github.com/cli/cli" ;;
auth) echo "You are not logged into any GitHub hosts" >&2; exit 1 ;;
*) exit 1 ;;
esac
`
	brokenBin = `echo "the tool exploded" >&2
exit 1
`
)

func stubGit(t *testing.T, body string) string {
	t.Helper()
	path := writeScript(t, "git", body)
	t.Setenv(worktree.EnvGitPath, path)
	return path
}

func stubGH(t *testing.T, body string) string {
	t.Helper()
	path := writeScript(t, "gh", body)
	t.Setenv(github.EnvCLIPath, path)
	return path
}

func stubEmptyEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(worktree.EnvGitPath, "")
	t.Setenv(github.EnvCLIPath, "")
	t.Setenv("PATH", t.TempDir())
}

func TestCheckGitReportsPathAndVersion(t *testing.T) {
	stubEmptyEnvironment(t)
	path := stubGit(t, workingGit)

	c := checkGit(context.Background())
	if !c.OK {
		t.Fatalf("expected git check to pass: %+v", c)
	}
	if c.Name != "git" {
		t.Errorf("Name = %q, want %q", c.Name, "git")
	}
	if want := path + " (2.99.0)"; c.Detail != want {
		t.Errorf("Detail = %q, want %q", c.Detail, want)
	}
}

func TestCheckGitAbsent(t *testing.T) {
	stubEmptyEnvironment(t)

	c := checkGit(context.Background())
	if c.OK {
		t.Fatalf("expected git check to fail when git is absent: %+v", c)
	}
	if c.Name != "git" {
		t.Errorf("Name = %q, want %q", c.Name, "git")
	}
	if !strings.Contains(c.Detail, worktree.EnvGitPath) {
		t.Errorf("Detail = %q, want it to name the override variable", c.Detail)
	}
}

func TestCheckGitStaleOverride(t *testing.T) {
	stubEmptyEnvironment(t)
	t.Setenv(worktree.EnvGitPath, filepath.Join(t.TempDir(), "nope"))

	if c := checkGit(context.Background()); c.OK {
		t.Fatalf("expected git check to fail for a stale override: %+v", c)
	}
}

func TestCheckGitVersionFails(t *testing.T) {
	stubEmptyEnvironment(t)
	stubGit(t, brokenBin)

	c := checkGit(context.Background())
	if c.OK {
		t.Fatalf("expected git check to fail when --version errors: %+v", c)
	}
	if !strings.Contains(c.Detail, "the tool exploded") {
		t.Errorf("Detail = %q, want git's own stderr", c.Detail)
	}
}

func TestCheckGHReportsPathAndVersion(t *testing.T) {
	stubEmptyEnvironment(t)
	path := stubGH(t, workingGH)

	c := checkGH(context.Background())
	if !c.OK {
		t.Fatalf("expected gh check to pass: %+v", c)
	}
	if c.Name != "gh" {
		t.Errorf("Name = %q, want %q", c.Name, "gh")
	}
	if want := path + " (2.50.0 (2024-01-01))"; c.Detail != want {
		t.Errorf("Detail = %q, want %q", c.Detail, want)
	}
}

func TestCheckGHAbsent(t *testing.T) {
	stubEmptyEnvironment(t)

	c := checkGH(context.Background())
	if c.OK {
		t.Fatalf("expected gh check to fail when gh is absent: %+v", c)
	}
	if c.Name != "gh" {
		t.Errorf("Name = %q, want %q", c.Name, "gh")
	}
	if !strings.Contains(c.Detail, github.EnvCLIPath) {
		t.Errorf("Detail = %q, want it to name the override variable", c.Detail)
	}
}

func TestCheckGHVersionFails(t *testing.T) {
	stubEmptyEnvironment(t)
	stubGH(t, brokenBin)

	c := checkGH(context.Background())
	if c.OK {
		t.Fatalf("expected gh check to fail when --version errors: %+v", c)
	}
	if !strings.Contains(c.Detail, "the tool exploded") {
		t.Errorf("Detail = %q, want gh's own stderr", c.Detail)
	}
}

func TestCheckGHAuthAuthenticated(t *testing.T) {
	stubEmptyEnvironment(t)
	stubGH(t, workingGH)

	c := checkGHAuth(context.Background())
	if !c.OK {
		t.Fatalf("expected gh auth check to pass: %+v", c)
	}
	if c.Name != "gh auth" || c.Detail != "authenticated" {
		t.Errorf("check = %+v", c)
	}
}

func TestCheckGHAuthNotAuthenticated(t *testing.T) {
	stubEmptyEnvironment(t)
	stubGH(t, unauthenticatedGH)

	c := checkGHAuth(context.Background())
	if c.OK {
		t.Fatalf("expected gh auth check to fail when not logged in: %+v", c)
	}
	if !strings.Contains(c.Detail, "gh auth login") {
		t.Errorf("Detail = %q, want the remedy for a logged-out gh", c.Detail)
	}
}

func TestCheckGHAuthDistinguishesMissingGH(t *testing.T) {
	stubEmptyEnvironment(t)

	c := checkGHAuth(context.Background())
	if c.OK {
		t.Fatalf("expected gh auth check to fail when gh is absent: %+v", c)
	}
	if c.Detail != "gh not available" {
		t.Errorf("Detail = %q, want it to distinguish a missing gh from a logged-out one", c.Detail)
	}
}

func TestRunAllPrerequisitesPresent(t *testing.T) {
	stubEmptyEnvironment(t)
	stubGit(t, workingGit)
	stubGH(t, workingGH)

	var buf bytes.Buffer
	ok, err := Run(context.Background(), &buf, present.Structured)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true, report:\n%s", buf.String())
	}
	if lines := strings.Count(buf.String(), "\n"); lines != 3 {
		t.Errorf("expected 3 report lines, got %d:\n%s", lines, buf.String())
	}
	if strings.Contains(buf.String(), "FAIL") {
		t.Errorf("unexpected failure in report:\n%s", buf.String())
	}
}

func TestRunContinuesAfterAFailedCheck(t *testing.T) {
	stubEmptyEnvironment(t)
	stubGH(t, workingGH)

	var buf bytes.Buffer
	ok, err := Run(context.Background(), &buf, present.JSON)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ok {
		t.Error("expected ok=false when git is missing")
	}
	checks := decodeChecks(t, buf.Bytes())
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks even though the first failed, got %d: %+v", len(checks), checks)
	}
	if checks[0].OK || !checks[1].OK || !checks[2].OK {
		t.Errorf("expected only the git check to fail: %+v", checks)
	}
}

func TestRunBothPrerequisitesAbsent(t *testing.T) {
	stubEmptyEnvironment(t)

	var buf bytes.Buffer
	ok, err := Run(context.Background(), &buf, present.JSON)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ok {
		t.Error("expected ok=false when neither prerequisite is installed")
	}
	checks := decodeChecks(t, buf.Bytes())
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d: %+v", len(checks), checks)
	}
	for _, c := range checks {
		if c.OK {
			t.Errorf("expected %s to fail: %+v", c.Name, c)
		}
	}
}

func decodeChecks(t *testing.T, b []byte) []Check {
	t.Helper()
	var checks []Check
	if err := json.Unmarshal(b, &checks); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, b)
	}
	return checks
}

func TestReportAllOK(t *testing.T) {
	var buf bytes.Buffer
	ok, err := report(&buf, []Check{
		{Name: "git", OK: true, Detail: "/usr/bin/git (2.40)"},
		{Name: "gh", OK: true, Detail: "/usr/bin/gh (2.40)"},
	}, present.Structured)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !ok {
		t.Error("expected ok=true")
	}
	out := buf.String()
	if !strings.Contains(out, "ok") || strings.Contains(out, "FAIL") {
		t.Errorf("unexpected report:\n%s", out)
	}
}

func TestReportWithFailure(t *testing.T) {
	var buf bytes.Buffer
	ok, err := report(&buf, []Check{
		{Name: "git", OK: true, Detail: "/usr/bin/git (2.40)"},
		{Name: "gh auth", OK: false, Detail: "not authenticated"},
	}, present.Structured)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if ok {
		t.Error("expected ok=false when a check fails")
	}
	if !strings.Contains(buf.String(), "FAIL") {
		t.Errorf("expected FAIL in report:\n%s", buf.String())
	}
}

func TestReportColorMarksDiffer(t *testing.T) {
	var plain, colored bytes.Buffer
	checks := []Check{{Name: "gh auth", Detail: "not authenticated"}}
	if _, err := report(&plain, checks, present.Structured); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := report(&colored, checks, present.Color); err != nil {
		t.Fatalf("report: %v", err)
	}
	if plain.String() == colored.String() {
		t.Errorf("expected colored output to differ from plain:\n%q", colored.String())
	}
	if !strings.Contains(colored.String(), "FAIL") {
		t.Errorf("expected colored output to still name the verdict:\n%q", colored.String())
	}
}

func TestReportJSON(t *testing.T) {
	var buf bytes.Buffer
	ok, err := report(&buf, []Check{
		{Name: "git", OK: true, Detail: "/usr/bin/git (2.40)"},
		{Name: "gh auth", OK: false, Detail: "not authenticated"},
	}, present.JSON)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if ok {
		t.Error("expected ok=false when a check fails")
	}
	checks := decodeChecks(t, buf.Bytes())
	if len(checks) != 2 || checks[1].Name != "gh auth" || checks[1].OK {
		t.Errorf("unexpected decoded checks: %+v", checks)
	}
}

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReportWriterError(t *testing.T) {
	want := errors.New("disk on fire")
	ok, err := report(errWriter{want}, []Check{{Name: "git", OK: true, Detail: "/usr/bin/git (2.40)"}}, present.Structured)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if ok {
		t.Error("expected ok=false when the report cannot be written")
	}
}

func TestReportWriterErrorOnMultiLineDetail(t *testing.T) {
	want := errors.New("pipe closed")
	checks := []Check{{Name: "git", Detail: "fatal: bad object\nfatal: try again"}}
	ok, err := report(errWriter{want}, checks, present.Structured)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if ok {
		t.Error("expected ok=false when the report cannot be written")
	}
}
