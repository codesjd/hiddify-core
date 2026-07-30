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

// TestRestart_WhenNotStarted_DoesNotPanic guards the fix for a nil-pointer dereference that used
// to live here: Restart read static.HiddifyOptions (nil on a fresh instance - see static_data.go,
// never initialized there) and dereferenced opts.EnableTun with no nil check. restart.go now
// treats a nil opts as EnableTun=false and falls through to StartService, whose own nil check
// (start.go) returns a clean, immediate MessageType_ERROR_BUILDING_CONFIG instead of a panic
// recovered 5 seconds later by config.DeferPanicToError with a generic MessageType_UNEXPECTED_ERROR.
func TestRestart_WhenNotStarted_DoesNotPanic(t *testing.T) {
	static.StartedService.Store(nil)

	var resp *CoreInfoResponse
	var err error
	require.NotPanics(t, func() {
		resp, err = Restart(context.Background(), &StartRequest{ConfigContent: "{}"})
	}, "Restart must never crash the whole process, even on a fresh, never-configured instance")

	require.Error(t, err, "a never-configured instance should still report a failure")
	require.NotNil(t, resp)
	require.Equal(t, MessageType_ERROR_BUILDING_CONFIG, resp.MessageType,
		"expected the same clean 'HiddifyOptions not initialized' path StartService takes, not a recovered panic")
	require.False(t, strings.Contains(err.Error(), "nil pointer dereference"),
		"regression: Restart should no longer dereference a nil HiddifyOptions")

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
