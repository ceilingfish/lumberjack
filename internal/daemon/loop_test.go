package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
)

type recordingLifecycle struct{ hooks []fx.Hook }

func (l *recordingLifecycle) Append(h fx.Hook) { l.hooks = append(l.hooks, h) }

func awaitEvent(t *testing.T, events <-chan Event, want EventType) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("subscriber closed before a %v event arrived", want)
			}
			if ev.Type == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a %v event", want)
		}
	}
}

func startLoop(t *testing.T, h *harness) chan time.Time {
	t.Helper()
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		syncLoop(ctx, h.svc, h.db, tick)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("syncLoop did not return after its context was cancelled")
		}
	})
	return tick
}

func lastSyncStatus(t *testing.T, h *harness, repo *schema.Repository) string {
	t.Helper()
	stored, err := h.db.FindRepository(context.Background(), repo.LocalPath)
	if err != nil {
		t.Fatalf("FindRepository: %v", err)
	}
	if stored.LastSyncStatus == nil {
		t.Fatal("last_sync_status was never recorded")
	}
	return *stored.LastSyncStatus
}

func TestSyncLoopSyncsImmediatelyThenOnEachTick(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	events, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	tick := startLoop(t, h)

	awaitEvent(t, events, EventSyncFinished)
	wts, err := h.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil || len(wts) != 1 {
		t.Fatalf("first pass: worktrees=%d err=%v, want 1", len(wts), err)
	}

	h.gh.prs = append(h.gh.prs, github.PR{Number: 2, HeadBranch: "feature/b"})
	tick <- time.Time{}
	awaitEvent(t, events, EventSyncFinished)

	wts, err = h.db.ListWorktrees(context.Background(), repo.ID)
	if err != nil || len(wts) != 2 {
		t.Fatalf("after tick: worktrees=%d err=%v, want 2", len(wts), err)
	}
}

func TestSyncLoopFailingRepositoryDoesNotStopTheRunOrTheLoop(t *testing.T) {
	h := newHarness(t)
	broken := h.namedRepo(t, "broken")
	healthy := h.namedRepo(t, "healthy")
	h.git.fetchErrByPath[broken.LocalPath] = errors.New("network is unreachable")
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "feature/a"}}

	events, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	tick := startLoop(t, h)

	awaitEvent(t, events, EventSyncFinished)
	awaitEvent(t, events, EventSyncFinished)

	wts, err := h.db.ListWorktrees(context.Background(), healthy.ID)
	if err != nil || len(wts) != 1 {
		t.Fatalf("healthy repo: worktrees=%d err=%v, want 1", len(wts), err)
	}
	if got := lastSyncStatus(t, h, broken); got != schema.SyncStatusError {
		t.Errorf("broken repo last_sync_status = %q, want %q", got, schema.SyncStatusError)
	}

	h.gh.prs = append(h.gh.prs, github.PR{Number: 2, HeadBranch: "feature/b"})
	tick <- time.Time{}
	awaitEvent(t, events, EventSyncFinished)
	awaitEvent(t, events, EventSyncFinished)

	wts, err = h.db.ListWorktrees(context.Background(), healthy.ID)
	if err != nil || len(wts) != 2 {
		t.Fatalf("healthy repo after tick: worktrees=%d err=%v, want 2", len(wts), err)
	}
}

func TestSyncAllGivesUpWhenRepositoriesCannotBeListed(t *testing.T) {
	h := newHarness(t)
	h.repo(t)
	events, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()
	if err := h.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}

	syncAll(context.Background(), h.svc, h.db)

	select {
	case ev := <-events:
		t.Errorf("expected no sync to be attempted, got %v", ev.Type)
	default:
	}
}

func TestRunSyncLoopStopWaitsForTheInFlightSync(t *testing.T) {
	h := newHarness(t)
	h.repo(t)
	h.git.fetchBlock = make(chan struct{})

	events, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()

	lc := &recordingLifecycle{}
	runSyncLoop(lc, h.svc, h.db)
	if len(lc.hooks) != 1 {
		t.Fatalf("hooks appended = %d, want 1", len(lc.hooks))
	}
	hook := lc.hooks[0]
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	awaitEvent(t, events, EventSyncStarted)

	stopped := make(chan error, 1)
	go func() { stopped <- hook.OnStop(context.Background()) }()
	select {
	case err := <-stopped:
		t.Fatalf("OnStop returned (%v) while a sync was still in flight", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(h.git.fetchBlock)
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("OnStop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnStop did not return after the in-flight sync finished")
	}
}
