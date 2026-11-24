// Package natsconn creates NATS connections with the platform's defaults.
package natsconn

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/agent-platform/agent-platform/internal/platform/metrics"
)

// Connect dials NATS and waits for the server to answer a ping.
func Connect(ctx context.Context, url, name string) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name(name),
		nats.Timeout(5 * time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(*nats.Conn, error) { metrics.NATSConnected.Set(0) }),
		nats.ReconnectHandler(func(*nats.Conn) { metrics.NATSConnected.Set(1) }),
		nats.ClosedHandler(func(*nats.Conn) { metrics.NATSConnected.Set(0) }),
	}

	conn, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.FlushWithContext(pingCtx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats: flush: %w", err)
	}
	metrics.NATSConnected.Set(1)
	return conn, nil
}

// JetStream returns the JetStream context used by the orchestrator and worker.
func JetStream(conn *nats.Conn) (jetstream.JetStream, error) {
	js, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	return js, nil
}

// HealthCheck reports whether the connection is currently established.
func HealthCheck(conn *nats.Conn) func(context.Context) error {
	return func(context.Context) error {
		if !conn.IsConnected() {
			return fmt.Errorf("nats: not connected")
		}
		return nil
	}
}
