// Package httpserver provides the shared HTTP surface for all Go services: middleware, liveness, readiness and
// graceful shutdown. Product routes are attached by each service.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// Check is a readiness probe. Returning an error makes /readyz return 503.
type Check func(context.Context) error

// Options configures a server. Middleware is installed before the built-in health routes so services can mount
// CORS, auth or tracing without tripping chi's "middleware before routes" rule.
type Options struct {
	Addr        string
	Logger      *zap.Logger
	Middlewares []func(http.Handler) http.Handler
	Checks      []Check
}

// Server is an http.Server with a chi router.
type Server struct {
	*http.Server
	router chi.Router
	log    *zap.Logger
}

// New builds a server. checks are evaluated on every readiness request.
func New(opts Options) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// middleware.RealIP is deliberately not used: it trusts client-supplied X-Forwarded-For values and is
	// deprecated for IP spoofing. A trusted-proxy implementation belongs here when a proxy tier exists.
	r.Use(middleware.Recoverer)
	for _, mw := range opts.Middlewares {
		r.Use(mw)
	}
	r.Use(requestLogger(opts.Logger))
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		for _, check := range opts.Checks {
			if err := check(req.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	return &Server{
		Server: &http.Server{
			Addr:              opts.Addr,
			Handler:           r,
			ReadHeaderTimeout: 10 * time.Second,
		},
		router: r,
		log:    opts.Logger,
	}
}

// Router exposes the chi router so services can mount their own routes.
func (s *Server) Router() chi.Router { return s.router }

// Start serves until ctx is cancelled, then shuts down with a grace period.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http listening", zap.String("addr", s.Addr))
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.Shutdown(shutdownCtx)
	}
}

func requestLogger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("http request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rec.status),
				zap.Duration("duration", time.Since(start)),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			)
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

// Flush keeps SSE usable through the logging wrapper.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// WriteJSON is exported for product handlers that want consistent encoding.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already committed; encoding errors can only truncate the body.
	_ = json.NewEncoder(w).Encode(body)
}
