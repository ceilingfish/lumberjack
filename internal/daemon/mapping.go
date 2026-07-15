package daemon

import (
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// displayName is the human-facing name of a repository, used both as the label
// in Sync progress events and as a value GetRepository can resolve.
func displayName(repo *schema.Repository) string { return repo.DirPrefix }

// toProtoRepository maps a stored repository row onto its wire message.
func toProtoRepository(r *schema.Repository) *lumberjackv1.Repository {
	pb := &lumberjackv1.Repository{
		Id:                r.ID,
		LocalPath:         r.LocalPath,
		WorktreeParentDir: r.WorktreeParentDir,
		DirPrefix:         r.DirPrefix,
		GithubOwner:       r.GithubOwner,
		GithubName:        r.GithubName,
		DefaultRemote:     r.DefaultRemote,
		Host:              r.Host,
		LastSyncStatus:    toProtoSyncStatus(r.LastSyncStatus),
		LastSyncError:     r.LastSyncError,
		CreatedAt:         timestamppb.New(r.CreatedAt),
	}
	pb.LastSyncedAt = toProtoTimestamp(r.LastSyncedAt)
	return pb
}

// toProtoWorktree maps a live worktree view onto its wire message, carrying the
// derived reconciliation fields.
func toProtoWorktree(v WorktreeView) *lumberjackv1.Worktree {
	wt := v.Worktree
	pb := &lumberjackv1.Worktree{
		BranchName:          wt.BranchName,
		DirectoryPath:       wt.DirectoryPath,
		GithubPrNumber:      wt.GithubPRNumber,
		CreatedBy:           toProtoCreatedBy(wt.CreatedBy),
		NeedsReconciliation: v.Status.NeedsReconciliation,
		Dirty:               v.Status.Dirty,
		Orphaned:            v.Status.Orphaned,
		LocalOnlyCommits:    v.Status.LocalOnlyCommits,
		ReconciliationNote:  v.Status.Note,
	}
	pb.LastSyncedAt = toProtoTimestamp(wt.LastSyncedAt)
	return pb
}

// toProtoTimestamp maps an optional time onto an optional protobuf timestamp.
func toProtoTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// toProtoSyncStatus maps the stored status string onto the enum.
func toProtoSyncStatus(s *string) lumberjackv1.SyncStatus {
	if s == nil {
		return lumberjackv1.SyncStatus_SYNC_STATUS_UNSPECIFIED
	}
	switch *s {
	case schema.SyncStatusOK:
		return lumberjackv1.SyncStatus_SYNC_STATUS_OK
	case schema.SyncStatusError:
		return lumberjackv1.SyncStatus_SYNC_STATUS_ERROR
	default:
		return lumberjackv1.SyncStatus_SYNC_STATUS_UNSPECIFIED
	}
}

// toProtoCreatedBy maps the stored created_by string onto the enum.
func toProtoCreatedBy(s string) lumberjackv1.CreatedBy {
	switch s {
	case schema.CreatedByLumberjack:
		return lumberjackv1.CreatedBy_CREATED_BY_LUMBERJACK
	case schema.CreatedByPreexisting:
		return lumberjackv1.CreatedBy_CREATED_BY_PREEXISTING
	default:
		return lumberjackv1.CreatedBy_CREATED_BY_UNSPECIFIED
	}
}
