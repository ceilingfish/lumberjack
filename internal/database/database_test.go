package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// openTemp opens a fresh database in a temp dir, migrations applied.
func openTemp(t *testing.T) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	client, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestDefaultPathEnvOverride(t *testing.T) {
	t.Setenv(EnvDBPath, "/custom/db.sqlite")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != "/custom/db.sqlite" {
		t.Errorf("got %q, want the LUMBERJACK_DB_PATH override", got)
	}
}

func TestDefaultPathHomeFallback(t *testing.T) {
	// Empty override falls back to ~/.lumberjack/db.sqlite.
	t.Setenv(EnvDBPath, "")

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if want := filepath.Join(home, ".lumberjack", "db.sqlite"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOpenDefault(t *testing.T) {
	// Point the default at a temp path so OpenDefault runs end-to-end.
	t.Setenv(EnvDBPath, filepath.Join(t.TempDir(), "default.sqlite"))

	client, err := OpenDefault(context.Background())
	if err != nil {
		t.Fatalf("OpenDefault: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var n int
	if err := client.NewRaw("SELECT count(*) FROM repositories").Scan(context.Background(), &n); err != nil {
		t.Errorf("querying migrated table: %v", err)
	}
}

func TestOpenCreatesParentDirs(t *testing.T) {
	// Nested, not-yet-existing parents must be created by Open.
	path := filepath.Join(t.TempDir(), "a", "b", "c", "db.sqlite")

	client, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = client.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected database file at %q: %v", path, err)
	}
}

func TestOpenParentDirError(t *testing.T) {
	// A regular file where Open needs a directory makes MkdirAll fail.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Open(context.Background(), filepath.Join(file, "sub", "db.sqlite")); err == nil {
		t.Fatal("expected error when parent path is a file, got nil")
	}
}

func TestOpenMigrateError(t *testing.T) {
	// Point the DB path at an existing directory: sql.Open succeeds lazily but
	// the first migration query fails, exercising Open's migrate-error path.
	dir := t.TempDir()

	if _, err := Open(context.Background(), dir); err == nil {
		t.Fatal("expected migrate error when path is a directory, got nil")
	}
}

func TestOpenAppliesMigrations(t *testing.T) {
	client := openTemp(t)

	// All four tables from the initial migration should exist and be queryable.
	for _, table := range []string{"repositories", "pull_requests", "worktrees", "sync_runs"} {
		var n int
		if err := client.NewRaw("SELECT count(*) FROM "+table).Scan(context.Background(), &n); err != nil {
			t.Errorf("querying table %q: %v", table, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")

	first, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = first.Close()

	// Re-opening the same database re-runs migrate with nothing pending.
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	_ = second.Close()
}

func TestRepositoryRoundTrip(t *testing.T) {
	client := openTemp(t)
	ctx := context.Background()

	repo := &schema.Repository{
		LocalPath:         "/path/to/my_repo",
		WorktreeParentDir: "/path/to",
		DirPrefix:         "my_repo",
		GithubOwner:       "ceilingfish",
		GithubName:        "Lumberjack",
		DefaultRemote:     "origin",
		Host:              "github.com",
	}
	if _, err := client.NewInsert().Model(repo).Exec(ctx); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if repo.ID == 0 {
		t.Fatal("expected autoincrement ID to be set")
	}

	got := new(schema.Repository)
	if err := client.NewSelect().Model(got).Where("id = ?", repo.ID).Scan(ctx); err != nil {
		t.Fatalf("select repository: %v", err)
	}
	if got.LocalPath != repo.LocalPath || got.DirPrefix != "my_repo" {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected created_at default to be populated")
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	client := openTemp(t)
	ctx := context.Background()

	// repository_id 999 does not exist; the FK constraint must reject it.
	wt := &schema.Worktree{
		RepositoryID:  999,
		BranchName:    "feature/x",
		DirectoryPath: "/path/to/my_repo-x",
		CreatedBy:     schema.CreatedByLumberjack,
	}
	if _, err := client.NewInsert().Model(wt).Exec(ctx); err == nil {
		t.Fatal("expected foreign-key violation, got nil error")
	}
}

func TestCascadeDelete(t *testing.T) {
	client := openTemp(t)
	ctx := context.Background()

	repo := &schema.Repository{
		LocalPath: "/repo", WorktreeParentDir: "/", DirPrefix: "repo",
		GithubOwner: "o", GithubName: "n", DefaultRemote: "origin", Host: "github.com",
	}
	if _, err := client.NewInsert().Model(repo).Exec(ctx); err != nil {
		t.Fatalf("insert repository: %v", err)
	}

	now := time.Now()
	run := &schema.SyncRun{RepositoryID: repo.ID, StartedAt: now, Status: schema.SyncStatusOK}
	if _, err := client.NewInsert().Model(run).Exec(ctx); err != nil {
		t.Fatalf("insert sync_run: %v", err)
	}

	if _, err := client.NewDelete().Model(repo).WherePK().Exec(ctx); err != nil {
		t.Fatalf("delete repository: %v", err)
	}

	n, err := client.NewSelect().Model((*schema.SyncRun)(nil)).Count(ctx)
	if err != nil {
		t.Fatalf("count sync_runs: %v", err)
	}
	if n != 0 {
		t.Errorf("expected cascade to remove sync_runs, %d remain", n)
	}
}
