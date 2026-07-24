package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/present"
)

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
	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(decoded) != 2 || decoded[1]["name"] != "gh auth" || decoded[1]["ok"] != false {
		t.Errorf("unexpected decoded checks: %+v", decoded)
	}
}

func TestCheckGitResolvesEnvOverride(t *testing.T) {
	// A bogus override makes git resolution fail, exercising the error branch
	// without depending on the host's git.
	t.Setenv("LUMBERJACK_GIT_PATH", "/definitely/not/git")
	c := checkGit(context.Background())
	if c.OK {
		t.Errorf("expected git check to fail with bad override: %+v", c)
	}
	if c.Name != "git" {
		t.Errorf("name = %q", c.Name)
	}
}

func TestCheckGHResolvesEnvOverride(t *testing.T) {
	t.Setenv("LUMBERJACK_GITHUB_CLI_PATH", "/definitely/not/gh")
	if c := checkGH(context.Background()); c.OK {
		t.Errorf("expected gh check to fail with bad override: %+v", c)
	}
	if c := checkGHAuth(context.Background()); c.OK {
		t.Errorf("expected gh auth check to fail with bad override: %+v", c)
	}
}

func TestRunWritesReport(t *testing.T) {
	// End-to-end: Run must always produce a report and never error on the
	// writer. Whether checks pass depends on the host, so we only assert the
	// report is non-empty and Run returns no writer error.
	var buf bytes.Buffer
	if _, err := Run(context.Background(), &buf, present.Structured); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected a non-empty report")
	}
}
