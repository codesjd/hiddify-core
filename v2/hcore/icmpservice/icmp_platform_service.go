package icmpservice

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	hutils "github.com/hiddify/hiddify-core/v2/hutils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// icmpServicePort is deliberately different from tunnelservice's 18020 - this is an independent
// service with its own lifecycle, not a piggyback on the TUN helper (see the elevated-ICMP-helper
// plan: xicmp usage doesn't correlate with TUN usage, and coupling them would mean a bug in one
// helper's relay logic can take an unrelated feature down with it).
const icmpServicePort uint16 = 18021

// idleTimeout is how long the helper waits with zero open sessions before exiting on its own.
// Unlike TUN (installed as a persistent Windows Service since it's commonly active for an entire
// session), xicmp is one obfuscation transport among several and likely used far less
// continuously - a lingering elevated process for a rarely-used feature is not a proportionate
// permanent footprint, so this helper prefers to just exit and be re-elevated on demand next time.
const idleTimeout = 8 * time.Minute

var (
	exitOnce   sync.Once
	exitSignal = make(chan struct{})
)

func requestExit() {
	exitOnce.Do(func() { close(exitSignal) })
}

type idleWatcher struct {
	mu       sync.Mutex
	timer    *time.Timer
	sessions int
}

func newIdleWatcher() *idleWatcher {
	w := &idleWatcher{}
	w.timer = time.AfterFunc(idleTimeout, func() {
		log.Printf("icmpservice: idle for %s with no open sessions, exiting", idleTimeout)
		requestExit()
	})
	return w
}

func (w *idleWatcher) onSessionCountChanged(count int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessions = count
	if count > 0 {
		w.timer.Stop()
	} else {
		w.timer.Reset(idleTimeout)
	}
}

// tokenAuthInterceptor rejects any unary RPC whose incoming gRPC metadata "token" key doesn't
// match expected, using a constant-time comparison. Same shape as tunnelservice's interceptor
// (plan 019) - duplicated rather than shared since the two services are otherwise unrelated.
func tokenAuthInterceptor(expected string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok || subtle.ConstantTimeCompare([]byte(strings.Join(md.Get("token"), "")), []byte(expected)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid token")
		}
		return handler(ctx, req)
	}
}

// StartIcmpService is the entry point invoked from HiddifyCli.exe's "icmp run" subcommand
// (see hiddify-core/cmd/cmd_icmp_service.go). It starts a plain, loopback-only gRPC server and
// blocks until told to exit (via the Exit RPC or the idle timeout above) or the port is
// unavailable.
func StartIcmpService() (int, string) {
	watcher := newIdleWatcher()
	svc := NewIcmpService(watcher.onSessionCountChanged)

	dir, err := executableDir()
	if err != nil {
		return 1, fmt.Sprintf("failed to resolve executable directory: %v", err)
	}
	token, err := hutils.GenerateAndPersistServiceToken(dir, "icmp")
	if err != nil {
		return 1, fmt.Sprintf("failed to generate auth token: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", icmpServicePort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return 1, fmt.Sprintf("failed to listen on %s: %v", addr, err)
	}

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(tokenAuthInterceptor(token)))
	RegisterIcmpServiceServer(server, svc)

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Printf("icmpservice: server stopped: %v", err)
		}
	}()

	<-exitSignal
	server.GracefulStop()
	return 0, "OK"
}
