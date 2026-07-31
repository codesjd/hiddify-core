package main

// Blank-imported so admin_service_vpn's init() registers its TUN-elevation
// extension (see plans/039-fix-tun-elevation-dead-wiring.md) — without this
// import, the package's RegisterExtension call in its own init() never runs
// and the extension stays invisible to hiddify-core/extension's registry.
import (
	_ "github.com/hiddify/hiddify-core/extension/system/admin_service_vpn"
)
