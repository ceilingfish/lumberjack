package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements the generated LumberjackServiceServer. The gRPC layer is
// transport only: each method validates input, delegates to the domain Service
// (init/sync/delete) or the database, and maps domain errors onto gRPC status
// codes. Business logic stays in the domain layer so it is testable without a
// running server (AGENTS.md).
type Server struct {
	lumberjackv1.UnimplementedLumberjackServiceServer

	info Info
	db   *database.Client
	svc  *Service
}

// NewServer constructs the LumberjackService implementation. fx supplies its
// dependencies.
func NewServer(info Info, db *database.Client, svc *Service) *Server {
	return &Server{info: info, db: db, svc: svc}
}

// RegisterGRPC satisfies ServiceRegistrar.
func (s *Server) RegisterGRPC(g *grpc.Server) {
	lumberjackv1.RegisterLumberjackServiceServer(g, s)
}

// Health reports that the daemon is up.
func (s *Server) Health(context.Context, *lumberjackv1.HealthRequest) (*lumberjackv1.HealthResponse, error) {
	return &lumberjackv1.HealthResponse{
		Version:   s.info.Version,
		StartedAt: timestamppb.New(s.info.StartedAt),
	}, nil
}

// InitRepository registers the repo at req.LocalPath.
func (s *Server) InitRepository(ctx context.Context, req *lumberjackv1.InitRepositoryRequest) (*lumberjackv1.InitRepositoryResponse, error) {
	if req.GetLocalPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "local_path is required")
	}
	repo, adopted, err := s.svc.InitRepository(ctx, req.GetLocalPath())
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.InitRepositoryResponse{
		Repository:      toProtoRepository(repo),
		AdoptedBranches: adopted,
	}, nil
}

// ListRepositories returns every tracked repository.
func (s *Server) ListRepositories(ctx context.Context, _ *lumberjackv1.ListRepositoriesRequest) (*lumberjackv1.ListRepositoriesResponse, error) {
	repos, err := s.db.ListRepositories(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*lumberjackv1.Repository, len(repos))
	for i := range repos {
		out[i] = toProtoRepository(&repos[i])
	}
	return &lumberjackv1.ListRepositoriesResponse{Repositories: out}, nil
}

// GetRepository returns one repository resolved by name or path.
func (s *Server) GetRepository(ctx context.Context, req *lumberjackv1.GetRepositoryRequest) (*lumberjackv1.GetRepositoryResponse, error) {
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.GetRepositoryResponse{Repository: toProtoRepository(repo)}, nil
}

// SetLogin sets the gh account a repository operates under.
func (s *Server) SetLogin(ctx context.Context, req *lumberjackv1.SetLoginRequest) (*lumberjackv1.SetLoginResponse, error) {
	if req.GetRepository() == "" {
		return nil, status.Error(codes.InvalidArgument, "repository is required")
	}
	if req.GetLogin() == "" {
		return nil, status.Error(codes.InvalidArgument, "login is required")
	}
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	updated, err := s.svc.SetLogin(ctx, repo, req.GetLogin())
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.SetLoginResponse{Repository: toProtoRepository(updated)}, nil
}

// ListLogins returns the gh accounts authenticated for a repository's host.
func (s *Server) ListLogins(ctx context.Context, req *lumberjackv1.ListLoginsRequest) (*lumberjackv1.ListLoginsResponse, error) {
	if req.GetRepository() == "" {
		return nil, status.Error(codes.InvalidArgument, "repository is required")
	}
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	logins, err := s.svc.ListLogins(ctx, repo)
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.ListLoginsResponse{Logins: logins, Current: repo.Login}, nil
}

// ListWorktrees returns a repository's worktrees with live reconciliation.
func (s *Server) ListWorktrees(ctx context.Context, req *lumberjackv1.ListWorktreesRequest) (*lumberjackv1.ListWorktreesResponse, error) {
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	views, err := s.svc.WorktreeViews(ctx, repo)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*lumberjackv1.Worktree, len(views))
	for i, v := range views {
		out[i] = toProtoWorktree(v)
	}
	return &lumberjackv1.ListWorktreesResponse{Worktrees: out}, nil
}

