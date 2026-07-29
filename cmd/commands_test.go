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
	repos     []*lumberjackv1.Repository
	worktrees []*lumberjackv1.Worktree
	// addBranchCreated/addSetupError drive what AddWorktree reports; addErr, if
	// set, makes it fail. lastAddBranch records the branch it received.
	addBranchCreated bool
	addSetupError    string
	addErr           error
	lastAddBranch    string
	deleteConfirm    bool // first delete returns RequiresConfirmation
	deletedForced    bool // set when a forced delete arrived
	getNotFound      bool
	syncEvents       []*lumberjackv1.SyncResponse
	initAdopted      []*lumberjackv1.WorktreeChange // worktrees InitRepository reports as adopted
	lastInitPath     string
	lastSyncTarget   string
	lastGetRef       string
	// logins is what ListLogins reports; loginErr, if set, is returned by
	// SetLogin (e.g. an unauthenticated account). lastSetLogin records the login
	// SetLogin received.
	logins       []string
	loginCurrent string
	loginErr     error
	lastSetLogin string
	// setupConsentPending/setupConsentCommands drive GetSetupConsent;
	// setupConsentGiven records whether SetSetupConsent was called.
	setupConsentPending  bool
	setupConsentCommands []string
	setupConsentGiven    bool
	// tidyMoves is what Tidy reports; lastTidyTarget/lastTidyDryRun record the
	// request it received, so tests can assert on scoping and --dry-run.
	tidyMoves        []*lumberjackv1.TidyMove
	lastTidyTarget   string
	lastTidyWorktree string
	lastTidyDryRun   bool
	// tidyRequests records every Tidy request in order, since resolving locked
	// worktrees interactively takes two calls: a dry-run probe to find the locks,
	// then the real tidy carrying the user's answers.
	tidyRequests []*lumberjackv1.TidyRequest
}

func (s *stubService) Tidy(_ context.Context, req *lumberjackv1.TidyRequest) (*lumberjackv1.TidyResponse, error) {
	s.lastTidyTarget = req.GetRepository()
	s.lastTidyWorktree = req.GetWorktree()
	s.lastTidyDryRun = req.GetDryRun()
	s.tidyRequests = append(s.tidyRequests, req)
	return &lumberjackv1.TidyResponse{Moves: s.tidyMoves}, nil
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

// GetSetupConsent reports no pending consent unless a test sets
// setupConsentPending, in which case it also returns setupConsentCommands.
func (s *stubService) GetSetupConsent(context.Context, *lumberjackv1.GetSetupConsentRequest) (*lumberjackv1.GetSetupConsentResponse, error) {
	return &lumberjackv1.GetSetupConsentResponse{
		Pending:     s.setupConsentPending,
		RunCommands: s.setupConsentCommands,
	}, nil
}

// SetSetupConsent records that consent was given, for tests to assert on.
func (s *stubService) SetSetupConsent(_ context.Context, req *lumberjackv1.SetSetupConsentRequest) (*lumberjackv1.SetSetupConsentResponse, error) {
	s.setupConsentGiven = true
	return &lumberjackv1.SetSetupConsentResponse{Repository: &lumberjackv1.Repository{DirPrefix: req.GetRepository()}}, nil
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
		LastSyncStatus:      lumberjackv1.SyncStatus_SYNC_STATUS_OK,
		SetupConsentPending: s.setupConsentPending,
	}}, nil
}

func (s *stubService) ListWorktrees(context.Context, *lumberjackv1.ListWorktreesRequest) (*lumberjackv1.ListWorktreesResponse, error) {
	return &lumberjackv1.ListWorktreesResponse{Worktrees: s.worktrees}, nil
}

// AddWorktree echoes the branch it was asked for, recording it, and reports
// whatever addSetupError/addBranchCreated a test set.
func (s *stubService) AddWorktree(_ context.Context, req *lumberjackv1.AddWorktreeRequest) (*lumberjackv1.AddWorktreeResponse, error) {
	s.lastAddBranch = req.GetBranch()
	if s.addErr != nil {
		return nil, s.addErr
	}
	return &lumberjackv1.AddWorktreeResponse{
		DirectoryPath: "/p/n-x", Branch: req.GetBranch(),
		BranchCreated: s.addBranchCreated, SetupError: s.addSetupError,
	}, nil
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

func TestCmdInitPromptsForSetupConsentAndRecordsYes(t *testing.T) {
	stub := &stubService{
		setupConsentPending:  true,
		setupConsentCommands: []string{"go mod download"},
	}
	serveStub(t, stub)

	out, err := run(t, "y\n", "init", ".")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "go mod download") {
		t.Errorf("expected the run-command to be shown, got %q", out)
	}
	if !strings.Contains(out, "Consent recorded") {
		t.Errorf("expected consent-recorded confirmation, got %q", out)
	}
	if !stub.setupConsentGiven {
		t.Error("expected SetSetupConsent to have been called")
	}
}

