package admin_service_vpn

import (
	"testing"

	"github.com/sagernet/sing-box/option"

	hutils "github.com/hiddify/hiddify-core/v2/hutils"
	C "github.com/sagernet/sing-box/constant"
)

// TestOnMainServicePreStart_StripsTunAndCapturesMixedInbound proves that,
// once the process is not already TUN-allowed, OnMainServicePreStart pulls
// the TUN inbound out of the config (leaving only the Mixed inbound) and
// captures both inbounds' options via pointer type assertions (builder.go
// always stores *option.TunInboundOptions / *option.HTTPMixedInboundOptions,
// not values - see plan 039).
func TestOnMainServicePreStart_StripsTunAndCapturesMixedInbound(t *testing.T) {
	ext := &AdminServiceExtension{}

	tunOpts := &option.TunInboundOptions{StrictRoute: true, Stack: "gvisor"}
	mixedOpts := &option.HTTPMixedInboundOptions{
		ListenOptions: option.ListenOptions{ListenPort: 1080},
	}
	cfg := &option.Options{
		Inbounds: []option.Inbound{
			{Type: C.TypeTun, Tag: "tun-in", Options: tunOpts},
			{Type: C.TypeMixed, Tag: "mixed-in", Options: mixedOpts},
		},
	}

	if err := ext.OnMainServicePreStart(cfg); err != nil {
		t.Fatalf("OnMainServicePreStart: %v", err)
	}

	if hutils.TunAllowed() {
		// This process already has TUN privileges (common in CI/dev
		// sandboxes, same caveat admin_icmp_service_test.go documents) -
		// OnMainServicePreStart returns immediately without stripping
		// anything, so the strip-behavior assertions below don't apply.
		t.Skip("process is already TUN-allowed; strip behavior only exercises on a non-privileged process")
	}

	if len(cfg.Inbounds) != 1 || cfg.Inbounds[0].Type != C.TypeMixed {
		t.Fatalf("expected only the Mixed inbound to remain, got %+v", cfg.Inbounds)
	}
	if ext.tunInboundOptions != tunOpts {
		t.Fatalf("expected tunInboundOptions to be captured, got %v", ext.tunInboundOptions)
	}
	if ext.mixedOptions != mixedOpts {
		t.Fatalf("expected mixedOptions to be captured, got %v", ext.mixedOptions)
	}
}

// TestOnMainServiceStart_NoMixedInboundIsNoop proves that when no Mixed
// inbound was found (mixedOptions stays nil), OnMainServiceStart returns nil
// without attempting to dial the tunnel service - there is nothing to
// connect the TUN inbound's outbound proxy to.
func TestOnMainServiceStart_NoMixedInboundIsNoop(t *testing.T) {
	ext := &AdminServiceExtension{
		tunInboundOptions: &option.TunInboundOptions{},
		mixedOptions:      nil,
	}

	if err := ext.OnMainServiceStart(); err != nil {
		t.Fatalf("expected no-op nil error, got %v", err)
	}
}
