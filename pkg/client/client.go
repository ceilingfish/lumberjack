// Package client is the public, reusable client for talking to a Lumberjack
// daemon. It dials the daemon's local Unix socket, wraps the buf-generated
// gRPC stubs (in the lumberjack/v1 subpackage), and maps gRPC status codes
// onto idiomatic Go errors. Anything that wants to drive a Lumberjack daemon —
// the CLI in cmd/, or third-party tooling — uses this package rather than the
// raw stubs (see AGENTS.md, "pkg/client is public on purpose").
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// EnvSocketPath overrides the daemon socket location (see docs/prd.md).
const EnvSocketPath = "LUMBERJACK_SOCKET_PATH"

// Sentinel errors the CLI can branch on. They wrap the underlying gRPC error,
// so errors.Is matches while the original detail stays available via %w.
var (
	// ErrDaemonNotRunning indicates the daemon could not be reached — almost
	// always because it is not running. The CLI turns this into actionable
	// advice rather than a raw gRPC "Unavailable".
	ErrDaemonNotRunning = errors.New("lumberjack daemon is not running")
	// ErrNotFound maps codes.NotFound (unknown repository or worktree).
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists maps codes.AlreadyExists (repository already tracked).
	ErrAlreadyExists = errors.New("already exists")
)

// Client is a connected Lumberjack daemon client. Close it when done.
type Client struct {
	conn *grpc.ClientConn
	svc  lumberjackv1.LumberjackServiceClient
}

// DefaultSocketPath resolves the daemon socket: LUMBERJACK_SOCKET_PATH, else
// ~/.lumberjack/daemon.sock. It mirrors the daemon's own resolution so client
// and server agree without sharing internal code.
func DefaultSocketPath() (string, error) {
	if p := os.Getenv(EnvSocketPath); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir for default socket: %w", err)
	}
	return filepath.Join(home, ".lumberjack", "daemon.sock"), nil
}

// Dial connects to the daemon at the default socket path. The connection is
// lazy: a daemon-down condition surfaces on the first RPC as
// ErrDaemonNotRunning, not here.
func Dial() (*Client, error) {
	path, err := DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	return DialSocket(path)
}

// DialSocket connects to the daemon at an explicit socket path.
func DialSocket(path string) (*Client, error) {
	conn, err := grpc.NewClient("unix://"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dialing daemon socket %s: %w", path, err)
	}
	return &Client{conn: conn, svc: lumberjackv1.NewLumberjackServiceClient(conn)}, nil
}

// Close releases the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// Health returns the daemon's version/start time, or ErrDaemonNotRunning.
func (c *Client) Health(ctx context.Context) (*lumberjackv1.HealthResponse, error) {
	resp, err := c.svc.Health(ctx, &lumberjackv1.HealthRequest{})
	return resp, mapError(err)
}

// InitRepository registers the repository at localPath, returning the stored
// repository and the per-branch changes for any already-checked-out worktrees
// adopted into tracking during registration.
func (c *Client) InitRepository(ctx context.Context, localPath string) (*lumberjackv1.Repository, []*lumberjackv1.WorktreeChange, error) {
	resp, err := c.svc.InitRepository(ctx, &lumberjackv1.InitRepositoryRequest{LocalPath: localPath})
	if err != nil {
		return nil, nil, mapError(err)
	}
	return resp.GetRepository(), resp.GetAdopted(), nil
}

// ListRepositories returns every tracked repository.
func (c *Client) ListRepositories(ctx context.Context) ([]*lumberjackv1.Repository, error) {
	resp, err := c.svc.ListRepositories(ctx, &lumberjackv1.ListRepositoriesRequest{})
	if err != nil {
		return nil, mapError(err)
	}
	return resp.GetRepositories(), nil
}

// GetRepository resolves one repository by name or path.
func (c *Client) GetRepository(ctx context.Context, ref string) (*lumberjackv1.Repository, error) {
	resp, err := c.svc.GetRepository(ctx, &lumberjackv1.GetRepositoryRequest{Repository: ref})
	if err != nil {
		return nil, mapError(err)
	}
	return resp.GetRepository(), nil
}

// SetLogin sets the gh account the repository resolved by ref operates under,
// returning the updated repository.
func (c *Client) SetLogin(ctx context.Context, ref, login string) (*lumberjackv1.Repository, error) {
	resp, err := c.svc.SetLogin(ctx, &lumberjackv1.SetLoginRequest{Repository: ref, Login: login})
	if err != nil {
		return nil, mapError(err)
	}
	return resp.GetRepository(), nil
}

// ListLogins returns the gh accounts authenticated for the host of the
// repository resolved by ref, plus the login it currently operates under.
func (c *Client) ListLogins(ctx context.Context, ref string) (logins []string, current string, err error) {
	resp, err := c.svc.ListLogins(ctx, &lumberjackv1.ListLoginsRequest{Repository: ref})
	if err != nil {
		return nil, "", mapError(err)
	}
	return resp.GetLogins(), resp.GetCurrent(), nil
}

