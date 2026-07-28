package hcore

import (
	"runtime"
	"sync"

	"github.com/hiddify/hiddify-core/v2/hcore/icmpservice"
	hutils "github.com/hiddify/hiddify-core/v2/hutils"
	"github.com/xtls/xray-core/transport/internet/finalmask/xicmp"
)

// wireIcmpElevationOnce runs unconditionally, with no user-facing enable/disable toggle: on
// Windows, an unelevated process can't open xicmp's unprivileged ("dgram") ICMP sockets at all, so
// without this override every xicmp config simply fails outright for the overwhelming majority of
// users, who don't run the app as Administrator. There's nothing to opt into - the helper it wires
// up is itself lazy (icmpservice.EnsureIcmpHelperRunning only elevates, prompting UAC, the first
// time an xicmp outbound is actually dialed), so an unconditional wire-up has no cost for users who
// never touch xicmp.
//
// This intentionally bypasses the extension/service_manager framework (which previously carried
// this same assignment in extension/system/admin_service_vpn/admin_icmp_service.go): that path
// requires both a package that nothing in the shipped mobile/desktop binaries actually imports, and
// a per-extension "enabled" DB row nothing ever sets - so it silently never ran in any real build.
// Calling this directly from StartService, which every real entry point (platform/mobile,
// platform/desktop) goes through, guarantees it actually executes.
var wireIcmpElevationOnce sync.Once

func wireIcmpElevation() {
	wireIcmpElevationOnce.Do(func() {
		if runtime.GOOS != "windows" {
			return
		}
		if hutils.IsAdmin() {
			// Already elevated - xicmp's own default ListenICMP (real OS socket call) already
			// works, no helper needed.
			return
		}
		xicmp.ListenICMP = icmpservice.ProxiedListenICMP
	})
}
