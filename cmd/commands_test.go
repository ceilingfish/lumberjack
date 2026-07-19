package cmd

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubService is a controllable LumberjackService for driving the CLI commands
// end-to-end over a real socket.
type stubService struct {
	lumberjackv1.UnimplementedLumberjackServiceServer
	repos          []*lumberjackv1.Repository
	worktrees      []*lumberjackv1.Worktree
	deleteConfirm  bool // first delete returns RequiresConfirmation
	deletedForced  bool // set when a forced delete arrived
	getNotFound    bool
	syncEvents     []*lumberjackv1.SyncResponse
	initAdopted    []*lumberjackv1.WorktreeChange // worktrees InitRepository reports as adopted
	lastInitPath   string
	lastSyncTarget string
	lastGetRef     string
	// logins is what ListLogins reports; loginErr, if set, is returned by
	// SetLogin (e.g. an unauthenticated account). lastSetLogin records the login
	// SetLogin received.
	logins       []string
	loginCurrent string
	loginErr     error
	lastSetLogin string
}

func (s *stubService) SetLogin(_ context.Context, req *lumberjackv1.SetLoginRequest) (*lumberjackv1.SetLoginResponse, error) {
	if s.loginErr != nil {
		return nil, s.loginErr
	}
	s.lastSetLogin = req.GetLogin()
	return &lumberjackv1.SetLoginResponse{Repository: &lumberjackv1.Repository{
		DirPrefix: req.GetRepository(), Login: req.GetLogin(),
	}}, nil
}

func (s *stubService) ListLogins(context.Context, *lumberjackv1.ListLoginsRequest) (*lumberjackv1.ListLoginsResponse, error) {
	return &lumberjackv1.ListLoginsResponse{Logins: s.logins, Current: s.loginCurrent}, nil
}

func (s *stubService) InitRepository(_ context.Context, req *lumberjackv1.InitRepositoryRequest) (*lumberjackv1.InitRepositoryResponse, error) {
	s.lastInitPath = req.GetLocalPath()
	return &lumberjackv1.InitRepositoryResponse{
		Repository: &lumberjackv1.Repository{
			LocalPath: req.GetLocalPath(), GithubOwner: "o", GithubName: "n",
			WorktreeParentDir: filepath.Dir(req.GetLocalPath()), DirPrefix: "n",
		},
		Adopted: s.initAdopted,
	}, nil
}

func (s *stubService) ListRepositories(context.Context, *lumberjackv1.ListRepositoriesRequest) (*lumberjackv1.ListRepositoriesResponse, error) {
	return &lumberjackv1.ListRepositoriesResponse{Repositories: s.repos}, nil
}

func (s *stubService) GetRepository(_ context.Context, req *lumberjackv1.GetRepositoryRequest) (*lumberjackv1.GetRepositoryResponse, error) {
	s.lastGetRef = req.GetRepository()
	if s.getNotFound {
		return nil, status.Error(codes.NotFound, "repository not found")
	}
	return &lumberjackv1.GetRepositoryResponse{Repository: &lumberjackv1.Repository{
		DirPrefix: req.GetRepository(), LocalPath: "/p/n", GithubOwner: "o", GithubName: "n", Host: "github.com",
		LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_OK,
	}}, nil
}

func (s *stubService) ListWorktrees(context.Context, *lumberjackv1.ListWorktreesRequest) (*lumberjackv1.ListWorktreesResponse, error) {
	return &lumberjackv1.ListWorktreesResponse{Worktrees: s.worktrees}, nil
}

func (s *stubService) DeleteWorktree(_ context.Context, req *lumberjackv1.DeleteWorktreeRequest) (*lumberjackv1.DeleteWorktreeResponse, error) {
	if s.deleteConfirm && !req.GetForce() {
		return &lumberjackv1.DeleteWorktreeResponse{
			RequiresConfirmation: true, CommitsAtRisk: 3,
			Message: "worktree has 3 local-only commit(s) that will be lost",
		}, nil
	}
	if req.GetForce() {
		s.deletedForced = true
	}
	return &lumberjackv1.DeleteWorktreeResponse{Deleted: true, Message: "deleted n-x"}, nil
}

func (s *stubService) Sync(req *lumberjackv1.SyncRequest, stream grpc.ServerStreamingServer[lumberjackv1.SyncResponse]) error {
	s.lastSyncTarget = req.GetRepository()
	for _, e := range s.syncEvents {
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return nil
}

// serveStub starts stub on a short socket, points LUMBERJACK_SOCKET_PATH at it,
// and returns nothing (cleanup registered on t).
func serveStub(t *testing.T, stub *stubService) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ljcmd")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	lumberjackv1.RegisterLumberjackServiceServer(srv, stub)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	t.Setenv("LUMBERJACK_SOCKET_PATH", path)
}

// run executes the root command with args, returning combined stdout+stderr
// and any error. stdin supplies input for prompts.
func run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestCmdDoctor(t *testing.T) {
	// doctor is daemon-free; it reports on host git/gh. We only assert it runs
	// and produces a report — pass/fail depends on the host.
	out, err := run(t, "", "doctor")
	if out == "" {
		t.Errorf("expected a doctor report, got empty output (err=%v)", err)
	}
}

