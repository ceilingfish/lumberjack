package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{DryRun: true})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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
	moves, err = h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{Ref: "feature/foo"})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{Ref: "feature/foo"})
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
	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{Ref: "misnamed"})
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

	_, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{Ref: "ghost"})
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

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{Ref: "feature/foo"})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 0 {
		t.Errorf("moves=%+v, want none", moves)
	}
}

// lockedWorktree tracks a misplaced worktree at dir and has git hold a lock on
// it with reason, the state every lock-strategy test starts from.
func (h *harness) lockedWorktree(
	t *testing.T, repo *schema.Repository, branch, dir, reason string,
) *schema.Worktree {
	t.Helper()
	wt := h.track(t, repo, branch, dir)
	h.git.locks[dir] = reason
	return wt
}

// The default strategy leaves a locked worktree alone: git would refuse the move
// anyway, so tidy does not even attempt it and says why.
func TestTidyRepositorySkipsLockedWorktreeByDefault(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	wt := h.lockedWorktree(t, repo, "feature/foo", from, "in use")

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves=%+v, want 1", moves)
	}
	m := moves[0]
	if m.Moved || !m.Locked || m.LockReason != "in use" || !strings.Contains(m.Err, "locked") {
		t.Errorf("move=%+v, want an unmoved, locked entry reporting the lock", m)
	}
	if got := h.storedDir(t, wt.ID); got != from {
		t.Errorf("tracked directory=%s, want it left at %s", got, from)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none for a locked worktree", h.git.moves)
	}
	if _, stillLocked := h.git.locks[from]; !stillLocked {
		t.Error("lock was lifted, want it left in place")
	}
}

// LockUnlock is the interactive default: the lock is lifted only for the move
// and put back at the new location, with the reason it carried.
func TestTidyRepositoryUnlocksLockedWorktreeForTheMove(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	want := filepath.Join(h.parent, "n-foo")
	wt := h.lockedWorktree(t, repo, "feature/foo", from, "in use")

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockUnlock})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || !moves[0].Moved || moves[0].Err != "" || !moves[0].Locked {
		t.Fatalf("moves=%+v, want the locked worktree moved cleanly", moves)
	}
	if got := h.storedDir(t, wt.ID); got != want {
		t.Errorf("tracked directory=%s, want %s", got, want)
	}
	if reason, ok := h.git.locks[want]; !ok || reason != "in use" {
		t.Errorf("locks=%v, want %s locked again with its original reason", h.git.locks, want)
	}
	if _, stale := h.git.locks[from]; stale {
		t.Errorf("locks=%v, want no lock left behind at %s", h.git.locks, from)
	}
}

// LockDelete moves the worktree and leaves the lock off, which is the point of
// choosing it over LockUnlock.
func TestTidyRepositoryDeletesLockBeforeMoving(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	want := filepath.Join(h.parent, "n-foo")
	wt := h.lockedWorktree(t, repo, "feature/foo", from, "in use")

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockDelete})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || !moves[0].Moved || moves[0].Err != "" {
		t.Fatalf("moves=%+v, want the locked worktree moved cleanly", moves)
	}
	if got := h.storedDir(t, wt.ID); got != want {
		t.Errorf("tracked directory=%s, want %s", got, want)
	}
	if len(h.git.locks) != 0 {
		t.Errorf("locks=%v, want the lock gone", h.git.locks)
	}
}

// LockAbort cancels the whole run: not even the worktrees that were free to move
// are touched, so the user can deal with the lock and start again.
func TestTidyRepositoryAbortsOnLockedWorktree(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	locked := filepath.Join(h.parent, "elsewhere", "locked")
	movable := filepath.Join(h.parent, "elsewhere", "movable")
	h.lockedWorktree(t, repo, "feature/locked", locked, "in use")
	movableWT := h.track(t, repo, "feature/movable", movable)

	_, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockAbort})
	if !errors.Is(err, ErrTidyAborted) {
		t.Fatalf("err=%v, want ErrTidyAborted", err)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none: the run aborted", h.git.moves)
	}
	if got := h.storedDir(t, movableWT.ID); got != movable {
		t.Errorf("movable worktree directory=%s, want it untouched at %s", got, movable)
	}
}

