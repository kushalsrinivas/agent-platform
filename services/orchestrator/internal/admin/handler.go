// Package admin exposes the orchestrator's operator-only messaging status and DLQ redrive endpoints.
package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/messaging"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/lifecycle"
)

// Handler exposes operator-only messaging visibility and controlled DLQ redrive.
type Handler struct {
	service *lifecycle.Service
	js      jetstream.JetStream
	token   string
	log     *zap.Logger
}

// New builds the operator surface. An empty token disables every route.
func New(service *lifecycle.Service, js jetstream.JetStream, token string, log *zap.Logger) *Handler {
	return &Handler{service: service, js: js, token: token, log: log}
}

// Routes mounts the operator endpoints.
func (h *Handler) Routes(router chi.Router) {
	router.Get("/v1/messaging/status", h.status)
	router.Post("/v1/messaging/dlq/redrive", h.redrive)
}

type streamStatus struct {
	Messages  uint64 `json:"messages"`
	Bytes     uint64 `json:"bytes"`
	Consumers int    `json:"consumers"`
	MaxAge    string `json:"max_age,omitempty"`
}

type consumerStatus struct {
	Name          string     `json:"name"`
	AckPending    int        `json:"ack_pending"`
	Redelivered   int        `json:"redelivered"`
	NumPending    uint64     `json:"pending"`
	DeliveredSeq  uint64     `json:"delivered_sequence"`
	AckFloorSeq   uint64     `json:"ack_floor_sequence"`
	LastDelivered *time.Time `json:"last_delivered_at,omitempty"`
}

type statusResponse struct {
	Outbox    lifecycle.OutboxStats       `json:"outbox"`
	Streams   map[string]streamStatus     `json:"streams"`
	Consumers map[string][]consumerStatus `json:"consumers"`
	Workers   []lifecycle.WorkerSnapshot  `json:"workers"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	ctx := r.Context()
	outboxStats, err := h.service.OutboxStats(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	response := statusResponse{
		Outbox:    outboxStats,
		Streams:   map[string]streamStatus{},
		Consumers: map[string][]consumerStatus{},
	}
	for _, config := range messaging.Streams(messaging.StreamsConfig{}) {
		stream, err := h.js.Stream(ctx, config.Name)
		if err != nil {
			h.log.Warn("stream lookup failed", zap.String("stream", config.Name), zap.Error(err))
			continue
		}
		info, err := stream.Info(ctx)
		if err != nil {
			h.log.Warn("stream info failed", zap.String("stream", config.Name), zap.Error(err))
			continue
		}
		response.Streams[config.Name] = streamStatus{
			Messages:  info.State.Msgs,
			Bytes:     info.State.Bytes,
			Consumers: info.State.Consumers,
			MaxAge:    config.MaxAge.String(),
		}
		if config.Name == messaging.StreamTaskDispatch {
			lister := stream.ListConsumers(ctx)
			for consumerInfo := range lister.Info() {
				response.Consumers[config.Name] = append(response.Consumers[config.Name], consumerStatus{
					Name:          consumerInfo.Name,
					AckPending:    consumerInfo.NumAckPending,
					Redelivered:   consumerInfo.NumRedelivered,
					NumPending:    consumerInfo.NumPending,
					DeliveredSeq:  consumerInfo.Delivered.Stream,
					AckFloorSeq:   consumerInfo.AckFloor.Stream,
					LastDelivered: consumerInfo.Delivered.Last,
				})
			}
			if err := lister.Err(); err != nil {
				h.log.Warn("consumer listing failed", zap.Error(err))
			}
		}
	}
	workers, err := h.service.Workers(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	response.Workers = workers
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) redrive(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	var body struct {
		WorkerID string `json:"worker_id"`
		Limit    int    `json:"limit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.WorkerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker_id is required"})
		return
	}
	if body.Limit == 0 {
		body.Limit = 10
	}
	redriven, err := messaging.RedriveDLQ(r.Context(), h.js, body.WorkerID, body.Limit, h.log)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"worker_id": body.WorkerID, "redriven": redriven})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if h.token == "" {
		http.NotFound(w, r)
		return false
	}
	expected := []byte("Bearer " + h.token)
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "operator token required"})
		return false
	}
	return true
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	h.log.Error("operator request failed", zap.Error(err))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
