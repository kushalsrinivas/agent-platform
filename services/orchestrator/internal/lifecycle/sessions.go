package lifecycle

import (
	"sync"

	"go.uber.org/zap"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
)

// sessionSender is one live worker control stream. Sending is serialised by the owning WorkerServer.
type sessionSender struct {
	send func(*workerv1.OrchestratorMessage) error
}

// sessionHub tracks the newest control stream per worker so orchestration decisions can reach the process that
// owns an attempt. A worker that is offline is not an error: its lease expires and recovery owns the transition.
type sessionHub struct {
	mu       sync.Mutex
	sessions map[string]*sessionSender
}

func newSessionHub() *sessionHub {
	return &sessionHub{sessions: map[string]*sessionSender{}}
}

// Register installs the newest session for a worker and returns an unregister function that cannot evict a newer
// session created by a reconnect.
func (h *sessionHub) Register(workerID string, send func(*workerv1.OrchestratorMessage) error) func() {
	sender := &sessionSender{send: send}
	h.mu.Lock()
	h.sessions[workerID] = sender
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		if h.sessions[workerID] == sender {
			delete(h.sessions, workerID)
		}
		h.mu.Unlock()
	}
}

// Cancel asks a worker to stop one attempt. Delivery is best effort.
func (h *sessionHub) Cancel(workerID string, message *workerv1.CancelTask, log *zap.Logger) {
	h.mu.Lock()
	sender := h.sessions[workerID]
	h.mu.Unlock()
	if sender == nil {
		log.Info("cancellation has no live worker session",
			zap.String("worker_id", workerID),
			zap.String("task_id", message.GetTaskId()),
			zap.String("attempt_id", message.GetAttemptId()),
		)
		return
	}
	if err := sender.send(&workerv1.OrchestratorMessage{
		Body: &workerv1.OrchestratorMessage_CancelTask{CancelTask: message},
	}); err != nil {
		log.Warn("could not deliver task cancellation",
			zap.String("worker_id", workerID),
			zap.String("task_id", message.GetTaskId()),
			zap.Error(err),
		)
	}
}

// CancelNotification is emitted after a cancellation transaction commits.
type CancelNotification struct {
	WorkerID  string
	TaskID    string
	AttemptID string
	LeaseID   string
	Reason    string
	Epoch     int
}

func (s *Service) notifyCancellations(notifications []CancelNotification) {
	for _, notification := range notifications {
		s.sessions.Cancel(notification.WorkerID, &workerv1.CancelTask{
			TaskId: notification.TaskID, AttemptId: notification.AttemptID,
			LeaseId: notification.LeaseID, Reason: notification.Reason,
			CancelEpoch: int32(notification.Epoch), //nolint:gosec // cancellation epochs are monotonic counters.
		}, s.log)
	}
}
