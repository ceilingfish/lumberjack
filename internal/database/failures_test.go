package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

func closedClient(t *testing.T) *Client {
	t.Helper()
	c := openTemp(t)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return c
}

func readOnlyClient(t *testing.T) *Client {
	t.Helper()
	c := openTemp(t)
	if _, err := c.Exec("PRAGMA query_only = 1"); err != nil {
		t.Fatalf("PRAGMA query_only: %v", err)
	}
	return c
}

func TestCreateRepositoryExistsCheckError(t *testing.T) {
	c := closedClient(t)

	err := c.CreateRepository(context.Background(), newRepo("/a", "a", "A"))
	if err == nil {
		t.Fatal("expected an error from the existence check, got nil")
	}
	if errors.Is(err, ErrRepositoryExists) {
		t.Errorf("a query failure must not be reported as ErrRepositoryExists: %v", err)
	}
}

func TestCreateRepositoryInsertError(t *testing.T) {
	c := readOnlyClient(t)

	err := c.CreateRepository(context.Background(), newRepo("/a", "a", "A"))
	if err == nil {
		t.Fatal("expected an error inserting into a read-only database, got nil")
	}
	if errors.Is(err, ErrRepositoryExists) {
		t.Errorf("an insert failure must not be reported as ErrRepositoryExists: %v", err)
	}
}

func TestListRepositoriesError(t *testing.T) {
	c := closedClient(t)

	repos, err := c.ListRepositories(context.Background())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if repos != nil {
		t.Errorf("expected no repositories alongside the error, got %v", repos)
	}
}

func TestFindRepositoryErrorIsNotNotFound(t *testing.T) {
	c := closedClient(t)

	_, err := c.FindRepository(context.Background(), "a")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("a query failure must be distinguishable from not-found: %v", err)
	}
}

func TestFindRepositoryEnclosingError(t *testing.T) {
	c := closedClient(t)

	_, err := c.findRepositoryEnclosing(context.Background(), "/code/repo/cmd")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("a query failure must not be reported as not-found: %v", err)
	}
}

func TestRepositoryOwningDirError(t *testing.T) {
	c := closedClient(t)

	repo, err := c.repositoryOwningDir(context.Background(), "/code/repo")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if repo != nil {
		t.Errorf("expected no repository alongside the error, got %v", repo)
	}
}

func TestDeleteRepositoryTransactionError(t *testing.T) {
	c := closedClient(t)

	removed, err := c.DeleteRepository(context.Background(), 1)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("a transaction failure must not be reported as not-found: %v", err)
	}
	if removed != 0 {
		t.Errorf("worktreesRemoved = %d, want 0 when the transaction fails", removed)
	}
}

func TestDeleteRepositoryWorktreeDeleteError(t *testing.T) {
	c := readOnlyClient(t)

	if _, err := c.DeleteRepository(context.Background(), 1); err == nil {
		t.Fatal("expected an error deleting from a read-only database, got nil")
	}
}

func TestDeleteRepositoryRepositoryDeleteError(t *testing.T) {
	c := openTemp(t)
	if _, err := c.Exec("ALTER TABLE repositories RENAME TO repositories_moved"); err != nil {
		t.Fatalf("renaming repositories: %v", err)
	}

	_, err := c.DeleteRepository(context.Background(), 1)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrRepositoryNotFound) {
		t.Errorf("a failed delete must not be reported as not-found: %v", err)
	}
}

func TestRepositoryUpdateErrors(t *testing.T) {
	ctx := context.Background()
	updates := []struct {
		name string
		call func(*Client) error
	}{
		{"UpdateSyncResult", func(c *Client) error { return c.UpdateSyncResult(ctx, 1, time.Unix(0, 0), nil) }},
		{"UpdateLogin", func(c *Client) error { return c.UpdateLogin(ctx, 1, "someone") }},
		{"UpdateSetupConsent", func(c *Client) error { return c.UpdateSetupConsent(ctx, 1, "fingerprint") }},
	}
	for _, u := range updates {
		t.Run(u.name, func(t *testing.T) {
			if err := u.call(closedClient(t)); err == nil {
				t.Fatalf("%s: expected an error, got nil", u.name)
			}
		})
	}
}

