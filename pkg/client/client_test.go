package client

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

func (stubServer) SetLogin(_ context.Context, req *lumberjackv1.SetLoginRequest) (*lumberjackv1.SetLoginResponse, error) {
	return &lumberjackv1.SetLoginResponse{
		Repository: &lumberjackv1.Repository{DirPrefix: req.GetRepository(), Login: req.GetLogin()},
	}, nil
}

func (stubServer) ListLogins(context.Context, *lumberjackv1.ListLoginsRequest) (*lumberjackv1.ListLoginsResponse, error) {
	return &lumberjackv1.ListLoginsResponse{Logins: []string{"alice", "bob"}, Current: "bob"}, nil
}

func (stubServer) GetSetupConsent(context.Context, *lumberjackv1.GetSetupConsentRequest) (*lumberjackv1.GetSetupConsentResponse, error) {
	return &lumberjackv1.GetSetupConsentResponse{Pending: true, RunCommands: []string{"make deps"}}, nil
}

func (stubServer) SetSetupConsent(_ context.Context, req *lumberjackv1.SetSetupConsentRequest) (*lumberjackv1.SetSetupConsentResponse, error) {
	return &lumberjackv1.SetSetupConsentResponse{
		Repository: &lumberjackv1.Repository{DirPrefix: req.GetRepository()},
	}, nil
}

func (stubServer) AddWorktree(_ context.Context, req *lumberjackv1.AddWorktreeRequest) (*lumberjackv1.AddWorktreeResponse, error) {
	return &lumberjackv1.AddWorktreeResponse{
		DirectoryPath: "/repo-" + req.GetBranch(),
		Branch:        req.GetBranch(),
		BranchCreated: true,
		SetupError:    "setup failed but the worktree stands",
	}, nil
}

func (stubServer) DeleteRepository(context.Context, *lumberjackv1.DeleteRepositoryRequest) (*lumberjackv1.DeleteRepositoryResponse, error) {
	return &lumberjackv1.DeleteRepositoryResponse{WorktreesRemoved: 3}, nil
}

func (s *recordingServer) Tidy(_ context.Context, req *lumberjackv1.TidyRequest) (*lumberjackv1.TidyResponse, error) {
	s.mu.Lock()
	s.tidy = req
	s.mu.Unlock()
	moves := []*lumberjackv1.TidyMove{{From: "/wrong", To: "/right", Moved: !req.GetDryRun()}}
	return &lumberjackv1.TidyResponse{Moves: moves}, nil
}

func (stubServer) Watch(_ *lumberjackv1.WatchRequest, stream grpc.ServerStreamingServer[lumberjackv1.WatchResponse]) error {
	if err := stream.Send(&lumberjackv1.WatchResponse{Type: lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SNAPSHOT}); err != nil {
		return err
	}
	return stream.Send(&lumberjackv1.WatchResponse{
		Type:   lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_WORKTREE_CHANGED,
		Change: &lumberjackv1.WorktreeChange{Branch: "feature/x"},
	})
}

type recordingServer struct {
	stubServer
	mu   sync.Mutex
	tidy *lumberjackv1.TidyRequest
}

func (s *recordingServer) lastTidy() *lumberjackv1.TidyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tidy
}

type failServer struct {
	lumberjackv1.UnimplementedLumberjackServiceServer
	err error
}

func (s failServer) Health(context.Context, *lumberjackv1.HealthRequest) (*lumberjackv1.HealthResponse, error) {
	return nil, s.err
}

func (s failServer) InitRepository(context.Context, *lumberjackv1.InitRepositoryRequest) (*lumberjackv1.InitRepositoryResponse, error) {
	return nil, s.err
}

func (s failServer) ListRepositories(context.Context, *lumberjackv1.ListRepositoriesRequest) (*lumberjackv1.ListRepositoriesResponse, error) {
	return nil, s.err
}

func (s failServer) GetRepository(context.Context, *lumberjackv1.GetRepositoryRequest) (*lumberjackv1.GetRepositoryResponse, error) {
	return nil, s.err
}

func (s failServer) SetLogin(context.Context, *lumberjackv1.SetLoginRequest) (*lumberjackv1.SetLoginResponse, error) {
	return nil, s.err
}

func (s failServer) ListLogins(context.Context, *lumberjackv1.ListLoginsRequest) (*lumberjackv1.ListLoginsResponse, error) {
	return nil, s.err
}

