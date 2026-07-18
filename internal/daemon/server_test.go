package daemon

import (
	"context"
	"testing"
	"time"

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

func TestServerListWorktrees(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

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
	if wt.GetCreatedBy() != lumberjackv1.CreatedBy_CREATED_BY_LUMBERJACK {
		t.Errorf("created_by = %v", wt.GetCreatedBy())
	}
}

func TestServerDeleteWorktree(t *testing.T) {
	h := newHarness(t)
	srv := newServer(h)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

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
	_, _, _ = h.svc.SyncRepository(context.Background(), repo, nil)

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

func TestRegisterGRPC(t *testing.T) {
	srv := newServer(newHarness(t))
	g := grpc.NewServer()
	srv.RegisterGRPC(g) // must not panic
}