// The per-worktree decisions the CLI's prompt produces: one worktree unlocked
// for its move, another left alone, in a single pass.
func TestTidyRepositoryLockDecisionsOverrideTheStrategy(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	unlockMe := filepath.Join(h.parent, "elsewhere", "unlock-me")
	skipMe := filepath.Join(h.parent, "elsewhere", "skip-me")
	unlockWT := h.lockedWorktree(t, repo, "feature/unlock-me", unlockMe, "")
	skipWT := h.lockedWorktree(t, repo, "feature/skip-me", skipMe, "")

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{
		LockStrategy:  LockSkip,
		LockDecisions: map[string]LockStrategy{unlockMe: LockUnlock},
	})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	byBranch := map[string]TidyMove{}
	for _, m := range moves {
		byBranch[m.Branch] = m
	}
	if m := byBranch["feature/unlock-me"]; !m.Moved || m.Err != "" {
		t.Errorf("unlock-me move=%+v, want moved", m)
	}
	if m := byBranch["feature/skip-me"]; m.Moved || m.Err == "" {
		t.Errorf("skip-me move=%+v, want skipped with a reason", m)
	}
	if got, want := h.storedDir(t, unlockWT.ID), filepath.Join(h.parent, "n-unlock-me"); got != want {
		t.Errorf("unlock-me directory=%s, want %s", got, want)
	}
	if got := h.storedDir(t, skipWT.ID); got != skipMe {
		t.Errorf("skip-me directory=%s, want it untouched", got)
	}
}

// A dry run reports the lock it found without touching it, which is how the CLI
// discovers which worktrees to prompt about.
func TestTidyRepositoryDryRunReportsLocksWithoutTouchingThem(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.lockedWorktree(t, repo, "feature/foo", from, "in use")

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{DryRun: true, LockStrategy: LockUnlock})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	// Err stays empty: asked as if the lock were going to be lifted, the lock is
	// the worktree's only obstacle, so it is a worktree worth asking about.
	if len(moves) != 1 || moves[0].Moved || moves[0].Err != "" {
		t.Fatalf("moves=%+v, want one reported-but-unmoved entry", moves)
	}
	if m := moves[0]; !m.Locked || m.LockReason != "in use" {
		t.Errorf("move=%+v, want the lock and its reason reported", m)
	}
	if reason, ok := h.git.locks[from]; !ok || reason != "in use" {
		t.Errorf("locks=%v, want the lock untouched on a dry run", h.git.locks)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none on a dry run", h.git.moves)
	}
}

// Unlocking is the first thing that can fail, and it fails before anything has
// moved: the worktree stays put, still locked.
func TestTidyRepositoryReportsUnlockFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	wt := h.lockedWorktree(t, repo, "feature/foo", from, "in use")
	h.git.unlockErr = map[string]error{from: errors.New("permission denied")}

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockUnlock})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || !strings.Contains(moves[0].Err, "unlocking") {
		t.Fatalf("moves=%+v, want one entry reporting the failed unlock", moves)
	}
	if got := h.storedDir(t, wt.ID); got != from {
		t.Errorf("tracked directory=%s, want it untouched", got)
	}
	if _, stillLocked := h.git.locks[from]; !stillLocked {
		t.Error("lock was lifted despite the failure")
	}
}

// A lock lifted for a move that then fails is put back where the worktree still
// is, so a failed tidy leaves the worktree exactly as it found it.
func TestTidyRepositoryRestoresLockWhenTheMoveFails(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.lockedWorktree(t, repo, "feature/foo", from, "in use")
	h.git.moveErr = map[string]error{from: errors.New("git said no")}

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockUnlock})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || moves[0].Moved || moves[0].Err == "" {
		t.Fatalf("moves=%+v, want one entry reporting the failed move", moves)
	}
	if reason, ok := h.git.locks[from]; !ok || reason != "in use" {
		t.Errorf("locks=%v, want the lock restored at %s", h.git.locks, from)
	}
}

// A move that landed but could not be re-locked is reported as both: the
// worktree did move, and the lock the user asked to keep is gone.
func TestTidyRepositoryReportsRelockFailureOnAMoveThatLanded(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	want := filepath.Join(h.parent, "n-foo")
	wt := h.lockedWorktree(t, repo, "feature/foo", from, "in use")
	h.git.lockErr = map[string]error{want: errors.New("git said no")}

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockUnlock})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || !moves[0].Moved || !strings.Contains(moves[0].Err, "re-locking") {
		t.Fatalf("moves=%+v, want a completed move that reports the failed re-lock", moves)
	}
	if got := h.storedDir(t, wt.ID); got != want {
		t.Errorf("tracked directory=%s, want %s", got, want)
	}
}

