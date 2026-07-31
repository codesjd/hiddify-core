# Spike: how should macOS get TUN privilege elevation?

Status: investigation only, no code changed. See
`plans/041-investigate-macos-tun-elevation.md` (Dart repo) for the plan this
document fulfills. **Nothing here is a decision** — it is a survey of the
current state and the real options, for a maintainer (whoever owns the
release-channel / platform-support call) to pick from.

## Confirmed current state (re-verified independently, not taken on faith)

The plan's "Why this matters" section was written from a single
investigation pass and flagged one of its own claims as needing
verification. Re-checking all three claims directly:

1. **Entitlements**: `macos/Runner/Release.entitlements` (Dart repo) lines
   9-10 set `com.apple.security.app-sandbox` to `false`, and every
   NetworkExtension-related entitlement is commented out (lines 15-30):
   `com.apple.developer.system-extension.install`,
   `com.apple.developer.networking.networkextension` (with
   `packet-tunnel-provider` etc.), `com.apple.developer.networking.vpn.api`.
   Confirmed verbatim as the plan described.

2. **No helper-tool/system-extension code**: `grep -rln
   "SMJobBless\|NetworkExtension\|NEPacketTunnelProvider\|systemextensiond\|SMAppService"
   macos/` returns no matches. Confirmed absent. Also checked for
   `osascript`/`AuthorizationExecuteWithPrivileges`/`sudo ` — also no
   matches. There is no privilege-elevation code path anywhere under
   `macos/`.

