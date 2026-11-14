package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/pullstate"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func viewsByBranch(t *testing.T, h *harness, repo *schema.Repository) map[string]WorktreeView {
	t.Helper()
	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorktreeViews: %v", err)
	}
	out := map[string]WorktreeView{}
	for _, v := range views {
		out[v.Worktree.BranchName] = v
	}
	return out
}

func TestSyncFetchesTheRollupForOpenPRs(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "me"
	h.gh.prs = []github.PR{{Number: 2, HeadBranch: "b", AuthorLogin: "me"}, {Number: 1, HeadBranch: "a", AuthorLogin: "dmitri"}}
	h.gh.states = map[int64]github.PullState{
		1: {
			Number: 1, URL: "u1", Additions: 3, Deletions: 2,
			LatestReviews:   []github.Review{{Login: "me", State: "APPROVED"}, {Login: "zoe", State: "COMMENTED"}},
			RequestedLogins: []string{"ME"},
			Checks:          []github.Check{{Required: true, Status: "COMPLETED", Outcome: "SUCCESS"}},
		},
	}
	h.seedSync(t, repo)

	if len(h.gh.statesCalls) != 1 || len(h.gh.statesCalls[0]) != 2 || h.gh.statesCalls[0][0] != 1 {
		t.Fatalf("PullStates calls = %v, want one call for #1 and #2 in order", h.gh.statesCalls)
	}
	views := viewsByBranch(t, h, repo)
	if views["b"].PullRequest != nil {
		t.Errorf("a PR missing from the rollup should have no state, got %+v", views["b"].PullRequest)
	}
	pv := views["a"].PullRequest
	if pv == nil {
		t.Fatal("expected PR #1's state")
	}
	if pv.AuthorLogin != "dmitri" || pv.State.URL != "u1" || pv.Checks.State != pullstate.ChecksPassing || !pv.RequestedFromMe {
		t.Errorf("pull view = %+v", pv)
	}
	if len(pv.Reviews) != 2 || !pv.Reviews[0].IsMe || pv.Reviews[1].IsMe {
		t.Errorf("reviews = %+v, want only the user's own review marked", pv.Reviews)
	}
	if pv.Score != 5 {
		t.Errorf("score = %d, want the change size for a theirs PR", pv.Score)
	}
}

func TestReadsNeverFetchTheRollup(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	before := len(h.gh.statesCalls)
	viewsByBranch(t, h, repo)
	viewsByBranch(t, h, repo)
	if len(h.gh.statesCalls) != before {
		t.Errorf("PullStates calls went from %d to %d on reads", before, len(h.gh.statesCalls))
	}
}

func TestRollupFailureFailsTheSyncButKeepsTheLastRollup(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.gh.states = map[int64]github.PullState{1: {Number: 1, URL: "u1"}}
	h.seedSync(t, repo)

	h.gh.statesErr = errors.New("rate limited")
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v, want the rollup failure reported", err)
	}
	if pv := viewsByBranch(t, h, repo)["a"].PullRequest; pv == nil || pv.State.URL != "u1" {
		t.Errorf("pull view = %+v, want the previous rollup kept", pv)
	}
}

func TestRollupConditionFailureFailsTheSync(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.gh.states = map[int64]github.PullState{1: {Number: 1}}
	h.seedSync(t, repo)
	if _, err := h.db.ExecContext(context.Background(),
		"CREATE TRIGGER no_conditions BEFORE UPDATE OF checks_failing_since ON pull_requests BEGIN SELECT RAISE(ABORT, 'boom'); END"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the condition update failure reported", err)
	}
}

func TestNewActivityAgainstTheLastView(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "me"
	clock := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	h.svc.now = func() time.Time { return clock }
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a", AuthorLogin: "me"}}
	h.gh.states = map[int64]github.PullState{1: {Number: 1}}
	h.seedSync(t, repo)

	if views := viewsByBranch(t, h, repo); views["a"].PullRequest.NewActivity {
		t.Error("nothing is new before the tab has ever been viewed")
	}

	seen := clock
	if err := h.svc.MarkViewed(context.Background(), repo, schema.AuthorshipMine, &seen); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	if views := viewsByBranch(t, h, repo); views["a"].PullRequest.NewActivity {
		t.Error("nothing has happened since the view")
	}

	clock = clock.Add(time.Hour)
	h.gh.states = map[int64]github.PullState{1: {
		Number: 1, MergeStateStatus: "DIRTY",
		Activity: []github.Event{{Login: "me", At: clock}},
	}}
	h.seedSync(t, repo)
	pv := viewsByBranch(t, h, repo)["a"].PullRequest
	if !pv.NewActivity {
		t.Error("a new conflict should count as new activity")
	}
	if pv.Score != 40+25 {
		t.Errorf("score = %d, want new activity plus the conflict", pv.Score)
	}

	later := clock
	if err := h.svc.MarkViewed(context.Background(), repo, schema.AuthorshipMine, &later); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	if viewsByBranch(t, h, repo)["a"].PullRequest.NewActivity {
		t.Error("the conflict was seen; it is no longer new")
	}
}

