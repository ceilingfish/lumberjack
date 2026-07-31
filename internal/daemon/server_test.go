package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
	"github.com/ceilingfish/lumberjack/internal/worktree"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newServer(h *harness) *Server {
	return NewServer(Info{Version: "test", StartedAt: time.Unix(0, 0)}, h.db, h.svc)
}

// fakeSyncStream captures streamed SyncResponses. Embedding the nil
// grpc.ServerStream is fine because the server only calls Send and Context.
type fakeSyncStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*lumberjackv1.SyncResponse
	err  error
}

func (f *fakeSyncStream) Send(r *lumberjackv1.SyncResponse) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, r)
	return nil
}
func (f *fakeSyncStream) Context() context.Context { return f.ctx }

// fakeWatchStream captures streamed WatchResponses on a channel so a test can
// consume them as they arrive (Watch itself blocks until ctx is cancelled).
type fakeWatchStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan *lumberjackv1.WatchResponse
}

func (f *fakeWatchStream) Send(r *lumberjackv1.WatchResponse) error {
	select {
	case f.sent <- r:
		return nil
	case <-f.ctx.Done():
		return f.ctx.Err()
	}
}
func (f *fakeWatchStream) Context() context.Context { return f.ctx }

// recvWatch waits briefly for the next streamed WatchResponse, failing the
// test if none arrives.
func recvWatch(t *testing.T, ch <-chan *lumberjackv1.WatchResponse) *lumberjackv1.WatchResponse {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a WatchResponse")
		return nil
	}
}

func TestServerHealth(t *testing.T) {
	srv := newServer(newHarness(t))
	resp, err := srv.Health(context.Background(), &lumberjackv1.HealthRequest{})
	if err != nil || resp.GetVersion() != "test" {
		t.Errorf("Health = %v, %v", resp, err)
	}
}

func TestServerInitRepository(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)

	resp, err := srv.InitRepository(context.Background(),
		&lumberjackv1.InitRepositoryRequest{LocalPath: h.parent + "/repo"})
	if err != nil {
		t.Fatalf("InitRepository: %v", err)
	}
	if resp.GetRepository().GetDirPrefix() != "repo" {
		t.Errorf("repo = %+v", resp.GetRepository())
	}
}

