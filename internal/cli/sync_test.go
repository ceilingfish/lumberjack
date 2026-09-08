package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ceilingfish/lumberjack/internal/present"
	lumberjackv1 "github.com/ceilingfish/lumberjack/pkg/client/lumberjack/v1"
)

func syncEvents() []*lumberjackv1.SyncResponse {
	pr := int64(4)
	return []*lumberjackv1.SyncResponse{
		{Repository: "n", Message: "fetching"},
		{Repository: "n", Change: &lumberjackv1.WorktreeChange{
			Branch: "feature", PrNumber: &pr,
			Action: lumberjackv1.WorktreeAction_WORKTREE_ACTION_CHECKED_OUT,
		}},
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{
			Status: lumberjackv1.SyncStatus_SYNC_STATUS_OK, WorktreesCreated: 1,
		}},
	}
}

func TestRunSyncRendersProgressChangesAndASummary(t *testing.T) {
	serve(t, &stub{syncEvents: syncEvents()})
	var out bytes.Buffer

	err := RunSync(context.Background(), cmdWith("", &out), dial(t), "n", present.Structured)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	for _, want := range []string{"n: fetching", "BRANCH", "#4", "checked out", "n: synced (+1 worktree(s), -0)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}
}

// Under json only valid JSON may reach stdout, so progress messages are
// dropped and the whole run is buffered into one bare array.
func TestRunSyncJSONEmitsOneArray(t *testing.T) {
	serve(t, &stub{syncEvents: syncEvents()})
	var out bytes.Buffer

	if err := RunSync(context.Background(), cmdWith("", &out), dial(t), "n", present.JSON); err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	if strings.Contains(out.String(), "fetching") {
		t.Errorf("out = %q, want progress messages suppressed under json", out.String())
	}
	var got []syncResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json = %q: %v", out.String(), err)
	}
	if len(got) != 1 || got[0].Repository != "n" || len(got[0].Changes) != 1 {
		t.Errorf("results = %+v, want one repository carrying one change", got)
	}
}

func TestRunSyncReportsAnErroredSummary(t *testing.T) {
	boom := "boom"
	serve(t, &stub{syncEvents: []*lumberjackv1.SyncResponse{
		{Repository: "n", Completed: true, Summary: &lumberjackv1.SyncSummary{
			Status: lumberjackv1.SyncStatus_SYNC_STATUS_ERROR, Error: &boom,
		}},
	}})
	var out bytes.Buffer

	if err := RunSync(context.Background(), cmdWith("", &out), dial(t), "n", present.Structured); err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	for _, want := range []string{"n: error", ": boom"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to contain %q", out.String(), want)
		}
	}
}

func TestRunSyncSurfacesAStreamFailure(t *testing.T) {
	serve(t, &stub{err: errors.New("boom")})
	err := RunSync(context.Background(), cmdWith("", io.Discard), dial(t), "n", present.Structured)
	if err == nil {
		t.Error("expected the stream failure to surface")
	}
}

func TestRunSyncSurfacesFailedWrites(t *testing.T) {
	cases := []struct {
		name    string
		format  present.Format
		succeed int
	}{
		{"the progress message", present.Structured, 0},
		{"the change table", present.Structured, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			serve(t, &stub{syncEvents: syncEvents()})
			cmd := cmdWith("", &flakyWriter{succeed: c.succeed})
			if err := RunSync(context.Background(), cmd, dial(t), "n", c.format); !errors.Is(err, errWrite) {
				t.Errorf("err = %v, want the failed write", err)
			}
		})
	}
}

func TestRunSyncJSONSurfacesAFailedWrite(t *testing.T) {
	serve(t, &stub{syncEvents: syncEvents()})
	cmd := cmdWith("", failWriter{})
	if err := RunSync(context.Background(), cmd, dial(t), "n", present.JSON); !errors.Is(err, errWrite) {
		t.Errorf("err = %v, want the failed write", err)
	}
}
