// Package metrics defines the shared Prometheus contract for every deployable. Labels are deliberately bounded:
// route patterns, pools, providers and outcome classes; execution/task identifiers belong in traces and logs.
package metrics

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Shared collectors. Route/method/provider/pool labels are bounded by construction.
var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "http", Name: "requests_total",
		Help: "HTTP requests by service, method, route pattern and status class.",
	}, []string{"service", "method", "route", "status"})
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "agent_platform", Subsystem: "http", Name: "request_duration_seconds",
		Help: "HTTP request latency.", Buckets: prometheus.DefBuckets,
	}, []string{"service", "method", "route"})
	RateLimited = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "http", Name: "rate_limited_total",
		Help: "Requests rejected by the local rate limiter.",
	}, []string{"service"})

	QueuePendingTasks = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "queue", Name: "pending_tasks",
		Help: "Tasks waiting for dispatch.",
	})
	QueueOldestPendingSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "queue", Name: "oldest_pending_seconds",
		Help: "Age of the oldest dispatchable pending task.",
	})
	QueueActiveTasks = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "queue", Name: "active_tasks",
		Help: "Tasks assigned or running.",
	})
	ExecutionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "execution", Name: "active",
		Help: "Executions in pending or running state.",
	})
	OutboxPending = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "outbox", Name: "pending",
		Help: "Transactional outbox rows not yet published.",
	})
	OutboxOldestSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "outbox", Name: "oldest_pending_seconds",
		Help: "Age of the oldest unpublished outbox row.",
	})

	DispatchTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "orchestrator", Name: "dispatches_total",
		Help: "Tasks assigned to a worker.",
	})
	RetryTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "orchestrator", Name: "retries_total",
		Help: "Task attempts scheduled for retry.",
	})
	RecoveryTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "orchestrator", Name: "recoveries_total",
		Help: "Interrupted attempts recovered by the orchestrator.",
	})
	LeaseExpirationTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "orchestrator", Name: "lease_expirations_total",
		Help: "Attempts reaped after lease expiry.",
	})
	WorkerTasksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "worker", Name: "tasks_total",
		Help: "Worker task attempts by outcome.",
	}, []string{"pool", "outcome"})
	WorkerRuntimeErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "worker", Name: "runtime_errors_total",
		Help: "Runtime failures by error code.",
	}, []string{"pool", "code"})
	WorkerActiveTasks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "worker", Name: "active_tasks",
		Help: "Attempts currently held by this worker process.",
	}, []string{"pool"})
	WorkerAvailableSlots = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "worker", Name: "available_slots",
		Help: "Free task slots reported by this worker.",
	}, []string{"pool"})
	WorkerSlotsTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "worker", Name: "slots_total",
		Help: "Configured task slots for this worker.",
	}, []string{"pool"})

	AITokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "ai", Name: "tokens_total",
		Help: "Model tokens reported by the runtime.",
	}, []string{"provider", "direction"})
	ModelLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "agent_platform", Subsystem: "ai", Name: "model_latency_seconds",
		Help:    "Model call latency reported by the runtime.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"provider"})
	ProviderErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "ai", Name: "provider_errors_total",
		Help: "Provider failures by code.",
	}, []string{"provider", "code"})
	ToolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "agent_platform", Subsystem: "ai", Name: "tool_calls_total",
		Help: "Tool invocations reported by the runtime.",
	}, []string{"tool"})

	DBConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "db", Name: "connections",
		Help: "Database pool connections by state.",
	}, []string{"state"})
	NATSConnected = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "agent_platform", Subsystem: "nats", Name: "connected",
		Help: "1 when the service has an active NATS connection.",
	})
)

// HTTPMiddleware records bounded-cardinality request metrics. It must be installed before routes so it observes
// unmatched requests too.
func HTTPMiddleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			route := "unmatched"
			if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
				route = pattern
			}
			statusClass := strconv.Itoa(recorder.status/100) + "xx"
			HTTPRequests.WithLabelValues(service, r.Method, route, statusClass).Inc()
			HTTPDuration.WithLabelValues(service, r.Method, route).Observe(time.Since(start).Seconds())
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) { return r.ResponseWriter.Write(data) }

// Flush preserves Server-Sent Events through the metrics wrapper.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (r *statusRecorder) ReadFrom(source io.Reader) (int64, error) {
	if reader, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return reader.ReadFrom(source)
	}
	return io.Copy(r.ResponseWriter, source)
}
