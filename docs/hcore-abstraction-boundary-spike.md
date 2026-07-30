# Spike: does `v2/hcore` need an abstraction boundary in front of sing-box?

Status: investigation only, no code changes made. See plan
`plans/037-investigate-hcore-sing-box-abstraction-boundary.md` (Dart repo)
for the original scope. This document is the deliverable.

## Drift from the plan's inventory

The plan recorded 11 files under `v2/hcore/*.go` importing
`sagernet/sing-box` at the time it was written. Re-running
`grep -rl "sagernet/sing-box" v2/hcore/*.go | wc -l` today finds **17**,
not 11 — real drift, not measurement noise. The 6 files added since the
plan was written:

- `custom.go`
- `service_manager_callback.go`
- `standalone.go`
- `start.go`
- `static_data.go`
- `log_interface_test.go` (a test file, not production code — noted
  separately below)

The imported-package list is materially the same as recorded in the plan
(`adapter`, `common/monitoring`, `common/urltest`, `daemon`,
`experimental/clashapi`, `experimental/clashapi/trafficontrol`,
`experimental/libbox`, `log`, `option`, `protocol/group`,
`protocol/group/balancer`, `constant`, plus the top-level `sing-box`
package itself) — no new packages, just more files touching the same
surface. All 17 files were read for this spike (not just the 11 originally
listed), so the classification below is complete against the current
codebase.

## File-by-file classification

Legend: **(a)** thin pass-through — an interface could wrap this call
trivially, little is lost by leaving it alone. **(b)** business logic
interleaved with sing-box-specific type assertions — the harder case, where
an abstraction would have to encode real decisions, not just forward a call.

