package config

import (
	"sync"
	"testing"

	"github.com/sagernet/sing-box/option"
)

// getIPs guards its ipMaps cache with ipMapsMutex; a cache hit must short-circuit the live DNS
// lookup entirely rather than only being consulted as a fallback after a failed lookup. Using a
// domain that cannot resolve (an .invalid TLD, reserved by RFC 2606) proves the short-circuit: if
// the live lookup ran instead of returning the seeded cache entry, the result would come back
// empty.
func TestGetIPs_CacheShortCircuit(t *testing.T) {
	const domain = "this-domain-does-not-exist.invalid"
	seeded := []string{"203.0.113.1"}

	ipMapsMutex.Lock()
	ipMaps[domain] = seeded
	ipMapsMutex.Unlock()
	t.Cleanup(func() {
		ipMapsMutex.Lock()
		delete(ipMaps, domain)
		ipMapsMutex.Unlock()
	})

	got := getIPs(domain)
	if len(got) != 1 || got[0] != seeded[0] {
		t.Fatalf("getIPs(%q) = %v, want seeded cache value %v (live lookup should have been skipped)", domain, got, seeded)
	}
}

// getIPs is reachable concurrently (isBlockedDomain runs on every BuildConfig call, i.e. every
// StartService/Restart). Both the read and write of ipMaps must go through ipMapsMutex - run with
// `go test -race` to confirm no data race is reported.
func TestGetIPs_ConcurrentAccessNoRace(t *testing.T) {
	const domain = "concurrent-getips-test.invalid"
	seeded := []string{"203.0.113.2"}

	ipMapsMutex.Lock()
	ipMaps[domain] = seeded
	ipMapsMutex.Unlock()
	t.Cleanup(func() {
		ipMapsMutex.Lock()
		delete(ipMaps, domain)
		ipMapsMutex.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getIPs(domain)
		}()
	}
	wg.Wait()
}

// getIPs on an empty domain list must not panic on domains[0].
func TestGetIPs_NoDomains(t *testing.T) {
	if got := getIPs(); got != nil {
		t.Fatalf("getIPs() = %v, want nil", got)
	}
}

// setOutbounds used to panic on tags[0] when input has zero outbounds/endpoints and Warp is
// disabled, since tags stays empty. A degenerate/empty config should produce a selector with no
// user outbounds, not crash the whole core.
func TestSetOutbounds_NoOutbounds_DoesNotPanic(t *testing.T) {
	opt := DefaultHiddifyOptions()
	input := &option.Options{}
	options := &option.Options{}
	staticIPs := map[string][]string{}

	err := setOutbounds(options, input, opt, &staticIPs)
	if err != nil {
		t.Fatalf("setOutbounds returned an error for an empty config: %v", err)
	}
	if len(options.Outbounds) == 0 {
		t.Fatal("expected setOutbounds to still produce the selector/direct outbounds")
	}
}

// setInbound must set an explicit, fixed InterfaceName on the TUN inbound so the adapter can be
// reliably identified across runs (firewall rules, diagnostics, support instructions), and must
// pass through a valid TUNStack unchanged.
func TestSetInbound_TUN_ValidStack_SetsInterfaceName(t *testing.T) {
	opt := DefaultHiddifyOptions()
	opt.EnableTun = true
	opt.TUNStack = "gvisor"
	options := &option.Options{}

	if err := setInbound(options, opt); err != nil {
		t.Fatalf("setInbound returned an error for a valid stack: %v", err)
	}

	var tunOpts *option.TunInboundOptions
	for _, inbound := range options.Inbounds {
		if inbound.Tag == InboundTUNTag {
			tunOpts = inbound.Options.(*option.TunInboundOptions)
		}
	}
	if tunOpts == nil {
		t.Fatal("expected a tun inbound to be added")
	}
	if tunOpts.Stack != "gvisor" {
		t.Fatalf("tunOpts.Stack = %q, want %q", tunOpts.Stack, "gvisor")
	}
	if tunOpts.InterfaceName != "HiddifyTun" {
		t.Fatalf("tunOpts.InterfaceName = %q, want %q", tunOpts.InterfaceName, "HiddifyTun")
	}
}

// An unrecognized TUNStack (e.g. from an imported/hand-edited config JSON, which is not
// enum-constrained the way the Dart UI is) must be rejected at this boundary rather than reaching
// sing-tun's own adapter construction unchecked.
func TestSetInbound_TUN_InvalidStack_ReturnsError(t *testing.T) {
	opt := DefaultHiddifyOptions()
	opt.EnableTun = true
	opt.TUNStack = "bogus"
	options := &option.Options{}

	err := setInbound(options, opt)
	if err == nil {
		t.Fatal("expected setInbound to return an error for an invalid TUNStack, got nil")
	}
}
