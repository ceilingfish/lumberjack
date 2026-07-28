package daemon

import (
	"context"
	"fmt"
	"os"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/worktree"
)

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
	Err string
}

// TidyRepository moves repo's tracked worktrees back to the locations its
// naming convention gives them, so a worktree checked out by hand somewhere
// else (say under `.claude/worktrees/`) ends up beside its siblings as
// `<worktree_parent_dir>/<dir_prefix>-<slug>`. Worktrees already in place are
// not reported.
//
// It returns one TidyMove per misplaced worktree. A worktree that cannot be
// moved — its destination is occupied, git has it locked, its directory is
// gone — is reported with Err set and the rest are still tidied; only a
// failure to enumerate the worktrees is returned as an error.
//
// One pass, so a chain resolves over successive runs: when A's destination is
// the directory B is itself moving out of, whether A moves in this pass depends
// on the arbitrary order the rows come back in. A blocked A is reported as
// occupied and moves on the next run, once B has vacated. Deliberately not
// ordered or retried — a chain needs a branch to have been renamed onto another
// worktree's slug, and running tidy again is a cheaper remedy than a
// topological sort that still cannot break a cycle without a scratch directory.
//
// ref, when non-empty, restricts the tidy to the single worktree it names (a
// branch, directory path, or directory base name — schema.Worktree.Matches),
// returning database.ErrWorktreeNotFound when nothing matches. Every other
// worktree in the repository still counts as occupying its directory, so
// narrowing the tidy can never move one worktree onto another's path.
//
// With dryRun set nothing is touched on disk or in the database; the moves that
// would be made are reported with Moved false and Err empty.
func (s *Service) TidyRepository(ctx context.Context, repo *schema.Repository, ref string, dryRun bool) ([]TidyMove, error) {
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

	var moves []TidyMove
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
		m := s.tidyOne(ctx, repo, wt, want, occupied, dryRun)
		if m.Moved {
			delete(occupied, wt.DirectoryPath)
			occupied[want] = true
		}
		moves = append(moves, m)
	}
	if ref != "" && !matched {
		// An unmatched ref is a mistyped branch or directory, not a tidy repo;
		// reporting "nothing to do" would hide the typo.
		return nil, database.ErrWorktreeNotFound
	}
	return moves, nil
}

// tidyOne relocates a single misplaced worktree to want, returning the move it
// made (or the reason it made none). occupied holds the directories other
// tracked worktrees hold; it is not mutated here — the caller updates it once
// the move is known to have succeeded.
func (s *Service) tidyOne(
	ctx context.Context, repo *schema.Repository, wt schema.Worktree,
	want string, occupied map[string]bool, dryRun bool,
) TidyMove {
	m := TidyMove{Branch: wt.BranchName, From: wt.DirectoryPath, To: want}

	switch {
	case occupied[want]:
		m.Err = "destination is already tracked by another worktree; run tidy again if that one moves"
		return m
	case pathExists(want):
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
	case dryRun:
		return m
	}

	if err := s.git.MoveWorktree(ctx, repo.LocalPath, wt.DirectoryPath, want); err != nil {
		m.Err = err.Error()
		return m
	}
	if err := s.db.SetWorktreeDirectory(ctx, wt.ID, want); err != nil {
		// The tree is already at `want`, so record that rather than leaving the
		// database pointing at a directory that no longer exists. Moving it back
		// would trade one inconsistency for another; the next tidy re-reports it.
		m.Err = fmt.Sprintf("moved on disk but recording it failed: %v", err)
		return m
	}

	m.Moved = true
	s.emitChange(repo, nil, WorktreeChange{
		Branch: wt.BranchName, PRNumber: wt.GithubPRNumber, Action: ActionUpdated,
		Detail: "moved from " + wt.DirectoryPath, DirectoryPath: want,
		LastSyncedAt: wt.LastSyncedAt,
	})
	return m
}

// pathExists reports whether path is present on disk. A stat error other than
// not-exist (a permission problem, say) counts as present, so tidy refuses to
// move onto a path it cannot see rather than assuming it is free.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}
