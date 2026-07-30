# Spike: consolidating the sing-box/xray-core protocol builders

Status: investigation only, no code changed. See `plans/036-investigate-protocol-builder-consolidation.md`
in the Dart repo for the plan this document fulfills.

`ray2sing/ray2sing/` turns share-links (`vless://`, `vmess://`, ...) into outbound
configs for two engines: sing-box (native) and an embedded xray-core. Today exactly
**4 protocols** are built by both backends — confirmed directly from `convert.go`'s
`xrayConfigTypes` map (not the ~4 estimate the plan started from, the actual, current,
complete list):

```go
var xrayConfigTypes = map[string]ParserFunc{
	"vmess://":  VmessXray,
	"vless://":  VlessXray,
	"trojan://": TrojanXray,
	"direct://": DirectXray,
}
```

Each of the 4 pairs independently re-parses the same decoded param map
(`ParseUrl`'s `map[string]string`, confirmed shared and unmodified between the two
call paths — the duplication starts strictly after parsing, not at parsing itself)
into its own output shape: sing-box builders return a `*T.Outbound` (sing-box's
typed `option` package); xray builders build a `map[string]any` shaped like
xray-core's JSON config, which is then wrapped into a `*T.Outbound` too (via
`makeXrayOptions` in `xray_base.go`, `XrayOutboundOptions.XConfig`) — so both paths
end up returning the same Go type, the actual output-shape mismatch is one level
deeper (`option.VLESSOutboundOptions` struct fields vs. a loosely-typed
`map[string]any`).

## 1. Per-protocol field inventory

### VLESS — `vless.go` (sing-box) vs. `xrayvless.go` + `xray_common.go` (xray)

| | sing-box (`VlessSingbox`) | xray (`VlessXray`) |
|---|---|---|
| Reads | `security`/`tls`/`reality`, `sni`/`add`, `ech`, `fp`, `alpn`, `net`/`type`, `host`, `path`/`servicename`, `flow`, `packetencoding`, `nosni`, `pcs`/`insecure`, `pbk`/`sid`, `muxtype`/`muxmaxc`/`muxsmax`/`muxup`/`muxdown`/`muxpad`/`mux` | `security`/`tls`/`reality`, `sni`/`add`, `fp`, `alpn`, `net`/`type`, `host`, `path`/`servicename`, `flow`, `pcs`/`insecure`, `pbk`/`sid`/`spx`, `fm`, `mux` |
| Writes | `option.VLESSOutboundOptions{DialerOptions, ServerOptions, UUID, PacketEncoding, Flow, TLS(+Reality), Transport, Multiplex}` | `map{protocol:"vless", settings.vnext[0]{address,port,users[0]{id,encryption,flow}}, streamSettings{network,<type>Settings,security,tlsSettings/realitySettings,finalmask}, mux}` |
| Shared helper | `getTLSOptions` / `getTransportOptions` / `getMuxOptions` / `getDialerOptions` (`common.go`) | `getTLSOptionsXray` / `getRealityOptionsXray` / `getStreamSettingsXray` / `getMuxOptionsXray` (`xray_common.go`) |

Confirmed divergences:
- **ECH** (`decoded["ech"]`) is only implemented in `getTLSOptions` (`common.go:38-50`).
  `getTLSOptionsXray`/`getRealityOptionsXray` have no ECH handling at all — a link
  using ECH silently loses that setting when routed through the xray backend.
- **`nosni`** (`DisableSNI`) is only honored by `getTLSOptions` (`common.go:62`,
  which also skips setting `UTLS` when SNI is disabled). The xray TLS/reality
  helpers have no equivalent flag — same param, different behavior per backend.
- **Mux feature set**: `getMuxOptions` reads `muxtype`, `muxmaxc`, `muxsmax`,
  `muxup`/`muxdown` (Brutal), `muxpad` in addition to `mux` (min streams).
  `getMuxOptionsXray` reads only `mux`, mapped to `concurrency`. A link setting
  any of the other mux params gets full sing-box mux behavior but only partial
  (concurrency-only) xray mux behavior.
- **finalmask (`fm`)** is xray-only by design (confirmed via `convert.go`'s
  `requiresXrayCore`, which forces the xray backend whenever `fm` or KCP is
  present) — not a bug, but it means the "shared" transport surface isn't
  actually 100% shared even conceptually; any consolidated IR needs an
  xray-only escape hatch for this field.
