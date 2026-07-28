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

// RegisterGRPC binds the LumberjackService onto the gRPC server.
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
	adoptedPB := make([]*lumberjackv1.WorktreeChange, len(adopted))
	for i, c := range adopted {
		adoptedPB[i] = toProtoChange(c)
	}
	pb := toProtoRepository(repo)
	s.decorateSetupConsent(ctx, pb, repo)
	return &lumberjackv1.InitRepositoryResponse{
		Repository: pb,
		Adopted:    adoptedPB,
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
		s.decorateSetupConsent(ctx, out[i], &repos[i])
	}
	return &lumberjackv1.ListRepositoriesResponse{Repositories: out}, nil
}

// GetRepository returns one repository resolved by name or path.
func (s *Server) GetRepository(ctx context.Context, req *lumberjackv1.GetRepositoryRequest) (*lumberjackv1.GetRepositoryResponse, error) {
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	pb := toProtoRepository(repo)
	s.decorateSetupConsent(ctx, pb, repo)
	return &lumberjackv1.GetRepositoryResponse{Repository: pb}, nil
}

// decorateSetupConsent sets pb.SetupConsentPending from repo's live
// setup-consent status. A failure reading the trusted config (e.g. the
// daemon has never fetched this repo yet) is swallowed — it must not break
// an otherwise-successful list/get, and the CLI's dedicated GetSetupConsent
// call surfaces the same failure explicitly when it matters.
func (s *Server) decorateSetupConsent(ctx context.Context, pb *lumberjackv1.Repository, repo *schema.Repository) {
	consent, err := s.svc.GetSetupConsent(ctx, repo)
	if err != nil {
		return
	}
	pb.SetupConsentPending = consent.Pending
}

// GetSetupConsent reports whether a repository's trusted run-command setup
// steps are pending the local user's consent.
func (s *Server) GetSetupConsent(ctx context.Context, req *lumberjackv1.GetSetupConsentRequest) (*lumberjackv1.GetSetupConsentResponse, error) {
	if req.GetRepository() == "" {
		return nil, status.Error(codes.InvalidArgument, "repository is required")
	}
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	consent, err := s.svc.GetSetupConsent(ctx, repo)
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.GetSetupConsentResponse{
		Pending:     consent.Pending,
		RunCommands: consent.Commands,
	}, nil
}

// SetSetupConsent records the local user's consent to a repository's current
// trusted run-command setup steps.
func (s *Server) SetSetupConsent(ctx context.Context, req *lumberjackv1.SetSetupConsentRequest) (*lumberjackv1.SetSetupConsentResponse, error) {
	if req.GetRepository() == "" {
		return nil, status.Error(codes.InvalidArgument, "repository is required")
	}
	repo, err := s.db.FindRepository(ctx, req.GetRepository())
	if err != nil {
		return nil, toStatus(err)
	}
	updated, err := s.svc.SetSetupConsent(ctx, repo)
	if err != nil {
		return nil, toStatus(err)
	}
	return &lumberjackv1.SetSetupConsentResponse{Repository: toProtoRepository(updated)}, nil
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
	repos, err := s.resolveScope(stream.Context(), req.GetRepository())
	if err != nil {
		return err
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
	progress := func(c WorktreeChange) {
		// A send error (client hung up) surfaces via the next Send in the
		// summary path; progress events are best-effort.
		_ = stream.Send(&lumberjackv1.SyncResponse{Repository: name, Change: toProtoChange(c)})
	}

	created, removed, syncErr := s.svc.SyncRepository(stream.Context(), repo, progress)

	return stream.Send(&lumberjackv1.SyncResponse{
		Repository: name,
		Completed:  true,
		Summary:    toProtoSyncSummary(created, removed, syncErr),
	})
}

// Tidy moves misplaced worktrees back to their idiomatic locations, for one
// repository (req.Repository set) or every tracked one (empty).
func (s *Server) Tidy(ctx context.Context, req *lumberjackv1.TidyRequest) (*lumberjackv1.TidyResponse, error) {
	// A worktree reference is only unambiguous within one repository. Rejected on
	// the request, not on how many repositories happen to be tracked, so the same
	// command is not valid on a one-repo machine and invalid on the next.
	if req.GetWorktree() != "" && req.GetRepository() == "" {
		return nil, status.Error(codes.InvalidArgument, "worktree requires a repository")
	}
	repos, err := s.resolveScope(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}
	resp := &lumberjackv1.TidyResponse{}
	for _, repo := range repos {
		moves, err := s.svc.TidyRepository(ctx, repo, req.GetWorktree(), req.GetDryRun())
		if err != nil {
			return nil, toStatus(err)
		}
		name := displayName(repo)
		for _, m := range moves {
			resp.Moves = append(resp.Moves, &lumberjackv1.TidyMove{
				Repository: name, Branch: m.Branch,
				From: m.From, To: m.To, Moved: m.Moved, Error: m.Err,
			})
		}
	}
	return resp, nil
}

// resolveScope turns a request's repository field into the repositories to
// operate on: the one it names, or every tracked repository when empty. It is
// the shared scoping rule for the RPCs that accept either (Sync, Tidy).
func (s *Server) resolveScope(ctx context.Context, ref string) ([]*schema.Repository, error) {
	if ref != "" {
		repo, err := s.db.FindRepository(ctx, ref)
		if err != nil {
			return nil, toStatus(err)
		}
		return []*schema.Repository{repo}, nil
	}
	all, err := s.db.ListRepositories(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	repos := make([]*schema.Repository, len(all))
	for i := range all {
		repos[i] = &all[i]
	}
	return repos, nil
}

// Watch opens a long-lived stream of worktree/repository change events. It
// first sends one SNAPSHOT event per tracked repository (current state, so a
// client can render immediately), then forwards live deltas from the
// Service's Broadcaster until the client disconnects or the subscriber falls
// too far behind and is dropped.
func (s *Server) Watch(_ *lumberjackv1.WatchRequest, stream grpc.ServerStreamingServer[lumberjackv1.WatchResponse]) error {
	ctx := stream.Context()

	// Subscribe before reading the snapshot so no event published in between is
	// missed — worst case a delta is sent twice (once implicitly via the
	// snapshot, once as a delta), never dropped.
	events, unsubscribe := s.svc.Subscribe()
	defer unsubscribe()

	repos, err := s.db.ListRepositories(ctx)
	if err != nil {
		return toStatus(err)
	}
	for i := range repos {
		repo := &repos[i]
		views, err := s.svc.WorktreeViews(ctx, repo)
		if err != nil {
			return toStatus(err)
		}
		worktrees := make([]*lumberjackv1.Worktree, len(views))
		for j, v := range views {
			worktrees[j] = toProtoWorktree(v)
		}
		if err := stream.Send(&lumberjackv1.WatchResponse{
			Type:       lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SNAPSHOT,
			Repository: toProtoRepository(repo),
			Worktrees:  worktrees,
		}); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return status.Error(codes.ResourceExhausted, "watch subscriber fell behind and was disconnected")
			}
			if err := stream.Send(toProtoWatchResponse(ev)); err != nil {
				return err
			}
		}
	}
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
