package messaging

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/outbox"
)

// PublisherLoop continuously drains the transactional outbox. A crash, restart or NATS outage loses nothing:
// unpublished rows stay due and are reclaimed on the next pass.
type PublisherLoop struct {
	store      *outbox.Store
	publisher  outbox.Publisher
	log        *zap.Logger
	interval   time.Duration
	batch      int
	retryAfter time.Duration
}

// NewPublisherLoop builds the restart-safe outbox drain loop.
func NewPublisherLoop(store *outbox.Store, publisher outbox.Publisher, log *zap.Logger,
	interval time.Duration, batch int, retryAfter time.Duration) *PublisherLoop {
	if interval <= 0 {
		interval = time.Second
	}
	if batch <= 0 || batch > 100 {
		batch = 50
	}
	if retryAfter <= 0 {
		retryAfter = 5 * time.Second
	}
	return &PublisherLoop{
		store: store, publisher: publisher, log: log,
		interval: interval, batch: batch, retryAfter: retryAfter,
	}
}

// Run drains the outbox until the context is cancelled.
func (l *PublisherLoop) Run(ctx context.Context) error {
	l.log.Info("outbox publisher started",
		zap.Duration("interval", l.interval),
		zap.Int("batch", l.batch),
		zap.Duration("retry_after", l.retryAfter),
	)
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for {
		published, err := l.store.DispatchOnce(ctx, l.publisher, l.batch, l.retryAfter)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			l.log.Warn("outbox dispatch pass failed",
				zap.Int("published", published),
				zap.Duration("retry_after", l.retryAfter),
				zap.Error(err),
			)
		} else if published > 0 {
			l.log.Debug("outbox batch published", zap.Int("count", published))
		}

		select {
		case <-ctx.Done():
			l.log.Info("outbox publisher stopped")
			return nil
		case <-ticker.C:
		}
	}
}
