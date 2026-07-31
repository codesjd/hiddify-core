package icmpservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	hcommon "github.com/hiddify/hiddify-core/v2/hcommon"
	hutils "github.com/hiddify/hiddify-core/v2/hutils"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var icmpServiceAddress = fmt.Sprintf("127.0.0.1:%d", icmpServicePort)

// ErrElevationDeclined is returned (wrapped) by EnsureIcmpHelperRunning when the user declines
// the UAC elevation prompt. Both the udp4 and udp6 opens in xicmp's NewConnClient route through
// this same call, so both err4/err6 end up carrying this error, and NewConnClient's existing
// "fail only if neither family works" logic naturally produces a clean failure through the
// errors.Combine(err4, err6) path it already has - no xray-core-side changes needed for this to
// surface sensibly.
var ErrElevationDeclined = errors.New("xicmp: administrator elevation was declined; ICMP obfuscation requires it on Windows")

var (
	elevationMu       sync.Mutex
	elevationDeclined bool // cached for the process lifetime so a retry doesn't re-prompt UAC after a denial
)

// helperStartupTimeout covers UAC dialog render + a human noticing/clicking "Yes" + HiddifyCli.exe's
// Go runtime cold-starting + its gRPC listener coming up - 5s (the previous value) routinely wasn't
// enough for the human-reaction-time part alone, causing the first xicmp connection after a cold
// start to fail even though the helper would come up moments later.
const helperStartupTimeout = 45 * time.Second

// EnsureIcmpHelperRunning makes sure the elevated ICMP helper is reachable at
// 127.0.0.1:<icmpServicePort>, elevating and launching it (triggering a UAC prompt) if it isn't
// already. Idempotent: safe to call before every xicmp dial attempt.
func EnsureIcmpHelperRunning() error {
	elevationMu.Lock()
	declined := elevationDeclined
	elevationMu.Unlock()
	if declined {
		return ErrElevationDeclined
	}

	if pingHelper() == nil {
		return nil
	}

	if err := launchHelper(); err != nil {
		if isElevationDeclined(err) {
			elevationMu.Lock()
			elevationDeclined = true
			elevationMu.Unlock()
			return ErrElevationDeclined
		}
		return fmt.Errorf("xicmp: failed to launch elevated helper: %w", err)
	}

	deadline := time.Now().Add(helperStartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = pingHelper(); lastErr == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("xicmp: helper process did not become reachable: %w", lastErr)
}

// executableDir returns the directory containing the running executable, used as the location of
// the token file shared with the elevated helper (same file both the helper and its callers read).
func executableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}

// dialIcmpService dials the ICMP helper's gRPC server and returns a context carrying the auth
// token as outgoing metadata. Callers must Close() the returned conn and apply their own
// per-call timeout on top of the returned context (mirrors tunnelservice's dialTunnelService).
func dialIcmpService() (*grpc.ClientConn, context.Context, context.CancelFunc, error) {
	dir, err := executableDir()
	if err != nil {
		return nil, nil, nil, err
	}
	token, err := hutils.ReadServiceToken(dir, "icmp")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read icmp service token: %w", err)
	}
	conn, err := grpc.Dial(icmpServiceAddress, grpc.WithInsecure())
	if err != nil {
		return nil, nil, nil, err
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", token)
	return conn, ctx, nil, nil
}

// pingHelper calls a real, side-effect-free RPC (CloseSession on a session id that can't exist)
// to confirm something implementing IcmpService is actually listening and responding - not just
// that the port happens to be occupied.
func pingHelper() error {
	conn, ctx, _, err := dialIcmpService()
	if err != nil {
		return err
	}
	defer conn.Close()

	c := NewIcmpServiceClient(conn)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = c.CloseSession(ctx, &CloseSessionRequest{SessionId: "__liveness_probe__"})
	return err
}

func launchHelper() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("elevated ICMP helper is only implemented for windows")
	}
	executablePath, err := getIcmpServicePath()
	if err != nil {
		return err
	}
	_, err = hutils.ExecuteCmd(executablePath, true, "icmp", "run")
	return err
}

// isElevationDeclined recognizes the class of error ShellExecute's "runas" verb returns when the
// user clicks "No" on the UAC prompt (ERROR_CANCELLED). hutils.ExecuteCmd doesn't currently
// distinguish this from other launch failures, so this is intentionally a loose, message-based
// check rather than a typed sentinel from hutils.
func isElevationDeclined(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cancel") || strings.Contains(msg, "denied") || strings.Contains(msg, "1223")
}

func getIcmpServicePath() (string, error) {
	binFolder, err := executableDir()
	if err != nil {
		return "", err
	}
	fullPath := "HiddifyCli.exe"
	abspath, err := filepath.Abs(filepath.Join(binFolder, fullPath))
	if err != nil {
		return "", err
	}
	return abspath, nil
}

// ExitIcmpHelper asks the helper to shut itself down, if it's running. Best-effort: errors are
// logged, not returned, since callers use this on process shutdown where there's nothing more
// useful to do with a failure.
func ExitIcmpHelper() {
	conn, ctx, _, err := dialIcmpService()
	if err != nil {
		return
	}
	defer conn.Close()
	c := NewIcmpServiceClient(conn)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.Exit(ctx, &hcommon.Empty{}); err != nil {
		log.Printf("icmpservice: exit request failed: %v", err)
	}
}
