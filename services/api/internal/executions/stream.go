package executions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/pagination"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

// Stream writes execution audit events as Server-Sent Events. It closes after delivering all events of a
// terminal execution, so a client can follow an execution to completion with one request.
func (h *Handlers) Stream(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	projectID := chi.URLParam(r, "project_id")
	executionID := chi.URLParam(r, "execution_id")
	after := int64(0)
	rawAfter := r.URL.Query().Get("after")
	if rawAfter == "" {
		rawAfter = r.Header.Get("Last-Event-ID")
	}
	if rawAfter != "" {
		parsed, err := strconv.ParseInt(rawAfter, 10, 64)
		if err != nil || parsed < 0 {
			respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "after must be a non-negative event sequence")
			return
		}
		after = parsed
	}
	if _, err := h.store.Get(r.Context(), membership.OrgID, projectID, executionID); err != nil {
		h.respondStoreError(w, r, err, "could not load execution")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 2000\n: connected\n\n")
	flusher.Flush()

	limit := pagination.Limit(r, 200, 500)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	for {
		events, err := h.store.ListEvents(r.Context(), membership.OrgID, projectID, executionID, after, limit)
		if err != nil {
			return
		}
		for _, event := range events {
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.EventType, payload)
			after = event.Seq
		}
		if len(events) > 0 {
			flusher.Flush()
			continue
		}
		execution, err := h.store.Get(r.Context(), membership.OrgID, projectID, executionID)
		if err != nil {
			return
		}
		if terminalStreamState(execution.State) {
			flusher.Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-poll.C:
		}
	}
}

func terminalStreamState(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}
