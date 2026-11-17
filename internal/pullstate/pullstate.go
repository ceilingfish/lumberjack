package pullstate

import (
	"cmp"
	"time"

	"github.com/ceilingfish/lumberjack/internal/authorship"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
)

type CheckState int

const (
	ChecksNone CheckState = iota
	ChecksPending
	ChecksPassing
	ChecksFailing
)

type ChecksSummary struct {
	State   CheckState
	Passing int
	Failing int
	Pending int
}

var failingOutcomes = map[string]bool{
	"FAILURE": true, "CANCELLED": true, "TIMED_OUT": true, "ACTION_REQUIRED": true,
	"STARTUP_FAILURE": true, "ERROR": true,
}

var pendingStatusOutcomes = map[string]bool{"PENDING": true, "EXPECTED": true}

func SummariseChecks(checks []github.Check) ChecksSummary {
	var s ChecksSummary
	for _, c := range checks {
		if !c.Required {
			continue
		}
		switch {
		case failingOutcomes[c.Outcome]:
			s.Failing++
		case c.Status != "" && c.Status != "COMPLETED", pendingStatusOutcomes[c.Outcome]:
			s.Pending++
		default:
			s.Passing++
		}
	}
	switch {
	case s.Failing > 0:
		s.State = ChecksFailing
	case s.Pending > 0:
		s.State = ChecksPending
	case s.Passing > 0:
		s.State = ChecksPassing
	}
	return s
}

const (
	MergeStateDirty   = "DIRTY"
	MergeStateUnknown = "UNKNOWN"
)

type Seen struct {
	FirstSeenAt        *time.Time
	ConflictedSince    *time.Time
	ChecksFailingSince *time.Time
}

func LatestActivity(events []github.Event, ids authorship.Identities) time.Time {
	var latest time.Time
	for _, e := range events {
		if !ids.Contains(e.Login) && e.At.After(latest) {
			latest = e.At
		}
	}
	return latest
}

func HasNewActivity(ps github.PullState, seen Seen, ids authorship.Identities, viewedAt *time.Time) bool {
	if viewedAt == nil {
		return false
	}
	for _, t := range []*time.Time{seen.FirstSeenAt, seen.ConflictedSince, seen.ChecksFailingSince} {
		if t != nil && t.After(*viewedAt) {
			return true
		}
	}
	return LatestActivity(ps.Activity, ids).After(*viewedAt)
}

func Score(a schema.Authorship, ps github.PullState, checks ChecksSummary, newActivity bool) int64 {
	conflicted := ps.MergeStateStatus == MergeStateDirty
	failing := checks.State == ChecksFailing
	if a == schema.AuthorshipMine {
		var s int64
		s += when(newActivity, 40)
		s += when(ps.ReviewDecision == "CHANGES_REQUESTED", 30)
		s += when(conflicted, 25)
		s += when(failing, 25)
		s += 8 * int64(min(ps.UnresolvedThreads, 3))
		s -= when(ps.IsDraft, 10)
		return s
	}
	s := int64(ps.Additions + ps.Deletions)
	s += when(failing, 800)
	s += when(conflicted, 600)
	s += when(ps.UnresolvedThreads > 0, 300)
	return s
}

func when(cond bool, weight int64) int64 {
	if cond {
		return weight
	}
	return 0
}

type Ranked struct {
	Authorship schema.Authorship
	Score      int64
	UpdatedAt  time.Time
}

func Compare(a, b Ranked) int {
	if a.Authorship != b.Authorship {
		if a.Authorship == schema.AuthorshipMine {
			return -1
		}
		return 1
	}
	byScore := cmp.Compare(b.Score, a.Score)
	if a.Authorship == schema.AuthorshipTheirs {
		byScore = -byScore
	}
	if byScore != 0 {
		return byScore
	}
	return b.UpdatedAt.Compare(a.UpdatedAt)
}