func TestReplaceOpenPRsUpsertError(t *testing.T) {
	c := readOnlyClient(t)

	err := c.ReplaceOpenPRs(context.Background(), 1,
		[]TrackedPR{{Number: 7, Branch: "feature/seven"}}, time.Unix(0, 0))
	if err == nil {
		t.Fatal("expected an error upserting into a read-only database, got nil")
	}
}

func TestReplaceOpenPRsPruneError(t *testing.T) {
	c := readOnlyClient(t)

	if err := c.ReplaceOpenPRs(context.Background(), 1, nil, time.Unix(0, 0)); err == nil {
		t.Fatal("expected an error pruning in a read-only database, got nil")
	}
}

func TestSyncRunErrors(t *testing.T) {
	ctx := context.Background()
	c := closedClient(t)

	if _, err := c.StartSyncRun(ctx, 1, time.Unix(0, 0)); err == nil {
		t.Error("StartSyncRun: expected an error, got nil")
	}

	run := &schema.SyncRun{ID: 1}
	if err := c.FinishSyncRun(ctx, run, time.Unix(0, 0), 0, 0, nil); err == nil {
		t.Error("FinishSyncRun: expected an error, got nil")
	}

	latest, err := c.LatestSyncRun(ctx, 1)
	if err == nil {
		t.Error("LatestSyncRun: expected an error, got nil")
	}
	if latest != nil {
		t.Errorf("LatestSyncRun = %v, want nil alongside the error", latest)
	}
}

func TestWorktreeErrors(t *testing.T) {
	ctx := context.Background()
	ops := []struct {
		name string
		call func(*Client) error
	}{
		{"CreateWorktree", func(c *Client) error {
			return c.CreateWorktree(ctx, &schema.Worktree{RepositoryID: 1, BranchName: "b", DirectoryPath: "/d"})
		}},
		{"SetWorktreePR", func(c *Client) error { return c.SetWorktreePR(ctx, 1, nil, "b") }},
		{"TouchWorktreesSyncedAt", func(c *Client) error { return c.TouchWorktreesSyncedAt(ctx, 1, time.Unix(0, 0)) }},
		{"SetWorktreeDirectory", func(c *Client) error { return c.SetWorktreeDirectory(ctx, 1, "/d") }},
		{"SetWorktreeSetupError", func(c *Client) error { return c.SetWorktreeSetupError(ctx, 1, nil) }},
		{"DeleteWorktree", func(c *Client) error { return c.DeleteWorktree(ctx, 1) }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			if err := op.call(closedClient(t)); err == nil {
				t.Fatalf("%s: expected an error, got nil", op.name)
			}
		})
	}
}

func TestListWorktreesErrorReturnsNoRows(t *testing.T) {
	wts, err := closedClient(t).ListWorktrees(context.Background(), 1)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if wts != nil {
		t.Errorf("expected no worktrees alongside the error, got %v", wts)
	}
}

func TestFindWorktreeErrorIsNotNotFound(t *testing.T) {
	_, err := closedClient(t).FindWorktree(context.Background(), 1, "feature/x")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrWorktreeNotFound) {
		t.Errorf("a query failure must be distinguishable from not-found: %v", err)
	}
}

func TestDefaultPathWithoutHome(t *testing.T) {
	t.Setenv(EnvDBPath, "")
	t.Setenv("HOME", "")

	if _, err := DefaultPath(); err == nil {
		t.Fatal("expected an error when the home directory cannot be resolved, got nil")
	}
}

func TestOpenOnAlreadyMigratedDatabasePreservesRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.sqlite")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	repo := newRepo("/a", "a", "A")
	if err := first.CreateRepository(ctx, repo); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	got, err := second.FindRepository(ctx, "/a")
	if err != nil {
		t.Fatalf("FindRepository after re-open: %v", err)
	}
	if got.ID != repo.ID {
		t.Errorf("re-opened database lost the row: got id %d, want %d", got.ID, repo.ID)
	}
}