func TestCmdInitPromptsForSetupConsentAndRespectsNo(t *testing.T) {
	stub := &stubService{
		setupConsentPending:  true,
		setupConsentCommands: []string{"rm -rf /"},
	}
	serveStub(t, stub)

	out, err := run(t, "n\n", "init", ".")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "Not consented") {
		t.Errorf("expected a not-consented message, got %q", out)
	}
	if stub.setupConsentGiven {
		t.Error("SetSetupConsent should not have been called after declining")
	}
}

func TestCmdInitNoPromptWhenConsentNotPending(t *testing.T) {
	stub := &stubService{}
	serveStub(t, stub)

	out, err := run(t, "", "init", ".")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.Contains(out, "run on every new worktree") {
		t.Errorf("did not expect a consent prompt, got %q", out)
	}
}

func TestCmdStatusDetailSurfacesPendingConsent(t *testing.T) {
	stub := &stubService{setupConsentPending: true, setupConsentCommands: []string{"echo hi"}}
	serveStub(t, stub)

	out, err := run(t, "n\n", "status", "--repository", "n")
	if err != nil {
		t.Fatalf("status --repository n: %v", err)
	}
	if !strings.Contains(out, "consent pending") {
		t.Errorf("expected detail to flag pending consent, got %q", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Errorf("expected the consent prompt to run, got %q", out)
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

func TestCmdWorktreeAdd(t *testing.T) {
	stub := &stubService{}
	serveStub(t, stub)
	out, err := run(t, "", "worktree", "add", "feature/#325080-accept-proofread-suggestions", "--repository", "n")
	if err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	if stub.lastAddBranch != "feature/#325080-accept-proofread-suggestions" {
		t.Errorf("branch sent = %q", stub.lastAddBranch)
	}
	if !strings.Contains(out, "Checked out") || !strings.Contains(out, "/p/n-x") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdWorktreeAddReportsCreatedBranch(t *testing.T) {
	serveStub(t, &stubService{addBranchCreated: true})
	out, err := run(t, "", "worktree", "add", "feature/new", "--repository", "n")
	if err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	if !strings.Contains(out, "Created branch") {
		t.Errorf("out = %q", out)
	}
}

// A setup-step failure is a warning, not an error: the worktree is created and
// tracked either way, so the command must still succeed.
func TestCmdWorktreeAddWarnsOnSetupFailure(t *testing.T) {
	serveStub(t, &stubService{addSetupError: "step 1 (copy-file) failed: no such file"})
	out, err := run(t, "", "worktree", "add", "feature/x", "--repository", "n")
	if err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	if !strings.Contains(out, "setup failed") || !strings.Contains(out, "copy-file") {
		t.Errorf("out = %q", out)
	}
}

func TestCmdWorktreeAddRequiresBranch(t *testing.T) {
	serveStub(t, &stubService{})
	if _, err := run(t, "", "worktree", "add", "--repository", "n"); err == nil {
		t.Error("expected an error when BRANCH is omitted")
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

func TestCmdTidyCurrentRepo(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{{
		Repository: "n", Branch: "feature/foo",
		From: "/p/n/.claude/worktrees/foo", To: "/p/n-foo", Moved: true,
	}}}
	serveStub(t, stub)

	out, err := run(t, "", "tidy")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	cwd, _ := os.Getwd()
	if stub.lastTidyTarget != cwd {
		t.Errorf("tidy target = %q, want the cwd %q", stub.lastTidyTarget, cwd)
	}
	if stub.lastTidyDryRun {
		t.Error("tidy sent dry_run without --dry-run")
	}
	for _, want := range []string{"feature/foo", "/p/n-foo", "moved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCmdTidyDryRun(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{{
		Repository: "n", Branch: "feature/foo", From: "/elsewhere/foo", To: "/p/n-foo",
	}}}
	serveStub(t, stub)

	out, err := run(t, "", "tidy", "--repository", "n", "--dry-run")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if !stub.lastTidyDryRun {
		t.Error("tidy did not send dry_run for --dry-run")
	}
	if !strings.Contains(out, "would move") {
		t.Errorf("output missing the dry-run verb:\n%s", out)
	}
}

func TestCmdTidyWorktreeFlag(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{{
		Repository: "n", Branch: "feature/foo", From: "/elsewhere/foo", To: "/p/n-foo", Moved: true,
	}}}
	serveStub(t, stub)

	out, err := run(t, "", "tidy", "--repository", "n", "--worktree", "feature/foo")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if stub.lastTidyWorktree != "feature/foo" {
		t.Errorf("tidy worktree = %q, want feature/foo", stub.lastTidyWorktree)
	}
}

func TestCmdTidyAllIsUnknownCommand(t *testing.T) {
	serveStub(t, &stubService{})

	if _, err := run(t, "", "tidy-all"); err == nil {
		t.Error("tidy-all should not be a command")
	}
}

func TestCmdTidyReportsSkippedMove(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{{
		Repository: "n", Branch: "feature/foo", From: "/elsewhere/foo", To: "/p/n-foo",
		Error: "destination already exists on disk",
	}}}
	serveStub(t, stub)

	out, err := run(t, "", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if !strings.Contains(out, "destination already exists on disk") {
		t.Errorf("output missing the skip reason:\n%s", out)
	}
}

func TestCmdTidyNothingToDo(t *testing.T) {
	serveStub(t, &stubService{})

	out, err := run(t, "", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if !strings.Contains(out, "idiomatic locations") {
		t.Errorf("output missing the all-clear message:\n%s", out)
	}
}

// lockedMove is a misplaced worktree that git has locked and that nothing else
// stands in the way of moving — the one shape tidy prompts about.
func lockedMove() *lumberjackv1.TidyMove {
	return &lumberjackv1.TidyMove{
		Repository: "n", Branch: "feature/foo", From: "/elsewhere/foo", To: "/p/n-foo",
		Locked: true, LockReason: "in use",
	}
}

// answerLockPrompt makes the terminal look interactive and answers every prompt
// with strategy, recording the worktrees it was asked about.
func answerLockPrompt(t *testing.T, strategy lumberjackv1.LockStrategy) *[]string {
	t.Helper()
	var asked []string
	prevInteractive, prevPrompter := interactiveTerminal, lockPrompter
	interactiveTerminal = func() bool { return true }
	lockPrompter = func(_ *cobra.Command, path, _ string) (lumberjackv1.LockStrategy, error) {
		asked = append(asked, path)
		return strategy, nil
	}
	t.Cleanup(func() { interactiveTerminal, lockPrompter = prevInteractive, prevPrompter })
	return &asked
}

// --lock-strategy answers up front, so the tidy is a single call and no prompt
// is shown even on a terminal.
func TestCmdTidyLockStrategyFlagSkipsThePrompt(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{lockedMove()}}
	serveStub(t, stub)
	asked := answerLockPrompt(t, lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP)

	out, err := run(t, "", "tidy", "--repository", "n", "--lock-strategy", "unlock")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if len(stub.tidyRequests) != 1 {
		t.Fatalf("tidy requests = %d, want 1: the flag answers without probing", len(stub.tidyRequests))
	}
	req := stub.tidyRequests[0]
	if req.GetLockStrategy() != lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK {
		t.Errorf("lock strategy = %v, want UNLOCK", req.GetLockStrategy())
	}
	if len(*asked) != 0 {
		t.Errorf("prompted about %v, want no prompt when the flag is given", *asked)
	}
}

func TestCmdTidyRejectsUnknownLockStrategy(t *testing.T) {
	serveStub(t, &stubService{})

	out, err := run(t, "", "tidy", "--repository", "n", "--lock-strategy", "maybe")
	if err == nil {
		t.Fatalf("tidy accepted an unknown --lock-strategy (%s)", out)
	}
	if !strings.Contains(err.Error(), "unlock") {
		t.Errorf("error should list the valid values, got %v", err)
	}
}

// On a terminal with no flag, tidy probes with a dry run, asks about each locked
// worktree, and sends the answers back keyed by the worktree's directory.
func TestCmdTidyPromptsForLockedWorktree(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{lockedMove()}}
	serveStub(t, stub)
	asked := answerLockPrompt(t, lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE)

	out, err := run(t, "", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if len(*asked) != 1 || (*asked)[0] != "/elsewhere/foo" {
		t.Errorf("prompted about %v, want the locked worktree's directory", *asked)
	}
	if len(stub.tidyRequests) != 2 {
		t.Fatalf("tidy requests = %d, want a dry-run probe then the real tidy", len(stub.tidyRequests))
	}
	if probe := stub.tidyRequests[0]; !probe.GetDryRun() {
		t.Error("the first call should be a dry run, so nothing moves before the user answers")
	}
	actual := stub.tidyRequests[1]
	if actual.GetDryRun() {
		t.Error("the second call should not be a dry run")
	}
	// Unasked-about locks stay put; the answered one carries its own decision.
	if actual.GetLockStrategy() != lumberjackv1.LockStrategy_LOCK_STRATEGY_SKIP {
		t.Errorf("fallback strategy = %v, want SKIP", actual.GetLockStrategy())
	}
	if len(actual.GetLockDecisions()) != 1 {
		t.Fatalf("lock decisions = %v, want one", actual.GetLockDecisions())
	}
	d := actual.GetLockDecisions()[0]
	if d.GetWorktreePath() != "/elsewhere/foo" ||
		d.GetStrategy() != lumberjackv1.LockStrategy_LOCK_STRATEGY_DELETE {
		t.Errorf("lock decision = %+v, want /elsewhere/foo deleted", d)
	}
}

// Aborting at the prompt stops the command before anything is moved.
func TestCmdTidyPromptAbortMovesNothing(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{lockedMove()}}
	serveStub(t, stub)
	answerLockPrompt(t, lumberjackv1.LockStrategy_LOCK_STRATEGY_ABORT)

	out, err := run(t, "", "tidy", "--repository", "n")
	if err == nil {
		t.Fatalf("tidy succeeded after an abort (%s)", out)
	}
	if len(stub.tidyRequests) != 1 || !stub.tidyRequests[0].GetDryRun() {
		t.Errorf("requests = %v, want only the dry-run probe", stub.tidyRequests)
	}
}

// A locked worktree that could not be moved anyway is not worth a question.
func TestCmdTidyDoesNotPromptForLockedWorktreeBlockedAnyway(t *testing.T) {
	blocked := lockedMove()
	blocked.Error = "destination already exists on disk"
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{blocked}}
	serveStub(t, stub)
	asked := answerLockPrompt(t, lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK)

	out, err := run(t, "", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted about %v, want no prompt for a worktree blocked by something else", *asked)
	}
}

