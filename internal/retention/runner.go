package retention

import (
	"context"
	"log/slog"
	"time"

	"tadmin/internal/store"
)

const (
	cleanupInterval = 24 * time.Hour
	cleanupBatch    = 500
)

type Runner struct {
	database *store.Store
	logger   *slog.Logger
	now      func() time.Time
	interval time.Duration
}

func New(database *store.Store, logger *slog.Logger) *Runner {
	return &Runner{database: database, logger: logger, now: time.Now, interval: cleanupInterval}
}

func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.database == nil {
		return
	}
	r.cleanup(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.cleanup(ctx)
		}
	}
}

func (r *Runner) cleanup(ctx context.Context) {
	stats, err := r.database.CleanupRetention(ctx, r.now().UTC().Truncate(time.Second), cleanupBatch)
	if err != nil {
		if ctx.Err() == nil && r.logger != nil {
			r.logger.Error("retention cleanup failed", "error", err)
		}
		return
	}
	if stats.Total() > 0 && r.logger != nil {
		r.logger.Info("retention cleanup completed", "deleted_records", stats.Total())
	}
}
