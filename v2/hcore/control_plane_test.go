package hcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Plan 030: narrow, harness-free tests for the control-plane RPCs (Stop, Close, Restart,
// Pause/Wake) that every client calls to start/stop/restart/pause the VPN, covering only the
// state-machine behavior reachable with no core instance started (see the plan's "Why this
// matters" for why full coverage is out of scope here).
//
// `static` is a package-level singleton shared with start_test.go and log_interface_test.go -
// these tests only touch static.StartedService (an atomic.Pointer, safe to Store(nil) from
// anywhere) and read static.CoreState, so they don't need t.Cleanup to avoid cross-test
// pollution: they always leave StartedService nil and CoreState STOPPED, the same state the
// existing tests already assume on entry.

func TestStop_WhenNotStarted_ReturnsAlreadyStopped(t *testing.T) {
	static.StartedService.Store(nil)

	resp, err := Stop()

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, CoreStates_STOPPED, resp.CoreState)
	require.Equal(t, MessageType_ALREADY_STOPPED, resp.MessageType)
	require.Equal(t, CoreStates_STOPPED, static.CoreState)
}

func TestClose_NilRequest_ReturnsNilNoError(t *testing.T) {
	resp, err := (&CoreService{}).Close(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, resp)
}

func TestClose_UnstartedMode_ReturnsNilNoError(t *testing.T) {
	const mode = SetupMode_GRPC_BACKGROUND
	// Confirm this mode has no live grpcServer entry before asserting the no-op path - other
	// tests in this package (grpc_server_test.go) only ever start SetupMode_GRPC_NORMAL_INSECURE
	// and always clean it up, but don't assume that without checking.
	if grpcServerExists(mode) {
		t.Skipf("grpcServer already has a live entry for %v (from another test) - skipping rather than asserting a false negative", mode)
	}

	resp, err := (&CoreService{}).Close(context.Background(), &CloseRequest{Mode: mode})
	require.NoError(t, err)
	require.Nil(t, resp)
}

// TestRestart_WhenNotStarted_DoesNotPanic answers this plan's nil-pointer-dereference question
// from "Current state": Restart reads static.HiddifyOptions (nil on a fresh instance - see
// static_data.go, never initialized there) and immediately dereferences opts.EnableTun without a
// nil check. Unlike the plan's original assumption, this does NOT crash the process: Restart has
// its own `defer config.DeferPanicToError(...)` (restart.go, matching Stop's and StartService's
// pattern) which recovers any panic inside Restart and converts it into a normal
// (*CoreInfoResponse, error) return - so require.NotPanics below is expected to hold either way.
// What it does NOT do cleanly, unlike StartService (start.go:106-114, which nil-checks
// HiddifyOptions and returns a clear MessageType_ERROR_BUILDING_CONFIG before ever touching it),
// is avoid the dereference in the first place: if it fires, the caller gets a generic
// MessageType_UNEXPECTED_ERROR after config.DeferPanicToError's unconditional 5-second sleep,
// with a panic stack trace as the message, instead of an immediate, clean error. See this test's
// package-level finding written up in the implementing plan's final report.
func TestRestart_WhenNotStarted_DoesNotPanic(t *testing.T) {
	static.StartedService.Store(nil)

	var resp *CoreInfoResponse
	var err error
	require.NotPanics(t, func() {
		resp, err = Restart(context.Background(), &StartRequest{ConfigContent: "{}"})
	}, "Restart must never crash the whole process, even on a fresh, never-configured instance")

	if err != nil && strings.Contains(err.Error(), "nil pointer dereference") {
		t.Logf("CONFIRMED (recovered internally, not a process crash): Restart() dereferences "+
			"a nil static.HiddifyOptions via opts.EnableTun in restart.go when the instance was "+
			"never configured; config.DeferPanicToError's recover() in Restart's own defer "+
			"catches it (after its unconditional 5s sleep) and returns it as a generic "+
			"MessageType_UNEXPECTED_ERROR instead of the clean, immediate error StartService "+
			"gives for the same nil case: %v", err)
	}
	_ = resp

	static.StartedService.Store(nil)
}

// TestPauseWake_NoInstance_DoesNotPanic confirms Pause/Wake's existing static.Instance() != nil
// guards (pause.go) make both safe to call with no core running - no harness needed.
func TestPauseWake_NoInstance_DoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		Pause()
		Wake()
	})
}