- Fingerprint default (`fp` defaulting to `"chrome"` for reality when omitted):
  **already fixed**, see finding 2 below — not live divergence evidence anymore,
  included here only for completeness of the historical pattern.

### VMess — `vmess.go` (sing-box) vs. `xrayvmess.go` + `xray_common.go` (xray)

Both start from the same `decodeVmess` (base64+JSON, not `ParseUrl` — vmess links
carry their own JSON blob rather than query params) so the shared-decoder property
holds here too, just via a different decoder.

| | sing-box (`VmessSingbox`) | xray (`VmessXray`) |
|---|---|---|
| Reads | `ps`, `add`, `port`, `id`, `aid`, `scy`, `net`/`type`/`host`/`path` (transport), TLS/mux fields as above | `ps`, `add`, `port`, `id`, `scy`, same transport/mux fields |
| Writes | `option.VMessOutboundOptions{..., UUID, Security, AlterId, GlobalPadding:false, AuthenticatedLength:true, PacketEncoding, ...}` | `map{protocol:"vmess", settings.vnext[0]{address,port,users[0]{id,security}}, streamSettings, mux}` |

Confirmed divergences:
- **`aid` (AlterId)** is read and set by the sing-box builder (`vmess.go:76`,
  `AlterId: toInt(decoded["aid"])`) but never read at all by `VmessXray` — a
  legacy vmess link with a non-zero `aid` silently gets `alterId: 0` (VMessAEAD
  default) through the xray backend only.
- **Port parsing type mismatch — a live correctness bug, not just a style
  difference.** `vmess.go:52` parses the port with `toUInt16(decoded["port"], 443)`
  (`uint16`, correct 0–65535 range). `xrayvmess.go:13` parses the *same* field with
  `toInt16(decoded["port"], 443)` (`int16`, `common.go:497-505`). `toInt16` accepts
  17-bit values from `strconv.ParseInt(s, 10, 17)` (so parsing itself doesn't
  error for e.g. `"51820"`) but then casts to a signed 16-bit int, so any port
  above 32767 wraps to a negative number (`51820` → `-13716`) before it's written
  into the xray JSON `"port"` field. This is exactly the class of bug this plan's
  "why this matters" section warned about — two independent re-implementations
  of "parse this field" drifting apart — found by direct inspection while doing
  this inventory (`grep -rn "toInt16(" ray2sing/ray2sing/` shows it used nowhere
  else in a way that would have surfaced this earlier). Per this plan's scope,
  **not fixed here** — flagged as supporting evidence only.
- `GlobalPadding`/`AuthenticatedLength` (VMessAEAD framing flags) have no explicit
  xray-side equivalent; relies on xray-core's own defaults matching sing-box's
  hardcoded `false`/`true`. Not verified either way — flagged as an open question,
  not a confirmed bug.

### Trojan — `trojan.go` vs. `xraytrojan.go`

Fully symmetric — no confirmed divergence. Both read exactly `security`/`tls`,
`sni`/`add`, transport (`net`/`type`/`host`/`path`), and mux fields through the
same two helper families, and write straightforward `password`/`address`/`port`
shapes. This is the cleanest pair and the best low-risk starting point if the
maintainer wants a single protocol to pilot consolidation on (see recommendation).

### Direct — `direct.go` vs. `xraydirect.go`

| | sing-box (`DirectSingbox`) | xray (`DirectXray`) |
|---|---|---|
| Reads | nothing beyond `ParseUrl`'s own host/port (fragment/tricks helpers exist in `common.go` but are dead code — see below) | `frg`/`fragment` (packet fragmentation, parsed manually: `packets,length,interval`) |
| Writes | `option.DirectOutboundOptions{DialerOptions}` | `map{protocol:"freedom", domainStrategy:"AsIs", settings.fragment, streamSettings.sockopt{tcpNoDelay,tcpKeepAliveIdle}}` |

