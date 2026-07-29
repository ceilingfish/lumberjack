package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

// LockStrategy is what tidy does with a misplaced worktree git has locked.
// `git worktree move` refuses to move a locked worktree, so tidy needs a
// decision for each one. It mirrors lumberjack.v1.LockStrategy; the interactive
// prompt lives in the CLI, so the daemon only ever receives a resolved choice.
type LockStrategy int

const (
	// LockSkip leaves the worktree, and its lock, alone. It is the zero value:
	// the daemon cannot prompt, and doing nothing is the safe default.
	LockSkip LockStrategy = iota
	// LockUnlock lifts the lock for the move and restores it, with its original
	// reason, at the new location.
	LockUnlock
	// LockDelete lifts the lock for the move and leaves the worktree unlocked.
	LockDelete
	// LockAbort cancels the whole tidy when any worktree in scope is locked.
	LockAbort
)

// ErrTidyAborted is returned by TidyRepository when the lock strategy is
// LockAbort and a misplaced worktree in scope is locked. Nothing has been moved
// when it is returned; the server maps it to codes.Aborted.
var ErrTidyAborted = errors.New("tidy aborted: worktree is locked")

// TidyOptions narrows and configures a tidy.
type TidyOptions struct {
	// Ref, when non-empty, restricts the tidy to the single worktree it names (a
	// branch, directory path, or directory base name — schema.Worktree.Matches).
	Ref string
	// DryRun reports the moves that would be made without touching disk, the
	// database, or any lock.
	DryRun bool
	// LockStrategy applies to every locked worktree without a LockDecision.
	LockStrategy LockStrategy
	// LockDecisions overrides LockStrategy for individual worktrees, keyed by the
	// directory the worktree is currently at — what the CLI's interactive prompt
	// names, and what TidyMove.From reports. Entries for worktrees that are not
	// locked, or not in scope, are ignored.
	LockDecisions map[string]LockStrategy
}

// strategyFor is the lock strategy that applies to the worktree at dir: its own
// decision when the caller made one, otherwise the run-wide strategy.
func (o TidyOptions) strategyFor(dir string) LockStrategy {
	if s, ok := o.LockDecisions[dir]; ok {
		return s
	}
	return o.LockStrategy
}

// TidyMove is one tracked worktree found outside its idiomatic location — the
// directory worktree.Path derives from the repository's worktree parent dir and
// dir prefix. It mirrors the lumberjack.v1.TidyMove message.
type TidyMove struct {
	Branch string
	From   string
	To     string
	// Moved is true only when the worktree was actually relocated: false for a
	// dry run, and false when Err explains why the move could not be made.
	Moved bool
	// Err is the reason the move was skipped, empty when Moved or on a dry run.
	// It is set alongside Moved for a move that landed but left something else
	// undone — the database not updated, or a lifted lock not restored.
	Err string
	// Locked records that git had the worktree locked when tidy looked at it,
	// with the reason the lock carried (empty when it carried none). It stays
	// true for a worktree that was unlocked, moved and re-locked: it describes
	// what tidy found, which is what a dry run must report so the CLI knows
	// which worktrees to ask the user about.
	Locked     bool
	LockReason string
}

