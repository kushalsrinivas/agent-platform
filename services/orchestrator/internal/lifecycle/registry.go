package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
)

// WorkerRegistration is the persisted result of one successful Hello.
type WorkerRegistration struct {
	WorkerID  string
	SessionID string
	BootID    string
}

// LeaseRenewal names one active attempt the worker still owns.
type LeaseRenewal struct {
	TaskID    string
	AttemptID string
	LeaseID   string
}

// Register upserts the worker and returns a session ID. A restart with the same worker ID takes over the row; the
// old process's attempts are fenced by their leases and recovered by the reaper.
func (s *Service) Register(ctx context.Context, hello *workerv1.Hello) (WorkerRegistration, error) {
	if hello == nil {
		return WorkerRegistration{}, errors.New("lifecycle: hello is required")
	}
	workerID := strings.TrimSpace(hello.GetWorkerId())
	if err := validateWorkerID(workerID); err != nil {
		return WorkerRegistration{}, err
	}
	bootID := hello.GetBootId()
	if _, err := uuid.Parse(bootID); err != nil {
		return WorkerRegistration{}, fmt.Errorf("lifecycle: boot_id must be a UUID: %w", err)
	}
	pool := hello.GetPool()
	if pool == "" {
		pool = "default"
	}
	if len(pool) > 64 {
		return WorkerRegistration{}, errors.New("lifecycle: pool must be at most 64 characters")
	}
	protocol := int(hello.GetProtocolVersion())
	if protocol < 1 {
		protocol = 1
	}
	maxConcurrency := int(hello.GetCapacity().GetMaxConcurrency())
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if maxConcurrency > 1024 {
		maxConcurrency = 1024
	}
	available := int(hello.GetCapacity().GetAvailableSlots())
	if available < 0 {
		available = 0
	}
	if available > maxConcurrency {
		available = maxConcurrency
	}
	labels := hello.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	runtimeABIs := hello.GetRuntimeAbis()
	if runtimeABIs == nil {
		runtimeABIs = []int32{}
	}
	runtimeDigests := hello.GetRuntimeDigests()
	if runtimeDigests == nil {
		runtimeDigests = []string{}
	}
	binaryVersion := hello.GetBinaryVersion()
	if binaryVersion == "" {
		binaryVersion = "unknown"
	}
	sessionID := idgen.New("sess")

	_, err := s.pool.Exec(ctx, `
		INSERT INTO execution.workers
			(id, boot_id, pool, status, max_concurrency, reported_available_slots, labels,
			 runtime_abis, runtime_digests, binary_version, protocol_version, session_id,
			 registered_at, last_heartbeat_at)
		VALUES ($1, $2::uuid, $3, 'active', $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
		ON CONFLICT (id) DO UPDATE
		SET boot_id = EXCLUDED.boot_id,
		    pool = EXCLUDED.pool,
		    status = 'active',
		    max_concurrency = EXCLUDED.max_concurrency,
		    reported_available_slots = EXCLUDED.reported_available_slots,
		    labels = EXCLUDED.labels,
		    runtime_abis = EXCLUDED.runtime_abis,
		    runtime_digests = EXCLUDED.runtime_digests,
		    binary_version = EXCLUDED.binary_version,
		    protocol_version = EXCLUDED.protocol_version,
		    session_id = EXCLUDED.session_id,
		    registered_at = now(),
		    last_heartbeat_at = now(),
		    drained_at = NULL,
		    updated_at = now()`,
		workerID, bootID, pool, maxConcurrency, available, labels,
		runtimeABIs, runtimeDigests, binaryVersion, protocol, sessionID)
	if err != nil {
		return WorkerRegistration{}, fmt.Errorf("lifecycle: register worker: %w", err)
	}
	s.log.Info("worker registered",
		zap.String("worker_id", workerID),
		zap.String("pool", pool),
		zap.String("boot_id", bootID),
		zap.Int("max_concurrency", maxConcurrency),
	)
	return WorkerRegistration{WorkerID: workerID, SessionID: sessionID, BootID: bootID}, nil
}