This is the most divergent pair of the four, structurally: the xray builder
implements its own ad hoc fragmentation and socket-option handling that the
sing-box builder doesn't attempt at all. `getDialerOptions` (`common.go:426-432`,
used by every sing-box protocol builder including `direct.go`) is dead code —
its fragment/TLS-tricks wiring is entirely commented out, while
`option.TLSFragmentOptions`/`option.TLSTricksOptions` (defined in `common.go:94-133`,
also unused anywhere in `ray2sing`) *are* live and wired up elsewhere in the
non-`ray2sing` config builder (`v2/config/builder.go`, `v2/config/outbound.go`,
`hiddify-sing-box/option/{fragment,outbound,tls,tls_tricks}.go`). That's a
pre-existing, unrelated dead-code/duplication finding outside this plan's scope
(`ray2sing` only) — not fixed here, noted only as further evidence that these two
builder families (and even the sing-box side alone, vs. the rest of the codebase)
already drift.

No dedicated `direct` test exists in `ray2sing_test/` (see open question (c)).

## 2. Fingerprint-divergence finding — re-verified, now fixed

The plan's motivating example (`common.go:52-54` defaulting `fp` to `"chrome"` for
reality links, `xray_common.go:271-273` having the same default commented out) was
re-checked directly against the current code and **is no longer live**:

- `common.go:52-54` — sing-box side, unchanged: defaults `fp` to `"chrome"` only
  when `security == "reality"`.
- `xray_common.go:271-276` (`getTLSOptionsXray`, non-reality TLS path) —
  now carries an explicit comment stating the empty-fingerprint behavior is
  *intentional* and matches sing-box's non-reality behavior.
- `xray_common.go:325-328` (`getRealityOptionsXray`, reality path) — now
  unconditionally defaults `fp` to `"chrome"` when empty, matching `common.go:52-54`.