func (s failServer) GetSetupConsent(context.Context, *lumberjackv1.GetSetupConsentRequest) (*lumberjackv1.GetSetupConsentResponse, error) {
	return nil, s.err
}

func (s failServer) SetSetupConsent(context.Context, *lumberjackv1.SetSetupConsentRequest) (*lumberjackv1.SetSetupConsentResponse, error) {
	return nil, s.err
}

func (s failServer) ListWorktrees(context.Context, *lumberjackv1.ListWorktreesRequest) (*lumberjackv1.ListWorktreesResponse, error) {
	return nil, s.err
}

func (s failServer) AddWorktree(context.Context, *lumberjackv1.AddWorktreeRequest) (*lumberjackv1.AddWorktreeResponse, error) {
	return nil, s.err
}

func (s failServer) DeleteWorktree(context.Context, *lumberjackv1.DeleteWorktreeRequest) (*lumberjackv1.DeleteWorktreeResponse, error) {
	return nil, s.err
}

func (s failServer) DeleteRepository(context.Context, *lumberjackv1.DeleteRepositoryRequest) (*lumberjackv1.DeleteRepositoryResponse, error) {
	return nil, s.err
}

func (s failServer) Tidy(context.Context, *lumberjackv1.TidyRequest) (*lumberjackv1.TidyResponse, error) {
	return nil, s.err
}

func (s failServer) Sync(*lumberjackv1.SyncRequest, grpc.ServerStreamingServer[lumberjackv1.SyncResponse]) error {
	return s.err
}

func (s failServer) Watch(*lumberjackv1.WatchRequest, grpc.ServerStreamingServer[lumberjackv1.WatchResponse]) error {
	return s.err
}

// startStub starts the stub server on a temp unix socket and returns a
// connected client.
func startStub(t *testing.T) *Client {
	t.Helper()
	return serve(t, &recordingServer{})
}

