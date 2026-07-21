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