func TestMarkViewedDefaultsAndClamps(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	h.svc.now = func() time.Time { return now }

	if err := h.svc.MarkViewed(context.Background(), repo, schema.AuthorshipMine, nil); err != nil {
		t.Fatalf("MarkViewed with nothing synced: %v", err)
	}
	if views, _ := h.db.ListViews(context.Background(), repo.ID); len(views) != 0 {
		t.Errorf("views = %v, want nothing recorded before any sync", views)
	}

	synced := now.Add(-time.Hour)
	repo.LastSyncedAt = &synced
	if err := h.svc.MarkViewed(context.Background(), repo, schema.AuthorshipMine, nil); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	future := now.Add(time.Hour)
	if err := h.svc.MarkViewed(context.Background(), repo, schema.AuthorshipTheirs, &future); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	views, _ := h.db.ListViews(context.Background(), repo.ID)
	if !views[schema.AuthorshipMine].Equal(synced) || !views[schema.AuthorshipTheirs].Equal(now) {
		t.Errorf("views = %v, want mine at the last sync and theirs clamped to now", views)
	}
}

func TestWorktreeViewsAreInDisplayOrder(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	repo.Login = "me"
	h.gh.prs = []github.PR{
		{Number: 1, HeadBranch: "theirs-big", AuthorLogin: "x"},
		{Number: 2, HeadBranch: "mine-quiet", AuthorLogin: "me"},
		{Number: 3, HeadBranch: "theirs-small", AuthorLogin: "x"},
		{Number: 4, HeadBranch: "mine-busy", AuthorLogin: "me"},
	}
	h.gh.states = map[int64]github.PullState{
		1: {Number: 1, Additions: 500},
		2: {Number: 2},
		3: {Number: 3, Additions: 5},
		4: {Number: 4, ReviewDecision: "CHANGES_REQUESTED"},
	}
	h.seedSync(t, repo)

	views, err := h.svc.WorktreeViews(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, v := range views {
		order = append(order, v.Worktree.BranchName)
	}
	want := "mine-busy mine-quiet theirs-small theirs-big"
	if strings.Join(order, " ") != want {
		t.Errorf("order = %v, want %s", order, want)
	}
}

func TestWorktreeViewsReportViewLookupFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	if _, err := h.db.ExecContext(context.Background(), "ALTER TABLE list_views RENAME COLUMN viewed_at TO gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.WorktreeViews(context.Background(), repo); err == nil {
		t.Error("expected the list view lookup failure to be reported")
	}
}

func TestToProtoPullRequestState(t *testing.T) {
	if toProtoPullRequestState(nil) != nil {
		t.Error("no pull view should map to an absent message")
	}
	updated := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pv := &PullView{
		AuthorLogin: "dmitri",
		State: github.PullState{
			URL: "u", IsDraft: true, UpdatedAt: updated, MergeStateStatus: "DIRTY", ReviewDecision: "APPROVED",
			BaseRefName: "main", Additions: 4, Deletions: 2, UnresolvedThreads: 3,
		},
		Checks:          pullstate.ChecksSummary{State: pullstate.ChecksFailing, Passing: 1, Failing: 2, Pending: 3},
		Reviews:         []ReviewView{{Review: github.Review{Login: "me", State: "APPROVED"}, IsMe: true}},
		RequestedFromMe: true, NewActivity: true, Score: 99,
	}
	pb := toProtoPullRequestState(pv)
	want := &lumberjackv1.PullRequestState{
		Url: "u", AuthorLogin: "dmitri", IsDraft: true, UpdatedAt: timestamppb.New(updated),
		MergeStateStatus:      lumberjackv1.MergeStateStatus_MERGE_STATE_STATUS_DIRTY,
		ReviewDecision:        lumberjackv1.ReviewDecision_REVIEW_DECISION_APPROVED,
		ReviewRequestedFromMe: true, UnresolvedThreadCount: 3,
		Checks: &lumberjackv1.ChecksSummary{
			State: lumberjackv1.CheckState_CHECK_STATE_FAILING, Passing: 1, Failing: 2, Pending: 3,
		},
		Additions: 4, Deletions: 2, HasNewActivity: true, Score: 99, BaseRefName: "main",
		LatestReviews: []*lumberjackv1.Review{{Login: "me", State: lumberjackv1.ReviewState_REVIEW_STATE_APPROVED, IsMe: true}},
	}
	if pb.String() != want.String() {
		t.Errorf("pb = %v\nwant %v", pb, want)
	}
	if toProtoPullRequestState(&PullView{}).GetUpdatedAt() != nil {
		t.Error("a zero update time should be absent")
	}
}

