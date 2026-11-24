package executions

import (
	"context"
	"crypto/tls"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
)

// OrchestratorClient is the API's only path to execution state changes.
type OrchestratorClient struct {
	conn   *grpc.ClientConn
	client orchestratorv1.ExecutionCommandsClient
}

func DialOrchestrator(addr string, insecureConn bool, token string, log *zap.Logger) (*OrchestratorClient, error) {
	if addr == "" {
		return nil, fmt.Errorf("orchestrator address is required")
	}
	var credential credentials.TransportCredentials
	if insecureConn {
		credential = insecure.NewCredentials()
	} else {
		credential = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credential),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any,
			conn *grpc.ClientConn, invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-internal-token", token)
			return invoker(ctx, method, req, reply, conn, callOpts...)
		}))
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial orchestrator: %w", err)
	}
	log.Info("orchestrator client configured", zap.String("addr", addr), zap.Bool("insecure", insecureConn))
	return &OrchestratorClient{conn: conn, client: orchestratorv1.NewExecutionCommandsClient(conn)}, nil
}

func (c *OrchestratorClient) Submit(ctx context.Context, req *orchestratorv1.SubmitExecutionRequest) (*orchestratorv1.SubmitExecutionResponse, error) {
	return c.client.SubmitExecution(ctx, req)
}

func (c *OrchestratorClient) Cancel(ctx context.Context,
	req *orchestratorv1.CancelExecutionRequest) (*orchestratorv1.CancelExecutionResponse, error) {
	return c.client.CancelExecution(ctx, req)
}

func (c *OrchestratorClient) SignalTask(ctx context.Context,
	req *orchestratorv1.SignalTaskRequest) (*orchestratorv1.SignalTaskResponse, error) {
	return c.client.SignalTask(ctx, req)
}

func (c *OrchestratorClient) Close() error {
	return c.conn.Close()
}
