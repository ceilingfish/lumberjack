package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
	"github.com/ceilingfish/lumberjack/internal/github"
)

func (h *harness) namedRepo(t *testing.T, name string) *schema.Repository {
	t.Helper()
	r := &schema.Repository{
		LocalPath: filepath.Join(h.parent, name), WorktreeParentDir: h.parent,
		DirPrefix: name, GithubOwner: "o", GithubName: name,
		DefaultRemote: "origin", Host: "github.com",
	}
	if err := h.db.CreateRepository(context.Background(), r); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	return r
}

func TestSyncAllRunsRepositoriesConcurrently(t *testing.T) {
	h := newHarness(t)
	for i := range syncConcurrency + 2 {
		h.namedRepo(t, fmt.Sprintf("r%d", i))
	}

	var (
		mu          sync.Mutex
		inFlight    int
		maxInFlight int
	)
	arrived := make(chan struct{}, syncConcurrency+2)
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()

	h.gh.listHook = func() {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		arrived <- struct{}{}
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		syncAll(context.Background(), h.svc, h.db)
	}()

	for i := range syncConcurrency {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d repositories were in flight at once, want %d", i, syncConcurrency)
		}
	}
	releaseOnce()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight != syncConcurrency {
		t.Errorf("peak concurrency = %d, want %d", maxInFlight, syncConcurrency)
	}
}

func TestSyncAllReportsPerRepositoryFailuresAndKeepsGoing(t *testing.T) {
	h := newHarness(t)
	for i := range 3 {
		h.namedRepo(t, fmt.Sprintf("r%d", i))
	}
	h.git.fetchErr = errors.New("network unreachable")

	syncAll(context.Background(), h.svc, h.db)

	repos, err := h.db.ListRepositories(context.Background())
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	for _, r := range repos {
		if r.LastSyncError == nil || *r.LastSyncError == "" {
			t.Errorf("%s should have recorded the fetch failure", r.GithubName)
		}
	}
}

func TestSyncAllCreatesWorktreesForEveryRepository(t *testing.T) {
	h := newHarness(t)
	for i := range 3 {
		h.namedRepo(t, fmt.Sprintf("r%d", i))
	}
	h.gh.prs = []github.PR{{Number: 1, HeadBranch: "a"}}

	syncAll(context.Background(), h.svc, h.db)

	repos, err := h.db.ListRepositories(context.Background())
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	for _, r := range repos {
		wts, err := h.db.ListWorktrees(context.Background(), r.ID)
		if err != nil {
			t.Fatalf("ListWorktrees: %v", err)
		}
		if len(wts) != 1 {
			t.Errorf("%s has %d worktrees, want 1", r.GithubName, len(wts))
		}
	}
}

func TestSyncAllStopsFeedingWhenContextIsCancelled(t *testing.T) {
	h := newHarness(t)
	for i := range syncConcurrency + 3 {
		h.namedRepo(t, fmt.Sprintf("r%d", i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	syncAll(ctx, h.svc, h.db)

	repos, err := h.db.ListRepositories(context.Background())
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	synced := 0
	for _, r := range repos {
		if r.LastSyncedAt != nil {
			synced++
		}
	}
	if synced == len(repos) {
		t.Errorf("a cancelled sync should not have completed all %d repositories", len(repos))
	}
}

func TestSyncAllSyncsEveryRepository(t *testing.T) {
	h := newHarness(t)
	for i := range syncConcurrency + 2 {
		h.namedRepo(t, fmt.Sprintf("r%d", i))
	}

	syncAll(context.Background(), h.svc, h.db)

	repos, err := h.db.ListRepositories(context.Background())
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	for _, r := range repos {
		if r.LastSyncedAt == nil {
			t.Errorf("%s was never synced", r.GithubName)
		}
	}
}
