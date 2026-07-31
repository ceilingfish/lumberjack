package cmd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc"
)

var errWrite = errors.New("write failed")

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errWrite }

func runCmd(t *testing.T, in string, out, errOut io.Writer, args ...string) error {
	t.Helper()
	root := newRootCmd()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetIn(strings.NewReader(in))
	root.SetArgs(args)
	return root.Execute()
}

func serveService(t *testing.T, impl lumberjackv1.LumberjackServiceServer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ljcov")
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
	lumberjackv1.RegisterLumberjackServiceServer(srv, impl)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)

	t.Setenv("LUMBERJACK_SOCKET_PATH", path)
}

func noDaemon(t *testing.T) {
	t.Helper()
	t.Setenv("LUMBERJACK_SOCKET_PATH", "")
	t.Setenv("HOME", "")
}

type coverStub struct {
	lumberjackv1.UnimplementedLumberjackServiceServer
	repos         []*lumberjackv1.Repository
	repo          *lumberjackv1.Repository
	worktrees     []*lumberjackv1.Worktree
	logins        []string
	deleteRepo    *lumberjackv1.DeleteRepositoryResponse
	deleteWT      []*lumberjackv1.DeleteWorktreeResponse
	addWT         *lumberjackv1.AddWorktreeResponse
	tidyMoves     []*lumberjackv1.TidyMove
	syncEvents    []*lumberjackv1.SyncResponse
	adopted       []*lumberjackv1.WorktreeChange
	consent       *lumberjackv1.GetSetupConsentResponse
	err           error
	consentErr    error
	setConsentErr error
	block         time.Duration

	deleteWTCalls int
}

func (s *coverStub) wait(ctx context.Context) error {
	if s.block > 0 {
		select {
		case <-time.After(s.block):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}

func (s *coverStub) ListRepositories(ctx context.Context, _ *lumberjackv1.ListRepositoriesRequest) (*lumberjackv1.ListRepositoriesResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.ListRepositoriesResponse{Repositories: s.repos}, nil
}

func (s *coverStub) GetRepository(ctx context.Context, _ *lumberjackv1.GetRepositoryRequest) (*lumberjackv1.GetRepositoryResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.GetRepositoryResponse{Repository: s.repo}, nil
}

func (s *coverStub) ListWorktrees(ctx context.Context, _ *lumberjackv1.ListWorktreesRequest) (*lumberjackv1.ListWorktreesResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.ListWorktreesResponse{Worktrees: s.worktrees}, nil
}

func (s *coverStub) ListLogins(ctx context.Context, _ *lumberjackv1.ListLoginsRequest) (*lumberjackv1.ListLoginsResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.ListLoginsResponse{Logins: s.logins}, nil
}

func (s *coverStub) SetLogin(ctx context.Context, req *lumberjackv1.SetLoginRequest) (*lumberjackv1.SetLoginResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.SetLoginResponse{Repository: &lumberjackv1.Repository{
		DirPrefix: req.GetRepository(), Login: req.GetLogin(),
	}}, nil
}

func (s *coverStub) DeleteRepository(ctx context.Context, _ *lumberjackv1.DeleteRepositoryRequest) (*lumberjackv1.DeleteRepositoryResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return s.deleteRepo, nil
}

func (s *coverStub) DeleteWorktree(ctx context.Context, _ *lumberjackv1.DeleteWorktreeRequest) (*lumberjackv1.DeleteWorktreeResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	i := s.deleteWTCalls
	s.deleteWTCalls++
	if i >= len(s.deleteWT) {
		return nil, errors.New("unexpected DeleteWorktree call")
	}
	return s.deleteWT[i], nil
}

func (s *coverStub) AddWorktree(ctx context.Context, _ *lumberjackv1.AddWorktreeRequest) (*lumberjackv1.AddWorktreeResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return s.addWT, nil
}

func (s *coverStub) Tidy(ctx context.Context, _ *lumberjackv1.TidyRequest) (*lumberjackv1.TidyResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.TidyResponse{Moves: s.tidyMoves}, nil
}

func (s *coverStub) InitRepository(ctx context.Context, req *lumberjackv1.InitRepositoryRequest) (*lumberjackv1.InitRepositoryResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &lumberjackv1.InitRepositoryResponse{
		Repository: &lumberjackv1.Repository{
			LocalPath: req.GetLocalPath(), GithubOwner: "o", GithubName: "n", DirPrefix: "n",
		},
		Adopted: s.adopted,
	}, nil
}

func (s *coverStub) GetSetupConsent(ctx context.Context, _ *lumberjackv1.GetSetupConsentRequest) (*lumberjackv1.GetSetupConsentResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	if s.consentErr != nil {
		return nil, s.consentErr
	}
	if s.consent == nil {
		return &lumberjackv1.GetSetupConsentResponse{}, nil
	}
	return s.consent, nil
}

func (s *coverStub) SetSetupConsent(ctx context.Context, req *lumberjackv1.SetSetupConsentRequest) (*lumberjackv1.SetSetupConsentResponse, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	if s.setConsentErr != nil {
		return nil, s.setConsentErr
	}
	return &lumberjackv1.SetSetupConsentResponse{Repository: &lumberjackv1.Repository{DirPrefix: req.GetRepository()}}, nil
}

func (s *coverStub) Sync(_ *lumberjackv1.SyncRequest, stream grpc.ServerStreamingServer[lumberjackv1.SyncResponse]) error {
	if err := s.wait(stream.Context()); err != nil {
		return err
	}
	for _, e := range s.syncEvents {
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return nil
}
