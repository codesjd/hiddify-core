package admin_service_vpn

import (
	"runtime"

	"github.com/sagernet/sing-box/option"
	"github.com/xtls/xray-core/transport/internet/finalmask/xicmp"

	ex "github.com/hiddify/hiddify-core/extension"
	"github.com/hiddify/hiddify-core/v2/hcore/icmpservice"
	hutils "github.com/hiddify/hiddify-core/v2/hutils"
)

var _ ex.Extension = (*AdminIcmpExtension)(nil)

// AdminIcmpExtension makes xicmp's unprivileged ("dgram") ICMP mode actually work on Windows for
// non-Administrator users. Windows only allows that socket mode to elevated processes (confirmed
// via real testing - not an OS-support gap, purely a privilege one), and Hiddify.exe deliberately
// doesn't request elevation for the whole app (see windows/runner/runner.exe.manifest). Instead,
// this extension installs an override that lazily launches a separate, on-demand-elevated helper
// process (icmpservice) the first time an xicmp outbound is actually dialed - most users who
// never select an xicmp server never see a UAC prompt at all. Mirrors admin_tun_service.go's
// pattern for the same class of problem (TUN also needs admin on Windows), but deliberately as an
// independent extension/service rather than folded into that one - xicmp usage doesn't correlate
// with TUN usage, and the two helpers have different lifecycles (see the elevated-ICMP-helper
// design notes in hiddify-core/v2/hcore/icmpservice).
type AdminIcmpExtension struct {
	ex.Base[AdminServiceExtensionData]
}

func (b *AdminIcmpExtension) OnMainServicePreStart(singconfig *option.Options) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if hutils.IsAdmin() {
		// Already elevated - xicmp's own default ListenICMP (real OS socket call) already works,
		// exactly matching the user's own confirmed-working elevated test. No helper needed.
		return nil
	}
	xicmp.ListenICMP = icmpservice.ProxiedListenICMP
	return nil
}

func NewAdminIcmpExtension() ex.Extension {
	return &AdminIcmpExtension{}
}

func init() {
	ex.RegisterExtension(
		ex.ExtensionFactory{
			Id:          "github.com/hiddify/hiddify-core/extension/system/admin_service_xicmp",
			Title:       "Admin ICMP Service",
			Description: "Elevated helper for xicmp's unprivileged ICMP mode on Windows",
			Builder:     NewAdminIcmpExtension,
		},
	)
}
