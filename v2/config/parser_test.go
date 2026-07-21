package config

import (
	"strings"
	"testing"

	"github.com/sagernet/sing-box/experimental/libbox"
)

// Sing-box and Xray-core configs both use a top-level "outbounds" key, but sing-box identifies
// each outbound's kind with a "type" field while Xray-core uses "protocol" (with the real
// per-protocol settings nested under "settings"/"streamSettings"). looksLikeSingboxSchema must
// tell them apart so a raw Xray-core config falls through to a parser that understands it instead
// of being force-fit through the sing-box unmarshaller.
func TestLooksLikeSingboxSchema(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]interface{}
		want bool
	}{
		{
			name: "no outbounds key",
			obj:  map[string]interface{}{"log": map[string]interface{}{}},
			want: true,
		},
		{
			name: "singbox outbounds",
			obj: map[string]interface{}{
				"outbounds": []interface{}{
					map[string]interface{}{"type": "vmess", "tag": "proxy"},
				},
			},
			want: true,
		},
		{
			name: "xray-core outbounds",
			obj: map[string]interface{}{
				"outbounds": []interface{}{
					map[string]interface{}{
						"protocol": "vmess",
						"tag":      "proxy",
						"settings": map[string]interface{}{},
					},
				},
			},
			want: false,
		},
		{
			name: "xray-core outbounds with freedom/blackhole tail",
			obj: map[string]interface{}{
				"outbounds": []interface{}{
					map[string]interface{}{"protocol": "vmess", "settings": map[string]interface{}{}},
					map[string]interface{}{"protocol": "freedom", "tag": "direct"},
					map[string]interface{}{"protocol": "blackhole", "tag": "block"},
				},
			},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikeSingboxSchema(c.obj); got != c.want {
				t.Fatalf("looksLikeSingboxSchema(%v) = %v, want %v", c.obj, got, c.want)
			}
		})
	}
}

// A raw Xray-core config document (real "protocol"-shaped outbounds, no share-links) can't be
// converted by any parser today - ray2sing only understands individual share-links - but it must
// fail via that path's own error, not get misrouted into the sing-box unmarshaller and fail there
// with an unrelated schema error.
func TestParseConfigContent_XrayCoreDocumentDoesNotMisrouteToSingboxParser(t *testing.T) {
	xrayConfig := `{
		"log": {"loglevel": "warning"},
		"outbounds": [
			{"protocol": "vmess", "tag": "proxy", "settings": {"vnext": []}, "streamSettings": {"network": "tcp"}},
			{"protocol": "freedom", "tag": "direct"}
		],
		"routing": {"rules": []}
	}`

	ctx := libbox.BaseContext(nil)
	_, err := parseConfigContent(ctx, []byte(xrayConfig), false, nil, false)
	if err == nil {
		t.Fatalf("expected an error (this format isn't actually convertible yet), got nil")
	}
	if strings.Contains(err.Error(), "SingboxParser") {
		t.Fatalf("xray-core config was misrouted into the sing-box parser: %v", err)
	}
}

// Some subscription panels export a top-level JSON array of complete, self-contained Xray-core
// config documents (one "profile" per element, each with its own inbounds/outbounds/routing)
// instead of a list of share-links or a single sing-box config. Each element should become its
// own selectable sing-box outbound (type "xray"), delegating the whole document to an embedded
// Xray-core instance - not get rejected as "Incorrect Json Format".
func TestParseConfigContent_XrayConfigArrayParses(t *testing.T) {
	// Trimmed down from a real subscription response reported as "not recognized at all".
	xrayConfigArray := `[
		{
			"remarks": "example vless xhttp",
			"log": {"loglevel": "warning"},
			"inbounds": [
				{"tag": "socks", "port": 10808, "listen": "127.0.0.1", "protocol": "socks", "settings": {"auth": "noauth"}}
			],
			"outbounds": [
				{
					"tag": "proxy",
					"protocol": "vless",
					"settings": {
						"vnext": [{"address": "203.0.113.1", "port": 443, "users": [{"id": "6aca7d1d-632c-464f-b8de-f640962d89c7", "encryption": "none"}]}]
					},
					"streamSettings": {"security": "tls", "network": "xhttp", "tlsSettings": {"serverName": "example.test"}}
				},
				{"tag": "direct", "protocol": "freedom", "settings": {}},
				{"tag": "block", "protocol": "blackhole", "settings": {}}
			],
			"routing": {
				"rules": [
					{"type": "field", "outboundTag": "direct", "domain": ["geosite:cn"]},
					{"type": "field", "port": "0-65535", "outboundTag": "proxy"}
				]
			}
		},
		{
			"remarks": "example trojan grpc",
			"log": {"loglevel": "warning"},
			"outbounds": [
				{
					"tag": "proxy",
					"protocol": "trojan",
					"settings": {"servers": [{"address": "203.0.113.2", "port": 443, "password": "hunter2"}]},
					"streamSettings": {"security": "tls", "network": "grpc"}
				},
				{"tag": "direct", "protocol": "freedom", "settings": {}}
			]
		}
	]`

	ctx := libbox.BaseContext(nil)
	options, err := parseConfigContent(ctx, []byte(xrayConfigArray), false, nil, false)
	if err != nil {
		t.Fatalf("expected the xray-config array to parse, got error: %v", err)
	}
	if len(options.Outbounds) != 2 {
		t.Fatalf("expected 2 outbounds (one per array element), got %d: %+v", len(options.Outbounds), options.Outbounds)
	}
	for _, ob := range options.Outbounds {
		if ob.Type != "xray" {
			t.Fatalf("expected outbound type %q, got %q (tag %q)", "xray", ob.Type, ob.Tag)
		}
		if !strings.HasPrefix(ob.Tag, "example ") {
			t.Fatalf("expected outbound tag to start with its 'remarks', got %q", ob.Tag)
		}
	}
}