func TestServerInitRepositoryEmptyPath(t *testing.T) {
	srv := newServer(newHarness(t))
	_, err := srv.InitRepository(context.Background(), &lumberjackv1.InitRepositoryRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestServerInitRepositoryDuplicate(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	req := &lumberjackv1.InitRepositoryRequest{LocalPath: h.parent + "/repo"}
	if _, err := srv.InitRepository(context.Background(), req); err != nil {
		t.Fatalf("first init: %v", err)
	}
	_, err := srv.InitRepository(context.Background(), req)
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("expected AlreadyExists, got %v", err)
	}
}

func TestServerListAndGetRepository(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)

	list, err := srv.ListRepositories(context.Background(), &lumberjackv1.ListRepositoriesRequest{})
	if err != nil || len(list.GetRepositories()) != 1 {
		t.Fatalf("ListRepositories = %v, %v", list, err)
	}

	got, err := srv.GetRepository(context.Background(), &lumberjackv1.GetRepositoryRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if got.GetRepository().GetId() != repo.ID {
		t.Errorf("GetRepository id = %d, want %d", got.GetRepository().GetId(), repo.ID)
	}

	_, err = srv.GetRepository(context.Background(), &lumberjackv1.GetRepositoryRequest{Repository: "ghost"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestServerGetSetupConsent(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	h.git.configFiles = map[string][]byte{
		"origin/main:.lumberjack.yml": []byte(`
steps:
  - type: run-command
    run_command:
      command: echo hi
`),
	}

	resp, err := srv.GetSetupConsent(context.Background(), &lumberjackv1.GetSetupConsentRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if !resp.GetPending() {
		t.Error("expected pending=true")
	}
	if len(resp.GetRunCommands()) != 1 || resp.GetRunCommands()[0] != "echo hi" {
		t.Errorf("RunCommands = %v", resp.GetRunCommands())
	}
}

func TestServerGetSetupConsentEmptyRepository(t *testing.T) {
	srv := newServer(newHarness(t))
	_, err := srv.GetSetupConsent(context.Background(), &lumberjackv1.GetSetupConsentRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestServerSetSetupConsent(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	h.git.configFiles = map[string][]byte{
		"origin/main:.lumberjack.yml": []byte(`
steps:
  - type: run-command
    run_command:
      command: echo hi
`),
	}

	if _, err := srv.SetSetupConsent(context.Background(), &lumberjackv1.SetSetupConsentRequest{Repository: "n"}); err != nil {
		t.Fatalf("SetSetupConsent: %v", err)
	}
	resp, err := srv.GetSetupConsent(context.Background(), &lumberjackv1.GetSetupConsentRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if resp.GetPending() {
		t.Error("expected pending=false after SetSetupConsent")
	}
}

func TestServerListWorktrees(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	h.seedSync(t, repo)

	resp, err := srv.ListWorktrees(context.Background(), &lumberjackv1.ListWorktreesRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(resp.GetWorktrees()) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(resp.GetWorktrees()))
	}
	wt := resp.GetWorktrees()[0]
	if wt.GetBranchName() != "feature/a" || wt.GetGithubPrNumber() != 1 {
		t.Errorf("worktree = %+v", wt)
	}
}

func TestServerDeleteWorktree(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)

	resp, err := srv.DeleteWorktree(context.Background(),
		&lumberjackv1.DeleteWorktreeRequest{Repository: "n", Worktree: "a"})
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !resp.GetDeleted() {
		t.Errorf("expected deleted: %+v", resp)
	}

	_, err = srv.DeleteWorktree(context.Background(),
		&lumberjackv1.DeleteWorktreeRequest{Repository: "n", Worktree: "ghost"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestServerDeleteRepository(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)

	resp, err := srv.DeleteRepository(context.Background(),
		&lumberjackv1.DeleteRepositoryRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}
	if resp.GetWorktreesRemoved() != 1 {
		t.Errorf("worktreesRemoved = %d, want 1", resp.GetWorktreesRemoved())
	}

	// The repository is gone.
	_, err = srv.GetRepository(context.Background(), &lumberjackv1.GetRepositoryRequest{Repository: "n"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound after delete, got %v", err)
	}
}

func TestServerDeleteRepositoryEmpty(t *testing.T) {
	srv := newServer(newHarness(t))
	_, err := srv.DeleteRepository(context.Background(), &lumberjackv1.DeleteRepositoryRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestServerDeleteRepositoryUnknown(t *testing.T) {
	srv := newServer(newHarness(t))
	_, err := srv.DeleteRepository(context.Background(),
		&lumberjackv1.DeleteRepositoryRequest{Repository: "ghost"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestServerSyncSingleRepo(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	stream := &fakeSyncStream{ctx: context.Background()}
	if err := srv.Sync(&lumberjackv1.SyncRequest{Repository: "n"}, stream); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(stream.sent) == 0 {
		t.Fatal("expected streamed events")
	}
	last := stream.sent[len(stream.sent)-1]
	if !last.GetCompleted() || last.GetSummary().GetWorktreesCreated() != 1 {
		t.Errorf("final event = %+v", last)
	}
	if last.GetSummary().GetStatus() != lumberjackv1.SyncStatus_SYNC_STATUS_OK {
		t.Errorf("status = %v", last.GetSummary().GetStatus())
	}
	_ = repo
}

func TestServerSyncAllRepos(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t) // one repo
	h.gh.prs = nil

	stream := &fakeSyncStream{ctx: context.Background()}
	if err := srv.Sync(&lumberjackv1.SyncRequest{}, stream); err != nil {
		t.Fatalf("Sync all: %v", err)
	}
	// One completed event for the single repo.
	completed := 0
	for _, e := range stream.sent {
		if e.GetCompleted() {
			completed++
		}
	}
	if completed != 1 {
		t.Errorf("expected 1 completed event, got %d", completed)
	}
}

func TestServerSyncUnknownRepo(t *testing.T) {
	srv := newServer(newHarness(t))
	stream := &fakeSyncStream{ctx: context.Background()}
	err := srv.Sync(&lumberjackv1.SyncRequest{Repository: "ghost"}, stream)
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// TestServerWatchSnapshotThenDelta checks that a Watch call first emits a
// SNAPSHOT event per tracked repository (with its current worktrees), then
// forwards a live delta once one happens.
func TestServerWatchSnapshotThenDelta(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	h.seedSync(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeWatchStream{ctx: ctx, sent: make(chan *lumberjackv1.WatchResponse, 10)}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Watch(&lumberjackv1.WatchRequest{}, stream) }()

	snap := recvWatch(t, stream.sent)
	if snap.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SNAPSHOT {
		t.Fatalf("first event type = %v, want SNAPSHOT", snap.GetType())
	}
	if snap.GetRepository().GetDirPrefix() != "n" || len(snap.GetWorktrees()) != 1 {
		t.Errorf("snapshot = %+v", snap)
	}

	// Now that the snapshot has been observed, the subscription is definitely
	// established: trigger a delta and confirm it streams next.
	if _, err := h.svc.DeleteWorktree(context.Background(), repo, "a", false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}

	delta := recvWatch(t, stream.sent)
	if delta.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_WORKTREE_CHANGED {
		t.Fatalf("delta event type = %v, want WORKTREE_CHANGED", delta.GetType())
	}
	if delta.GetChange().GetAction() != lumberjackv1.WorktreeAction_WORKTREE_ACTION_DELETED ||
		delta.GetChange().GetBranch() != "a" {
		t.Errorf("delta change = %+v", delta.GetChange())
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("Watch returned %v after context cancellation, want nil", err)
	}
}

// TestServerWatchMultipleSubscribers checks that two concurrent Watch calls
// each receive their own independent snapshot and delta feed.
func TestServerWatchMultipleSubscribers(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streams := make([]*fakeWatchStream, 2)
	errCh := make(chan error, 2)
	for i := range streams {
		streams[i] = &fakeWatchStream{ctx: ctx, sent: make(chan *lumberjackv1.WatchResponse, 10)}
		go func(s *fakeWatchStream) { errCh <- srv.Watch(&lumberjackv1.WatchRequest{}, s) }(streams[i])
	}

	for _, s := range streams {
		if snap := recvWatch(t, s.sent); snap.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SNAPSHOT {
			t.Fatalf("snapshot type = %v", snap.GetType())
		}
	}

	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	if _, _, err := h.svc.SyncRepository(context.Background(), repo, nil); err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}

	for _, s := range streams {
		if ev := recvWatch(t, s.sent); ev.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_STARTED {
			t.Errorf("expected SYNC_STARTED for each subscriber, got %v", ev.GetType())
		}
		// Drain the rest of this sync's events (a worktree change, then
		// SYNC_FINISHED) so no Send is in flight when the context is cancelled.
		recvWatch(t, s.sent)
		if ev := recvWatch(t, s.sent); ev.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_FINISHED {
			t.Errorf("expected SYNC_FINISHED for each subscriber, got %v", ev.GetType())
		}
	}

	cancel()
	for range streams {
		if err := <-errCh; err != nil {
			t.Errorf("Watch returned %v after context cancellation, want nil", err)
		}
	}
}

func TestRegisterGRPC(t *testing.T) {
	srv := newServer(newHarness(t))
	g := grpc.NewServer()
	srv.RegisterGRPC(g) // must not panic
}

func TestServerTidySingleRepo(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.track(t, repo, "feature/foo", from)

	resp, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if len(resp.GetMoves()) != 1 {
		t.Fatalf("moves=%v, want 1", resp.GetMoves())
	}
	m := resp.GetMoves()[0]
	want := filepath.Join(h.parent, "n-foo")
	if m.GetRepository() != "n" || m.GetBranch() != "feature/foo" || m.GetFrom() != from ||
		m.GetTo() != want || !m.GetMoved() || m.GetError() != "" {
		t.Errorf("move=%+v, want a clean move of feature/foo to %s", m, want)
	}
}

func TestServerTidyAllReposDryRun(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.track(t, repo, "feature/foo", from)

	// No CLI command exposes the all-repositories scope today, but the RPC
	// still supports it (an empty repository), so it stays covered.
	resp, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{DryRun: true})
	if err != nil {
		t.Fatalf("Tidy all: %v", err)
	}
	if len(resp.GetMoves()) != 1 || resp.GetMoves()[0].GetMoved() {
		t.Fatalf("moves=%v, want one unmoved entry", resp.GetMoves())
	}
	if _, err := os.Stat(from); err != nil {
		t.Errorf("dry run moved the worktree off %s: %v", from, err)
	}
}

func TestServerTidyUnknownRepo(t *testing.T) {
	srv := newServer(newHarness(t))
	_, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{Repository: "ghost"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestServerTidyWorktreeScoped(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.track(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"))
	h.track(t, repo, "feature/bar", filepath.Join(h.parent, "elsewhere", "bar"))

	resp, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{
		Repository: "n", Worktree: "feature/foo",
	})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if len(resp.GetMoves()) != 1 || resp.GetMoves()[0].GetBranch() != "feature/foo" {
		t.Errorf("moves=%v, want only feature/foo", resp.GetMoves())
	}
}

// The proto's lock fields reach the service: a per-worktree decision beats the
// request-wide strategy, and the lock state tidy found comes back on the move.
func TestServerTidyLockDecisionOverridesStrategy(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	from := filepath.Join(h.parent, "elsewhere", "foo")
	h.lockedWorktree(t, repo, "feature/foo", from, "in use")

	resp, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{
		Repository:   "n",
		LockStrategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP,
		LockDecisions: []*lumberjackv1.LockDecision{{
			WorktreePath: from, Strategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE,
		}},
	})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if len(resp.GetMoves()) != 1 {
		t.Fatalf("moves=%v, want 1", resp.GetMoves())
	}
	m := resp.GetMoves()[0]
	if !m.GetMoved() || !m.GetLocked() || m.GetLockReason() != "in use" {
		t.Errorf("move=%+v, want it moved and reported as having been locked", m)
	}
	if len(h.git.locks) != 0 {
		t.Errorf("locks=%v, want the lock deleted", h.git.locks)
	}
}

// Aborting is a failed call, not a report of nothing done, so the CLI can exit
// non-zero without inspecting the moves.
func TestServerTidyAbortOnLockIsAborted(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.lockedWorktree(t, repo, "feature/foo", filepath.Join(h.parent, "elsewhere", "foo"), "")

	_, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{
		Repository: "n", LockStrategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT,
	})
	if status.Code(err) != codes.Aborted {
		t.Errorf("expected Aborted, got %v", err)
	}
}

func TestServerTidyUnknownWorktree(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	_, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{
		Repository: "n", Worktree: "ghost",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// A worktree reference only makes sense within one repository, so pairing it
// with the all-repositories scope is rejected rather than guessed at.
// A worktree reference without a repository is rejected on the request alone,
// so the same command is not accepted on a machine tracking one repository and
// refused on a machine tracking two.
func TestServerTidyWorktreeWithoutRepositoryRejected(t *testing.T) {
	for _, repos := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d-tracked", repos), func(t *testing.T) {
			h := newHarness(t)
			srv := newServer(h)
			h.repo(t)
			for i := 1; i < repos; i++ {
				name := fmt.Sprintf("m%d", i)
				extra := &schema.Repository{
					LocalPath: filepath.Join(h.parent, name), WorktreeParentDir: h.parent,
					DirPrefix: name, GithubOwner: "o", GithubName: name,
					DefaultRemote: "origin", Host: "github.com",
				}
				if err := h.db.CreateRepository(context.Background(), extra); err != nil {
					t.Fatalf("CreateRepository: %v", err)
				}
			}

			_, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{Worktree: "feature/foo"})
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

// Abort promises nothing is moved. Across repositories that has to hold too:
// tidying A in full and only then aborting on B would be doubly wrong, since
// Tidy discards the response along with the error, so A's moves would be
// neither prevented nor reported.
func TestServerTidyAbortAcrossReposMovesNothingAnywhere(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)

	// Two tracked repositories: the first has a freely movable misplaced
	// worktree, the second a locked one that trips the abort.
	free := h.repo(t)
	freeFrom := filepath.Join(h.parent, "elsewhere", "free")
	h.track(t, free, "feature/free", freeFrom)

	locked := &schema.Repository{
		LocalPath: filepath.Join(h.parent, "m"), WorktreeParentDir: h.parent,
		DirPrefix: "m", GithubOwner: "o", GithubName: "m",
		DefaultRemote: "origin", Host: "github.com",
	}
	if err := h.db.CreateRepository(context.Background(), locked); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	lockedFrom := filepath.Join(h.parent, "elsewhere", "locked")
	h.lockedWorktree(t, locked, "feature/locked", lockedFrom, "in use")

	_, err := srv.Tidy(context.Background(), &lumberjackv1.TidyRequest{
		LockStrategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT,
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("Tidy err=%v, want Aborted", err)
	}
	// The freely movable worktree in the *other* repository must be untouched.
	if _, statErr := os.Stat(freeFrom); statErr != nil {
		t.Errorf("abort moved a worktree in another repository off %s: %v", freeFrom, statErr)
	}
	if len(h.git.moves) != 0 {
		t.Errorf("git moves=%v, want none after an abort", h.git.moves)
	}
}

// failingWatchStream lets `allow` sends through — signalling each on sent — and
// fails every send after that, so a test can fail the snapshot or a later delta.
type failingWatchStream struct {
	grpc.ServerStream
	ctx   context.Context
	allow int
	n     int
	sent  chan struct{}
	err   error
}

func (f *failingWatchStream) Send(*lumberjackv1.WatchResponse) error {
	f.n++
	if f.n > f.allow {
		return f.err
	}
	f.sent <- struct{}{}
	return nil
}
func (f *failingWatchStream) Context() context.Context { return f.ctx }

// blockingWatchStream holds its first Send until release is closed — signalling
// on entered that it is stalled — so a test can pile events up behind the
// subscriber's buffer while the stream cannot drain. Later sends pass straight
// through.
type blockingWatchStream struct {
	grpc.ServerStream
	ctx     context.Context
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *blockingWatchStream) Send(*lumberjackv1.WatchResponse) error {
	b.once.Do(func() {
		b.entered <- struct{}{}
		<-b.release
	})
	return nil
}
func (b *blockingWatchStream) Context() context.Context { return b.ctx }

func TestServerInitRepositoryReportsAdoptedWorktrees(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	dir := filepath.Join(h.parent, "repo")
	h.git.worktrees = []worktree.Ref{
		{Dir: dir, Branch: "main"},
		{Dir: filepath.Join(h.parent, "repo-feature"), Branch: "feature/x"},
	}

	resp, err := srv.InitRepository(context.Background(),
		&lumberjackv1.InitRepositoryRequest{LocalPath: dir})
	if err != nil {
		t.Fatalf("InitRepository: %v", err)
	}
	if len(resp.GetAdopted()) != 1 {
		t.Fatalf("adopted = %v, want one entry", resp.GetAdopted())
	}
	got := resp.GetAdopted()[0]
	if got.GetBranch() != "feature/x" ||
		got.GetAction() != lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED {
		t.Errorf("adopted = %+v", got)
	}
}

func TestServerListRepositoriesDatabaseFailureIsInternal(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}
	_, err := srv.ListRepositories(context.Background(), &lumberjackv1.ListRepositoriesRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestServerListRepositoriesSurvivesUnreadableSetupConfig covers the decoration
// being best-effort: a repository whose trusted config cannot be read must
// still be listed.
func TestServerListRepositoriesSurvivesUnreadableSetupConfig(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	h.git.showFileErr = errors.New("fatal: not a valid object name")

	resp, err := srv.ListRepositories(context.Background(), &lumberjackv1.ListRepositoriesRequest{})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(resp.GetRepositories()) != 1 {
		t.Fatalf("repositories = %v, want one", resp.GetRepositories())
	}
	if resp.GetRepositories()[0].GetSetupConsentPending() {
		t.Error("consent must not be reported as pending when it could not be read")
	}
}

func TestServerGetSetupConsentFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	_, err := srv.GetSetupConsent(context.Background(),
		&lumberjackv1.GetSetupConsentRequest{Repository: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown repo: expected NotFound, got %v", err)
	}

	h.git.showFileErr = errors.New("fatal: not a valid object name")
	_, err = srv.GetSetupConsent(context.Background(),
		&lumberjackv1.GetSetupConsentRequest{Repository: "n"})
	if status.Code(err) != codes.Internal {
		t.Errorf("unreadable config: expected Internal, got %v", err)
	}
}

func TestServerSetSetupConsentFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	_, err := srv.SetSetupConsent(context.Background(), &lumberjackv1.SetSetupConsentRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty repository: expected InvalidArgument, got %v", err)
	}
	_, err = srv.SetSetupConsent(context.Background(),
		&lumberjackv1.SetSetupConsentRequest{Repository: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown repo: expected NotFound, got %v", err)
	}

	h.git.showFileErr = errors.New("fatal: not a valid object name")
	_, err = srv.SetSetupConsent(context.Background(),
		&lumberjackv1.SetSetupConsentRequest{Repository: "n"})
	if status.Code(err) != codes.Internal {
		t.Errorf("unreadable config: expected Internal, got %v", err)
	}
}

func TestServerSetLogin(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	h.gh.logins = []string{"octocat"}
	h.gh.active = "someone-else"

	resp, err := srv.SetLogin(context.Background(),
		&lumberjackv1.SetLoginRequest{Repository: "n", Login: "octocat"})
	if err != nil {
		t.Fatalf("SetLogin: %v", err)
	}
	if resp.GetRepository().GetLogin() != "octocat" {
		t.Errorf("login = %q, want octocat", resp.GetRepository().GetLogin())
	}
}

func TestServerSetLoginFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	cases := map[string]struct {
		req  *lumberjackv1.SetLoginRequest
		want codes.Code
	}{
		"no repository": {&lumberjackv1.SetLoginRequest{Login: "octocat"}, codes.InvalidArgument},
		"no login":      {&lumberjackv1.SetLoginRequest{Repository: "n"}, codes.InvalidArgument},
		"unknown repository": {
			&lumberjackv1.SetLoginRequest{Repository: "nope", Login: "octocat"}, codes.NotFound,
		},
		"login gh does not know": {
			&lumberjackv1.SetLoginRequest{Repository: "n", Login: "octocat"}, codes.Internal,
		},
	}
	for name, c := range cases {
		if _, err := srv.SetLogin(context.Background(), c.req); status.Code(err) != c.want {
			t.Errorf("%s: expected %v, got %v", name, c.want, err)
		}
	}
}

func TestServerListLogins(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	repo.Login = "octocat"
	if err := h.db.UpdateLogin(context.Background(), repo.ID, "octocat"); err != nil {
		t.Fatalf("UpdateLogin: %v", err)
	}
	h.gh.logins = []string{"octocat", "hubot"}

	resp, err := srv.ListLogins(context.Background(), &lumberjackv1.ListLoginsRequest{Repository: "n"})
	if err != nil {
		t.Fatalf("ListLogins: %v", err)
	}
	if len(resp.GetLogins()) != 2 || resp.GetCurrent() != "octocat" {
		t.Errorf("logins = %v, current = %q", resp.GetLogins(), resp.GetCurrent())
	}
}

func TestServerListLoginsFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	_, err := srv.ListLogins(context.Background(), &lumberjackv1.ListLoginsRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty repository: expected InvalidArgument, got %v", err)
	}
	_, err = srv.ListLogins(context.Background(), &lumberjackv1.ListLoginsRequest{Repository: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown repo: expected NotFound, got %v", err)
	}

	h.gh.loginsErr = errors.New("gh: not logged in")
	_, err = srv.ListLogins(context.Background(), &lumberjackv1.ListLoginsRequest{Repository: "n"})
	if status.Code(err) != codes.Internal {
		t.Errorf("gh failure: expected Internal, got %v", err)
	}
}

func TestServerAddWorktree(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	resp, err := srv.AddWorktree(context.Background(),
		&lumberjackv1.AddWorktreeRequest{Repository: "n", Branch: "feature/x"})
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if resp.GetBranch() != "feature/x" || resp.GetDirectoryPath() == "" {
		t.Errorf("response = %+v", resp)
	}
	if resp.GetSetupError() != "" {
		t.Errorf("setup error = %q, want empty", resp.GetSetupError())
	}
}

func TestServerAddWorktreeFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	_, err := srv.AddWorktree(context.Background(),
		&lumberjackv1.AddWorktreeRequest{Repository: "n"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty branch: expected InvalidArgument, got %v", err)
	}
	_, err = srv.AddWorktree(context.Background(),
		&lumberjackv1.AddWorktreeRequest{Repository: "nope", Branch: "feature/x"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown repo: expected NotFound, got %v", err)
	}

	h.git.fetchErr = errors.New("network is unreachable")
	_, err = srv.AddWorktree(context.Background(),
		&lumberjackv1.AddWorktreeRequest{Repository: "n", Branch: "feature/x"})
	if status.Code(err) != codes.Internal {
		t.Errorf("fetch failure: expected Internal, got %v", err)
	}
}

func TestServerListWorktreesFailures(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)

	_, err := srv.ListWorktrees(context.Background(),
		&lumberjackv1.ListWorktreesRequest{Repository: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown repo: expected NotFound, got %v", err)
	}

	h.git.fetchErr = errors.New("network is unreachable")
	_, err = srv.ListWorktrees(context.Background(),
		&lumberjackv1.ListWorktreesRequest{Repository: "n"})
	if status.Code(err) != codes.Internal {
		t.Errorf("fetch failure: expected Internal, got %v", err)
	}
}

func TestServerDeleteWorktreeUnknownRepository(t *testing.T) {
	srv := newServer(newHarness(t))
	_, err := srv.DeleteWorktree(context.Background(),
		&lumberjackv1.DeleteWorktreeRequest{Repository: "nope", Worktree: "feature/x"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestServerSyncSendFailureIsReturned(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	sendErr := errors.New("client hung up")
	stream := &fakeSyncStream{ctx: context.Background(), err: sendErr}

	if err := srv.Sync(&lumberjackv1.SyncRequest{Repository: "n"}, stream); !errors.Is(err, sendErr) {
		t.Errorf("Sync = %v, want the send error", err)
	}
}

func TestServerSyncScopeFailureIsInternal(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}
	stream := &fakeSyncStream{ctx: context.Background()}
	err := srv.Sync(&lumberjackv1.SyncRequest{}, stream)
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

func TestServerWatchDatabaseFailureIsInternal(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}
	stream := &fakeWatchStream{ctx: context.Background(), sent: make(chan *lumberjackv1.WatchResponse, 1)}
	if err := srv.Watch(&lumberjackv1.WatchRequest{}, stream); status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

func TestServerWatchSnapshotFailureIsInternal(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	h.repo(t)
	h.git.fetchErr = errors.New("network is unreachable")

	stream := &fakeWatchStream{ctx: context.Background(), sent: make(chan *lumberjackv1.WatchResponse, 1)}
	if err := srv.Watch(&lumberjackv1.WatchRequest{}, stream); status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

func TestServerWatchStopsOnSendFailure(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	sendErr := errors.New("client hung up")

	// The snapshot itself failing ends the stream.
	stream := &failingWatchStream{ctx: context.Background(), sent: make(chan struct{}, 1), err: sendErr}
	if err := srv.Watch(&lumberjackv1.WatchRequest{}, stream); !errors.Is(err, sendErr) {
		t.Errorf("snapshot failure: Watch = %v, want the send error", err)
	}

	// A later delta failing ends it too, after the snapshot went out.
	stream = &failingWatchStream{
		ctx: context.Background(), allow: 1, sent: make(chan struct{}, 1), err: sendErr,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Watch(&lumberjackv1.WatchRequest{}, stream) }()
	<-stream.sent
	h.svc.events.Publish(Event{Type: EventSyncStarted, Repository: repo})
	select {
	case err := <-done:
		if !errors.Is(err, sendErr) {
			t.Errorf("delta failure: Watch = %v, want the send error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after a delta send failed")
	}
}

// TestServerWatchDisconnectsASubscriberThatFellBehind covers the bounded
// subscriber buffer: a client too slow to keep up is disconnected with
// ResourceExhausted rather than stalling the daemon.
func TestServerWatchDisconnectsASubscriberThatFellBehind(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)

	stream := &blockingWatchStream{
		ctx:     context.Background(),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() { done <- srv.Watch(&lumberjackv1.WatchRequest{}, stream) }()

	// Wait until the stream is stalled inside the snapshot send, then overrun the
	// subscriber's buffer while it cannot drain.
	<-stream.entered
	for i := 0; i <= subscriberBuffer; i++ {
		h.svc.events.Publish(Event{Type: EventSyncStarted, Repository: repo})
	}
	close(stream.release)

	select {
	case err := <-done:
		if status.Code(err) != codes.ResourceExhausted {
			t.Errorf("Watch = %v, want ResourceExhausted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after its subscriber was dropped")
	}
}

func TestToStatusMapsDomainErrors(t *testing.T) {
	cases := []struct {
		err  error
		want codes.Code
	}{
		{database.ErrRepositoryNotFound, codes.NotFound},
		{database.ErrWorktreeNotFound, codes.NotFound},
		{fmt.Errorf("wrapped: %w", database.ErrRepositoryNotFound), codes.NotFound},
		{database.ErrRepositoryExists, codes.AlreadyExists},
		{ErrTidyAborted, codes.Aborted},
		{errors.New("something else"), codes.Internal},
	}
	for _, c := range cases {
		if got := status.Code(toStatus(c.err)); got != c.want {
			t.Errorf("toStatus(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestToLockStrategy(t *testing.T) {
	cases := map[lumberjackv1.LockStrategy]LockStrategy{
		lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK:      LockUnlock,
		lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE:      LockDelete,
		lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT:       LockAbort,
		lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP:        LockSkip,
		lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED: LockSkip,
	}
	for in, want := range cases {
		if got := toLockStrategy(in); got != want {
			t.Errorf("toLockStrategy(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestToLockDecisions(t *testing.T) {
	if got := toLockDecisions(nil); got != nil {
		t.Errorf("toLockDecisions(nil) = %v, want nil", got)
	}
	// A repeated path means the client resent its decision: the later one wins.
	got := toLockDecisions([]*lumberjackv1.LockDecision{
		{WorktreePath: "/a", Strategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP},
		{WorktreePath: "/a", Strategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE},
	})
	if len(got) != 1 || got["/a"] != LockDelete {
		t.Errorf("toLockDecisions = %v, want {/a: delete}", got)
	}
}

func TestCanAbortOnLock(t *testing.T) {
	cases := map[string]struct {
		opts TidyOptions
		want bool
	}{
		"no abort anywhere": {TidyOptions{LockStrategy: LockSkip}, false},
		"abort as strategy": {TidyOptions{LockStrategy: LockAbort}, true},
		"abort as a per-worktree decision": {
			TidyOptions{
				LockStrategy:  LockSkip,
				LockDecisions: map[string]LockStrategy{"/a": LockSkip, "/b": LockAbort},
			},
			true,
		},
		"decisions with no abort": {
			TidyOptions{LockStrategy: LockSkip, LockDecisions: map[string]LockStrategy{"/a": LockUnlock}},
			false,
		},
	}
	for name, c := range cases {
		if got := c.opts.CanAbortOnLock(); got != c.want {
			t.Errorf("%s: CanAbortOnLock = %v, want %v", name, got, c.want)
		}
	}
}