func TestServerMarkViewed(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	seen := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	if _, err := srv.MarkViewed(context.Background(), &lumberjackv1.MarkViewedRequest{
		Repository: "n", Authorship: lumberjackv1.Authorship_AUTHORSHIP_THEIRS, SeenThrough: timestamppb.New(seen),
	}); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	views, _ := h.db.ListViews(context.Background(), repo.ID)
	if !views[schema.AuthorshipTheirs].Equal(seen) {
		t.Errorf("views = %v", views)
	}
	if _, err := srv.MarkViewed(context.Background(), &lumberjackv1.MarkViewedRequest{
		Repository: "n", Authorship: lumberjackv1.Authorship_AUTHORSHIP_MINE,
	}); err != nil {
		t.Errorf("MarkViewed without seen_through: %v", err)
	}

	cases := map[string]struct {
		req  *lumberjackv1.MarkViewedRequest
		want codes.Code
	}{
		"no authorship":      {&lumberjackv1.MarkViewedRequest{Repository: "n"}, codes.InvalidArgument},
		"unknown repository": {&lumberjackv1.MarkViewedRequest{Repository: "nope", Authorship: lumberjackv1.Authorship_AUTHORSHIP_MINE}, codes.NotFound},
	}
	for name, tc := range cases {
		if _, err := srv.MarkViewed(context.Background(), tc.req); status.Code(err) != tc.want {
			t.Errorf("%s: code = %v, want %v", name, status.Code(err), tc.want)
		}
	}

	_ = h.db.Close()
	if _, err := srv.MarkViewed(context.Background(), &lumberjackv1.MarkViewedRequest{
		Repository: "n", Authorship: lumberjackv1.Authorship_AUTHORSHIP_MINE,
	}); err == nil {
		t.Error("expected a database failure to be reported")
	}
}

func watchUntilSyncFinished(t *testing.T, h *harness, beforeSync func()) *lumberjackv1.WatchResponse {
	t.Helper()
	srv := newServer(h)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream := &fakeWatchStream{ctx: ctx, sent: make(chan *lumberjackv1.WatchResponse, 32)}
	go func() { _ = srv.Watch(&lumberjackv1.WatchRequest{}, stream) }()
	recvWatch(t, stream.sent)
	beforeSync()

	repo, err := h.db.FindRepository(context.Background(), "n")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _, _ = h.svc.SyncRepository(context.Background(), repo, nil) }()
	for {
		if ev := recvWatch(t, stream.sent); ev.GetType() == lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_FINISHED {
			return ev
		}
	}
}

func TestServerWatchSyncFinishedCarriesWorktrees(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.gh.states = map[int64]github.PullState{1: {Number: 1, URL: "u1"}}
	h.seedSync(t, repo)

	ev := watchUntilSyncFinished(t, h, func() {})
	if len(ev.GetWorktrees()) != 1 || ev.GetWorktrees()[0].GetPullRequest().GetUrl() != "u1" {
		t.Errorf("SYNC_FINISHED worktrees = %+v, want the refreshed worktree with its PR state", ev.GetWorktrees())
	}
}

func TestServerWatchSyncFinishedWithoutWorktreesOnFailure(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)
	ev := watchUntilSyncFinished(t, h, func() {
		h.gh.mu.Lock()
		h.gh.prsErr = errors.New("gh down")
		h.gh.mu.Unlock()
	})
	if len(ev.GetWorktrees()) != 0 {
		t.Errorf("worktrees = %+v, want none when they cannot be listed", ev.GetWorktrees())
	}
}

func TestServerMarkViewedStoreFailure(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	if _, err := h.db.ExecContext(context.Background(), "ALTER TABLE list_views RENAME COLUMN viewed_at TO gone"); err != nil {
		t.Fatal(err)
	}
	_, err := srv.MarkViewed(context.Background(), &lumberjackv1.MarkViewedRequest{
		Repository: "n", Authorship: lumberjackv1.Authorship_AUTHORSHIP_MINE, SeenThrough: timestamppb.Now(),
	})
	if status.Code(err) != codes.Internal {
		t.Errorf("code = %v, want Internal", status.Code(err))
	}
}