3. **`darwin_utils.go` — CORRECTION to the plan**: the plan explicitly
   flagged this as needing verification ("verify this file really doesn't
   exist before treating it as fact"). It was wrong to assume absence:
   `hiddify-core/v2/hutils/darwin_utils.go` **does exist**. However, reading
   it (it is 8 lines) shows it only defines `RedirectStderr` — crash-output
   redirection, unrelated to TUN or privilege elevation:

   ```go
   //go:build darwin
   package hutils
   func RedirectStderr(path string) error {
       return redirectStderrToFile(path)
   }
   ```

   `TunAllowed()`, `ExecuteCmd()`, and `IsAdmin()` for macOS are **not**
   defined in `darwin_utils.go` — they still come from `stub_utils.go`,
   whose build tag `(!linux && !windows) || android` covers darwin (darwin
   is neither linux nor windows). So the plan's substantive conclusion holds
   even though its literal file-existence premise didn't:
   `TunAllowed()` is hardcoded `false` and `ExecuteCmd()` is a silent no-op
   (`return "", nil`, does not launch anything) on macOS today
   (`v2/hutils/stub_utils.go:7-13`).

**Two further findings beyond what the plan asked to check, found while
tracing the actual code path (Step 3), worth flagging because they change
how "intentional" the current gap looks**:

- `removeTunnelIfNeeded()` in `v2/hcore/buildconfighelper.go:175-195` — the
  function the plan's own prior investigation cited as evidence of a
  `TunAllowed()` gate — is **dead code**. `grep -rn "removeTunnelIfNeeded"`
  across the whole repo finds only its own definition, no callers. The
  logic it contains was superseded by (and now duplicated in)
  `extension/system/admin_service_vpn/admin_tun_service.go`'s
  `AdminServiceExtension.OnMainServicePreStart`, which is the extension
  actually registered (`AlwaysEnabled: true`) and exercised at runtime.
- `tunnelservice.isSupportedOS()` (`admin_service_commander.go:25-27`,
  `runtime.GOOS == "windows" || runtime.GOOS == "linux"`) — the function
  the plan asks "is that because it was never implemented, or because
  something about the design is Windows/Linux-specific" — is **also dead
  code**. It is defined but never called anywhere in the repo. So it isn't
  actively excluding macOS from anything; it reads as a note-to-self that
  was never wired into the actual gate. Nothing at runtime currently
  prevents `ActivateTunnelService` from being invoked on darwin — it just
  fails there because `hutils.ExecuteCmd` is a no-op stub, not because of
  an explicit OS check.

No STOP condition was triggered: macOS TUN elevation is not handled by any
mechanism this investigation missed (checked packaging scripts, CI, Swift
code, and Go `hutils` — see next section for packaging/CI detail).

### Packaging / CI check

- `macos/packaging/pkg/make_config.yaml` (a `.pkg` installer config) has no
  postinstall script and no ACL/capability step — just `install-path:
  /Applications` and a commented-out sign-identity.
- `macos/packaging/dmg/make_config.yaml` is a plain drag-to-Applications
  DMG (background image + two icon positions) — no elevation mechanism.
- `.github/workflows/build.yml` lines 160-163: the entire `macos` build
  matrix entry (`os: macos-15`, `targets: dmg,pkg`) is commented out, same
  for the `ios` entry below it. **No macOS build currently ships from CI at
  all**, regardless of the TUN question — whatever gets decided here, macOS
  packaging itself needs to be turned back on first before it matters in
  practice.
- No App Store distribution evidence found: no `ExportOptions.plist` under
  `macos/` (only `ios/exportOptions.plist`, a different target), no
  App-Store-specific CI steps, and the only packaging targets defined are
  `dmg`/`pkg` (direct-download shapes). Distribution today is
  direct-download-only as far as this repo's config shows.

## The four options

**(a) `NetworkExtension` / `NEPacketTunnelProvider` system extension.**
Requires enabling the commented-out entitlements, a new Swift/ObjC
`NEPacketTunnelProvider` extension target, an Apple Developer Program
entitlement request, and wiring packet I/O to the Go core. That last part
is more promising than it sounds: `v2/hcore/platform_interface.go`'s
`MobilePlatformInterface.OpenTun` (lines 41-46) is a pure pass-through —
`return h.platform.OpenTun(options)` — to a `libbox.PlatformInterface`
supplied from the native side, exactly the shape Android/iOS already use.
No macOS-specific Go work is obviously required for that half; the mobile
plumbing is already OS-agnostic at this layer. Effort: **M-XL** — the Go
side may be closer to "reuse as-is" than expected, but the Swift extension
target, provisioning, and notarization are unavoidable and this spike
cannot validate any of that without a real Mac and a paid Developer account
entitlement grant. Risk: the entitlement request itself
(`com.apple.developer.networking.networkextension`) is an Apple approval
gate outside this repo's control, and could stall the whole option
indefinitely.

**(b) A privileged helper tool via `SMJobBless`/`SMAppService`.** Closer to
today's Windows Tunnel Helper Service shape and would reuse plan 039's
`tunnelservice` package almost as-is: same gRPC proto, same
`tunnelservice.ActivateTunnelService`/`DeactivateTunnelService` call sites
in `admin_tun_service.go`, same token-auth pattern. What's genuinely
macOS-specific is *how the helper gets installed with elevated rights in
the first place* — `kardianos/service`'s darwin backend (confirmed by
reading `service_darwin.go` in the vendored module,
`github.com/kardianos/service@v1.2.2`) supports both a user-level
LaunchAgent (`~/Library/LaunchAgents`, no elevation, but also no root — a
LaunchAgent runs as the logged-in user) and a system-level LaunchDaemon
(`/Library/LaunchDaemons`, requires root to write). The current
`StartTunnelService` config (`tunnel_platform_service.go:94-104`) doesn't
set `UserService`, so it defaults to targeting the LaunchDaemon path — but
nothing installs it with elevation today; `SMJobBless`/`SMAppService`
wiring (the actual mechanism that would let macOS's one-time
admin-password prompt authorize writing to `/Library/LaunchDaemons`) does
not exist anywhere in this repo. Effort: **M-L** — smaller than (a) if plan
039 lands first, since the cross-process protocol is already built; the
net-new work is Swift/ObjC helper-tool packaging plus
`SMJobBless`/`SMAppService` plist wiring. Uncertain without a real Mac:
whether the install prompt is truly one-time (survives app updates,
`SMAppService`'s modern replacement for `SMJobBless` reportedly handles
updates better, but this needs hands-on confirmation, not read from docs
here) or needs periodic re-prompting the way Windows's
`ShellExecute("runas")` does per-launch. Risk: `SMJobBless` itself is
Apple-deprecated in favor of `SMAppService` (macOS 13+), so a new
implementation should probably target `SMAppService` directly rather than
the older API — worth confirming the minimum supported macOS version this
app targets before committing to either.

