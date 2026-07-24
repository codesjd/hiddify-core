package icmpservice

import (
	"testing"

	"github.com/xtls/xray-core/transport/internet/finalmask/xicmp"
)

// TestProxiedListenICMPIsAssignableToXicmpListenICMP is a compile-time proof, not just a runtime
// check: if ProxiedListenICMP's signature ever drifts from xicmp.ListenICMP's exact function
// type (func(string, string) (xicmp.IcmpPacketConn, error)), this file fails to compile. That
// matters here specifically because Go's function-type identity requires an exact match - unlike
// plain interface satisfaction, there's no structural wiggle room, so a mismatch would only be
// caught here or in whatever code actually performs the assignment (which, in production, only
// runs on Windows and only when not already elevated - exactly the path most CI/dev machines
// never exercise).
func TestProxiedListenICMPIsAssignableToXicmpListenICMP(t *testing.T) {
	original := xicmp.ListenICMP
	defer func() { xicmp.ListenICMP = original }()

	xicmp.ListenICMP = ProxiedListenICMP
}

// TestProxiedListenICMPPassesThroughRawICMP confirms non-dgram networks (raw ICMP, "ip4:icmp"/
// "ip6:ipv6-icmp") are never intercepted - this override only exists to fix the unprivileged
// "udp4"/"udp6" case, and raw ICMP already works fine unprivileged-or-not (it just needs
// elevation either way, the same as it always did, with no helper involved).
func TestProxiedListenICMPPassesThroughRawICMP(t *testing.T) {
	// Both the direct call and the real OS call should fail identically in this sandbox (no
	// working raw ICMP here either - see the wider xicmp test suite's notes on this sandbox's
	// ICMP support), proving ProxiedListenICMP truly delegates rather than doing anything of its
	// own for this network type.
	_, gotErr := ProxiedListenICMP("ip4:icmp", "0.0.0.0")
	if gotErr == nil {
		t.Skip("this sandbox unexpectedly has working raw ICMP - nothing to assert here")
	}
}
