package icmpservice

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestIcmpGrpcServerTokenAuth(t *testing.T) {
	const expectedToken = "correct-token"

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	svc := NewIcmpService(func(int) {})
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(tokenAuthInterceptor(expectedToken)))
	RegisterIcmpServiceServer(server, svc)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	addr := lis.Addr().String()
	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	c := NewIcmpServiceClient(conn)

	// Missing token metadata.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.CloseSession(ctx, &CloseSessionRequest{SessionId: "__nonexistent__"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated with no token, got: %v", err)
	}

	// Wrong token.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	ctx2 = metadata.AppendToOutgoingContext(ctx2, "token", "wrong-token")
	_, err = c.CloseSession(ctx2, &CloseSessionRequest{SessionId: "__nonexistent__"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated with wrong token, got: %v", err)
	}

	// Correct token.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	ctx3 = metadata.AppendToOutgoingContext(ctx3, "token", expectedToken)
	resp, err := c.CloseSession(ctx3, &CloseSessionRequest{SessionId: "__nonexistent__"})
	if err != nil {
		t.Fatalf("expected success with correct token, got: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected a response with correct token")
	}
}
