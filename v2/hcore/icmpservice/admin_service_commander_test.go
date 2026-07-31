package icmpservice

import (
	"testing"
	"time"
)

// TestHelperStartupTimeoutIsGenerouslyLong is a deliberately weak floor check, not a behavior test:
// its only job is to fail loudly if a future edit accidentally shrinks helperStartupTimeout back
// down without anyone noticing. It cannot validate the real UAC/elevation flow, which needs a real
// Windows session (out of reach for this repo's test suite, same as plans 019/020).
func TestHelperStartupTimeoutIsGenerouslyLong(t *testing.T) {
	if helperStartupTimeout < 30*time.Second {
		t.Fatalf("helperStartupTimeout = %v, want at least 30s to cover a human UAC click-through", helperStartupTimeout)
	}
}
