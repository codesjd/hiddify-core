package admin_service_vpn

import (
	"runtime"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/xtls/xray-core/transport/internet/finalmask/xicmp"
	"golang.org/x/net/icmp"
)

// TestOnMainServicePreStartOnlyInstallsOverrideOnWindows proves AdminIcmpExtension leaves
// xicmp.ListenICMP untouched on this test's platform - the extension must not change xicmp's
// behavior anywhere except non-elevated Windows. Caveat: this only exercises the combined
// "not Windows OR already admin" early-return as a whole, not the runtime.GOOS check in
// isolation - CI/most dev sandboxes (this one included: confirmed running as uid 0) are already
// "admin" by hutils.IsAdmin()'s own definition, which independently short-circuits before the
// GOOS check would matter. A real Windows machine running as a normal (non-admin) user is the
// only environment that can exercise the GOOS-specific branch on its own.
//
// Go func values aren't comparable, so "unchanged" is proven the same way the xicmp package's
// own TestNewConnClientDefaultListenICMPUsesRealSocket does: by confirming xicmp.ListenICMP
// still produces the identical error a direct icmp.ListenPacket call does, rather than the very
// different failure shape icmpservice.ProxiedListenICMP would produce (which tries to dial an
// elevated helper that was never started in this test).
func TestOnMainServicePreStartOnlyInstallsOverrideOnWindows(t *testing.T) {
	original := xicmp.ListenICMP
	defer func() { xicmp.ListenICMP = original }()

	ext := &AdminIcmpExtension{}
	if err := ext.OnMainServicePreStart(&option.Options{}); err != nil {
		t.Fatalf("OnMainServicePreStart: %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("this test only asserts the non-Windows no-op behavior; Windows-specific behavior needs a real Windows machine")
	}

	wantConn, wantErr := icmp.ListenPacket("udp4", "0.0.0.0")
	if wantConn != nil {
		wantConn.Close()
	}
	gotConn, gotErr := xicmp.ListenICMP("udp4", "0.0.0.0")
	if gotConn != nil {
		gotConn.Close()
	}

	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("expected ListenICMP to remain the untouched default on %s, got a different error shape: want=%v got=%v", runtime.GOOS, wantErr, gotErr)
	}
	if wantErr != nil && gotErr.Error() != wantErr.Error() {
		t.Fatalf("expected ListenICMP to remain the untouched default on %s:\nwant: %v\ngot:  %v", runtime.GOOS, wantErr, gotErr)
	}
}