// TidyRepository moves repo's tracked worktrees back to the locations its
// naming convention gives them, so a worktree checked out by hand somewhere
// else (say under `.claude/worktrees/`) ends up beside its siblings as
// `<worktree_parent_dir>/<dir_prefix>-<slug>`. Worktrees already in place are
// not reported.
//
// It returns one TidyMove per misplaced worktree. A worktree that cannot be
// moved — its destination is occupied, its directory is gone, or git has it
// locked and the strategy is LockSkip — is reported with Err set and the rest
// are still tidied. Only a failure to enumerate the worktrees, or an abort on a
// locked one (ErrTidyAborted), is returned as an error.
//
// One pass, so a chain resolves over successive runs: when A's destination is
// the directory B is itself moving out of, whether A moves in this pass depends
// on the arbitrary order the rows come back in. A blocked A is reported as
// occupied and moves on the next run, once B has vacated. Deliberately not
// ordered or retried — a chain needs a branch to have been renamed onto another
// worktree's slug, and running tidy again is a cheaper remedy than a
// topological sort that still cannot break a cycle without a scratch directory.
//
// opts.Ref narrows what is moved, returning database.ErrWorktreeNotFound when
// nothing matches. Every other worktree in the repository still counts as
// occupying its directory, so narrowing the tidy can never move one worktree
// onto another's path.
//
// With opts.DryRun nothing is touched on disk, in the database, or in git's
// locks; the moves that would be made are reported with Moved false.
func (s *Service) TidyRepository(ctx context.Context, repo *schema.Repository, opts TidyOptions) ([]TidyMove, error) {
	// Serialise with every other worktree mutation: the daemon is the single
	// writer, and a concurrent sync must not create a worktree at a path tidy
	// is in the middle of claiming. No withRepoLogin here, unlike sync and
	// delete — moving a worktree is purely local, touching neither the remote
	// nor gh.
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := s.db.ListWorktrees(ctx, repo.ID)
	if err != nil {
		return nil, err
	}

	// Directories tracked worktrees already occupy, so tidy never moves one
	// worktree onto another's path — including a path that is itself about to
	// be vacated, since the order we visit rows in is arbitrary. Built from
	// every worktree in the repository, not just the ones in scope: a worktree
	// ref filters what tidy *moves*, never what blocks a move.
	occupied := make(map[string]bool, len(stored))
	for _, wt := range stored {
		occupied[wt.DirectoryPath] = true
	}

	// The misplaced worktrees in scope, each with the lock (if any) git holds on
	// it. Resolved up front so an abort can refuse before anything has moved.
	candidates, err := s.tidyCandidates(ctx, repo, stored, opts)
	if err != nil {
		return nil, err
	}
	// A dry run moves nothing, so there is nothing for an abort to prevent.
	// Refusing here would deny the preview the flag exists for, at exactly the
	// moment the user is trying to find out what is locked.
	if !opts.DryRun {
		if err := abortIfLocked(candidates, opts); err != nil {
			return nil, err
		}
	}

	moves := make([]TidyMove, 0, len(candidates))
	for _, c := range candidates {
		m := s.tidyOne(ctx, repo, c, occupied, opts)
		if m.Moved {
			delete(occupied, c.wt.DirectoryPath)
			occupied[c.want] = true
		}
		moves = append(moves, m)
	}
	return moves, nil
}

// abortIfLocked returns ErrTidyAborted when any candidate is locked and its
// strategy is LockAbort. Abort means "move nothing", so this runs before the
// first move rather than as each worktree is reached.
func abortIfLocked(candidates []tidyCandidate, opts TidyOptions) error {
	for _, c := range candidates {
		if c.lock.Locked && opts.strategyFor(c.wt.DirectoryPath) == LockAbort {
			return fmt.Errorf("%w: %s", ErrTidyAborted, c.wt.DirectoryPath)
		}
	}
	return nil
}

// CanAbortOnLock reports whether opts could abort at all, so a caller tidying
// several repositories can skip the pre-pass — and its `git worktree list` per
// repository — when no abort is possible.
func (o TidyOptions) CanAbortOnLock() bool {
	if o.LockStrategy == LockAbort {
		return true
	}
	for _, s := range o.LockDecisions {
		if s == LockAbort {
			return true
		}
	}
	return false
}

// TidyAbortCheck returns ErrTidyAborted when tidying repo under opts would hit
// a locked worktree the options say to abort on. It moves nothing.
//
// It exists so a tidy spanning several repositories can honour abort as the
// "nothing is moved" it promises. TidyRepository can only refuse before its own
// first move, which on a multi-repository run is already too late: the earlier
// repositories have been tidied and, because the caller discards the response
// along with the error, their moves would go unreported as well as unmentioned.
// Running this over every repository in scope first makes abort mean nothing
// moved anywhere.
//
// It takes and releases the lock per repository rather than holding it across
// the whole run, so a lock taken from outside the daemon between this check and
// the moves can still produce the mid-run abort it exists to prevent. Narrowing
// that window would mean holding s.mu across every repository's moves — blocking
// all other RPCs for the length of a full multi-repository tidy — to defend
// against someone running `git worktree lock` by hand in the second it takes.
// Not worth it: the window is small, the failure is loud, and abort is a safety
// valve rather than a transaction.
func (s *Service) TidyAbortCheck(ctx context.Context, repo *schema.Repository, opts TidyOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := s.db.ListWorktrees(ctx, repo.ID)
	if err != nil {
		return err
	}
	candidates, err := s.tidyCandidates(ctx, repo, stored, opts)
	if err != nil {
		return err
	}
	return abortIfLocked(candidates, opts)
}

