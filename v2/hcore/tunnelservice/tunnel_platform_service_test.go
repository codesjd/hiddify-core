package tunnelservice

import (
	"context"
	"net"
	"testing"
	"time"

	hcommon "github.com/hiddify/hiddify-core/v2/hcommon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestTunnelGrpcServerTokenAuth(t *testing.T) {
	const expectedToken = "correct-token"

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()

	m := &hiddifyNext{}
	s, err := m.StartTunnelGrpcServer(addr, expectedToken)
	if err != nil {
		t.Fatalf("StartTunnelGrpcServer: %v", err)
	}
	defer s.Stop()

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	c := NewTunnelServiceClient(conn)

	// Missing token metadata.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.Status(ctx, &hcommon.Empty{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated with no token, got: %v", err)
	}

	// Wrong token.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	ctx2 = metadata.AppendToOutgoingContext(ctx2, "token", "wrong-token")
	_, err = c.Status(ctx2, &hcommon.Empty{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated with wrong token, got: %v", err)
	}

	// Correct token.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	ctx3 = metadata.AppendToOutgoingContext(ctx3, "token", expectedToken)
	resp, err := c.Status(ctx3, &hcommon.Empty{})
	if err != nil {
		t.Fatalf("expected success with correct token, got: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected a response with correct token")
	}
}
