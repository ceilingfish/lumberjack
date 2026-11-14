package pullstate

import (
	"slices"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/authorship"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
)

func TestSummariseChecks(t *testing.T) {
	cases := []struct {
		name   string
		checks []github.Check
		want   ChecksSummary
	}{
		{"no checks", nil, ChecksSummary{}},
		{"only optional checks", []github.Check{{Status: "COMPLETED", Outcome: "FAILURE"}}, ChecksSummary{}},
		{"passing, skipped and neutral all pass", []github.Check{
			{Required: true, Status: "COMPLETED", Outcome: "SUCCESS"},
			{Required: true, Status: "COMPLETED", Outcome: "SKIPPED"},
			{Required: true, Status: "COMPLETED", Outcome: "NEUTRAL"},
			{Required: true, Outcome: "SUCCESS"},
		}, ChecksSummary{State: ChecksPassing, Passing: 4}},
		{"queued, in progress and pending statuses are pending", []github.Check{
			{Required: true, Status: "QUEUED"},
			{Required: true, Status: "IN_PROGRESS"},
			{Required: true, Outcome: "PENDING"},
			{Required: true, Outcome: "EXPECTED"},
			{Required: true, Status: "COMPLETED", Outcome: "SUCCESS"},
		}, ChecksSummary{State: ChecksPending, Pending: 4, Passing: 1}},
		{"any blocking outcome fails", []github.Check{
			{Required: true, Status: "COMPLETED", Outcome: "FAILURE"},
			{Required: true, Status: "COMPLETED", Outcome: "CANCELLED"},
			{Required: true, Status: "COMPLETED", Outcome: "TIMED_OUT"},
			{Required: true, Status: "COMPLETED", Outcome: "ACTION_REQUIRED"},
			{Required: true, Status: "COMPLETED", Outcome: "STARTUP_FAILURE"},
			{Required: true, Outcome: "FAILURE"},
			{Required: true, Outcome: "ERROR"},
			{Required: true, Status: "IN_PROGRESS"},
		}, ChecksSummary{State: ChecksFailing, Failing: 7, Pending: 1}},
	}
	for _, tc := range cases {
		if got := SummariseChecks(tc.checks); got != tc.want {
			t.Errorf("%s: SummariseChecks = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func at(hour int) *time.Time {
	t := time.Date(2026, 11, 1, hour, 0, 0, 0, time.UTC)
	return &t
}

func TestLatestActivityIgnoresTheUser(t *testing.T) {
	ids := authorship.NewIdentities("me")
	events := []github.Event{
		{Login: "dmitri", At: *at(9)},
		{Login: "Me", At: *at(11)},
		{Login: "coderabbitai", At: *at(10)},
		{At: *at(8)},
	}
	if got := LatestActivity(events, ids); !got.Equal(*at(10)) {
		t.Errorf("LatestActivity = %v, want the newest event not by the user", got)
	}
	if got := LatestActivity(nil, ids); !got.IsZero() {
		t.Errorf("LatestActivity of nothing = %v, want zero", got)
	}
}

func TestHasNewActivity(t *testing.T) {
	ids := authorship.NewIdentities("me")
	viewed := at(12)
	quiet := github.PullState{Activity: []github.Event{{Login: "dmitri", At: *at(10)}, {Login: "me", At: *at(14)}}}
	cases := []struct {
		name     string
		ps       github.PullState
		seen     Seen
		viewedAt *time.Time
		want     bool
	}{
		{"never viewed", github.PullState{Activity: []github.Event{{Login: "dmitri", At: *at(14)}}}, Seen{FirstSeenAt: at(13)}, nil, false},
		{"nothing since the view", quiet, Seen{FirstSeenAt: at(1), ConflictedSince: at(11), ChecksFailingSince: at(12)}, viewed, false},
		{"newly synced", quiet, Seen{FirstSeenAt: at(13)}, viewed, true},
		{"new comment by someone else", github.PullState{Activity: []github.Event{{Login: "dmitri", At: *at(13)}}}, Seen{}, viewed, true},
		{"new conflict", quiet, Seen{ConflictedSince: at(13)}, viewed, true},
		{"new failing check", quiet, Seen{ChecksFailingSince: at(13)}, viewed, true},
	}
	for _, tc := range cases {
		if got := HasNewActivity(tc.ps, tc.seen, ids, tc.viewedAt); got != tc.want {
			t.Errorf("%s: HasNewActivity = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestScoreMine(t *testing.T) {
	failing := ChecksSummary{State: ChecksFailing, Failing: 1}
	cases := []struct {
		name string
		ps   github.PullState
		cs   ChecksSummary
		news bool
		want int64
	}{
		{"nothing to do", github.PullState{}, ChecksSummary{State: ChecksPassing}, false, 0},
		{"new activity", github.PullState{}, ChecksSummary{}, true, 40},
		{"changes requested", github.PullState{ReviewDecision: "CHANGES_REQUESTED"}, ChecksSummary{}, false, 30},
		{"conflict", github.PullState{MergeStateStatus: "DIRTY"}, ChecksSummary{}, false, 25},
		{"failing checks", github.PullState{}, failing, false, 25},
		{"threads cap at three", github.PullState{UnresolvedThreads: 5}, ChecksSummary{}, false, 24},
		{"two threads", github.PullState{UnresolvedThreads: 2}, ChecksSummary{}, false, 16},
		{"draft", github.PullState{IsDraft: true}, ChecksSummary{}, false, -10},
		{"everything", github.PullState{ReviewDecision: "CHANGES_REQUESTED", MergeStateStatus: "DIRTY", UnresolvedThreads: 1, IsDraft: true}, failing, true, 40 + 30 + 25 + 25 + 8 - 10},
	}
	for _, tc := range cases {
		if got := Score(schema.AuthorshipMine, tc.ps, tc.cs, tc.news); got != tc.want {
			t.Errorf("%s: Score = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestScoreTheirs(t *testing.T) {
	failing := ChecksSummary{State: ChecksFailing, Failing: 1}
	cases := []struct {
		name string
		ps   github.PullState
		cs   ChecksSummary
		want int64
	}{
		{"size alone", github.PullState{Additions: 120, Deletions: 30}, ChecksSummary{}, 150},
		{"failing checks", github.PullState{Additions: 1}, failing, 801},
		{"conflict", github.PullState{MergeStateStatus: "DIRTY"}, ChecksSummary{}, 600},
		{"unresolved threads count once", github.PullState{UnresolvedThreads: 4}, ChecksSummary{}, 300},
		{"new activity does not change size", github.PullState{Additions: 5}, ChecksSummary{}, 5},
	}
	for _, tc := range cases {
		if got := Score(schema.AuthorshipTheirs, tc.ps, tc.cs, true); got != tc.want {
			t.Errorf("%s: Score = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCompare(t *testing.T) {
	items := []Ranked{
		{Authorship: schema.AuthorshipTheirs, Score: 900, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipMine, Score: 10, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipTheirs, Score: 50, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipMine, Score: 65, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipMine, Score: 10, UpdatedAt: *at(5)},
		{Authorship: schema.AuthorshipTheirs, Score: 50, UpdatedAt: *at(7)},
	}
	slices.SortStableFunc(items, Compare)
	want := []Ranked{
		{Authorship: schema.AuthorshipMine, Score: 65, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipMine, Score: 10, UpdatedAt: *at(5)},
		{Authorship: schema.AuthorshipMine, Score: 10, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipTheirs, Score: 50, UpdatedAt: *at(7)},
		{Authorship: schema.AuthorshipTheirs, Score: 50, UpdatedAt: *at(1)},
		{Authorship: schema.AuthorshipTheirs, Score: 900, UpdatedAt: *at(1)},
	}
	for i := range want {
		if items[i].Authorship != want[i].Authorship || items[i].Score != want[i].Score || !items[i].UpdatedAt.Equal(want[i].UpdatedAt) {
			t.Errorf("order[%d] = %+v, want %+v", i, items[i], want[i])
		}
	}
}