// DeleteWorktree removes a worktree, requiring confirmation when work is at
// risk (see the domain Service.DeleteWorktree).
func (s *Server) DeleteWorktree(ctx context.Context, req *lumberjackv1.DeleteWorktreeRequest) (*lumberjackv1.DeleteWorktreeResponse, error) {
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	res, err := s.svc.DeleteWorktree(ctx, repo, req.GetWorktree(), req.GetForce())
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.DeleteWorktreeResponse{
		Deleted:              res.Deleted,
		RequiresConfirmation: res.RequiresConfirmation,
		CommitsAtRisk:        res.CommitsAtRisk,
		Message:              res.Message,
	}, nil
}

// DeleteRepository stops tracking a repository, removing it and its worktree
// rows from the database only (nothing on disk or on GitHub).
func (s *Server) DeleteRepository(ctx context.Context, req *lumberjackv1.DeleteRepositoryRequest) (*lumberjackv1.DeleteRepositoryResponse, error) {
	if req.GetRepository() == "" {
		return nil, status.Error(codes.InvalidArgument, "repository is required")
	}
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	removed, err := s.db.DeleteRepository(ctx, repo.ID)
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.DeleteRepositoryResponse{
		WorktreesRemoved: removed,
		Message:          fmt.Sprintf("stopped tracking %s (removed %d worktree(s) from the database)", displayName(repo), removed),
	}, nil
}

// Sync reconciles one repository (req.Repository set) or every tracked
// repository (empty), streaming progress as it runs.
func (s *Server) Sync(req *lumberjackv1.SyncRequest, stream grpc.ServerStreamingServer[lumberjackv1.SyncResponse]) error {
	ctx := stream.Context()

	var repos []*schema.Repository
	if ref := req.GetRepository(); ref != "" {
		repo, err := s.db.FindRepository(ctx, ref)
		if err != nil {
			return toStatus(err)
		}
		repos = append(repos, repo)
	} else {
		all, err := s.db.ListRepositories(ctx)
		if err != nil {
			return toStatus(err)
		}
		for i := range all {
			repos = append(repos, &all[i])
		}
	}

	for _, repo := range repos {
		if err := s.syncOne(stream, repo); err != nil {
			return err
		}
	}
	return nil
}

// syncOne runs one repository's sync, streaming progress then a final summary.
func (s *Server) syncOne(stream grpc.ServerStreamingServer[lumberjackv1.SyncResponse], repo *schema.Repository) error {
	name := displayName(repo)
	progress := func(msg string) {
		// A send error (client hung up) surfaces via the next Send in the
		// summary path; progress lines are best-effort.
		_ = stream.Send(&lumberjackv1.SyncResponse{Repository: name, Message: msg})
	}

	created, removed, syncErr := s.svc.SyncRepository(stream.Context(), repo, progress)

	summary := &lumberjackv1.SyncSummary{
		Status:           lumberjackv1.SyncStatus_SYNC_STATUS_OK,
		WorktreesCreated: int64(created),
		WorktreesRemoved: int64(removed),
	}
	if syncErr != nil {
		summary.Status = lumberjackv1.SyncStatus_SYNC_STATUS_ERROR
		msg := syncErr.Error()
		summary.Error = &msg
	}
	return stream.Send(&lumberjackv1.SyncResponse{
		Repository: name,
		Completed:  true,
		Summary:    summary,
	})
}

// toStatus maps domain errors onto gRPC status codes. Sentinel errors from the
// database package become NotFound/AlreadyExists; everything else is Internal.
func toStatus(err error) error {
	switch {
	case errors.Is(err, database.ErrRepositoryNotFound), errors.Is(err, database.ErrWorktreeNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, database.ErrRepositoryExists):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
