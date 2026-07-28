package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// track inserts a worktree row for branch at dir and creates the directory, so
// tidy sees the same state a real checkout would present.
func (h *harness) track(t *testing.T, repo *schema.Repository, branch, dir string) *schema.Worktree {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	wt := &schema.Worktree{RepositoryID: repo.ID, BranchName: branch, DirectoryPath: dir}
	if err := h.db.CreateWorktree(context.Background(), wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	return wt
}

// storedDir reads back a worktree row's tracked directory.
func (h *harness) storedDir(t *testing.T, id int64) string {
	t.Helper()
	wts, err := h.db.ListWorktrees(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	for _, wt := range wts {
		if wt.ID == id {
			return wt.DirectoryPath
		}
	}
	t.Fatalf("worktree %d not found", id)
	return ""
}

func TestTidyRepositoryMovesMisplacedWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// The shape this feature exists for: a worktree checked out under
	// .claude/worktrees/ rather than beside its siblings as <prefix>-<slug>.
	from := filepath.Join(repo.LocalPath, ".claude", "worktrees", "foo")
	want := filepath.Join(h.parent, "n-foo")
	wt := h.track(t, repo, "feature/foo", from)

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves=%v, want 1", moves)
	}
	m := moves[0]
	if !m.Moved || m.Err != "" || m.From != from || m.To != want || m.Branch != "feature/foo" {
		t.Errorf("move=%+v, want a clean move %s -> %s", m, from, want)
	}
	if got := h.storedDir(t, wt.ID); got != want {
		t.Errorf("tracked directory=%s, want %s", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("worktree not on disk at %s: %v", want, err)
	}
	if len(h.git.moves) != 1 || h.git.moves[0] != [2]string{from, want} {
		t.Errorf("git moves=%v, want one %s -> %s", h.git.moves, from, want)
	}
}

func TestTidyRepositoryIgnoresWorktreesAlreadyInPlace(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.track(t, repo, "feature/foo", filepath.Join(h.parent, "n-foo"))

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 0 {
		t.Errorf("moves=%v, want none for a worktree already in place", moves)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none", h.git.moves)
	}
}

func TestTidyRepositoryDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	wt := h.track(t, repo, "feature/foo", from)

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", true)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || moves[0].Err != "" {
		t.Fatalf("moves=%+v, want one reported-but-unmoved entry", moves)
	}
	if got := h.storedDir(t, wt.ID); got != from {
		t.Errorf("tracked directory=%s, want it left at %s", got, from)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none on a dry run", h.git.moves)
	}
}

func TestTidyRepositorySkipsOccupiedDestination(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// Two branches whose slugs collide: "foo" already holds the idiomatic path,
	// so the other one has nowhere convention-conforming to go.
	h.track(t, repo, "foo", filepath.Join(h.parent, "n-foo"))
	other := h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"))

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || moves[0].Err == "" {
		t.Fatalf("moves=%+v, want one skipped entry with a reason", moves)
	}
	if got := h.storedDir(t, other.ID); got != other.DirectoryPath {
		t.Errorf("tracked directory=%s, want it left at %s", got, other.DirectoryPath)
	}
}

