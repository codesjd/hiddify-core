package icmpservice

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeICMPConn is a net.PacketConn double standing in for a real ICMP socket in tests. Real ICMP
// addressing has no port (see WriteTo's server-side handler, which only ever sends an IP), which
// a genuine net.ListenUDP loopback conn can't faithfully emulate (plain UDP requires a valid
// destination port at the OS level) - this fake sidesteps that entirely by just recording/
// replaying packets in memory.
type fakeICMPConn struct {
	mu     sync.Mutex
	closed bool
	inbox  chan fakePacket

	readDeadline time.Time
}

type fakePacket struct {
	data []byte
	addr net.Addr
}

func newFakeICMPConn() *fakeICMPConn {
	return &fakeICMPConn{inbox: make(chan fakePacket, 16)}
}

// deliver simulates a packet arriving from the network, for ReadFrom to pick up.
func (f *fakeICMPConn) deliver(data []byte, addr net.Addr) {
	f.inbox <- fakePacket{data: data, addr: addr}
}

func (f *fakeICMPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	f.mu.Lock()
	deadline := f.readDeadline
	f.mu.Unlock()

	var timeoutCh <-chan time.Time
	if !deadline.IsZero() {
		if d := time.Until(deadline); d <= 0 {
			return 0, nil, timeoutError{}
		} else {
			timer := time.NewTimer(d)
			defer timer.Stop()
			timeoutCh = timer.C
		}
	}

	select {
	case p := <-f.inbox:
		n := copy(b, p.data)
		return n, p.addr, nil
	case <-timeoutCh:
		return 0, nil, timeoutError{}
	}
}

func (f *fakeICMPConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if f.closed {
		return 0, net.ErrClosed
	}
	// Loop the write back as an inbound packet from the same address, so tests can exercise a
	// full WriteTo -> ReadFrom round trip through the RPC layer without a real socket.
	cp := make([]byte, len(b))
	copy(cp, b)
	f.deliver(cp, addr)
	return len(b), nil
}

func (f *fakeICMPConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeICMPConn) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func (f *fakeICMPConn) SetDeadline(t time.Time) error {
	_ = f.SetReadDeadline(t)
	return nil
}

func (f *fakeICMPConn) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.readDeadline = t
	f.mu.Unlock()
	return nil
}

func (f *fakeICMPConn) SetWriteDeadline(t time.Time) error { return nil }

// timeoutError (defined in remote_conn.go) already satisfies net.Error - reused here rather than
// declaring a second one in the same package.

func newTestService(t *testing.T) (*IcmpService, string, *fakeICMPConn) {
	t.Helper()
	conn := newFakeICMPConn()
	svc := NewIcmpService(nil)
	id := "test-session"
	svc.sessions[id] = &session{conn: conn}
	return svc, id, conn
}

func TestIcmpServiceWriteThenReadRoundTrip(t *testing.T) {
	svc, id, _ := newTestService(t)

	ctx := context.Background()
	writeResp, err := svc.WriteTo(ctx, &WriteToRequest{
		SessionId: id,
		Data:      []byte("hello"),
		AddrIp:    "203.0.113.1",
	})
	if err != nil {
		t.Fatalf("WriteTo RPC: %v", err)
	}
	if writeResp.Error != "" {
		t.Fatalf("WriteTo response error: %s", writeResp.Error)
	}
	if writeResp.N != 5 {
		t.Fatalf("expected n=5, got %d", writeResp.N)
	}

	readResp, err := svc.ReadFrom(ctx, &ReadFromRequest{SessionId: id, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("ReadFrom RPC: %v", err)
	}
	if readResp.Error != "" {
		t.Fatalf("ReadFrom response error: %s", readResp.Error)
	}
	if string(readResp.Data) != "hello" {
		t.Fatalf("expected to read back %q, got %q", "hello", readResp.Data)
	}
	if readResp.AddrIp != "203.0.113.1" {
		t.Fatalf("expected source addr %q, got %q", "203.0.113.1", readResp.AddrIp)
	}
}

func TestIcmpServiceReadFromTimesOut(t *testing.T) {
	svc, id, _ := newTestService(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Second).UnixNano()
	_, err := svc.SetDeadlines(ctx, &SetDeadlinesRequest{
		SessionId:            id,
		HasReadDeadline:      true,
		ReadDeadlineUnixNano: past,
	})
	if err != nil {
		t.Fatalf("SetDeadlines RPC: %v", err)
	}

	resp, err := svc.ReadFrom(ctx, &ReadFromRequest{SessionId: id, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("ReadFrom RPC: %v", err)
	}
	if !resp.TimedOut {
		t.Fatalf("expected TimedOut=true for an already-expired deadline, got %+v", resp)
	}
}

func TestIcmpServiceUnknownSessionIsHandledGracefully(t *testing.T) {
	svc := NewIcmpService(nil)
	ctx := context.Background()

	if resp, err := svc.ReadFrom(ctx, &ReadFromRequest{SessionId: "does-not-exist"}); err != nil || resp.Error == "" {
		t.Fatalf("expected a graceful error for ReadFrom on an unknown session, got resp=%+v err=%v", resp, err)
	}
	if resp, err := svc.WriteTo(ctx, &WriteToRequest{SessionId: "does-not-exist"}); err != nil || resp.Error == "" {
		t.Fatalf("expected a graceful error for WriteTo on an unknown session, got resp=%+v err=%v", resp, err)
	}
	// SetDeadlines/CloseSession on an unknown session must not panic or error - the client-side
	// liveness probe (pingHelper in admin_service_commander.go) deliberately calls CloseSession
	// with a session id that can never exist.
	if _, err := svc.SetDeadlines(ctx, &SetDeadlinesRequest{SessionId: "does-not-exist"}); err != nil {
		t.Fatalf("SetDeadlines on an unknown session must not error, got: %v", err)
	}
	if _, err := svc.CloseSession(ctx, &CloseSessionRequest{SessionId: "does-not-exist"}); err != nil {
		t.Fatalf("CloseSession on an unknown session must not error, got: %v", err)
	}
}

func TestIcmpServiceCloseSessionRemovesItAndNotifiesCount(t *testing.T) {
	var counts []int
	svc := NewIcmpService(func(count int) { counts = append(counts, count) })

	conn := newFakeICMPConn()
	id := "test-session"
	svc.sessions[id] = &session{conn: conn}
	svc.notifyCountChanged()

	ctx := context.Background()
	if _, err := svc.CloseSession(ctx, &CloseSessionRequest{SessionId: id}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	if _, ok := svc.sessions[id]; ok {
		t.Fatalf("expected session to be removed from the map after CloseSession")
	}
	if len(counts) < 2 || counts[len(counts)-1] != 0 {
		t.Fatalf("expected the last session-count notification to be 0 after closing the only session, got %v", counts)
	}
	if !conn.closed {
		t.Fatalf("expected the underlying conn to be closed after CloseSession")
	}
}