// tidyCandidate is one misplaced worktree in scope: the tracked row, where the
// naming convention says it belongs, and git's view of it — whose Locked field
// is what tidy has to work around to move it.
type tidyCandidate struct {
	wt   schema.Worktree
	want string
	lock worktree.Ref
}

// tidyCandidates selects the misplaced worktrees ref puts in scope and attaches
// each one's git lock state. It returns database.ErrWorktreeNotFound when a
// non-empty ref matches no worktree at all — an unmatched ref is a mistyped
// branch or directory, not a tidy repository, and reporting "nothing to do"
// would hide the typo.
func (s *Service) tidyCandidates(
	ctx context.Context, repo *schema.Repository, stored []schema.Worktree, opts TidyOptions,
) ([]tidyCandidate, error) {
	ref := opts.Ref
	var candidates []tidyCandidate
	matched := false
	for i := range stored {
		wt := stored[i]
		if ref != "" && !wt.Matches(ref) {
			continue
		}
		matched = true
		want := worktree.Path(repo.WorktreeParentDir, repo.DirPrefix, wt.BranchName)
		if want == wt.DirectoryPath {
			continue
		}
		candidates = append(candidates, tidyCandidate{wt: wt, want: want})
	}
	if ref != "" && !matched {
		return nil, database.ErrWorktreeNotFound
	}
	if len(candidates) == 0 {
		// Nothing to move, so nothing to ask git about.
		return nil, nil
	}

	// One `git worktree list` for the whole repository rather than a query per
	// worktree. git is the only place a lock is recorded, so a listing that
	// fails leaves tidy unable to tell a movable worktree from a locked one.
	//
	// That is not fatal for the strategies that only ever lift a lock they can
	// see: this call is otherwise the sole reason an unusable repository (its
	// LocalPath deleted, its .git corrupt) would sink a whole multi-repository
	// tidy — including the moves already made in the repositories before it,
	// which the caller discards along with the error. Carrying on with no lock
	// information restores what tidy did before locks were handled at all: the
	// move is attempted, and git refuses a locked one with its own message, per
	// worktree.
	//
	// Abort is the exception. It asks to be protected from moving anything while
	// any worktree is locked, and "we could not tell" is not "nothing is locked"
	// — silently treating every worktree as free would turn the most cautious
	// strategy into a partial tidy with no explanation.
	refs, err := s.git.ListWorktrees(ctx, repo.LocalPath)
	if err != nil {
		if opts.CanAbortOnLock() {
			return nil, fmt.Errorf("listing git worktrees for %s: %w", repo.LocalPath, err)
		}
		return candidates, nil
	}
	locks := make(map[string]worktree.Ref, len(refs))
	for _, r := range refs {
		locks[resolvePath(r.Dir)] = r
	}
	for i := range candidates {
		candidates[i].lock = locks[resolvePath(candidates[i].wt.DirectoryPath)]
	}
	return candidates, nil
}

