// Package grpcserver provides the shared gRPC listener for the control plane: health, reflection and graceful
// stop. Product services are registered on the returned server.
package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Server is a gRPC server bound to a listener.
type Server struct {
	*grpc.Server
	lis net.Listener
	log *zap.Logger
}

// New binds addr and registers the standard health service. Reflection is enabled only when env is development.
func New(addr, env string, log *zap.Logger, opts ...grpc.ServerOption) (*Server, error) {
	var listenConfig net.ListenConfig
	lis, err := listenConfig.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("grpc: listen %s: %w", addr, err)
	}

	srv := grpc.NewServer(opts...)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	if env == "development" {
		reflection.Register(srv)
	}

	return &Server{Server: srv, lis: lis, log: log}, nil
}

// Start serves until ctx is cancelled, then stops accepting new RPCs and waits for in-flight work.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("grpc listening", zap.String("addr", s.lis.Addr().String()))
		if err := s.Serve(s.lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		done := make(chan struct{})
		go func() {
			s.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			s.Stop()
		}
		return nil
	}
}