// Heartbeat refreshes liveness, capacity and the leases of attempts the worker still owns. Lease renewal stops for
// cancelling or failing executions so an uncooperative worker loses ownership within one TTL.
func (s *Service) Heartbeat(ctx context.Context, workerID string, capacity *workerv1.Capacity,
	active []LeaseRenewal) error {
	if workerID == "" {
		return errors.New("lifecycle: worker_id is required")
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		available := -1
		maxConcurrency := 0
		if capacity != nil {
			available = int(capacity.GetAvailableSlots())
			maxConcurrency = int(capacity.GetMaxConcurrency())
		}
		tag, err := tx.Exec(ctx, `
			UPDATE execution.workers
			SET last_heartbeat_at = now(),
			    reported_available_slots = CASE WHEN $2 >= 0 THEN LEAST($2, max_concurrency) ELSE reported_available_slots END,
			    max_concurrency = CASE WHEN $3 > 0 THEN LEAST($3, 1024) ELSE max_concurrency END,
			    status = CASE WHEN status = 'draining' THEN 'draining' ELSE 'active' END,
			    updated_at = now()
			WHERE id = $1`, workerID, available, maxConcurrency)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("lifecycle: worker is not registered")
		}

		for _, renewal := range active {
			if renewal.TaskID == "" || renewal.LeaseID == "" {
				continue
			}
			_, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks t
				SET lease_expires_at = now() + make_interval(secs => $4), updated_at = now()
				FROM execution.executions e
				WHERE t.id = $1
				  AND t.owner_worker_id = $2
				  AND t.lease_id = $3::uuid
				  AND t.state IN ('assigned', 'running')
				  AND e.id = t.execution_id
				  AND e.state IN ('pending', 'running')`,
				renewal.TaskID, workerID, renewal.LeaseID, int(s.leaseTTL/time.Second))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// Disconnect marks the worker offline. Its open attempts remain fenced by their leases and are recovered by the
// reaper, so a graceful goodbye cannot orphan a task.
func (s *Service) Disconnect(ctx context.Context, workerID, sessionID string) error {
	if workerID == "" || sessionID == "" {
		return nil
	}
	// A reconnecting worker may already have registered a new session; only the closing session may go offline.
	_, err := s.pool.Exec(ctx, `
		UPDATE execution.workers
		SET status = 'offline', session_id = NULL, updated_at = now()
		WHERE id = $1 AND session_id = $2`, workerID, sessionID)
	return err
}

// WorkerSnapshot is the operator-facing worker row.
type WorkerSnapshot struct {
	ID              string     `json:"id"`
	Pool            string     `json:"pool"`
	Status          string     `json:"status"`
	MaxConcurrency  int        `json:"max_concurrency"`
	ReservedSlots   int        `json:"reserved_slots"`
	AvailableSlots  int        `json:"available_slots"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at"`
	RegisteredAt    time.Time  `json:"registered_at"`
	BinaryVersion   string     `json:"binary_version"`
	ProtocolVersion int        `json:"protocol_version"`
	DrainedAt       *time.Time `json:"drained_at,omitempty"`
}

// Workers lists the fleet for monitoring.
func (s *Service) Workers(ctx context.Context) ([]WorkerSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pool, status, max_concurrency, reserved_slots, reported_available_slots,
		       last_heartbeat_at, registered_at, binary_version, protocol_version, drained_at
		FROM execution.workers
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workers []WorkerSnapshot
	for rows.Next() {
		var worker WorkerSnapshot
		var drained pgtype.Timestamptz
		if err := rows.Scan(&worker.ID, &worker.Pool, &worker.Status, &worker.MaxConcurrency,
			&worker.ReservedSlots, &worker.AvailableSlots, &worker.LastHeartbeatAt,
			&worker.RegisteredAt, &worker.BinaryVersion, &worker.ProtocolVersion,
			&drained); err != nil {
			return nil, err
		}
		worker.DrainedAt = timePtr(drained)
		workers = append(workers, worker)
	}
	return workers, rows.Err()
}

func validateWorkerID(workerID string) error {
	if len(workerID) < 12 || len(workerID) > 68 || !strings.HasPrefix(workerID, "wrk_") {
		return errors.New("lifecycle: worker_id must match wrk_[A-Za-z0-9_-]{8,64}")
	}
	for _, char := range workerID[4:] {
		if !validWorkerIDChar(char) {
			return errors.New("lifecycle: worker_id must be a single NATS subject token")
		}
	}
	return nil
}

func validWorkerIDChar(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') || char == '-' || char == '_'
}