// tidyOne relocates a single misplaced worktree to c.want, returning the move it
// made (or the reason it made none). occupied holds the directories other
// tracked worktrees hold; it is not mutated here — the caller updates it once
// the move is known to have succeeded.
func (s *Service) tidyOne(
	ctx context.Context, repo *schema.Repository, c tidyCandidate,
	occupied map[string]bool, opts TidyOptions,
) TidyMove {
	wt := c.wt
	m := TidyMove{
		Branch: wt.BranchName, From: wt.DirectoryPath, To: c.want,
		Locked: c.lock.Locked, LockReason: c.lock.LockReason,
	}

	switch {
	case occupied[c.want]:
		m.Err = "destination is already tracked by another worktree; run tidy again if that one moves"
		return m
	case pathExists(c.want):
		// Load-bearing, not just courtesy: `git worktree move` onto an existing
		// directory behaves like `mv` and buries the worktree at want/<basename>
		// rather than failing, which would leave the database pointing at a
		// location the worktree is not at (see Git.MoveWorktree).
		m.Err = "destination already exists on disk"
		return m
	case !pathExists(wt.DirectoryPath):
		// Nothing to move: the directory is gone. Sync's reconciliation is what
		// prunes such a row; tidy only reports it.
		m.Err = "worktree directory is missing"
		return m
	}

	// A lock has to be dealt with before the move, since git refuses to move a
	// locked worktree: leave the lock alone, or lift it.
	strategy := opts.strategyFor(wt.DirectoryPath)
	if c.lock.Locked && strategy == LockSkip {
		m.Err = lockedMessage(c.lock.LockReason)
		return m
	}
	// LockAbort only reaches here on a dry run — a real run is refused before the
	// first move. Saying "would move" would be a lie about the very thing the
	// preview is for, so the preview says what the real run would do instead.
	if c.lock.Locked && strategy == LockAbort {
		m.Err = "worktree is locked; --lock-strategy abort would cancel the whole tidy, moving nothing"
		return m
	}
	if opts.DryRun {
		return m
	}
	if c.lock.Locked {
		if err := s.git.UnlockWorktree(ctx, repo.LocalPath, wt.DirectoryPath); err != nil {
			m.Err = fmt.Sprintf("unlocking the worktree failed: %v", err)
			return m
		}
	}
	// Puts a lifted lock back, at whichever directory the worktree ended up in —
	// including the original one, when the move itself failed. LockDelete is the
	// case that deliberately leaves it off.
	relock := func(dir string) {
		if !c.lock.Locked || strategy != LockUnlock {
			return
		}
		if err := s.git.LockWorktree(ctx, repo.LocalPath, dir, c.lock.LockReason); err != nil {
			m.Err = appendReason(m.Err, fmt.Sprintf("re-locking the worktree failed: %v", err))
		}
	}

	if err := s.git.MoveWorktree(ctx, repo.LocalPath, wt.DirectoryPath, c.want); err != nil {
		m.Err = err.Error()
		relock(wt.DirectoryPath)
		return m
	}
	if err := s.db.SetWorktreeDirectory(ctx, wt.ID, c.want); err != nil {
		// The tree is already at `want`, so record that rather than leaving the
		// database pointing at a directory that no longer exists. Moving it back
		// would trade one inconsistency for another; the next tidy re-reports it.
		m.Err = fmt.Sprintf("moved on disk but recording it failed: %v", err)
		relock(c.want)
		return m
	}
	relock(c.want)

	m.Moved = true
	s.emitChange(repo, nil, WorktreeChange{
		Branch: wt.BranchName, PRNumber: wt.GithubPRNumber, Action: ActionUpdated,
		Detail: "moved from " + wt.DirectoryPath, DirectoryPath: c.want,
		LastSyncedAt: wt.LastSyncedAt,
	})
	return m
}

// lockedMessage explains a skip caused by a lock, quoting git's lock reason when
// the lock carries one.
func lockedMessage(reason string) string {
	msg := "worktree is locked"
	if reason != "" {
		msg += " (" + reason + ")"
	}
	return msg + "; use --lock-strategy to unlock it or delete the lock"
}

// appendReason joins two reasons into one message, so a move that both landed
// and left something undone reports both.
func appendReason(existing, extra string) string {
	if existing == "" {
		return extra
	}
	return existing + "; " + extra
}

// pathExists reports whether path is present on disk. A stat error other than
// not-exist (a permission problem, say) counts as present, so tidy refuses to
// move onto a path it cannot see rather than assuming it is free.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// resolvePath puts a path in the form used to match git's view of a worktree
// against the database's. git reports fully resolved directories, so a tracked
// path that runs through a symlink (anything under `/tmp` on macOS, say) would
// not compare equal without this. A path that cannot be resolved (one that no
// longer exists) falls back to being cleaned, which still matches whenever
// neither side involves a symlink.
func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
