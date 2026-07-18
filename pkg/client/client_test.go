package client

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubServer is a hand-written LumberjackService for exercising the client's
// dialing, method wrapping, and error mapping without the real daemon.
type stubServer struct {
	lumberjackv1.UnimplementedLumberjackServiceServer
}

func (stubServer) Health(context.Context, *lumberjackv1.HealthRequest) (*lumberjackv1.HealthResponse, error) {
	return &lumberjackv1.HealthResponse{Version: "stub"}, nil
}

func (stubServer) InitRepository(_ context.Context, req *lumberjackv1.InitRepositoryRequest) (*lumberjackv1.InitRepositoryResponse, error) {
	if req.GetLocalPath() == "/dupe" {
		return nil, status.Error(codes.AlreadyExists, "repository already tracked")
	}
	return &lumberjackv1.InitRepositoryResponse{
		Repository: &lumberjackv1.Repository{LocalPath: req.GetLocalPath(), DirPrefix: "repo"},
	}, nil
}

func (stubServer) ListRepositories(context.Context, *lumberjackv1.ListRepositoriesRequest) (*lumberjackv1.ListRepositoriesResponse, error) {
	return &lumberjackv1.ListRepositoriesResponse{
		Repositories: []*lumberjackv1.Repository{{DirPrefix: "a"}, {DirPrefix: "b"}},
	}, nil
}

func (stubServer) GetRepository(_ context.Context, req *lumberjackv1.GetRepositoryRequest) (*lumberjackv1.GetRepositoryResponse, error) {
	if req.GetRepository() == "ghost" {
		return nil, status.Error(codes.NotFound, "repository not found")
	}
	return &lumberjackv1.GetRepositoryResponse{Repository: &lumberjackv1.Repository{DirPrefix: req.GetRepository()}}, nil
}

func (stubServer) ListWorktrees(context.Context, *lumberjackv1.ListWorktreesRequest) (*lumberjackv1.ListWorktreesResponse, error) {
	return &lumberjackv1.ListWorktreesResponse{Worktrees: []*lumberjackv1.Worktree{{BranchName: "feature/x"}}}, nil
}

func (stubServer) DeleteWorktree(_ context.Context, req *lumberjackv1.DeleteWorktreeRequest) (*lumberjackv1.DeleteWorktreeResponse, error) {
	if !req.GetForce() {
		return &lumberjackv1.DeleteWorktreeResponse{RequiresConfirmation: true, CommitsAtRisk: 2}, nil
	}
	return &lumberjackv1.DeleteWorktreeResponse{Deleted: true, Message: "deleted"}, nil
}

func (stubServer) Sync(_ *lumberjackv1.SyncRequest, stream grpc.ServerStreamingServer[lumberjackv1.SyncResponse]) error {
	_ = stream.Send(&lumberjackv1.SyncResponse{Repository: "a", Message: "working"})
	return stream.Send(&lumberjackv1.SyncResponse{
		Repository: "a", Completed: true,
		Summary: &lumberjackv1.SyncSummary{WorktreesCreated: 1},
	})
}

// startStub starts the stub server on a temp unix socket and returns a
// connected client.
func startStub(t *testing.T) *Client {
	t.Helper()
	path := shortSocket(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	lumberjackv1.RegisterLumberjackServiceServer(srv, stubServer{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	c, err := DialSocket(path)
	if err != nil {
		t.Fatalf("DialSocket: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// shortSocket returns a socket path in a short temp dir, since Unix socket
// paths are limited to ~104 bytes and t.TempDir embeds the (long) test name.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ljc")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

func TestDefaultSocketPath(t *testing.T) {
	t.Setenv(EnvSocketPath, "/custom.sock")
	if got, _ := DefaultSocketPath(); got != "/custom.sock" {
		t.Errorf("override = %q", got)
	}
	t.Setenv(EnvSocketPath, "")
	got, err := DefaultSocketPath()
	if err != nil {
		t.Fatalf("DefaultSocketPath: %v", err)
	}
	if filepath.Base(got) != "daemon.sock" {
		t.Errorf("fallback = %q", got)
	}
}

func TestClientHealth(t *testing.T) {
	c := startStub(t)
	resp, err := c.Health(context.Background())
	if err != nil || resp.GetVersion() != "stub" {
		t.Errorf("Health = %v, %v", resp, err)
	}
}

func TestClientInitAndAlreadyExists(t *testing.T) {
	c := startStub(t)
	repo, _, err := c.InitRepository(context.Background(), "/new")
	if err != nil || repo.GetDirPrefix() != "repo" {
		t.Errorf("InitRepository = %v, %v", repo, err)
	}
	_, _, err = c.InitRepository(context.Background(), "/dupe")
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestClientListAndGet(t *testing.T) {
	c := startStub(t)
	repos, err := c.ListRepositories(context.Background())
	if err != nil || len(repos) != 2 {
		t.Fatalf("ListRepositories = %v, %v", repos, err)
	}
	if _, err := c.GetRepository(context.Background(), "a"); err != nil {
		t.Errorf("GetRepository: %v", err)
	}
	_, err = c.GetRepository(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestClientListWorktrees(t *testing.T) {
	c := startStub(t)
	wts, err := c.ListWorktrees(context.Background(), "a")
	if err != nil || len(wts) != 1 || wts[0].GetBranchName() != "feature/x" {
		t.Errorf("ListWorktrees = %v, %v", wts, err)
	}
}

func TestClientDeleteWorktreeConfirmationFlow(t *testing.T) {
	c := startStub(t)
	resp, err := c.DeleteWorktree(context.Background(), "a", "feature/x", false)
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !resp.GetRequiresConfirmation() || resp.GetCommitsAtRisk() != 2 {
		t.Errorf("unforced = %+v", resp)
	}
	resp, err = c.DeleteWorktree(context.Background(), "a", "feature/x", true)
	if err != nil || !resp.GetDeleted() {
		t.Errorf("forced = %+v, %v", resp, err)
	}
}

func TestClientSyncStreaming(t *testing.T) {
	c := startStub(t)
	var events []*lumberjackv1.SyncResponse
	err := c.Sync(context.Background(), "a", func(e *lumberjackv1.SyncResponse) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(events) != 2 || !events[1].GetCompleted() {
		t.Errorf("events = %+v", events)
	}
}

func TestClientSyncCallbackError(t *testing.T) {
	c := startStub(t)
	sentinel := errors.New("stop")
	err := c.Sync(context.Background(), "a", func(*lumberjackv1.SyncResponse) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("expected callback error, got %v", err)
	}
}

func TestClientDaemonNotRunning(t *testing.T) {
	// Dial a socket with no server behind it: the first RPC must map to
	// ErrDaemonNotRunning.
	c, err := DialSocket(shortSocket(t))
	if err != nil {
		t.Fatalf("DialSocket: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrDaemonNotRunning) {
		t.Errorf("expected ErrDaemonNotRunning, got %v", err)
	}
}

func TestDialDefault(t *testing.T) {
	// Dial() must resolve the default path without error (lazy connect).
	t.Setenv(EnvSocketPath, filepath.Join(t.TempDir(), "x.sock"))
	c, err := Dial()
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = c.Close()
}
