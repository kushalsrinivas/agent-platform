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

// WorkerUsage is the advisory telemetry carried by a heartbeat.
type WorkerUsage struct {
	RuntimeVersion string
	CPUPercent     float64
	MemoryUsedMB   int64
	Status         string
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
	var previousBoot string
	previousErr := s.pool.QueryRow(ctx,
		`SELECT boot_id::text FROM execution.workers WHERE id = $1`, workerID).Scan(&previousBoot)
	if previousErr != nil && !isNoRows(previousErr) {
		return WorkerRegistration{}, previousErr
	}
	bootChanged := previousErr == nil && previousBoot != bootID
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
	runtimeName := hello.GetRuntime()
	if runtimeName == "" {
		runtimeName = "python"
	}
	runtimeVersion := hello.GetRuntimeVersion()
	if runtimeVersion == "" {
		runtimeVersion = "unknown"
	}
	if len(runtimeVersion) > 128 {
		return WorkerRegistration{}, errors.New("lifecycle: runtime_version must be at most 128 characters")
	}
	capabilities := normalizeCapabilities(hello.GetCapabilities())
	resources := hello.GetResources()
	cpuCores := resources.GetCpuCores()
	if cpuCores <= 0 {
		cpuCores = 1
	}
	memoryMB := resources.GetMemoryMb()
	if memoryMB <= 0 {
		memoryMB = 1024
	}
	workerStatus := s.statusForWorker(0, maxConcurrency, available, "")
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
			 runtime, runtime_version, capabilities, cpu_cores, memory_mb,
			 runtime_abis, runtime_digests, binary_version, protocol_version, session_id,
			 registered_at, last_heartbeat_at, status_changed_at)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
		        now(), now(), now())
		ON CONFLICT (id) DO UPDATE
		SET boot_id = EXCLUDED.boot_id,
		    pool = EXCLUDED.pool,
		    status = EXCLUDED.status,
		    max_concurrency = EXCLUDED.max_concurrency,
		    reported_available_slots = EXCLUDED.reported_available_slots,
		    labels = EXCLUDED.labels,
		    runtime = EXCLUDED.runtime,
		    runtime_version = EXCLUDED.runtime_version,
		    capabilities = EXCLUDED.capabilities,
		    cpu_cores = EXCLUDED.cpu_cores,
		    memory_mb = EXCLUDED.memory_mb,
		    runtime_abis = EXCLUDED.runtime_abis,
		    runtime_digests = EXCLUDED.runtime_digests,
		    binary_version = EXCLUDED.binary_version,
		    protocol_version = EXCLUDED.protocol_version,
		    session_id = EXCLUDED.session_id,
		    registered_at = now(),
		    last_heartbeat_at = now(),
		    drained_at = NULL,
		    status_changed_at = CASE WHEN execution.workers.status IS DISTINCT FROM EXCLUDED.status
		        THEN now() ELSE execution.workers.status_changed_at END,
		    updated_at = now()`,
		workerID, bootID, pool, workerStatus, maxConcurrency, available, labels,
		runtimeName, runtimeVersion, capabilities, cpuCores, memoryMB,
		runtimeABIs, runtimeDigests, binaryVersion, protocol, sessionID)
	if err != nil {
		return WorkerRegistration{}, fmt.Errorf("lifecycle: register worker: %w", err)
	}
	if bootChanged {
		// A new boot for the same worker ID means the previous process is gone. Its open attempts are reclaimed
		// immediately rather than waiting for the lease TTL.
		rows, err := s.pool.Query(ctx, `
			SELECT id FROM execution.execution_tasks
			WHERE owner_worker_id = $1 AND state IN ('assigned', 'running')`, workerID)
		if err != nil {
			return WorkerRegistration{}, err
		}
		var taskIDs []string
		for rows.Next() {
			var taskID string
			if err := rows.Scan(&taskID); err != nil {
				rows.Close()
				return WorkerRegistration{}, err
			}
			taskIDs = append(taskIDs, taskID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return WorkerRegistration{}, err
		}
		for _, taskID := range taskIDs {
			if _, err := s.reapTask(ctx, taskID, true); err != nil {
				s.log.Warn("could not reclaim previous boot attempt",
					zap.String("worker_id", workerID), zap.String("task_id", taskID), zap.Error(err))
			}
		}
		s.log.Info("reclaimed previous boot attempts",
			zap.String("worker_id", workerID),
			zap.String("previous_boot_id", previousBoot),
			zap.Int("attempts", len(taskIDs)),
		)
	}

	s.log.Info("worker registered",
		zap.String("worker_id", workerID),
		zap.String("pool", pool),
		zap.String("boot_id", bootID),
		zap.String("runtime", runtimeName),
		zap.String("runtime_version", runtimeVersion),
		zap.Int("max_concurrency", maxConcurrency),
		zap.Strings("capabilities", capabilities),
	)
	return WorkerRegistration{WorkerID: workerID, SessionID: sessionID, BootID: bootID}, nil
}

// Heartbeat refreshes liveness, capacity, usage and the leases of attempts the worker still owns. A lease that
// already expired is never revived: that is the fencing boundary for a stale worker.
func (s *Service) Heartbeat(ctx context.Context, workerID string, capacity *workerv1.Capacity,
	active []LeaseRenewal, usage WorkerUsage) error {
	if workerID == "" {
		return errors.New("lifecycle: worker_id is required")
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var reserved, maxConcurrency, reportedAvailable int
		err := tx.QueryRow(ctx, `
			SELECT reserved_slots, max_concurrency, reported_available_slots
			FROM execution.workers WHERE id = $1 FOR UPDATE`, workerID).
			Scan(&reserved, &maxConcurrency, &reportedAvailable)
		if isNoRows(err) {
			return errors.New("lifecycle: worker is not registered")
		}
		if err != nil {
			return err
		}
		available := reportedAvailable
		newMax := maxConcurrency
		if capacity != nil {
			if capacity.GetAvailableSlots() >= 0 {
				available = int(capacity.GetAvailableSlots())
			}
			if capacity.GetMaxConcurrency() > 0 {
				newMax = int(capacity.GetMaxConcurrency())
			}
		}
		if newMax < 1 {
			newMax = 1
		}
		if newMax > 1024 {
			newMax = 1024
		}
		if available < 0 {
			available = 0
		}
		if available > newMax {
			available = newMax
		}
		status := s.statusForWorker(reserved, newMax, available, usage.Status)
		runtimeVersion := usage.RuntimeVersion
		if len(runtimeVersion) > 128 {
			runtimeVersion = runtimeVersion[:128]
		}
		cpu := usage.CPUPercent
		if cpu < 0 {
			cpu = 0
		}
		if cpu > 100 {
			cpu = 100
		}
		memory := usage.MemoryUsedMB
		if memory < 0 {
			memory = 0
		}

		tag, err := tx.Exec(ctx, `
			UPDATE execution.workers
			SET last_heartbeat_at = now(),
			    reported_available_slots = $2,
			    max_concurrency = $3,
			    cpu_usage_percent = $4,
			    memory_used_mb = $5,
			    runtime_version = CASE WHEN $6 <> '' THEN $6 ELSE runtime_version END,
			    status = $7,
			    status_changed_at = CASE WHEN status IS DISTINCT FROM $7 THEN now() ELSE status_changed_at END,
			    updated_at = now()
			WHERE id = $1`,
			workerID, available, newMax, cpu, memory, runtimeVersion, status)
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
			// The expiry guard is the fence: a lease that already lapsed must not be revived by a late heartbeat.
			_, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks t
				SET lease_expires_at = now() + make_interval(secs => $4), updated_at = now()
				FROM execution.executions e
				WHERE t.id = $1
				  AND t.owner_worker_id = $2
				  AND t.lease_id = $3::uuid
				  AND t.state IN ('assigned', 'running')
				  AND t.lease_expires_at > now()
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
		SET status = 'terminated', session_id = NULL, updated_at = now(),
		    status_changed_at = CASE WHEN status IS DISTINCT FROM 'terminated' THEN now() ELSE status_changed_at END
		WHERE id = $1 AND session_id = $2`, workerID, sessionID)
	return err
}