| File | Class | Why |
|---|---|---|
| `proxy_info.go` | **(b)** | `GetProxyInfo` type-asserts `adapter.OutboundGroup` and `*balancer.Balancer` and calls `monitoring.RealTag`/`monitoring.Get` as load-bearing logic that shapes the proto response. `GetAllProxiesInfo` walks `box.Endpoint().Endpoints()`/`box.Outbound().Outbounds()`, type-asserts `adapter.OutboundGroup` and `*group.Selector`, and builds a derived group tree. The clearest (b) example in the package. |
| `commands.go` | **(b)** | `readStatus`, `SelectOutbound`, `UrlTestActive`, `UrlTest` all type-assert `*group.Selector`/`adapter.OutboundGroup` against a live `box.Outbound()` and call `monitoring.Get(ctx).TestNow(...)` — the same pattern as `proxy_info.go`, applied to outbound-selection RPCs instead of the info-list RPC. (`GetLANIP` in the same file is unrelated OS-networking code with no sing-box coupling.) |
| `service.go` | (a), but load-bearing | `Box()`, `Instance()`, `Context()`, `UrlTestHistory()`, `TrafficManager()` are one-line accessors — but they return concrete sing-box types (`*box.Box`, `*daemon.Instance`, `*urltest.HistoryStorage`, `*trafficontrol.Manager`) straight out of `HiddifyInstance`, which is *why* `proxy_info.go` and `commands.go` end up doing their own type assertions downstream. `TrafficManager()` itself does a `.(*clashapi.Server)` assertion. This file is the actual seam between `hcore` and sing-box; the (b) files are just its two heaviest consumers. |
| `buildconfighelper.go` | (a) | Calls into this repo's own `config` package; `removeTunnelIfNeeded` filters `option.Inbound` by `C.TypeTun` — data shaping, not adapter-interface logic. |
| `independent_instance.go` | (a) | `RunInstance`/`RunInstanceString` are orchestration over `config.BuildConfig` + `NewService`; `ContentFromURL`/`Ping*` dial through the box's local SOCKS port with stdlib `net/http`, no sing-box types involved beyond the port number. |
| `log_interface.go` | (a) | `LogInterface` implements sing-box's `log.PlatformWriter` callback interface and forwards to `service_manager`/`publishServiceLogMessage`. Necessarily touches a sing-box-defined interface (see "Open questions" (a)), but the body is pure forwarding. |
| `logproto.go` | (a) | `logLevel()` is a switch translating this package's `LogLevel` enum into calls to sing-box's package-level `log.Debug/Info/Warn/Error/Trace` — a formatting/dispatch shim, not business logic. |
| `pause.go` | (a) | `Pause`/`Wake` call `box.PauseManager().DevicePause()/DeviceWake()` directly; `Close` (the gRPC handler) touches no sing-box types at all. |
| `platform_interface.go` | (a) — already an adapter | `MobilePlatformInterface` wraps `libbox.PlatformInterface` and delegates every method to `h.platform` with a nil-check. This is, verbatim, the pattern Step 3 of this spike is asked to sketch — just running in the other direction (native platform → sing-box) instead of (sing-box → gRPC). Worth citing as an existing precedent for what "thin adapter" looks like in this codebase. |
| `restart.go` | (a) | `Restart()` calls `Stop()`/`StartService()` (both this package's own functions) and one `log.Debug` call; the only sing-box-typed field it touches (`static.HiddifyOptions.EnableTun`) is this repo's own config struct, not a sing-box type. |
| `grpc_server.go` | (a) | `Setup()` wires `libbox.BaseContext`/`libbox.Setup`/`log.New` once at process start — construction and wiring, not per-request business logic. |
| `custom.go` | (a) | `errorWrapper`/`StopAndAlert`/`Close(mode)` call `static.StartedService.CloseService()` and `log.Debug`; no type assertions. |
| `service_manager_callback.go` | (a) | `hiddifyMainServiceManager` implements sing-box's `adapter.LifecycleService` interface, forwarding `Start`/`Close` to `service_manager`. Same shape as `log_interface.go`: an interface implementation, not consumption. |
| `standalone.go` | (a) | CLI-only entry point; constructs `option.ExperimentalOptions`/`option.ClashAPIOptions` values (plain struct literals) and calls `Setup`/`StartService`/`Stop` — config construction, not type-assertion logic. |
| `start.go` | (a), one light exception | `StartService` is orchestration (`service.MustRegister[adapter.PlatformInterface]`, `libbox.WrapPlatformInterface`, `libbox.FromContext`, `NewService`) plus one type assertion (`options.Inbounds[inb].Options.(option.SocksInboundOptions)`) to read back the bound port — a single data-extraction cast, not decision logic like the (b) files. |
| `static_data.go` | (a) — data only | `HiddifyInstance` struct declaration; fields are typed with sing-box types (`*daemon.StartedService`, `monitoring.Broadcaster[...]`, `libbox.PlatformInterface`, `log.Factory`) but the file contains no logic at all. |
| `log_interface_test.go` | n/a (test) | Already tests `LogInterface.WriteMessage` against sing-box's real `log` package (swapping `boxlog.SetStdLogger`) without a running `*box.Box` or `daemon.StartedService` — i.e., it's already an example of testing at the interface-implementation seam without a full instance. Relevant to the testability argument below, not to the (a)/(b) split. |

**Summary**: 2 of 17 files (`proxy_info.go`, `commands.go`) carry genuine
sing-box-specific business logic; the other 15 are thin pass-throughs,
struct/interface declarations, or wiring. This roughly matches the plan's
expectation, just with more (a)-classified files than counted originally
(the drift added only (a)-shaped files, not new (b) cases).

## Sketched adapter design (for the (b) files)

A `v2/hcore/singboxadapter` package (name illustrative) owned by `hcore`
itself, not by sing-box, exposing the operations `proxy_info.go` and
`commands.go` actually need:

```go
package singboxadapter

type GroupInfo struct {
    Tag          string
    Type         string
    Selectable   bool
    Selected     string
    Items        []string // member tags, in order
}

// ProxyInspector is the interface hcore's business logic would depend on
// instead of type-asserting against adapter.Outbound/adapter.OutboundGroup
// directly.
type ProxyInspector interface {
    // Outbounds returns every outbound + endpoint tag currently registered.
    Outbounds() []OutboundHandle

    // GroupInfo reports group membership/selection for a tag, or ok=false
    // if the tag isn't a group.
    GroupInfo(tag string) (GroupInfo, bool)

    // Select changes a selector group's active member.
    Select(groupTag, memberTag string) error

    // RealTag resolves through nested groups to find the tag actually
    // carrying traffic right now (wraps monitoring.RealTag).
    RealTag(tag string) string

    // TriggerURLTest starts (or reports active status of) a URL test.
    TriggerURLTest(tag string) error
}

type OutboundHandle interface {
    Tag() string
    DisplayType() string
    Detour() (tag string, ok bool)
}
```

One concrete implementation, `boxProxyInspector`, would hold the current
`*box.Box` and do exactly the type assertions `proxy_info.go`/`commands.go`
do today — just once, in one file, instead of scattered across two. A test
double (`fakeProxyInspector`) would implement the same interface with plain
Go structs and no sing-box import at all.

`proxy_info.go`'s `GetProxyInfo`/`GetAllProxiesInfo` and `commands.go`'s
`readStatus`/`SelectOutbound`/`UrlTestActive`/`UrlTest` would call through
`h.ProxyInspector()` (backed by `service.go`'s existing `Box()` accessor)
instead of calling `box.Outbound()`/`box.Endpoint()` and asserting types
themselves.

For the (a)-classified files: no boundary needed. `platform_interface.go`
already demonstrates the pattern for the platform-facing side; wrapping
`service.go`'s accessors or `grpc_server.go`'s wiring behind a similar
interface would add a layer with no consumer-side benefit, since nothing
downstream of those files does sing-box-specific reasoning.

## Concrete call sites that would move behind the interface

All in the two (b) files, all currently direct sing-box calls:

- `proxy_info.go:31` — `detour.(adapter.OutboundGroup)`
- `proxy_info.go:38` — `monitoring.RealTag(detour)`
- `proxy_info.go:41` — `detour.(*balancer.Balancer)`
- `proxy_info.go:90` — `service.FromContext[adapter.CacheFile](ctx)`
- `proxy_info.go:93-94` — `box.Endpoint().Endpoints()`, `[]adapter.OutboundGroup`
- `proxy_info.go:98,111-112` — `box.Outbound().Outbounds()`, `it.(adapter.OutboundGroup)`
- `proxy_info.go:133` — `iGroup.(*G.Selector)`
- `proxy_info.go:194,198,203,232` — `monitoring.Get(ctx)`, `monitor.SubscribeGroup/UnsubscribeGroup/OutboundsHistory`
- `commands.go:45-48` — `box.Outbound().Outbound(...)`, `currentOutBound.(*group.Selector)`
- `commands.go:52-57` — `box.Outbound().Outbound(current)`, `currentOutBound.(adapter.OutboundGroup)`
- `commands.go:224-244` — `box.Outbound().Outbound(in.GroupTag)`, `outboundGroup.(*group.Selector)`, `selector.SelectOutbound(...)`
- `commands.go:267-296` — `box.Outbound().Outbound(config.OutboundSelectTag)`, two `.(adapter.OutboundGroup)` assertions
- `commands.go:330-331` — `monitoring.Get(h.Context())`, `monitor.TestNow(in.Tag)`

## Testability argument

Currently untestable without a real, running `*daemon.StartedService` /
`*box.Box`:

- `HiddifyInstance.GetProxyInfo` and `GetAllProxiesInfo` (`proxy_info.go`) —
  every branch depends on live `adapter.Outbound`/`adapter.OutboundGroup`
  values from a real box.
- `HiddifyInstance.AllProxiesInfoStream` (`proxy_info.go`) — additionally
  needs a live `monitoring.Monitor` to subscribe to.
- `HiddifyInstance.readStatus` (`commands.go`) — needs a live `box.Outbound()`
  to exercise the "current outbound resolves through a nested group" branch.
- `(*CoreService).SelectOutbound`, `HiddifyInstance.UrlTestActive`,
  `HiddifyInstance.UrlTest` (`commands.go`) — same requirement.

With the `ProxyInspector` boundary sketched above, all of these become
testable against a `fakeProxyInspector` with no sing-box import, no real
network stack, and no box lifecycle to manage.

**Cross-reference with plan 030**: plan 030
(`plans/030-hcore-control-plane-tests.md`) is still **TODO** as of this
writing — `v2/hcore/control_plane_test.go` doesn't exist yet, only
`log_interface_test.go` and `start_test.go` predate it. Plan 030's own scope
explicitly excludes `GetAllProxiesInfo`/`AllProxiesInfoStream` and the
`SelectOutbound`/`UrlTest*` family, calling a fake/stub sing-box instance "a
larger, separate effort" — i.e., plan 030, even once done, deliberately
does not touch the functions this spike is about. It hits the exact same
wall (no fake `*box.Box` exists) rather than working around it. This spike's
testability motivation is therefore still fully live; nothing already
merged or currently planned closes this gap.

## Open questions for the maintainer

**(a) Does `hcore` realistically need to support a second backend, ever?**
No evidence of one today — the whole project (Dart client included) is
built around sing-box specifically (`lib/singbox/` models sing-box's own
config format, `hiddify-core`'s go.mod vendors a sing-box fork directly).
For a second backend to become plausible, this project would need: a reason
a single proxy engine stops covering its protocol/platform needs, a second
Go dependency tree willing to be vendored alongside sing-box, and someone
committing to maintain two adapter implementations forever after. Absent
that, "never" is the honest answer, and per this project's own
ponytail-documented convention against premature abstraction, that's a real
strike against building this now.

**(b) Would a partial abstraction (only the 2 (b) files) be an acceptable
middle ground, or does a half-migrated state read worse than today's
uniform pattern?** Arguably safer than most partial migrations: exactly two
production files (`proxy_info.go`, `commands.go`) would move, all through
one narrow new interface, and the remaining 15 files' direct imports are
either interface implementations (`log_interface.go`,
`service_manager_callback.go`) or wiring/data (`service.go`,
`grpc_server.go`, `static_data.go`, etc.) that a boundary wouldn't touch
anyway — so "some files use the interface, some don't" wouldn't read as
inconsistent, because the ones that don't were never candidates for it. The
main cost of stopping at 2 files is that any *new* type-assertion-heavy
`hcore` function added later (a third `GetProxyInfo`-shaped RPC) has no
established convention pulling it toward the interface instead of repeating
the same direct-type-assertion pattern.

**(c) Could most of the testability benefit be captured more cheaply,
without a full interface boundary?** Likely yes, for at least half of the
value: several of the type-assertion blocks above (e.g. `commands.go`'s
"resolve through one level of nested group" logic at lines 52-57 and
289-296) could be extracted into small pure functions that take already-
resolved primitives (a tag, a slice of member tags, a "now" selection) and
return a display string — testable with zero sing-box import, no interface,
no fake. That wouldn't help `GetAllProxiesInfo`'s tree-building or
`AllProxiesInfoStream`'s subscription logic (those inherently need to walk
live `adapter.Outbound` collections), but it would cover a meaningful slice
of the (b) surface for a much smaller diff than a full `ProxyInspector`.

## Recommendation

Given no concrete second-backend need exists today, do the narrower,
cheaper thing first: extract pure functions out of `proxy_info.go`'s and
`commands.go`'s type-assertion-heavy code per open question (c), and defer
the full `ProxyInspector` interface boundary unless/until a second backend
actually becomes plausible or the extracted-function approach turns out not
to cover enough of the (b) surface in practice.