func TestTidyRepositorySkipsDestinationOccupiedOnDisk(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	wt := h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"))
	// An untracked directory sitting on the destination path.
	if err := os.MkdirAll(filepath.Join(h.parent, "n-foo"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || moves[0].Err == "" {
		t.Fatalf("moves=%+v, want one skipped entry with a reason", moves)
	}
	if got := h.storedDir(t, wt.ID); got != wt.DirectoryPath {
		t.Errorf("tracked directory=%s, want it untouched", got)
	}
}

func TestTidyRepositoryReportsMoveFailureAndTidiesTheRest(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	locked := filepath.Join(h.parent, "elsewhere", "locked")
	movable := filepath.Join(h.parent, "elsewhere", "movable")
	lockedWT := h.track(t, repo, "feature/locked", locked)
	movableWT := h.track(t, repo, "feature/movable", movable)
	h.git.moveErr = map[string]error{locked: errors.New("worktree is locked")}

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 2 {
		t.Fatalf("moves=%+v, want 2", moves)
	}
	byBranch := map[string]TidyMove{}
	for _, m := range moves {
		byBranch[m.Branch] = m
	}
	if m := byBranch["feature/locked"]; m.Moved || m.Err == "" {
		t.Errorf("locked move=%+v, want skipped with a reason", m)
	}
	if m := byBranch["feature/movable"]; !m.Moved || m.Err != "" {
		t.Errorf("movable move=%+v, want moved", m)
	}
	if got := h.storedDir(t, lockedWT.ID); got != locked {
		t.Errorf("locked worktree directory=%s, want it untouched", got)
	}
	if got, want := h.storedDir(t, movableWT.ID), filepath.Join(h.parent, "n-movable"); got != want {
		t.Errorf("movable worktree directory=%s, want %s", got, want)
	}
}

func TestTidyRepositorySkipsMissingDirectory(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	gone := filepath.Join(h.parent, "elsewhere", "gone")
	h.track(t, repo, "feature/gone", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || moves[0].Err == "" {
		t.Fatalf("moves=%+v, want one skipped entry with a reason", moves)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none for a missing directory", h.git.moves)
	}
}

// One misplaced worktree can sit on the path another one belongs at. Tidy must
// never move a worktree onto an occupied path, so the blocked one waits: it is
// reported as skipped and a later run (once the occupant has moved) relocates
// it.
func TestTidyRepositoryWaitsForAnOccupantToVacate(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// "bar" belongs at n-bar, which "foo" currently occupies; "foo" is itself
	// misplaced and belongs at n-foo. Rows are visited in insertion order, so
	// bar is considered while n-bar is still taken.
	barWT := h.track(t, repo, "feature/bar", filepath.Join(h.parent, "elsewhere", "bar"))
	fooWT := h.track(t, repo, "feature/foo", filepath.Join(h.parent, "n-bar"))

	moves, err := h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 2 {
		t.Fatalf("moves=%+v, want 2", moves)
	}
	byBranch := map[string]TidyMove{}
	for _, m := range moves {
		byBranch[m.Branch] = m
	}
	if m := byBranch["feature/bar"]; m.Moved || m.Err == "" {
		t.Errorf("bar move=%+v, want skipped: its destination was still occupied", m)
	}
	if m := byBranch["feature/foo"]; !m.Moved {
		t.Errorf("foo move=%+v, want moved out of n-bar", m)
	}
	if got := h.storedDir(t, barWT.ID); got != barWT.DirectoryPath {
		t.Errorf("bar directory=%s, want it untouched", got)
	}

	// foo has now vacated n-bar, so a second run finishes the job.
	moves, err = h.svc.TidyRepository(context.Background(), repo, "", false)
	if err != nil {
		t.Fatalf("second TidyRepository: %v", err)
	}
	if len(moves) != 1 || !moves[0].Moved || moves[0].Branch != "feature/bar" {
		t.Fatalf("second run moves=%+v, want bar moved", moves)
	}
	if got, want := h.storedDir(t, barWT.ID), filepath.Join(h.parent, "n-bar"); got != want {
		t.Errorf("bar directory=%s, want %s", got, want)
	}
	if got, want := h.storedDir(t, fooWT.ID), filepath.Join(h.parent, "n-foo"); got != want {
		t.Errorf("foo directory=%s, want %s", got, want)
	}
}

func TestTidyRepositoryRestrictedToOneWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	fooWT := h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"))
	barWT := h.track(t, repo, "feature/bar", filepath.Join(h.parent, "elsewhere", "bar"))

	moves, err := h.svc.TidyRepository(context.Background(), repo, "feature/foo", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Branch != "feature/foo" || !moves[0].Moved {
		t.Fatalf("moves=%+v, want only feature/foo moved", moves)
	}
	if got, want := h.storedDir(t, fooWT.ID), filepath.Join(h.parent, "n-foo"); got != want {
		t.Errorf("foo directory=%s, want %s", got, want)
	}
	if got := h.storedDir(t, barWT.ID); got != barWT.DirectoryPath {
		t.Errorf("bar directory=%s, want it left alone", got)
	}
}

// The ref narrows what tidy moves, never what blocks a move: an out-of-scope
// worktree still holds its directory, so the named one must not be moved on
// top of it.
func TestTidyRepositoryRestrictedStillRespectsOtherWorktrees(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	// "foo" belongs at n-foo, which out-of-scope "other" occupies.
	fooWT := h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"))
	h.track(t, repo, "foo", filepath.Join(h.parent, "n-foo"))

	moves, err := h.svc.TidyRepository(context.Background(), repo, "feature/foo", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || moves[0].Err == "" {
		t.Fatalf("moves=%+v, want one skipped entry with a reason", moves)
	}
	if got := h.storedDir(t, fooWT.ID); got != fooWT.DirectoryPath {
		t.Errorf("foo directory=%s, want it untouched", got)
	}
}

func TestTidyRepositoryRestrictedByDirectoryBaseName(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	wt := h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "misnamed"))

	// The same reference forms `worktree delete` accepts resolve here too.
	moves, err := h.svc.TidyRepository(context.Background(), repo, "misnamed", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || !moves[0].Moved {
		t.Fatalf("moves=%+v, want the worktree moved", moves)
	}
	if got, want := h.storedDir(t, wt.ID), filepath.Join(h.parent, "n-foo"); got != want {
		t.Errorf("directory=%s, want %s", got, want)
	}
}

func TestTidyRepositoryUnknownWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"))

	_, err := h.svc.TidyRepository(context.Background(), repo, "ghost", false)
	if !errors.Is(err, database.ErrWorktreeNotFound) {
		t.Errorf("err=%v, want ErrWorktreeNotFound", err)
	}
}

// A worktree already in its idiomatic location reports no moves rather than a
// not-found error — the ref matched, there was just nothing to do.
func TestTidyRepositoryRestrictedToWorktreeAlreadyInPlace(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.track(t, repo, "feature/foo", filepath.Join(h.parent, "n-foo"))

	moves, err := h.svc.TidyRepository(context.Background(), repo, "feature/foo", false)
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 0 {
		t.Errorf("moves=%+v, want none", moves)
	}
}