Per this plan's STOP condition for this case: the original evidence is resolved,
so it is cited above for context only, not as live evidence. The two divergences
found in its place (as "supporting evidence that this is not hypothetical") are:
the VLESS **ECH**/**`nosni`** gaps and the VMess **port `int16` truncation bug**,
both documented in section 1 above.

No mux/transport-side sibling of the *original* fp bug (a present-on-one-side-only
*default value*) was found beyond what's already listed — the mux/transport
divergences found are feature-scope gaps (a param one side reads and the other
doesn't), not silently-different defaults for the same param.

## 3. Sketched shared intermediate-representation design

### VLESS, in detail

```go
// parsedVlessOptions is populated once from the decoded param map, then adapted
// to each backend's output shape. Every field either backend currently reads
// from the param map independently appears exactly once here.
type parsedVlessOptions struct {
	Tag            string
	Server         string
	Port           uint16
	UUID           string
	Flow           string
	PacketEncoding string // sing-box only; no xray equivalent field exists today

	Transport transportOptions // net/type, host, path — shared shape, see below
	TLS       *tlsOptions      // nil if neither "tls" nor "reality"
	Mux       *muxOptions      // nil if no mux param present

	// xray-only escape hatch — fields with no sing-box equivalent (finalmask).
	// Keeping this as an explicit, clearly-labeled field rather than silently
	// dropping it (or silently threading it through the shared struct as if it
	// were universal) is itself part of the design question in 4(a)/4(c).
	XrayFinalMask []any
}

type tlsOptions struct {
	ServerName    string
	ALPN          []string
	Fingerprint   string // already-resolved: reality default applied once, here
	DisableSNI    bool
	ECH           *echOptions // nil if absent; adapters decide xray has no ECH output
	Insecure      bool
	PinnedCertSha256 []string
	Reality       *realityOptions // nil unless security == "reality"
}

type realityOptions struct {
	PublicKey string
	ShortID   string
	SpiderX   string // xray-only today (getRealityOptionsXray sets it, sing-box's
	                 // Reality options struct has no SpiderX field) — another
	                 // concrete field-level gap this exercise surfaced.
}

type muxOptions struct {
	Enabled     bool
	Concurrency int    // -> xray "concurrency", sing-box MinStreams (today's mapping,
	                    // itself worth a closer look — see 4(c))
	Protocol    string // sing-box-only: muxtype
	MaxConnections, MaxStreams int // sing-box-only
	Padding     bool   // sing-box-only
	Brutal      *struct{ UpMbps, DownMbps int } // sing-box-only
}

func toSingboxOutbound(o parsedVlessOptions) *T.Outbound        { /* ... */ }
func toXrayJSON(o parsedVlessOptions) (map[string]any, error)   { /* ... */ }
```

The two adapters replace `vless.go` and `xrayvless.go`'s current from-scratch
reparse: one `parseVlessOptions(decoded map[string]string) (parsedVlessOptions, error)`
function becomes the single place `fp`'s reality-default (or any future
per-field default) is computed, so it is structurally impossible for the two
backends to see a different resolved value for the same input field — that is
the whole point of this consolidation, not just a line-count reduction.

### VMess, Trojan, Direct — briefer sketches

- **VMess** needs the same `transportOptions`/`tlsOptions`/`muxOptions` shapes as
  VLESS plus VMess-specific fields (`Security`, `AlterId`, `GlobalPadding`,
  `AuthenticatedLength`). The `AlterId` gap (section 1) means
  `toXrayJSON` needs an explicit decision — silently drop it (current de facto
  behavior) or thread it through and let xray-core's JSON schema accept/reject
  it. The port-parsing bug disappears for free once there's exactly one
  `toUInt16` call site instead of two.
- **Trojan** is the simplest case: `parsedTrojanOptions{Tag, Server, Port,
  Password, Transport, TLS, Mux}` — a direct subset of the VLESS shape with no
  protocol-specific fields at all. Given it's already fully symmetric (section
  1), this is the lowest-risk pilot candidate.
- **Direct** is the outlier: it needs a different shape entirely
  (`parsedDirectOptions{Tag, DomainStrategy, Fragment *fragmentOptions,
  SockOpt *sockOptOptions}`) since today the two backends don't even agree on
  which features exist (xray does ad hoc fragmentation/sockopt; sing-box's
  fragment wiring is dead code in this package). Consolidating this pair means
  first deciding whether sing-box's dead fragment code should become live
  (a scope decision, not a refactor detail) — flagged in the open questions.

## 4. Open questions for the maintainer

**(a) Is the risk worth it, given the real count is 4 (not growing)?**
`convert.go`'s `xrayConfigTypes` has held exactly 4 entries (vmess, vless,
trojan, direct) for as long as this investigation could observe — no evidence
of it trending up or down. The risk (config-generation hot path, a bad refactor
breaks proxy connectivity broadly) is fixed regardless of protocol count, but
the *benefit* scales with how many more divergence bugs are likely to accumulate
across just these 4 pairs. This spike found 2 additional concrete divergences
beyond the original fp one (VLESS ECH/`nosni` gaps, VMess port-type bug) in a
single read-through — that's evidence the drift is real and ongoing, not a
one-off.

**(b) Protocol-by-protocol or all at once?**
Given Trojan is already fully symmetric and the simplest pair, it's the
natural low-risk pilot. VLESS is next-simplest but has the most feature gaps to
reconcile (ECH, `nosni`, mux). Direct is structurally the most different
(different domain entirely — freedom/fragment vs. dialer options) and probably
shouldn't be bundled with the other 3 even if they're done together. VMess sits
in between.

**(c) What test coverage should exist first?**
`ray2sing_test/` has `vless_test.go`, `vmess_test.go`, `trojan_test.go` — but
**no `direct_test.go`**, and the existing tests (confirmed by reading
`vless_test.go`) only exercise the sing-box builder path — none of them appear
to call `VlessXray`/`VmessXray`/`TrojanXray`/`DirectXray` or otherwise exercise
`xrayConfigTypes` at all. That means today there is **no automated regression
coverage for the xray backend of any of the 4 dual-backend protocols** — a
refactor could silently break `VlessXray` etc. and every existing test would
still pass. Characterization tests covering the xray output shape (at minimum
one fixture per protocol, mirroring the existing sing-box-side JSON-comparison
style) should exist *before* any refactor is attempted, not as a follow-up.

## 5. Recommendation

**Do it protocol-by-protocol, starting with Trojan** (already symmetric, lowest
risk, validates the adapter-pair pattern cheaply) **only after adding xray-side
characterization tests for all 4 protocols** (open question (c)) — this is a
recommendation for the maintainer to weigh against other priorities, not a
decision being made here; a P3/tech-debt item with a real but bounded (4
protocols, currently stable) blast radius doesn't obviously justify immediate,
all-at-once L-effort work, but the two divergences found in this single
read-through (beyond the one that motivated the plan) suggest waiting for "a
3rd divergence bug" is already effectively satisfied.