// A dry run moves nothing, so there is nothing to consent to: it reports rather
// than asks, even on a terminal.
func TestCmdTidyDryRunDoesNotPrompt(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{lockedMove()}}
	serveStub(t, stub)
	asked := answerLockPrompt(t, lumberjackv1.LockStrategy_LOCK_STRATEGY_UNLOCK)

	out, err := run(t, "", "tidy", "--repository", "n", "--dry-run")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted about %v, want no prompt on a dry run", *asked)
	}
	if len(stub.tidyRequests) != 1 {
		t.Errorf("tidy requests = %d, want 1", len(stub.tidyRequests))
	}
}

// With no terminal to ask on and no flag, the strategy is left unspecified — the
// daemon then leaves locked worktrees, and their locks, alone.
func TestCmdTidyWithoutATerminalDoesNotProbe(t *testing.T) {
	stub := &stubService{tidyMoves: []*lumberjackv1.TidyMove{lockedMove()}}
	serveStub(t, stub)
	prev := interactiveTerminal
	interactiveTerminal = func() bool { return false }
	t.Cleanup(func() { interactiveTerminal = prev })

	out, err := run(t, "", "tidy", "--repository", "n")
	if err != nil {
		t.Fatalf("tidy: %v (%s)", err, out)
	}
	if len(stub.tidyRequests) != 1 {
		t.Fatalf("tidy requests = %d, want 1", len(stub.tidyRequests))
	}
	if got := stub.tidyRequests[0].GetLockStrategy(); got != lumberjackv1.LockStrategy_LOCK_STRATEGY_UNSPECIFIED {
		t.Errorf("lock strategy = %v, want it left unspecified", got)
	}
}
