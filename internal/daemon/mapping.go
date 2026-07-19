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
		Login:             r.Login,
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

// toProtoChange maps a domain per-branch worktree change onto its wire message.
func toProtoChange(c WorktreeChange) *lumberjackv1.WorktreeChange {
	return &lumberjackv1.WorktreeChange{
		Branch:        c.Branch,
		PrNumber:      c.PRNumber,
		Action:        toProtoAction(c.Action),
		Detail:        c.Detail,
		DirectoryPath: c.DirectoryPath,
		LastSyncedAt:  toProtoTimestamp(c.LastSyncedAt),
	}
}

// toProtoWatchResponse maps a domain Event onto its wire message for the Watch
// stream.
func toProtoWatchResponse(ev Event) *lumberjackv1.WatchResponse {
	pb := &lumberjackv1.WatchResponse{Repository: toProtoRepository(ev.Repository)}
	switch ev.Type {
	case EventWorktreeChanged:
		pb.Type = lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_WORKTREE_CHANGED
		if ev.Change != nil {
			pb.Change = toProtoChange(*ev.Change)
		}
	case EventSyncStarted:
		pb.Type = lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_STARTED
	case EventSyncFinished:
		pb.Type = lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_FINISHED
		pb.Summary = toProtoSyncSummary(ev.SyncCreated, ev.SyncRemoved, ev.SyncErr)
	}
	return pb
}

// toProtoSyncSummary builds a SyncSummary from a sync's outcome, shared by the
// Sync RPC's completion event and the Watch stream's SYNC_FINISHED event.
func toProtoSyncSummary(created, removed int, syncErr error) *lumberjackv1.SyncSummary {
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
	return summary
}

// toProtoAction maps a domain WorktreeAction onto the wire enum.
func toProtoAction(a WorktreeAction) lumberjackv1.WorktreeAction {
	switch a {
	case ActionCheckedOut:
		return lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT
	case ActionAdopted:
		return lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED
	case ActionUpdated:
		return lumberjackv1.WorktreeAction_WORKTREE_ACTION_UPDATED
	case ActionDeleted:
		return lumberjackv1.WorktreeAction_WORKTREE_ACTION_DELETED
	case ActionRetained:
		return lumberjackv1.WorktreeAction_WORKTREE_ACTION_RETAINED
	default:
		return lumberjackv1.WorktreeAction_WORKTREE_ACTION_UNSPECIFIED
	}
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