// Superseded by TestTidyRepositoryContinuesWhenGitCannotListWorktrees below:
// refusing the run made one unusable repository fatal to a whole multi-repository
// tidy, discarding the moves already made in the repositories before it.

// A locked worktree that could not be moved anyway is reported for the obstacle
// that actually blocks it, and its lock is never touched — there is no point
// asking about, or lifting, a lock that is not what stands in the way.
func TestTidyRepositoryPrefersOtherObstaclesOverTheLock(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.lockedWorktree(t, repo, "feature/foo", from, "in use")
	h.track(t, repo, "foo", filepath.Join(h.parent, "n-foo")) // holds the destination

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockUnlock})
	if err != nil {
		t.Fatalf("TidyRepository: %v", err)
	}
	if len(moves) != 1 || !strings.Contains(moves[0].Err, "destination") {
		t.Fatalf("moves=%+v, want the occupied destination reported", moves)
	}
	if _, stillLocked := h.git.locks[from]; !stillLocked {
		t.Error("lock was lifted for a move that could never happen")
	}
}

// A dry run moves nothing, so there is nothing for an abort to prevent: the
// preview is exactly what a user reaching for --lock-strategy abort wants to
// see first, and refusing it would deny the flag's own purpose.
func TestTidyRepositoryDryRunWithAbortStillPreviews(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	wt := h.lockedWorktree(t, repo, "feature/foo", from, "in use")

	moves, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{DryRun: true, LockStrategy: LockAbort})
	if err != nil {
		t.Fatalf("a dry run must not abort: %v", err)
	}
	if len(moves) != 1 || !moves[0].Locked || moves[0].Moved {
		t.Fatalf("moves=%+v, want one unmoved entry reporting the lock", moves)
	}
	// The preview must say what the real run would do — abort — rather than
	// leaving the error empty, which reads as "would move".
	if !strings.Contains(moves[0].Err, "abort") {
		t.Errorf("Err=%q, want it to say the real run would abort", moves[0].Err)
	}
	if got := h.storedDir(t, wt.ID); got != from {
		t.Errorf("tracked directory=%s, want it left at %s", got, from)
	}
	if _, stillLocked := h.git.locks[from]; !stillLocked {
		t.Error("a dry run lifted the lock")
	}
}

// A repository git cannot enumerate leaves tidy unable to tell a locked
// worktree from a free one, but that must not sink the run: on a tidy with no
// --repository it would discard the moves already made in the repositories
// before it. Tidy carries on with no lock information, and git refuses the
// locked move itself — which is what tidy did before locks were handled at all.
func TestTidyRepositoryContinuesWhenGitCannotListWorktrees(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	want := filepath.Join(h.parent, "n-foo")
	wt := h.track(t, repo, "feature/foo", from)
	h.git.listErr = errors.New("not a git repository")

	moves, err := h.svc.TidyRepository(context.Background(), repo, TidyOptions{})
	if err != nil {
		t.Fatalf("an unlistable repository must not fail the tidy: %v", err)
	}
	if len(moves) != 1 || !moves[0].Moved {
		t.Fatalf("moves=%+v, want the move still attempted and made", moves)
	}
	if got := h.storedDir(t, wt.ID); got != want {
		t.Errorf("tracked directory=%s, want %s", got, want)
	}
}

// Abort asks to be protected from moving anything while any worktree is locked,
// and a repository git cannot enumerate is one where "nothing is locked" is
// unknown rather than true. Degrading to "no locks found" here would quietly
// turn the most cautious strategy into a partial tidy.
func TestTidyRepositoryAbortFailsWhenLocksCannotBeRead(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.track(t, repo, "feature/foo", from)
	h.git.listErr = errors.New("not a git repository")

	if _, err := h.svc.TidyRepository(context.Background(), repo,
		TidyOptions{LockStrategy: LockAbort}); err == nil {
		t.Fatal("TidyRepository succeeded, want the unreadable lock state reported")
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none", h.git.moves)
	}
}
