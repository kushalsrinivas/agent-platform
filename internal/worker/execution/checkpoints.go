package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/worker/control"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// checkpointUploader watches the runtime's checkpoint directory and uploads each atomically written file. The
// runtime never talks to the control plane directly; the worker is the only writer.
type checkpointUploader struct {
	client         *control.Client
	dispatch       *contracts.TaskDispatch
	dir            string
	runtimeABI     int32
	runtimeVersion string
	log            *zap.Logger

	done chan struct{}
	wg   sync.WaitGroup

	mu       sync.Mutex
	seen     map[string]struct{}
	failed   error
	uploaded int
}

func startCheckpointUploader(ctx context.Context, client *control.Client, dispatch *contracts.TaskDispatch,
	dir string, runtimeABI int32, runtimeVersion string, log *zap.Logger) *checkpointUploader {
	uploader := &checkpointUploader{
		client: client, dispatch: dispatch, dir: dir, runtimeABI: runtimeABI,
		runtimeVersion: runtimeVersion, log: log, done: make(chan struct{}), seen: map[string]struct{}{},
	}
	uploader.wg.Add(1)
	go func() {
		defer uploader.wg.Done()
		uploader.run(ctx)
	}()
	return uploader
}

func (u *checkpointUploader) run(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-u.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.scan(ctx)
		}
	}
}

// StopAndDrain waits for the last atomic file and returns the first permanent upload failure.
func (u *checkpointUploader) StopAndDrain(ctx context.Context) error {
	close(u.done)
	u.wg.Wait()
	u.scan(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.failed
}

func (u *checkpointUploader) scan(ctx context.Context) {
	entries, err := os.ReadDir(u.dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			u.log.Warn("checkpoint directory could not be read", zap.String("dir", u.dir), zap.Error(err))
		}
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.gz") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		u.mu.Lock()
		_, exists := u.seen[name]
		u.mu.Unlock()
		if exists {
			continue
		}
		if err := u.upload(ctx, name); err != nil {
			u.mu.Lock()
			u.seen[name] = struct{}{}
			if u.failed == nil {
				u.failed = err
			}
			u.mu.Unlock()
			u.log.Warn("checkpoint upload failed",
				zap.String("task_id", u.dispatch.TaskID),
				zap.String("attempt_id", u.dispatch.AttemptID),
				zap.String("file", name),
				zap.Error(err),
			)
			continue
		}
		u.mu.Lock()
		u.seen[name] = struct{}{}
		u.uploaded++
		u.mu.Unlock()
	}
}

func (u *checkpointUploader) upload(ctx context.Context, name string) error {
	sequence, err := checkpointSequence(name)
	if err != nil {
		return err
	}
	path := filepath.Join(u.dir, name)
	data, err := os.ReadFile(path) //nolint:gosec // path is a file name read from the runtime's private directory.
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	if len(data) == 0 {
		return errors.New("checkpoint file is empty")
	}
	digest := sha256.Sum256(data)
	sha := "sha256:" + hex.EncodeToString(digest[:])

	request := &workerv1.PutCheckpointRequest{
		TaskId: u.dispatch.TaskID, AttemptId: u.dispatch.AttemptID, LeaseId: u.dispatch.LeaseID,
		Sequence: int32(sequence), CheckpointSchemaVersion: 1, RuntimeAbi: u.runtimeABI, //nolint:gosec // sequence is bounded by the runtime.
		RuntimeVersion: u.runtimeVersion, Sha256: sha, State: data,
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		response, err := u.client.PutCheckpoint(callCtx, request)
		cancel()
		if err == nil {
			u.log.Debug("checkpoint uploaded",
				zap.String("task_id", u.dispatch.TaskID),
				zap.Int("sequence", sequence),
				zap.String("checkpoint_id", response.GetCheckpointId()),
				zap.Bool("duplicate", response.GetDuplicate()),
			)
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("checkpoint upload interrupted: %w", lastErr)
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	return fmt.Errorf("checkpoint upload failed after retries: %w", lastErr)
}

// checkpointSequence parses "ckpt-000001-<sha>.json.gz".
func checkpointSequence(name string) (int, error) {
	trimmed := strings.TrimPrefix(name, "ckpt-")
	if trimmed == name {
		return 0, fmt.Errorf("unexpected checkpoint file %q", name)
	}
	parts := strings.SplitN(trimmed, "-", 2)
	sequence, err := strconv.Atoi(parts[0])
	if err != nil || sequence < 1 {
		return 0, fmt.Errorf("checkpoint file %q has no valid sequence", name)
	}
	return sequence, nil
}
