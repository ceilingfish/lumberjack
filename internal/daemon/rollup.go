package daemon

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ceilingfish/lumberjack/internal/authorship"
	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/pullstate"
)

type ReviewView struct {
	github.Review
	IsMe bool
}

type PullView struct {
	AuthorLogin     string
	State           github.PullState
	Checks          pullstate.ChecksSummary
	Reviews         []ReviewView
	RequestedFromMe bool
	NewActivity     bool
	Score           int64
}

func (s *Service) rollupFor(repo *schema.Repository) map[int64]github.PullState {
	v, _ := s.rollups.Load(repo.ID)
	r, _ := v.(map[int64]github.PullState)
	return r
}

func (s *Service) refreshRollup(ctx context.Context, repo *schema.Repository, open map[int64]github.PR, at time.Time) error {
	numbers := slices.Sorted(maps.Keys(open))
	states, err := s.gh.PullStates(ctx, repoInfo(repo), numbers)
	if err != nil {
		return fmt.Errorf("fetching pull request states: %w", err)
	}
	s.rollups.Store(repo.ID, states)
	conds := make([]database.PRCondition, 0, len(states))
	for _, n := range numbers {
		ps, ok := states[n]
		if !ok {
			continue
		}
		conds = append(conds, database.PRCondition{
			Number:          n,
			Conflicted:      ps.MergeStateStatus == pullstate.MergeStateDirty,
			ConflictUnknown: ps.MergeStateStatus == pullstate.MergeStateUnknown,
			ChecksFailing:   pullstate.SummariseChecks(ps.Checks).State == pullstate.ChecksFailing,
		})
	}
	return s.db.UpdatePRConditions(ctx, repo.ID, conds, at)
}

func pullViewFor(
	v WorktreeView, rollup map[int64]github.PullState, prs map[int64]schema.PullRequest,
	viewed map[schema.Authorship]time.Time, ids authorship.Identities,
) *PullView {
	if v.Worktree.GithubPRNumber == nil {
		return nil
	}
	n := *v.Worktree.GithubPRNumber
	ps, ok := rollup[n]
	if !ok {
		return nil
	}
	pv := &PullView{State: ps, Checks: pullstate.SummariseChecks(ps.Checks)}
	for _, r := range ps.LatestReviews {
		pv.Reviews = append(pv.Reviews, ReviewView{Review: r, IsMe: ids.Contains(r.Login)})
	}
	for _, l := range ps.RequestedLogins {
		pv.RequestedFromMe = pv.RequestedFromMe || ids.Contains(l)
	}
	row := prs[n]
	pv.AuthorLogin = row.AuthorLogin
	seen := pullstate.Seen{FirstSeenAt: row.FirstSeenAt, ConflictedSince: row.ConflictedSince, ChecksFailingSince: row.ChecksFailingSince}
	var viewedAt *time.Time
	if t, ok := viewed[v.Authorship]; ok {
		viewedAt = &t
	}
	pv.NewActivity = pullstate.HasNewActivity(ps, seen, ids, viewedAt)
	pv.Score = pullstate.Score(v.Authorship, ps, pv.Checks, pv.NewActivity)
	return pv
}

func ranked(v WorktreeView) pullstate.Ranked {
	r := pullstate.Ranked{Authorship: v.Authorship}
	if v.PullRequest != nil {
		r.Score, r.UpdatedAt = v.PullRequest.Score, v.PullRequest.State.UpdatedAt
	}
	return r
}

func compareViews(a, b WorktreeView) int {
	return pullstate.Compare(ranked(a), ranked(b))
}

func (s *Service) MarkViewed(ctx context.Context, repo *schema.Repository, a schema.Authorship, seenThrough *time.Time) error {
	if seenThrough == nil {
		seenThrough = repo.LastSyncedAt
	}
	if seenThrough == nil {
		return nil
	}
	return s.db.MarkViewed(ctx, repo.ID, a, minTime(*seenThrough, s.now()))
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
