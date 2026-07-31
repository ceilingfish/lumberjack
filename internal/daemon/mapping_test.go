package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func TestToProtoAction(t *testing.T) {
	cases := map[WorktreeAction]lumberjackv1.WorktreeAction{
		ActionCheckedOut:                lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT,
		ActionAdopted:                   lumberjackv1.WorktreeAction_WORKTREE_ACTION_ADOPTED,
		ActionUpdated:                   lumberjackv1.WorktreeAction_WORKTREE_ACTION_UPDATED,
		ActionDeleted:                   lumberjackv1.WorktreeAction_WORKTREE_ACTION_DELETED,
		ActionRetained:                  lumberjackv1.WorktreeAction_WORKTREE_ACTION_RETAINED,
		WorktreeAction("something new"): lumberjackv1.WorktreeAction_WORKTREE_ACTION_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := toProtoAction(in); got != want {
			t.Errorf("toProtoAction(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestToProtoSyncStatus(t *testing.T) {
	ok, syncErr, other := schema.SyncStatusOK, schema.SyncStatusError, "who knows"
	cases := []struct {
		in   *string
		want lumberjackv1.SyncStatus
	}{
		{nil, lumberjackv1.SyncStatus_SYNC_STATUS_UNSPECIFIED},
		{&ok, lumberjackv1.SyncStatus_SYNC_STATUS_OK},
		{&syncErr, lumberjackv1.SyncStatus_SYNC_STATUS_ERROR},
		{&other, lumberjackv1.SyncStatus_SYNC_STATUS_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := toProtoSyncStatus(c.in); got != c.want {
			t.Errorf("toProtoSyncStatus(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToProtoSyncSummaryCarriesTheFailure(t *testing.T) {
	ok := toProtoSyncSummary(2, 1, nil)
	if ok.GetStatus() != lumberjackv1.SyncStatus_SYNC_STATUS_OK || ok.Error != nil {
		t.Errorf("clean summary = %+v", ok)
	}
	if ok.GetWorktreesCreated() != 2 || ok.GetWorktreesRemoved() != 1 {
		t.Errorf("counts not carried: %+v", ok)
	}

	failed := toProtoSyncSummary(0, 0, errors.New("boom"))
	if failed.GetStatus() != lumberjackv1.SyncStatus_SYNC_STATUS_ERROR {
		t.Errorf("failed status = %v", failed.GetStatus())
	}
	if failed.GetError() != "boom" {
		t.Errorf("failed error = %q, want %q", failed.GetError(), "boom")
	}
}

func TestToProtoTimestampPassesNilThrough(t *testing.T) {
	if got := toProtoTimestamp(nil); got != nil {
		t.Errorf("toProtoTimestamp(nil) = %v, want nil", got)
	}
	at := time.Unix(1700000000, 0).UTC()
	if got := toProtoTimestamp(&at); !got.AsTime().Equal(at) {
		t.Errorf("toProtoTimestamp(%v) = %v", at, got.AsTime())
	}
}

func TestToProtoWatchResponsePerEventType(t *testing.T) {
	repo := &schema.Repository{DirPrefix: "n", GithubOwner: "o", GithubName: "n"}
	num := int64(7)

	changed := toProtoWatchResponse(Event{
		Type: EventWorktreeChanged, Repository: repo,
		Change: &WorktreeChange{Branch: "feature/x", PRNumber: &num, Action: ActionAdopted},
	})
	if changed.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_WORKTREE_CHANGED {
		t.Errorf("changed type = %v", changed.GetType())
	}
	if changed.GetChange().GetBranch() != "feature/x" || changed.GetChange().GetPrNumber() != num {
		t.Errorf("change not carried: %+v", changed.GetChange())
	}

	if got := toProtoWatchResponse(Event{Type: EventWorktreeChanged, Repository: repo}); got.GetChange() != nil {
		t.Errorf("change with no payload = %+v", got.GetChange())
	}

	started := toProtoWatchResponse(Event{Type: EventSyncStarted, Repository: repo})
	if started.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_STARTED {
		t.Errorf("started type = %v", started.GetType())
	}

	finished := toProtoWatchResponse(Event{
		Type: EventSyncFinished, Repository: repo,
		SyncCreated: 1, SyncRemoved: 2, SyncErr: errors.New("nope"),
	})
	if finished.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_SYNC_FINISHED {
		t.Errorf("finished type = %v", finished.GetType())
	}
	if finished.GetSummary().GetError() != "nope" {
		t.Errorf("summary = %+v", finished.GetSummary())
	}

	unspecified := toProtoWatchResponse(Event{Type: EventUnspecified, Repository: repo})
	if unspecified.GetType() != lumberjackv1.WatchResponseType_WATCH_RESPONSE_TYPE_UNSPECIFIED {
		t.Errorf("unspecified type = %v", unspecified.GetType())
	}
}