**(c) Ship elevated some other way** (a `.pkg` postinstall script that
grants a capability/ACL, or documenting a manual `sudo`/"run as different
user" workaround). Lowest engineering effort — literally nothing new to
build in the Go/Swift layers, just packaging config and/or a support doc.
Confirmed via the packaging check above: today's `.pkg` config
(`macos/packaging/pkg/make_config.yaml`) has zero postinstall steps, so
this is genuinely unbuilt, not partially done. Effort: **S**. Worst UX of
the four (a per-launch or per-install password dance, or a support article
telling users to use the terminal) and, per the packaging/CI check, this
app is not currently shown to target Mac App Store distribution at all — so
the App-Store-review objection the plan raised is *not* currently a blocker
for (c), though it would become one the moment App Store distribution is
ever pursued (a `.pkg` postinstall granting elevated capabilities is
exactly the kind of thing Apple review checks for on Store submissions).
Risk: this is the option most likely to bit-rot silently, since it depends
on doc/support-article maintenance rather than code that would fail loudly
in tests or CI.

**(d) Do nothing / explicitly unsupported.** Document TUN as
Windows/Linux/Android/iOS-only, macOS gets System-Proxy mode
(`ServiceMode.systemProxy`). Cheapest (effort: **S**, it's a documentation
and UI-gating change, not zero because of the concrete UX problem below).
See "Current user-facing behavior" — the honest cost of (d) is not "nothing
changes," it's "the confusing status quo becomes the intentional, permanent
state" unless the UI is also changed to stop offering `tun` as a choice on
macOS. Risk: forecloses a real feature gap (macOS users lose the strongest
of the three modes) with no path back except redoing this spike later.

## Current user-facing behavior on macOS (traced, not guessed)

TUN mode **is** selectable in the UI on macOS today, with no platform gate:

- `lib/singbox/model/singbox_config_enum.dart:24-32` —
  `ServiceMode.choices` explicitly returns `[proxy, systemProxy, tun]` for
  `Platform.isMacOS` (not just Windows/Linux). `tun` is a real, presented
  choice, not hidden or disabled.
- `lib/features/settings/overview/sections/inbound_options_page.dart:20-27`
  — the `ChoicePreferenceWidget` for `serviceMode` renders unconditionally,
  no `if (PlatformUtils...)` guard (unlike the tproxy-port and
  redirect-port tiles just below it, which *are* platform-gated).
- `lib/features/settings/data/config_option_repository.dart:507` —
  `enableTun: mode == ServiceMode.tun` is sent to the Go core with no
  macOS-specific check.

Tracing what happens next in the Go core (not a guess — the actual call
chain, confirmed by reading each hop):

1. `EnableTun=true` reaches `AdminServiceExtension.OnMainServicePreStart`
   (`extension/system/admin_service_vpn/admin_tun_service.go:25-48`).
   Since `hutils.TunAllowed()` is hardcoded `false` on darwin, it strips the
   native `tun` inbound out of the sing-box config entirely — the app never
   even attempts to open a real TUN device at the OS level, contrary to the
   plan's "likely just tries the raw sing-box tun inbound and fails at the
   OS level" guess. It does not get that far.
2. `OnMainServiceStart()` (same file, line 60) instead calls
   `tunnelservice.ActivateTunnelService(...)` — the Windows/Linux
   helper-service activation path.
3. On darwin this proceeds through `startTunnelRequestWithFailover` →
   `startTunnelRequest` → `runTunnelService`
   (`admin_service_commander.go:147-160`), which calls
   `hutils.ExecuteCmd(executablePath, false, "tunnel", "install")`. On
   darwin this resolves to `stub_utils.go`'s no-op — it returns `("", nil)`
   without launching anything.
4. Because that no-op call returns no error, the code treats the "install"
   as having succeeded, waits 1 second, and retries
   `startTunnelRequest(opt, false)`. Since nothing was ever actually
   spawned, the tunnel-service port is still not in use, and this second
   call returns `fmt.Errorf("service is not running")`.
5. That error propagates as a normal `adapter.LifecycleService.Start()`
   failure through sing-box's box-start path
   (`v2/hcore/service_manager_callback.go` →
   `v2/service_manager/hiddify.go:63-75`) — the same path any other
   box-start error takes.
6. On the Dart side this surfaces as a regular `AsyncError`/
   `ConnectionFailure` (`lib/features/connection/notifier/connection_notifier.dart`,
   e.g. lines 59, 76, 106, 145, 157, 167) — **not silent**, the user does
   see a failure. But the message that reaches them is the generic,
   opaque `"service is not running"` string, which gives no indication
   that the real cause is "macOS has no TUN elevation path implemented" —
   it reads like a transient bug, not a platform limitation. This is worse
   than either a clear "TUN mode isn't supported on macOS yet" message or a
   working feature, and it's the concrete cost a maintainer is accepting by
   picking (d) without also adding UI gating and a real error message.

## Recommendation

Not decided here — this is a spike, not an approval. A soft recommendation
for whoever owns this decision:

**(b) is probably the better near-term ROI** if plan 039 lands first: it
reuses the `tunnelservice` gRPC protocol, token-auth pattern, and
`admin_tun_service.go` call sites almost verbatim, and the only genuinely
new work is the `SMAppService`/helper-tool install wiring (prefer
`SMAppService` over the deprecated `SMJobBless` given the App Store option
below still requires a modern minimum macOS target either way). **(a) is
the only option that survives a future Mac App Store distribution goal** —
today's packaging config shows no App Store target at all, so this isn't
urgent, but if that ever becomes a goal, (c) is a non-starter and (b)'s
LaunchDaemon/root-helper shape is also atypical for sandboxed Store apps.
Worth confirming with whoever owns the release-channel decision before
committing engineering time to either.

Whichever of (a)-(d) is chosen, **fix the current-state UX regardless**:
today's status quo (TUN selectable, silently redirected through a
non-functional Windows/Linux-style helper path, failing with an opaque
`"service is not running"` error) is strictly worse than even the "do
nothing" option (d) properly executed — (d) done right means gating `tun`
out of `ServiceMode.choices` for macOS and/or showing a real "not supported
yet" message, not leaving today's confusing failure in place.
