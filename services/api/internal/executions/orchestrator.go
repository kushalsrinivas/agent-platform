package executions

import (
	"context"
	"crypto/tls"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
)

// OrchestratorClient is the API's only path to execution state changes.
type OrchestratorClient struct {
	conn   *grpc.ClientConn
	client orchestratorv1.ExecutionCommandsClient
}

func DialOrchestrator(addr string, insecureConn bool, log *zap.Logger) (*OrchestratorClient, error) {
	if addr == "" {
		return nil, fmt.Errorf("orchestrator address is required")
	}
	var credential credentials.TransportCredentials
	if insecureConn {
		credential = insecure.NewCredentials()
	} else {
		credential = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credential))
	if err != nil {
		return nil, fmt.Errorf("dial orchestrator: %w", err)
	}
	log.Info("orchestrator client configured", zap.String("addr", addr), zap.Bool("insecure", insecureConn))
	return &OrchestratorClient{conn: conn, client: orchestratorv1.NewExecutionCommandsClient(conn)}, nil
}

func (c *OrchestratorClient) Submit(ctx context.Context, req *orchestratorv1.SubmitExecutionRequest) (*orchestratorv1.SubmitExecutionResponse, error) {
	return c.client.SubmitExecution(ctx, req)
}

func (c *OrchestratorClient) Close() error {
	return c.conn.Close()
}
