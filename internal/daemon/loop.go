package daemon

import (
	"context"
	"log"
	"sync"
	"time"

	"go.uber.org/fx"

	"github.com/ceilingfish/lumberjack/internal/database"
	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// SyncInterval is how often the background loop reconciles every tracked
// repository (docs/prd.md: "on an hourly basis").
const SyncInterval = time.Hour

const syncConcurrency = 3

// runSyncLoop starts the hourly background sync under fx's lifecycle: it runs
// once shortly after startup, then on every tick, until the daemon stops.
func runSyncLoop(lc fx.Lifecycle, svc *Service, db *database.Client) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				syncLoop(ctx, svc, db)
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			// Cancel, then wait for the loop to unwind before returning so the
			// database (closed by a later stop hook) is never pulled out from
			// under an in-flight sync.
			cancel()
			<-done
			return nil
		},
	})
}

// syncLoop reconciles all repositories immediately, then every SyncInterval,
// until ctx is cancelled. Per-repository errors are logged and never stop the
// loop — a transient GitHub or network failure must not wedge the daemon.
func syncLoop(ctx context.Context, svc *Service, db *database.Client) {
	ticker := time.NewTicker(SyncInterval)
	defer ticker.Stop()

	syncAll(ctx, svc, db)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncAll(ctx, svc, db)
		}
	}
}

// syncAll reconciles every tracked repository once.
func syncAll(ctx context.Context, svc *Service, db *database.Client) {
	repos, err := db.ListRepositories(ctx)
	if err != nil {
		log.Printf("sync loop: listing repositories: %v", err)
		return
	}

	queue := make(chan *schema.Repository)
	var wg sync.WaitGroup
	for range min(syncConcurrency, len(repos)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range queue {
				syncOne(ctx, svc, repo)
			}
		}()
	}

feed:
	for i := range repos {
		select {
		case <-ctx.Done():
			break feed
		case queue <- &repos[i]:
		}
	}
	close(queue)
	wg.Wait()
}

func syncOne(ctx context.Context, svc *Service, repo *schema.Repository) {
	created, removed, err := svc.SyncRepository(ctx, repo, nil)
	if err != nil {
		log.Printf("sync loop: %s: %v", displayName(repo), err)
		return
	}
	if created > 0 || removed > 0 {
		log.Printf("sync loop: %s: +%d worktree(s), -%d", displayName(repo), created, removed)
	}
}
