package icmpservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"time"

	hcommon "github.com/hiddify/hiddify-core/v2/hcommon"
	"golang.org/x/net/icmp"
)

// session wraps one real OS ICMP socket, opened by OpenSession and released by CloseSession.
// One session per socket - this is a 1:1 relay, never a multiplexer.
type session struct {
	conn net.PacketConn
}

// IcmpService is the gRPC server that runs inside the elevated helper process. It's the only
// place in the whole system that still calls the real golang.org/x/net/icmp.ListenPacket after
// this feature ships - everywhere else goes through this service instead.
type IcmpService struct {
	UnimplementedIcmpServiceServer

	mu       sync.Mutex
	sessions map[string]*session

	onSessionCountChanged func(count int) // used by the idle-timeout watcher in icmp_platform_service.go
}

func NewIcmpService(onSessionCountChanged func(count int)) *IcmpService {
	return &IcmpService{
		sessions:              make(map[string]*session),
		onSessionCountChanged: onSessionCountChanged,
	}
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *IcmpService) notifyCountChanged() {
	if s.onSessionCountChanged == nil {
		return
	}
	s.onSessionCountChanged(len(s.sessions))
}

func (s *IcmpService) OpenSession(ctx context.Context, req *OpenSessionRequest) (*OpenSessionResponse, error) {
	var conn net.PacketConn
	var err error
	switch req.Family {
	case Family_V4:
		conn, err = icmp.ListenPacket("udp4", "0.0.0.0")
	case Family_V6:
		conn, err = icmp.ListenPacket("udp6", "::")
	default:
		return &OpenSessionResponse{Error: "unknown family"}, nil
	}
	if err != nil {
		return &OpenSessionResponse{Error: err.Error()}, nil
	}

	id := newSessionID()
	s.mu.Lock()
	s.sessions[id] = &session{conn: conn}
	s.notifyCountChanged()
	s.mu.Unlock()

	return &OpenSessionResponse{SessionId: id}, nil
}

func (s *IcmpService) getSession(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *IcmpService) ReadFrom(ctx context.Context, req *ReadFromRequest) (*ReadFromResponse, error) {
	sess := s.getSession(req.SessionId)
	if sess == nil {
		return &ReadFromResponse{Error: "unknown session"}, nil
	}

	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 65535
	}
	buf := make([]byte, maxBytes)
	n, addr, err := sess.conn.ReadFrom(buf)
	if err != nil {
		var netErr net.Error
		if e, ok := err.(net.Error); ok {
			netErr = e
		}
		if netErr != nil && netErr.Timeout() {
			return &ReadFromResponse{TimedOut: true}, nil
		}
		return &ReadFromResponse{Error: err.Error()}, nil
	}

	ip := addrIP(addr)
	return &ReadFromResponse{Data: buf[:n], AddrIp: ip}, nil
}

func (s *IcmpService) WriteTo(ctx context.Context, req *WriteToRequest) (*WriteToResponse, error) {
	sess := s.getSession(req.SessionId)
	if sess == nil {
		return &WriteToResponse{Error: "unknown session"}, nil
	}

	dst := &net.UDPAddr{IP: net.ParseIP(req.AddrIp)}
	n, err := sess.conn.WriteTo(req.Data, dst)
	if err != nil {
		return &WriteToResponse{Error: err.Error()}, nil
	}
	return &WriteToResponse{N: int32(n)}, nil
}

func (s *IcmpService) SetDeadlines(ctx context.Context, req *SetDeadlinesRequest) (*hcommon.Empty, error) {
	sess := s.getSession(req.SessionId)
	if sess == nil {
		return &hcommon.Empty{}, nil
	}
	if req.HasReadDeadline {
		if req.ClearReadDeadline {
			_ = sess.conn.SetReadDeadline(time.Time{})
		} else {
			_ = sess.conn.SetReadDeadline(time.Unix(0, req.ReadDeadlineUnixNano))
		}
	}
	if req.HasWriteDeadline {
		if req.ClearWriteDeadline {
			_ = sess.conn.SetWriteDeadline(time.Time{})
		} else {
			_ = sess.conn.SetWriteDeadline(time.Unix(0, req.WriteDeadlineUnixNano))
		}
	}
	return &hcommon.Empty{}, nil
}

func (s *IcmpService) CloseSession(ctx context.Context, req *CloseSessionRequest) (*hcommon.Empty, error) {
	s.mu.Lock()
	sess, ok := s.sessions[req.SessionId]
	if ok {
		delete(s.sessions, req.SessionId)
		s.notifyCountChanged()
	}
	s.mu.Unlock()
	if ok {
		_ = sess.conn.Close()
	}
	return &hcommon.Empty{}, nil
}

func (s *IcmpService) Exit(ctx context.Context, _ *hcommon.Empty) (*hcommon.Empty, error) {
	go requestExit()
	return &hcommon.Empty{}, nil
}

func addrIP(addr net.Addr) string {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP.String()
	case *net.IPAddr:
		return a.IP.String()
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err == nil {
			return host
		}
		return addr.String()
	}
}
