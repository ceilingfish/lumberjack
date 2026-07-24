package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

// TestCmdFormatUnknownRejected: an unrecognised --format value errors clearly
// rather than silently falling back to a default.
func TestCmdFormatUnknownRejected(t *testing.T) {
	serveStub(t, &stubService{})
	_, err := run(t, "", "--format", "yaml", "repositories")
	if err == nil || !strings.Contains(err.Error(), "yaml") {
		t.Errorf("expected an error naming the bad format, got %v", err)
	}
}

// TestCmdFormatDefaultIsUncoloured: tests run with a non-terminal stdout, so
// the default resolution (--format omitted) must be `structured`: plain text
// with no ANSI escape codes, identical in shape to the pre-#3 output.
func TestCmdFormatDefaultIsUncoloured(t *testing.T) {
	serveStub(t, &stubService{repos: []*lumberjackv1.Repository{
		{DirPrefix: "a", LocalPath: "/p/a"},
	}})
	out, err := run(t, "", "repositories")
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI escapes in the default (non-TTY) format, got %q", out)
	}
}

// TestCmdFormatColorExplicitGatedNonTTY: --format color still degrades to
// structured (no colour) when stdout is not a terminal — colour gating always
// wins, even over an explicit flag.
func TestCmdFormatColorExplicitGatedNonTTY(t *testing.T) {
	serveStub(t, &stubService{repos: []*lumberjackv1.Repository{
		{DirPrefix: "a", LocalPath: "/p/a"},
	}})
	out, err := run(t, "", "--format", "color", "repositories")
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected --format color to degrade to structured on a non-TTY, got %q", out)
	}
}

func TestCmdFormatJSONRepositoriesList(t *testing.T) {
	serveStub(t, &stubService{repos: []*lumberjackv1.Repository{
		{DirPrefix: "a", LocalPath: "/p/a", LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_OK},
		{DirPrefix: "b", LocalPath: "/p/b"},
	}})
	out, err := run(t, "", "--format", "json", "repositories")
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(decoded))
	}
	if decoded[0]["localPath"] != "/p/a" || decoded[0]["dirPrefix"] != "a" {
		t.Errorf("unexpected first element: %+v", decoded[0])
	}
}

func TestCmdFormatJSONRepositoriesListEmpty(t *testing.T) {
	serveStub(t, &stubService{})
	out, err := run(t, "", "--format", "json", "repositories")
	if err != nil {
		t.Fatalf("repositories: %v", err)
	}
	if got := strings.TrimSpace(out); got != "[]" {
		t.Errorf("expected a bare empty array, got %q", got)
	}
}

func TestCmdFormatJSONRepositoryDetail(t *testing.T) {
	serveStub(t, &stubService{})
	out, err := run(t, "", "--format", "json", "repositories", "n")
	if err != nil {
		t.Fatalf("repositories n: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON object: %v (%q)", err, out)
	}
	if decoded["githubOwner"] != "o" || decoded["githubName"] != "n" {
		t.Errorf("unexpected decoded repository: %+v", decoded)
	}
}

func TestCmdFormatJSONWorktrees(t *testing.T) {
	num := int64(7)
	serveStub(t, &stubService{worktrees: []*lumberjackv1.Worktree{
		{DirectoryPath: "/p/n-x", BranchName: "feature/x", GithubPrNumber: &num},
	}})
	out, err := run(t, "", "--format", "json", "repositories", "n", "worktrees")
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if len(decoded) != 1 || decoded[0]["branchName"] != "feature/x" {
		t.Errorf("unexpected decoded worktrees: %+v", decoded)
	}
}

func TestCmdFormatJSONDeleteWorktreeRequiresConfirmationSkipsPrompt(t *testing.T) {
	stub := &stubService{deleteConfirm: true}
	serveStub(t, stub)
	out, err := run(t, "", "--format", "json", "repositories", "n", "worktree", "feature/x", "delete")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded["requiresConfirmation"] != true || stub.deletedForced {
		t.Errorf("expected an informational, non-interactive JSON response, got %+v (forced=%v)", decoded, stub.deletedForced)
	}
}

func TestCmdFormatJSONDoctor(t *testing.T) {
	out, _ := run(t, "", "--format", "json", "doctor")
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if len(decoded) == 0 {
		t.Error("expected at least one check")
	}
}

func TestCmdFormatJSONSync(t *testing.T) {
	pr := int64(1)
	stub := &stubService{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Message: "creating worktree for PR #1"},
		{Repository: "n", Change: &lumberjackv1.WorktreeChange{
			Branch: "feature/x", PrNumber: &pr,
			Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT,
		}},
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{WorktreesCreated: 1}},
	}}
	serveStub(t, stub)
	out, err := run(t, "", "--format", "json", "sync")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	// Only valid JSON reaches stdout: the plain-text progress message is
	// suppressed, and the whole result is a single bare array.
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 repository result, got %d (%q)", len(decoded), out)
	}
	if decoded[0]["repository"] != "n" {
		t.Errorf("unexpected repository field: %+v", decoded[0])
	}
	changes, _ := decoded[0]["changes"].([]any)
	if len(changes) != 1 {
		t.Errorf("expected 1 change, got %+v", decoded[0]["changes"])
	}
	summary, _ := decoded[0]["summary"].(map[string]any)
	// protojson renders int64 fields as JSON strings (the proto3 JSON mapping,
	// since 64-bit integers don't round-trip precisely through JS numbers).
	if summary["worktreesCreated"] != "1" {
		t.Errorf("unexpected summary: %+v", summary)
	}
}

func TestCmdFormatJSONInit(t *testing.T) {
	stub := &stubService{initAdopted: []*lumberjackv1.WorktreeChange{
		{Branch: "feature/x", Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED},
	}}
	serveStub(t, stub)
	out, err := run(t, "", "--format", "json", "init", ".")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	repo, _ := decoded["repository"].(map[string]any)
	if repo["githubOwner"] != "o" {
		t.Errorf("unexpected repository field: %+v", decoded)
	}
	adopted, _ := decoded["adopted"].([]any)
	if len(adopted) != 1 {
		t.Errorf("expected 1 adopted change, got %+v", decoded["adopted"])
	}
}

func TestCmdFormatJSONDaemonStatus(t *testing.T) {
	out, _ := run(t, "", "--format", "json", "daemon", "status")
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded["message"] == nil {
		t.Errorf("expected a message field, got %+v", decoded)
	}
}
