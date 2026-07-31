package daemon

import (
	"context"
	"log"
	"time"

	"go.uber.org/fx"

	"github.com/ceilingfish/lumberjack/internal/database"
)

// SyncInterval is how often the background loop reconciles every tracked
// repository (docs/prd.md: "on an hourly basis").
const SyncInterval = time.Hour

// runSyncLoop starts the hourly background sync under fx's lifecycle: it runs
// once shortly after startup, then on every tick, until the daemon stops.
func runSyncLoop(lc fx.Lifecycle, svc *Service, db *database.Client) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				ticker := time.NewTicker(SyncInterval)
				defer ticker.Stop()
				syncLoop(ctx, svc, db, ticker.C)
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

// syncLoop reconciles all repositories immediately, then on every tick, until
// ctx is cancelled. Per-repository errors are logged and never stop the loop —
// a transient GitHub or network failure must not wedge the daemon.
func syncLoop(ctx context.Context, svc *Service, db *database.Client, tick <-chan time.Time) {
	syncAll(ctx, svc, db)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
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
	for i := range repos {
		repo := &repos[i]
		created, removed, err := svc.SyncRepository(ctx, repo, nil)
		if err != nil {
			log.Printf("sync loop: %s: %v", displayName(repo), err)
			continue
		}
		if created > 0 || removed > 0 {
			log.Printf("sync loop: %s: +%d worktree(s), -%d", displayName(repo), created, removed)
		}
	}
}