func serve(t *testing.T, impl lumberjackv1.LumberjackServiceServer) *Client {
	t.Helper()
	path := shortSocket(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	lumberjackv1.RegisterLumberjackServiceServer(srv, impl)
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

func TestDefaultSocketPathNoHome(t *testing.T) {
	t.Setenv(EnvSocketPath, "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := DefaultSocketPath(); err == nil {
		t.Fatal("expected an error when the home directory cannot be resolved")
	}
}

func TestDialPropagatesResolutionFailure(t *testing.T) {
	t.Setenv(EnvSocketPath, "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := Dial(); err == nil {
		t.Fatal("expected Dial to fail when the socket path cannot be resolved")
	}
}

func TestDialSocketRejectsUnparseablePath(t *testing.T) {
	path := "/tmp/100%zz/daemon.sock"
	c, err := DialSocket(path)
	if err == nil {
		_ = c.Close()
		t.Fatal("expected DialSocket to reject a path it cannot turn into a target")
	}
	if c != nil {
		t.Errorf("client = %+v, want nil alongside the error", c)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the socket path", err)
	}
}

func TestDialSocketMissingPathAndNoListenerBothReportDaemonDown(t *testing.T) {
	missing := shortSocket(t)

	stale := shortSocket(t)
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for name, path := range map[string]string{"missing": missing, "no listener": stale} {
		c, err := DialSocket(path)
		if err != nil {
			t.Fatalf("%s: DialSocket: %v", name, err)
		}
		_, err = c.Health(context.Background())
		if !errors.Is(err, ErrDaemonNotRunning) {
			t.Errorf("%s: expected ErrDaemonNotRunning, got %v", name, err)
		}
		if err := c.Close(); err != nil {
			t.Errorf("%s: Close: %v", name, err)
		}
	}
}

func TestClientSetLoginAndListLogins(t *testing.T) {
	c := startStub(t)
	repo, err := c.SetLogin(context.Background(), "a", "bob")
	if err != nil {
		t.Fatalf("SetLogin: %v", err)
	}
	if repo.GetLogin() != "bob" || repo.GetDirPrefix() != "a" {
		t.Errorf("SetLogin = %+v", repo)
	}
	logins, current, err := c.ListLogins(context.Background(), "a")
	if err != nil {
		t.Fatalf("ListLogins: %v", err)
	}
	if len(logins) != 2 || current != "bob" {
		t.Errorf("ListLogins = %v, %q", logins, current)
	}
}

func TestClientSetupConsent(t *testing.T) {
	c := startStub(t)
	pending, commands, err := c.GetSetupConsent(context.Background(), "a")
	if err != nil {
		t.Fatalf("GetSetupConsent: %v", err)
	}
	if !pending || len(commands) != 1 || commands[0] != "make deps" {
		t.Errorf("GetSetupConsent = %v, %v", pending, commands)
	}
	repo, err := c.SetSetupConsent(context.Background(), "a")
	if err != nil || repo.GetDirPrefix() != "a" {
		t.Errorf("SetSetupConsent = %+v, %v", repo, err)
	}
}

func TestClientAddWorktree(t *testing.T) {
	c := startStub(t)
	resp, err := c.AddWorktree(context.Background(), "a", "feature/x")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if !resp.GetBranchCreated() || resp.GetSetupError() == "" {
		t.Errorf("AddWorktree = %+v", resp)
	}
}

func TestClientDeleteRepository(t *testing.T) {
	c := startStub(t)
	resp, err := c.DeleteRepository(context.Background(), "a")
	if err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}
	if resp.GetWorktreesRemoved() != 3 {
		t.Errorf("DeleteRepository = %+v", resp)
	}
}

func TestClientTidyTranslatesOptions(t *testing.T) {
	impl := &recordingServer{}
	c := serve(t, impl)
	moves, err := c.Tidy(context.Background(), TidyOptions{
		Repository:   "a",
		Worktree:     "feature/x",
		DryRun:       true,
		LockStrategy: lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP,
		LockDecisions: map[string]lumberjackv1.LockStrategy{
			"/one": lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK,
			"/two": lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT,
		},
	})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if len(moves) != 1 || moves[0].GetMoved() {
		t.Errorf("moves = %+v", moves)
	}

	req := impl.lastTidy()
	if req.GetRepository() != "a" || req.GetWorktree() != "feature/x" || !req.GetDryRun() {
		t.Errorf("request = %+v", req)
	}
	if req.GetLockStrategy() != lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP {
		t.Errorf("lock strategy = %v", req.GetLockStrategy())
	}
	got := map[string]lumberjackv1.LockStrategy{}
	for _, d := range req.GetLockDecisions() {
		got[d.GetWorktreePath()] = d.GetStrategy()
	}
	want := map[string]lumberjackv1.LockStrategy{
		"/one": lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK,
		"/two": lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lock decisions = %v, want %v", got, want)
	}
}

func TestClientTidyWithoutLockDecisionsSendsNone(t *testing.T) {
	impl := &recordingServer{}
	c := serve(t, impl)
	if _, err := c.Tidy(context.Background(), TidyOptions{}); err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if decisions := impl.lastTidy().GetLockDecisions(); len(decisions) != 0 {
		t.Errorf("lock decisions = %+v, want none", decisions)
	}
}

func TestClientWatchStreaming(t *testing.T) {
	c := startStub(t)
	var events []*lumberjackv1.WatchResponse
	err := c.Watch(context.Background(), func(e *lumberjackv1.WatchResponse) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SNAPSHOT {
		t.Errorf("first event = %v", events[0].GetType())
	}
	if events[1].GetChange().GetBranch() != "feature/x" {
		t.Errorf("second event = %+v", events[1])
	}
}

func TestClientWatchCallbackError(t *testing.T) {
	c := startStub(t)
	sentinel := errors.New("stop")
	err := c.Watch(context.Background(), func(*lumberjackv1.WatchResponse) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("expected callback error, got %v", err)
	}
}

func TestClientStreamOpenFailure(t *testing.T) {
	c := startStub(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sync(ctx, "a", func(*lumberjackv1.SyncResponse) error { return nil }); err == nil {
		t.Error("expected Sync to fail on a cancelled context")
	}
	if err := c.Watch(ctx, func(*lumberjackv1.WatchResponse) error { return nil }); err == nil {
		t.Error("expected Watch to fail on a cancelled context")
	}
}

func TestEveryMethodMapsServerErrors(t *testing.T) {
	calls := map[string]func(*Client) error{
		"Health": func(c *Client) error {
			_, err := c.Health(context.Background())
			return err
		},
		"InitRepository": func(c *Client) error {
			_, _, err := c.InitRepository(context.Background(), "/p")
			return err
		},
		"ListRepositories": func(c *Client) error {
			_, err := c.ListRepositories(context.Background())
			return err
		},
		"GetRepository": func(c *Client) error {
			_, err := c.GetRepository(context.Background(), "a")
			return err
		},
		"SetLogin": func(c *Client) error {
			_, err := c.SetLogin(context.Background(), "a", "bob")
			return err
		},
		"ListLogins": func(c *Client) error {
			_, _, err := c.ListLogins(context.Background(), "a")
			return err
		},
		"GetSetupConsent": func(c *Client) error {
			_, _, err := c.GetSetupConsent(context.Background(), "a")
			return err
		},
		"SetSetupConsent": func(c *Client) error {
			_, err := c.SetSetupConsent(context.Background(), "a")
			return err
		},
		"ListWorktrees": func(c *Client) error {
			_, err := c.ListWorktrees(context.Background(), "a")
			return err
		},
		"AddWorktree": func(c *Client) error {
			_, err := c.AddWorktree(context.Background(), "a", "feature/x")
			return err
		},
		"DeleteWorktree": func(c *Client) error {
			_, err := c.DeleteWorktree(context.Background(), "a", "feature/x", false)
			return err
		},
		"DeleteRepository": func(c *Client) error {
			_, err := c.DeleteRepository(context.Background(), "a")
			return err
		},
		"Tidy": func(c *Client) error {
			_, err := c.Tidy(context.Background(), TidyOptions{})
			return err
		},
		"Sync": func(c *Client) error {
			return c.Sync(context.Background(), "a", func(*lumberjackv1.SyncResponse) error { return nil })
		},
		"Watch": func(c *Client) error {
			return c.Watch(context.Background(), func(*lumberjackv1.WatchResponse) error { return nil })
		},
	}

	for name, call := range calls {
		t.Run(name+"/NotFound", func(t *testing.T) {
			c := serve(t, failServer{err: status.Error(codes.NotFound, "no such thing")})
			err := call(c)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("expected ErrNotFound, got %v", err)
			}
		})
		t.Run(name+"/Unavailable", func(t *testing.T) {
			c := serve(t, failServer{err: status.Error(codes.Unavailable, "down")})
			if err := call(c); !errors.Is(err, ErrDaemonNotRunning) {
				t.Errorf("expected ErrDaemonNotRunning, got %v", err)
			}
		})
		t.Run(name+"/AlreadyExists", func(t *testing.T) {
			c := serve(t, failServer{err: status.Error(codes.AlreadyExists, "dupe")})
			if err := call(c); !errors.Is(err, ErrAlreadyExists) {
				t.Errorf("expected ErrAlreadyExists, got %v", err)
			}
		})
		t.Run(name+"/Internal", func(t *testing.T) {
			c := serve(t, failServer{err: status.Error(codes.Internal, "boom")})
			err := call(c)
			if err == nil || err.Error() != "boom" {
				t.Errorf("expected the daemon message verbatim, got %v", err)
			}
			for _, sentinel := range []error{ErrNotFound, ErrAlreadyExists, ErrDaemonNotRunning} {
				if errors.Is(err, sentinel) {
					t.Errorf("unmapped code matched %v", sentinel)
				}
			}
		})
	}
}

func TestMapError(t *testing.T) {
	if err := mapError(nil); err != nil {
		t.Errorf("mapError(nil) = %v", err)
	}

	plain := errors.New("not a status")
	if err := mapError(plain); !errors.Is(err, plain) {
		t.Errorf("mapError(plain) = %v, want it returned unchanged", err)
	}

	for code, want := range map[codes.Code]error{
		codes.Unavailable:   ErrDaemonNotRunning,
		codes.NotFound:      ErrNotFound,
		codes.AlreadyExists: ErrAlreadyExists,
	} {
		if err := mapError(status.Error(code, "detail")); !errors.Is(err, want) {
			t.Errorf("mapError(%v) = %v, want %v", code, err, want)
		}
	}

	for _, code := range []codes.Code{
		codes.Canceled, codes.Unknown, codes.InvalidArgument, codes.DeadlineExceeded,
		codes.PermissionDenied, codes.FailedPrecondition, codes.Aborted, codes.Internal,
		codes.Unimplemented, codes.Unauthenticated, codes.ResourceExhausted,
		codes.OutOfRange, codes.DataLoss,
	} {
		err := mapError(status.Error(code, "detail"))
		if err == nil || err.Error() != "detail" {
			t.Errorf("mapError(%v) = %v, want the bare message", code, err)
		}
	}

	if err := mapError(status.Error(codes.NotFound, "gone")); err.Error() != "gone: not found" {
		t.Errorf("NotFound message = %q", err.Error())
	}
	if err := mapError(status.Error(codes.Unavailable, "down")); !strings.Contains(err.Error(), "lumberjack daemon") {
		t.Errorf("Unavailable message = %q", err.Error())
	}
}
