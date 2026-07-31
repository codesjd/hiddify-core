package hcore

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hiddify/hiddify-core/v2/hello"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// freePort asks the OS for an unused TCP port, closing the probe listener before returning so
// StartGrpcServerByMode's own IsPortInUse check (and its subsequent net.Listen) can claim it.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}

// TestGrpcServerExists covers plan 024: grpcServerExists must report false for a mode with no
// registered server, and true once an entry is present in the map (manipulated directly here,
// under mu, since the test lives in the same package - no real listener needed).
func TestGrpcServerExists(t *testing.T) {
	const mode = SetupMode_GRPC_NORMAL_INSECURE

	require.False(t, grpcServerExists(mode))

	mu.Lock()
	grpcServer[mode] = &grpc.Server{}
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(grpcServer, mode)
		mu.Unlock()
	}()

	require.True(t, grpcServerExists(mode))
}

// TestGrpcSecretInterceptorRejectsMissingOrWrongSecret covers plan 018 step 1: once a
// configuredSecret is set, calls without the matching "secret" metadata must be rejected with
// codes.Unauthenticated, and calls with the correct secret must succeed.
func TestGrpcSecretInterceptorRejectsMissingOrWrongSecret(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	configuredSecret = "the-correct-secret"
	defer func() { configuredSecret = "" }()

	_, err := StartGrpcServerByMode(addr, SetupMode_GRPC_NORMAL_INSECURE)
	require.NoError(t, err)
	defer CloseGrpcServer(SetupMode_GRPC_NORMAL_INSECURE)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := hello.NewHelloClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// No secret metadata at all -> rejected.
	_, err = client.SayHello(ctx, &hello.HelloRequest{Name: "world"})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	// Wrong secret -> rejected.
	wrongCtx := metadata.AppendToOutgoingContext(ctx, "secret", "not-it")
	_, err = client.SayHello(wrongCtx, &hello.HelloRequest{Name: "world"})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	// Correct secret -> succeeds.
	rightCtx := metadata.AppendToOutgoingContext(ctx, "secret", "the-correct-secret")
	resp, err := client.SayHello(rightCtx, &hello.HelloRequest{Name: "world"})
	require.NoError(t, err)
	require.Equal(t, "Hello, world", resp.Message)
}

// TestGrpcSecretInterceptorSoftModeAllowsAllWhenUnconfigured covers step 1's soft mode: an empty
// configuredSecret (the pre-fix state for any platform not yet updated) must not block calls.
func TestGrpcSecretInterceptorSoftModeAllowsAllWhenUnconfigured(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	configuredSecret = ""

	_, err := StartGrpcServerByMode(addr, SetupMode_GRPC_NORMAL_INSECURE)
	require.NoError(t, err)
	defer CloseGrpcServer(SetupMode_GRPC_NORMAL_INSECURE)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := hello.NewHelloClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.SayHello(ctx, &hello.HelloRequest{Name: "world"})
	require.NoError(t, err)
	require.Equal(t, "Hello, world", resp.Message)
}
