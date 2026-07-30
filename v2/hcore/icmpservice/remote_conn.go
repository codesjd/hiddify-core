package icmpservice

import (
	"context"
	"fmt"
	"net"
	"time"

	grpc "google.golang.org/grpc"
)

// RemoteICMPConn implements the same 6-method shape xicmp's unexported icmpPacketConn interface
// declares (ReadFrom/WriteTo/Close/SetDeadline/SetReadDeadline/SetWriteDeadline) by forwarding
// each call to the elevated helper over gRPC. Go interfaces are satisfied structurally, so this
// type needs no shared interface declaration with hiddify-sing-box's vendored xicmp package -
// assigning a function that returns *RemoteICMPConn to xicmp.ListenICMP (a
// func(string, string) (icmpPacketConn, error) var) just works.
type RemoteICMPConn struct {
	conn      *grpc.ClientConn
	client    IcmpServiceClient
	sessionID string
	// authCtx carries the auth token as outgoing metadata (no deadline of its own) - every RPC
	// after the initial OpenSession must also present the token, since the server-side interceptor
	// checks it per-call, not per-connection.
	authCtx context.Context
}

// DialRemoteICMP ensures the elevated helper is running, opens a new session for the given
// family ("udp4" or "udp6" - xicmp's own network strings), and returns a conn proxying that
// session.
func DialRemoteICMP(network string) (*RemoteICMPConn, error) {
	if err := EnsureIcmpHelperRunning(); err != nil {
		return nil, err
	}

	var family Family
	switch network {
	case "udp4":
		family = Family_V4
	case "udp6":
		family = Family_V6
	default:
		return nil, fmt.Errorf("icmpservice: unsupported network %q", network)
	}

	conn, dialCtx, _, err := dialIcmpService()
	if err != nil {
		return nil, fmt.Errorf("icmpservice: dial helper: %w", err)
	}

	client := NewIcmpServiceClient(conn)
	ctx, cancel := context.WithTimeout(dialCtx, 5*time.Second)
	defer cancel()
	resp, err := client.OpenSession(ctx, &OpenSessionRequest{Family: family})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("icmpservice: open session: %w", err)
	}
	if resp.Error != "" {
		conn.Close()
		return nil, fmt.Errorf("icmpservice: open session: %s", resp.Error)
	}

	return &RemoteICMPConn{conn: conn, client: client, sessionID: resp.SessionId, authCtx: dialCtx}, nil
}

func (r *RemoteICMPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	ctx := r.authCtx
	resp, err := r.client.ReadFrom(ctx, &ReadFromRequest{SessionId: r.sessionID, MaxBytes: int32(len(b))})
	if err != nil {
		return 0, nil, err
	}
	if resp.Error != "" {
		return 0, nil, fmt.Errorf("icmpservice: %s", resp.Error)
	}
	if resp.TimedOut {
		return 0, nil, timeoutError{}
	}
	n := copy(b, resp.Data)
	return n, &net.IPAddr{IP: net.ParseIP(resp.AddrIp)}, nil
}

func (r *RemoteICMPConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	ip := addrIP(addr)
	ctx := r.authCtx
	resp, err := r.client.WriteTo(ctx, &WriteToRequest{SessionId: r.sessionID, Data: b, AddrIp: ip})
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("icmpservice: %s", resp.Error)
	}
	return int(resp.N), nil
}

func (r *RemoteICMPConn) Close() error {
	ctx, cancel := context.WithTimeout(r.authCtx, 2*time.Second)
	defer cancel()
	_, _ = r.client.CloseSession(ctx, &CloseSessionRequest{SessionId: r.sessionID})
	return r.conn.Close()
}

func (r *RemoteICMPConn) SetDeadline(t time.Time) error {
	req := &SetDeadlinesRequest{SessionId: r.sessionID, HasReadDeadline: true, HasWriteDeadline: true}
	setDeadlineField(t, &req.ClearReadDeadline, &req.ReadDeadlineUnixNano)
	setDeadlineField(t, &req.ClearWriteDeadline, &req.WriteDeadlineUnixNano)
	return r.sendSetDeadlines(req)
}

func (r *RemoteICMPConn) SetReadDeadline(t time.Time) error {
	req := &SetDeadlinesRequest{SessionId: r.sessionID, HasReadDeadline: true}
	setDeadlineField(t, &req.ClearReadDeadline, &req.ReadDeadlineUnixNano)
	return r.sendSetDeadlines(req)
}

func (r *RemoteICMPConn) SetWriteDeadline(t time.Time) error {
	req := &SetDeadlinesRequest{SessionId: r.sessionID, HasWriteDeadline: true}
	setDeadlineField(t, &req.ClearWriteDeadline, &req.WriteDeadlineUnixNano)
	return r.sendSetDeadlines(req)
}

// setDeadlineField encodes t into either the clear flag (t.IsZero(), meaning "block forever" per
// net.Conn's contract - see icmp_service.proto's comment on why this can't be inferred from a
// zero nanosecond value) or the absolute-nanosecond field.
func setDeadlineField(t time.Time, clear *bool, nanos *int64) {
	if t.IsZero() {
		*clear = true
		return
	}
	*nanos = t.UnixNano()
}

func (r *RemoteICMPConn) sendSetDeadlines(req *SetDeadlinesRequest) error {
	ctx, cancel := context.WithTimeout(r.authCtx, 2*time.Second)
	defer cancel()
	_, err := r.client.SetDeadlines(ctx, req)
	return err
}

// timeoutError satisfies net.Error so xicmp's existing goerrors.As(err, &netErr) &&
// netErr.Timeout() checks in recv4/recv6 keep working unmodified against a proxied conn.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
