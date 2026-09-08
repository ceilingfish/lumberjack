package cli

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ceilingfish/lumberjack/pkg/client"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc"
)

// stub is the slice of the daemon service these tests drive. Only the RPCs the
// cli package itself calls are implemented; everything else inherits the
// generated Unimplemented behaviour.
type stub struct {
	lumberjackv1.UnimplementedLumberjackServiceServer
	repos      []*lumberjackv1.Repository
	logins     []string
	syncEvents []*lumberjackv1.SyncResponse
	err        error
	consentErr error
	sendErr    bool
}

func (s *stub) ListRepositories(context.Context, *lumberjackv1.ListRepositoriesRequest) (*lumberjackv1.ListRepositoriesResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &lumberjackv1.ListRepositoriesResponse{Repositories: s.repos}, nil
}

func (s *stub) ListLogins(context.Context, *lumberjackv1.ListLoginsRequest) (*lumberjackv1.ListLoginsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &lumberjackv1.ListLoginsResponse{Logins: s.logins}, nil
}

func (s *stub) SetSetupConsent(_ context.Context, req *lumberjackv1.SetSetupConsentRequest) (*lumberjackv1.SetSetupConsentResponse, error) {
	if s.consentErr != nil {
		return nil, s.consentErr
	}
	return &lumberjackv1.SetSetupConsentResponse{
		Repository: &lumberjackv1.Repository{DirPrefix: req.GetRepository()},
		Accepted:   !s.sendErr,
	}, nil
}

func (s *stub) Sync(_ *lumberjackv1.SyncRequest, stream grpc.ServerStreamingServer[lumberjackv1.SyncResponse]) error {
	if s.err != nil {
		return s.err
	}
	for _, e := range s.syncEvents {
		if err := stream.Send(e); err != nil {
			return err
		}
	}
	return nil
}

// serve starts impl on a throwaway unix socket and points the client at it for
// the duration of the test.
func serve(t *testing.T, impl lumberjackv1.LumberjackServiceServer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ljcli")
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

// noDaemon leaves the client with no socket to dial and no home directory to
// derive a default one from, so Dial itself fails.
func noDaemon(t *testing.T) {
	t.Helper()
	t.Setenv("LUMBERJACK_SOCKET_PATH", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
}

// gitRepo makes an empty git repository and moves the test into it, for the
// helpers that resolve a worktree from the working directory.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Chdir(dir)
	return dir
}

func dial(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.Dial()
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
