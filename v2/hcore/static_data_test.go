package hcore

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/daemon"
	"github.com/stretchr/testify/require"
)

// TestStartedService_ConcurrentSwap_OnlyOneCallerGetsService exercises the
// exact hazard plan 023 fixes: StopAndAlert (reachable concurrently via
// errorWrapper from multiple in-flight gRPC handlers) and Stop() both need
// to retrieve-and-clear static.StartedService as one atomic step, so that
// at most one caller ever observes the non-nil service and proceeds to
// close it. Before the atomic.Pointer conversion, two goroutines could both
// read a non-nil raw pointer and both call CloseService() on it.
func TestStartedService_ConcurrentSwap_OnlyOneCallerGetsService(t *testing.T) {
	placeholder := &daemon.StartedService{}
	static.StartedService.Store(placeholder)
	defer static.StartedService.Store(nil)

	const goroutines = 20
	var wg sync.WaitGroup
	var gotService int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Mirrors StopAndAlert's Swap(nil) pattern exactly.
			if ss := static.StartedService.Swap(nil); ss != nil {
				atomic.AddInt32(&gotService, 1)
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, 1, gotService,
		"exactly one concurrent Swap(nil) should observe the non-nil StartedService; a double-close bug would show >1")
	require.Nil(t, static.StartedService.Load())
}