// GetSetupConsent reports whether the repository resolved by ref has
// `.lumberjack.yml` run-command setup steps pending the local user's consent,
// plus the command strings for a consent prompt.
func (c *Client) GetSetupConsent(ctx context.Context, ref string) (pending bool, commands []string, err error) {
	resp, err := c.svc.GetSetupConsent(ctx, &lumberjackv1.GetSetupConsentRequest{Repository: ref})
	if err != nil {
		return false, nil, mapError(err)
	}
	return resp.GetPending(), resp.GetRunCommands(), nil
}

// SetSetupConsent records the local user's consent to run the current
// trusted `.lumberjack.yml` run-command steps for the repository resolved by
// ref, returning the updated repository.
func (c *Client) SetSetupConsent(ctx context.Context, ref string) (*lumberjackv1.Repository, error) {
	resp, err := c.svc.SetSetupConsent(ctx, &lumberjackv1.SetSetupConsentRequest{Repository: ref})
	if err != nil {
		return nil, mapError(err)
	}
	return resp.GetRepository(), nil
}

// ListWorktrees returns a repository's worktrees with live reconciliation.
func (c *Client) ListWorktrees(ctx context.Context, ref string) ([]*lumberjackv1.Worktree, error) {
	resp, err := c.svc.ListWorktrees(ctx, &lumberjackv1.ListWorktreesRequest{Repository: ref})
	if err != nil {
		return nil, mapError(err)
	}
	return resp.GetWorktrees(), nil
}

// DeleteWorktree removes a worktree. When force is false and the worktree
// holds work at risk, the response has RequiresConfirmation=true and nothing
// is deleted (the CLI then re-calls with force=true).
func (c *Client) DeleteWorktree(ctx context.Context, ref, worktree string, force bool) (*lumberjackv1.DeleteWorktreeResponse, error) {
	resp, err := c.svc.DeleteWorktree(ctx, &lumberjackv1.DeleteWorktreeRequest{
		Repository: ref, Worktree: worktree, Force: force,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return resp, nil
}

// DeleteRepository stops tracking the repository resolved by ref, removing it
// and its worktree rows from the database only (nothing on disk or on GitHub).
// It returns the number of worktree rows removed.
func (c *Client) DeleteRepository(ctx context.Context, ref string) (*lumberjackv1.DeleteRepositoryResponse, error) {
	resp, err := c.svc.DeleteRepository(ctx, &lumberjackv1.DeleteRepositoryRequest{Repository: ref})
	if err != nil {
		return nil, mapError(err)
	}
	return resp, nil
}

// Sync reconciles one repository (ref set) or all of them (ref empty),
// invoking onEvent for each streamed progress update until the stream ends.
func (c *Client) Sync(ctx context.Context, ref string, onEvent func(*lumberjackv1.SyncResponse) error) error {
	// Cancel on any early return so an abandoned stream (callback error, or a
	// caller that stops reading) releases the underlying gRPC stream instead of
	// leaking it until the parent context is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.svc.Sync(ctx, &lumberjackv1.SyncRequest{Repository: ref})
	if err != nil {
		return mapError(err)
	}
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return mapError(err)
		}
		if cbErr := onEvent(resp); cbErr != nil {
			return cbErr
		}
	}
}

// Watch opens a long-lived stream of worktree/repository change events,
// invoking onEvent for each one until the stream ends. The daemon sends one
// SNAPSHOT event per tracked repository right away, then live deltas
// (worktree created/adopted/updated/deleted, repository sync
// started/finished) as they happen. Watch blocks until ctx is cancelled, the
// daemon disconnects the subscriber (it fell too far behind), or onEvent
// returns an error.
func (c *Client) Watch(ctx context.Context, onEvent func(*lumberjackv1.WatchResponse) error) error {
	// Cancel on any early return so an abandoned stream (callback error, or a
	// caller that stops reading) releases the underlying gRPC stream instead of
	// leaking it until the parent context is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.svc.Watch(ctx, &lumberjackv1.WatchRequest{})
	if err != nil {
		return mapError(err)
	}
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return mapError(err)
		}
		if cbErr := onEvent(resp); cbErr != nil {
			return cbErr
		}
	}
}

// mapError turns a gRPC status into a wrapped sentinel error so the CLI can
// branch with errors.Is while keeping the daemon's message.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.Unavailable:
		return fmt.Errorf("%w (start it with `lumberjack daemon`)", ErrDaemonNotRunning)
	case codes.NotFound:
		return fmt.Errorf("%s: %w", st.Message(), ErrNotFound)
	case codes.AlreadyExists:
		return fmt.Errorf("%s: %w", st.Message(), ErrAlreadyExists)
	default:
		return errors.New(st.Message())
	}
}
