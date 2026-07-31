package icmpservice

import (
	"github.com/xtls/xray-core/transport/internet/finalmask/xicmp"
	"golang.org/x/net/icmp"
)

// ProxiedListenICMP matches xicmp.ListenICMP's exact function type (func(string, string)
// (xicmp.IcmpPacketConn, error)) so it can be assigned directly to that var. Only "udp4"/"udp6"
// (the unprivileged "dgram" mode xicmp uses, and the only mode that actually needs elevation on
// Windows) are proxied through the elevated helper; anything else (raw ICMP) passes straight
// through to the real OS call untouched, so this override only ever changes behavior for the one
// case it exists to fix.
func ProxiedListenICMP(network, address string) (xicmp.IcmpPacketConn, error) {
	switch network {
	case "udp4", "udp6":
		return DialRemoteICMP(network)
	default:
		return icmp.ListenPacket(network, address)
	}
}