// Reported as "the SSH configuration isn't recognized by the client at all": a full sing-box JSON
// config (panel-generated) using the pre-1.13 "type": "dns" outbound + a route rule pointing at it
// via "outbound", which sing-box now hard-rejects during unmarshalling regardless of whether the
// route section is even kept (it isn't, outside full-config mode) - see migrateLegacyDNSOutbounds.
func TestParseConfigContent_LegacyDNSOutboundMigrates(t *testing.T) {
	legacyConfig := `{
		"outbounds": [
			{"type": "ssh", "tag": "ssh-out", "server": "203.0.113.1", "server_port": 22, "user": "root"},
			{"tag": "direct", "type": "direct"},
			{"tag": "block", "type": "block"},
			{"tag": "dns-out", "type": "dns"}
		],
		"route": {
			"final": "ssh-out",
			"rules": [
				{"outbound": "dns-out", "port": [53]}
			]
		}
	}`

	ctx := libbox.BaseContext(nil)
	options, err := parseConfigContent(ctx, []byte(legacyConfig), false, nil, false)
	if err != nil {
		t.Fatalf("expected the legacy dns-outbound config to parse, got error: %v", err)
	}
	for _, ob := range options.Outbounds {
		if ob.Type == "dns" {
			t.Fatalf("legacy 'dns' outbound should have been migrated away, still present: %+v", ob)
		}
	}
}

// Same report, but exercised in full-config mode (EnableFullConfig), where the route/dns/inbounds
// sections are kept verbatim rather than discarded - so the legacy DNS server address format
// ("address": "tcp://1.1.1.1") and legacy per-inbound sniff fields ("sniff": true on the inbound
// itself, pre-1.11) both need migrating too, or the config fails at unmarshal time even after the
// dns-outbound fix above. See migrateLegacyDNSServers / migrateLegacyInboundSniffFields.
//
// Known gaps intentionally not exercised here (both confirmed present in the real reported
// config, neither fixed by this pass): a DNS server using "rcode://" or "fakeip" has no
// mechanical new-format equivalent and is dropped rather than migrated; a route rule matching on
// legacy "geoip" is a separate deprecation (pre-1.12) this pass doesn't touch.
func TestParseConfigContent_LegacyDNSServerAndInboundFieldsMigrate(t *testing.T) {
	legacyConfig := `{
		"dns": {
			"servers": [
				{"address": "tcp://1.1.1.1", "address_resolver": "dns-local", "strategy": "prefer_ipv4", "tag": "dns-remote"},
				{"address": "local", "tag": "dns-local"}
			],
			"final": "dns-remote"
		},
		"inbounds": [
			{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080, "sniff": true, "sniff_override_destination": false}
		],
		"outbounds": [
			{"type": "ssh", "tag": "ssh-out", "server": "203.0.113.1", "server_port": 22, "user": "root"},
			{"tag": "direct", "type": "direct"}
		],
		"route": {
			"final": "ssh-out"
		}
	}`

	ctx := libbox.BaseContext(nil)
	fullOpt := DefaultHiddifyOptions()
	fullOpt.EnableFullConfig = true
	options, err := parseConfigContent(ctx, []byte(legacyConfig), false, fullOpt, true)
	if err != nil {
		t.Fatalf("expected the legacy full-config document to parse, got error: %v", err)
	}
	foundSniffRule := false
	for _, rule := range options.Route.Rules {
		if rule.DefaultOptions.Action == "sniff" {
			foundSniffRule = true
		}
	}
	if !foundSniffRule {
		t.Fatalf("expected the legacy 'sniff' inbound field to migrate into a sniff rule action, got rules: %+v", options.Route.Rules)
	}
}

func TestParseConfigContent_SingboxDocumentStillParses(t *testing.T) {
	singboxConfig := `{
		"outbounds": [
			{"type": "direct", "tag": "direct"}
		]
	}`

	ctx := libbox.BaseContext(nil)
	_, err := parseConfigContent(ctx, []byte(singboxConfig), false, nil, false)
	if err != nil {
		t.Fatalf("expected a valid sing-box config to parse, got error: %v", err)
	}
}