// WorkerSnapshot is the operator-facing worker row.
type WorkerSnapshot struct {
	ID              string     `json:"id"`
	Pool            string     `json:"pool"`
	Status          string     `json:"status"`
	Runtime         string     `json:"runtime"`
	RuntimeVersion  string     `json:"runtime_version"`
	Capabilities    []string   `json:"capabilities"`
	CPUCores        float64    `json:"cpu_cores"`
	MemoryMB        int64      `json:"memory_mb"`
	CPUUsagePercent *float64   `json:"cpu_usage_percent,omitempty"`
	MemoryUsedMB    *int64     `json:"memory_used_mb,omitempty"`
	MaxConcurrency  int        `json:"max_concurrency"`
	ReservedSlots   int        `json:"reserved_slots"`
	AvailableSlots  int        `json:"available_slots"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at"`
	RegisteredAt    time.Time  `json:"registered_at"`
	BinaryVersion   string     `json:"binary_version"`
	ProtocolVersion int        `json:"protocol_version"`
	StatusChangedAt time.Time  `json:"status_changed_at"`
	DrainedAt       *time.Time `json:"drained_at,omitempty"`
}

// Workers lists the fleet for monitoring.
func (s *Service) Workers(ctx context.Context) ([]WorkerSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, pool, status, runtime, runtime_version, capabilities, cpu_cores, memory_mb,
		       cpu_usage_percent, memory_used_mb, max_concurrency, reserved_slots,
		       reported_available_slots, last_heartbeat_at, registered_at, binary_version,
		       protocol_version, status_changed_at, drained_at
		FROM execution.workers
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workers []WorkerSnapshot
	for rows.Next() {
		var worker WorkerSnapshot
		var cpuUsage pgtype.Float8
		var memoryUsed pgtype.Int8
		var drained pgtype.Timestamptz
		if err := rows.Scan(&worker.ID, &worker.Pool, &worker.Status, &worker.Runtime,
			&worker.RuntimeVersion, &worker.Capabilities, &worker.CPUCores, &worker.MemoryMB,
			&cpuUsage, &memoryUsed, &worker.MaxConcurrency, &worker.ReservedSlots,
			&worker.AvailableSlots, &worker.LastHeartbeatAt, &worker.RegisteredAt,
			&worker.BinaryVersion, &worker.ProtocolVersion, &worker.StatusChangedAt,
			&drained); err != nil {
			return nil, err
		}
		if cpuUsage.Valid {
			value := cpuUsage.Float64
			worker.CPUUsagePercent = &value
		}
		if memoryUsed.Valid {
			value := memoryUsed.Int64
			worker.MemoryUsedMB = &value
		}
		worker.DrainedAt = timePtr(drained)
		workers = append(workers, worker)
	}
	return workers, rows.Err()
}

func normalizeCapabilities(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 64 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return []string{"agent_execution", "tool_execution"}
	}
	return result
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