func TestCmdInit(t *testing.T) {
	stub := &stubService{}
	serveStub(t, stub)

	out, err := run(t, "", "init", ".")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "Tracking o/n") {
		t.Errorf("unexpected output: %q", out)
	}
	if !filepath.IsAbs(stub.lastInitPath) {
		t.Errorf("init path should be absolute: %q", stub.lastInitPath)
	}
}

func TestCmdInitReportsAdoptedWorktrees(t *testing.T) {
	stub := &stubService{initAdopted: []*lumberjackv1.WorktreeChange{
		{Branch: "feature/x", Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED},
		{Branch: "fix/y", Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED},
	}}
	serveStub(t, stub)

	out, err := run(t, "", "init", ".")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	// A branch/PR/action table: header plus a row per adopted branch.
	for _, want := range []string{"BRANCH", "PR", "ACTION", "feature/x", "fix/y", "adopted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestCmdListEmpty(t *testing.T) {
	serveStub(t, &stubService{})
	out, err := run(t, "", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No repositories tracked") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdList(t *testing.T) {
	serveStub(t, &stubService{repos: []*lumberjackv1.Repository{
		{DirPrefix: "a", LocalPath: "/p/a", LastSyncStatus: lumberjackv1.SyncStatus_SYNC_STATUS_OK},
		{DirPrefix: "b", LocalPath: "/p/b"},
	}})
	out, err := run(t, "", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "/p/a") || !strings.Contains(out, "/p/b") || !strings.Contains(out, "never synced") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSyncAll(t *testing.T) {
	pr := int64(1)
	stub := &stubService{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "a", Change: &lumberjackv1.WorktreeChange{
			Branch: "feature/x", PrNumber: &pr,
			Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT,
		}},
		{Repository: "a", Completed: true, Summary: &lumberjackv1.SyncSummary{WorktreesCreated: 1}},
	}}
	serveStub(t, stub)
	out, err := run(t, "", "sync-all")
	if err != nil {
		t.Fatalf("sync-all: %v", err)
	}
	if stub.lastSyncTarget != "" {
		t.Errorf("expected all-repos sync (empty target), got %q", stub.lastSyncTarget)
	}
	for _, want := range []string{"BRANCH", "feature/x", "#1", "checked out", "synced"} {
		if !strings.Contains(out, want) {
			t.Errorf("out %q missing %q", out, want)
		}
	}
}

func TestCmdSyncNamedRepository(t *testing.T) {
	stub := &stubService{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Message: "creating worktree for PR #1"},
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{WorktreesCreated: 1}},
	}}
	serveStub(t, stub)
	out, err := run(t, "", "sync", "--repository", "n")
	if err != nil {
		t.Fatalf("sync --repository n: %v", err)
	}
	if stub.lastSyncTarget != "n" {
		t.Errorf("expected sync target %q, got %q", "n", stub.lastSyncTarget)
	}
	if !strings.Contains(out, "creating worktree") || !strings.Contains(out, "synced") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdStatusNamedRepository(t *testing.T) {
	serveStub(t, &stubService{})
	out, err := run(t, "", "status", "--repository", "n")
	if err != nil {
		t.Fatalf("status --repository n: %v", err)
	}
	if !strings.Contains(out, "GitHub:") || !strings.Contains(out, "o/n") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdStatus(t *testing.T) {
	stub := &stubService{}
	serveStub(t, stub)
	out, err := run(t, "", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// Same detail view as `status --repository NAME`.
	if !strings.Contains(out, "GitHub:") || !strings.Contains(out, "o/n") {
		t.Errorf("out = %q", out)
	}
	// Resolved against the current directory's absolute path.
	if !filepath.IsAbs(stub.lastGetRef) {
		t.Errorf("status should query by the cwd path, got %q", stub.lastGetRef)
	}
}

func TestCmdWorktreesCurrentRepo(t *testing.T) {
	// worktrees with no --repository lists the current-directory repo's
	// worktrees — a new capability that did not exist under `repositories`.
	num := int64(7)
	serveStub(t, &stubService{worktrees: []*lumberjackv1.Worktree{
		{DirectoryPath: "/p/n-x", BranchName: "feature/x", GithubPrNumber: &num},
	}})
	out, err := run(t, "", "worktrees")
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	if !strings.Contains(out, "n-x") || !strings.Contains(out, "#7") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdStatusNamedRepositoryNotFound(t *testing.T) {
	serveStub(t, &stubService{getNotFound: true})
	_, err := run(t, "", "status", "--repository", "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestCmdWorktreesNamedRepository(t *testing.T) {
	num := int64(7)
	serveStub(t, &stubService{worktrees: []*lumberjackv1.Worktree{
		{
			DirectoryPath: "/p/n-x", BranchName: "feature/x", GithubPrNumber: &num,
			NeedsReconciliation: true, ReconciliationNote: "uncommitted changes",
		},
	}})
	out, err := run(t, "", "worktrees", "--repository", "n")
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	if !strings.Contains(out, "n-x") || !strings.Contains(out, "#7") || !strings.Contains(out, "⚠") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdWorktreesEmpty(t *testing.T) {
	serveStub(t, &stubService{})
	out, err := run(t, "", "worktrees", "--repository", "n")
	if err != nil {
		t.Fatalf("worktrees: %v", err)
	}
	if !strings.Contains(out, "No worktrees tracked") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdWorktreeDeleteClean(t *testing.T) {
	serveStub(t, &stubService{})
	out, err := run(t, "", "worktree", "delete", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdWorktreeDeleteConfirmYes(t *testing.T) {
	stub := &stubService{deleteConfirm: true}
	serveStub(t, stub)
	out, err := run(t, "y\n", "worktree", "delete", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "Warning") || !stub.deletedForced {
		t.Errorf("expected forced delete after confirm; out=%q forced=%v", out, stub.deletedForced)
	}
}

func TestCmdWorktreeDeleteConfirmNo(t *testing.T) {
	stub := &stubService{deleteConfirm: true}
	serveStub(t, stub)
	out, err := run(t, "n\n", "worktree", "delete", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "Aborted") || stub.deletedForced {
		t.Errorf("expected abort; out=%q forced=%v", out, stub.deletedForced)
	}
}

func TestCmdWorktreeDeleteForceFlag(t *testing.T) {
	stub := &stubService{deleteConfirm: true}
	serveStub(t, stub)
	// --force skips the prompt and forces immediately.
	_, err := run(t, "", "worktree", "delete", "feature/x", "--repository", "n", "--force")
	if err != nil {
		t.Fatalf("delete --force: %v", err)
	}
	if !stub.deletedForced {
		t.Error("expected forced delete with --force")
	}
}

func TestCmdRepositoriesIsUnknownCommand(t *testing.T) {
	serveStub(t, &stubService{})
	_, err := run(t, "", "repositories")
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("expected `repositories` to be an unknown command, got %v", err)
	}
}

func TestCmdSetLoginNamedRepositoryExplicit(t *testing.T) {
	stub := &stubService{}
	serveStub(t, stub)
	out, err := run(t, "", "set-login", "work", "--repository", "n")
	if err != nil {
		t.Fatalf("set-login: %v", err)
	}
	if stub.lastSetLogin != "work" {
		t.Errorf("SetLogin got %q, want work", stub.lastSetLogin)
	}
	if !strings.Contains(out, "Set login") || !strings.Contains(out, "work") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSetLoginRejectedByDaemon(t *testing.T) {
	stub := &stubService{loginErr: status.Error(codes.InvalidArgument, `"ghost" is not a gh account authenticated for github.com`)}
	serveStub(t, stub)
	_, err := run(t, "", "set-login", "ghost", "--repository", "n")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected rejection naming the login, got %v", err)
	}
}

func TestCmdSetLoginPicker(t *testing.T) {
	stub := &stubService{logins: []string{"personal", "work"}, loginCurrent: "personal"}
	serveStub(t, stub)

	// Substitute the interactive picker with a deterministic choice.
	prev := loginPicker
	var offered []string
	loginPicker = func(_ *cobra.Command, logins []string, _ string) (string, error) {
		offered = logins
		return "work", nil
	}
	t.Cleanup(func() { loginPicker = prev })

	out, err := run(t, "", "set-login", "--repository", "n")
	if err != nil {
		t.Fatalf("set-login (picker): %v", err)
	}
	if len(offered) != 2 || offered[0] != "personal" {
		t.Errorf("picker offered %v, want the daemon's login list", offered)
	}
	if stub.lastSetLogin != "work" {
		t.Errorf("SetLogin got %q, want the picked login", stub.lastSetLogin)
	}
	if !strings.Contains(out, "work") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSetLoginPickerNoAccounts(t *testing.T) {
	serveStub(t, &stubService{logins: nil})
	_, err := run(t, "", "set-login", "--repository", "n")
	if err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("expected a no-accounts error pointing at gh auth login, got %v", err)
	}
}

func TestCmdSyncCurrentRepo(t *testing.T) {
	stub := &stubService{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{WorktreesRemoved: 2}},
	}}
	serveStub(t, stub)
	out, err := run(t, "", "sync")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if stub.lastSyncTarget == "" || !filepath.IsAbs(stub.lastSyncTarget) {
		t.Errorf("expected sync target to be the cwd path, got %q", stub.lastSyncTarget)
	}
	if !strings.Contains(out, "-2") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdSyncErrorSummary(t *testing.T) {
	errMsg := "boom"
	serveStub(t, &stubService{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{
			Status: lumberjackv1.SyncStatus_SYNC_STATUS_ERROR, Error: &errMsg,
		}},
	}})
	out, err := run(t, "", "sync")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(out, "error") || !strings.Contains(out, "boom") {
		t.Errorf("out = %q", out)
	}
}
