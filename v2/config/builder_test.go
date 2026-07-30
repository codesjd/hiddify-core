package config

import (
	"sync"
	"testing"
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
