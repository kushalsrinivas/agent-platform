// Package messaging owns the NATS JetStream topology, the transactional outbox publisher and the durable
// delivery consumer used by workers.
package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

// Normative stream names.
const (
	StreamTaskDispatch = "TASK_DISPATCH"
	StreamEvents       = "EVENTS"
	StreamTaskDLQ      = "TASK_DLQ"
)

// StreamsConfig controls replication. Local development uses one replica; production clusters should use three.
type StreamsConfig struct {
	Replicas int
}

// Streams returns the authoritative stream definitions.
func Streams(cfg StreamsConfig) []jetstream.StreamConfig {
	replicas := cfg.Replicas
	if replicas < 1 {
		replicas = 1
	}
	if replicas > 5 {
		replicas = 5
	}
	return []jetstream.StreamConfig{
		{
			Name:        StreamTaskDispatch,
			Description: "Commit-then-notify task dispatch; work-queue, one worker subject per stream entry.",
			Subjects:    []string{"tasks.dispatch.>"},
			Retention:   jetstream.WorkQueuePolicy,
			Discard:     jetstream.DiscardOld,
			Storage:     jetstream.FileStorage,
			Replicas:    replicas,
			MaxAge:      24 * time.Hour,
			MaxBytes:    4 << 30,
			MaxMsgSize:  64 << 10,
			Duplicates:  2 * time.Minute,
		},
		{
			Name:        StreamEvents,
			Description: "Execution lifecycle fan-out. Never read for correctness.",
			Subjects:    []string{"events.>"},
			Retention:   jetstream.LimitsPolicy,
			Discard:     jetstream.DiscardOld,
			Storage:     jetstream.FileStorage,
			Replicas:    replicas,
			MaxAge:      72 * time.Hour,
			MaxBytes:    2 << 30,
			MaxMsgSize:  256 << 10,
			Duplicates:  2 * time.Minute,
		},
		{
			Name:        StreamTaskDLQ,
			Description: "Dead-lettered dispatch messages retained for inspection and controlled redrive.",
			Subjects:    []string{"tasks.dlq.>"},
			Retention:   jetstream.LimitsPolicy,
			Discard:     jetstream.DiscardOld,
			Storage:     jetstream.FileStorage,
			Replicas:    replicas,
			MaxAge:      7 * 24 * time.Hour,
			MaxBytes:    1 << 30,
			MaxMsgSize:  512 << 10,
			Duplicates:  2 * time.Minute,
		},
	}
}

// EnsureStreams creates or updates the streams. It is safe to call from every process that needs them.
func EnsureStreams(ctx context.Context, js jetstream.JetStream, cfg StreamsConfig, log *zap.Logger) error {
	for _, streamConfig := range Streams(cfg) {
		if _, err := js.CreateOrUpdateStream(ctx, streamConfig); err != nil {
			return fmt.Errorf("messaging: ensure stream %s: %w", streamConfig.Name, err)
		}
		log.Info("nats stream ready",
			zap.String("stream", streamConfig.Name),
			zap.Int("replicas", streamConfig.Replicas),
			zap.Duration("max_age", streamConfig.MaxAge),
			zap.Int64("max_bytes", streamConfig.MaxBytes),
		)
	}
	return nil
}
