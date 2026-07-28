package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
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
