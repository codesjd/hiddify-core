package config

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hiddify/ray2sing/ray2sing"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/libbox"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/batch"
	SJ "github.com/sagernet/sing/common/json"
	"github.com/xmdhs/clash2singbox/convert"
	clash2singmodel "github.com/xmdhs/clash2singbox/model"
	"github.com/xmdhs/clash2singbox/model/clash"
	"gopkg.in/yaml.v3"
)

//go:embed config.json.template
var configByte []byte

func ReadContent(ctx context.Context, opt *ReadOptions) ([]byte, error) {
	if opt.Content == "" {
		contentBytes, err := os.ReadFile(opt.Path)
		if err != nil {
			return nil, err
		}
		opt.Content = string(contentBytes)
	}
	return []byte(opt.Content), nil
}

func ParseConfig(ctx context.Context, opt *ReadOptions, debug bool, configOpt *HiddifyOptions, fullConfig bool) (*option.Options, error) {
	content, err := ReadContent(ctx, opt)
	if err != nil {
		return nil, err
	}
	return parseConfigContent(ctx, content, debug, configOpt, fullConfig)
}

func ParseConfigBytes(ctx context.Context, opt *ReadOptions, debug bool, configOpt *HiddifyOptions, fullConfig bool) ([]byte, error) {

	options, err := ParseConfig(ctx, opt, debug, configOpt, fullConfig)
	if err != nil {
		return nil, err
	}

	return options.MarshalJSONContext(ctx)

}
func parseConfigContent(ctx context.Context, content []byte, debug bool, configOpt *HiddifyOptions, fullConfig bool) (*option.Options, error) {
	if configOpt == nil {
		configOpt = DefaultHiddifyOptions()
	}

	var jsonObj map[string]interface{} = make(map[string]interface{})

	var tmpJsonResult any
	jsonDecoder := json.NewDecoder(SJ.NewCommentFilter(bytes.NewReader(content)))
	if err := jsonDecoder.Decode(&tmpJsonResult); err == nil {
		if tmpJsonObj, ok := tmpJsonResult.(map[string]interface{}); ok && looksLikeSingboxSchema(tmpJsonObj) {
			fmt.Printf("Convert using json\n")
			migrateLegacyDNSOutbounds(tmpJsonObj)
			if dns, ok := tmpJsonObj["dns"].(map[string]interface{}); ok {
				migrateLegacyDNSServers(dns)
			}
			migrateLegacyInboundSniffFields(tmpJsonObj)
			if tmpJsonObj["outbounds"] == nil && tmpJsonObj["endpoints"] == nil {
				jsonObj["outbounds"] = []interface{}{tmpJsonObj}
			} else {
				if fullConfig || (configOpt != nil && configOpt.EnableFullConfig) {
					jsonObj = tmpJsonObj
				} else {
					if tmpJsonObj["outbounds"] != nil {
						jsonObj["outbounds"] = tmpJsonObj["outbounds"]
					}
					if tmpJsonObj["endpoints"] != nil {
						jsonObj["endpoints"] = tmpJsonObj["endpoints"]
					}
				}
			}

			newContent, _ := json.MarshalIndent(jsonObj, "", "  ")

			return patchConfigStr(ctx, newContent, "SingboxParser", configOpt)
		} else if arr, ok := tmpJsonResult.([]interface{}); ok {
			// A top-level JSON array is how some panels export a subscription offering several
			// complete, self-contained Xray-core configs at once (one "profile" per array
			// element, each with its own inbounds/outbounds/routing) rather than a list of
			// share-links. Each element becomes a sing-box outbound of the existing "xray" type,
			// handing the whole document to an embedded Xray-core instance to run as a
			// self-contained outbound - the same mechanism ray2sing already uses for individual
			// xray-core-backed links (see xray_base.go's makeXrayOptions).
			if outbounds := xrayConfigArrayToOutbounds(arr); len(outbounds) > 0 {
				return patchConfigOptions(ctx, &option.Options{Outbounds: outbounds}, "XrayJsonArrayParser", configOpt)
			}
		}
		// Valid JSON, but not sing-box's outbound schema - most commonly a raw Xray-core config
		// (which also has a top-level "outbounds" key, just with "protocol"-shaped entries
		// instead of sing-box's "type"-shaped ones). Fall through to the clash/ray2sing parsers
		// below instead of forcing it through the sing-box path, where it can only fail.
	}

	fmt.Printf("Convert using clash\n")
	clashObj := clash.Clash{}
	if err := yaml.Unmarshal(content, &clashObj); err == nil && clashObj.Proxies != nil {
		if len(clashObj.Proxies) == 0 {
			return nil, fmt.Errorf("[ClashParser] no outbounds found")
		}
		converted, endpoints, err := convert.Clash2sing(clashObj, clash2singmodel.SINGLATEST)
		if err != nil {
			return nil, fmt.Errorf("[ClashParser] converting clash to sing-box error: %w", err)
		}
		output := configByte
		output, err = convert.Patch(output, converted, endpoints, "", "", nil)
		if err != nil {
			return nil, fmt.Errorf("[ClashParser] patching clash config error: %w", err)
		}
		return patchConfigStr(ctx, output, "ClashParser", configOpt)
	}

	v2ray, err := ray2sing.Ray2SingboxOptions(ctx, string(content), configOpt.UseXrayCoreWhenPossible)
	if err == nil {
		return patchConfigOptions(ctx, v2ray, "V2rayParser", configOpt)
	}

	return nil, fmt.Errorf("unable to determine config format")
}

// migrateLegacyDNSServers rewrites the pre-1.12 DNS server format (identified by a bare "address"
// URL, e.g. "tcp://1.1.1.1") into the current "type"+"server" shape, and renames the per-server
// "address_resolver" field to "domain_resolver" (see migration.md, "Migrate to new DNS server
// formats" / "Servers with domain address"). Only handles the schemes with a direct type+server
// equivalent (local/tcp/udp/tls/https/quic/h3) - "rcode://", "fakeip", and "dhcp://" servers have
// no server-level equivalent at all now (rcode:// becomes a DNS rule action, fakeip needs its
// top-level "dns.fakeip" block merged in, dhcp needs an "interface" field derived from the
// address), so rather than guess at those structural rewrites, such servers are dropped from the
// list entirely - same reasoning as migrateLegacyDNSOutbounds: a config that fails to parse can't
// use that server anyway, so removing it (and leaving anything that referenced its tag to fall
// back to the DNS config's "final" server) is strictly better than a hard failure.
//
// The per-server "strategy" field is also dropped: it's a hard unknown-field error under the new
// schema, and its replacement is context-dependent (migration.md moves it to either the top-level
// "dns.strategy" default or a specific DNS rule's "strategy", depending on which server it was on)
// in a way that can't be inferred generically here. Losing a non-default per-server strategy
// override is a real, known gap, but strictly better than the config not parsing at all.
func migrateLegacyDNSServers(dns map[string]interface{}) {
	servers, ok := dns["servers"].([]interface{})
	if !ok {
		return
	}
	filtered := servers[:0]
	for _, item := range servers {
		server, ok := item.(map[string]interface{})
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		if _, hasType := server["type"]; hasType {
			filtered = append(filtered, item)
			continue
		}
		addr, ok := server["address"].(string)
		if !ok {
			filtered = append(filtered, item)
			continue
		}

		var serverType, host string
		switch {
		case addr == "local":
			serverType = "local"
		case strings.Contains(addr, "://"):
			u, err := url.Parse(addr)
			if err != nil {
				continue
			}
			switch u.Scheme {
			case "tcp", "udp", "tls", "https", "quic", "h3":
				serverType = u.Scheme
				host = u.Host
			default:
				continue // rcode://, fakeip, dhcp:// etc - no mechanical equivalent, drop
			}
		default:
			serverType = "udp"
			host = addr
		}

		delete(server, "address")
		delete(server, "strategy")
		server["type"] = serverType
		if host != "" {
			server["server"] = host
		}
		if resolver, ok := server["address_resolver"]; ok {
			server["domain_resolver"] = resolver
			delete(server, "address_resolver")
		}
		filtered = append(filtered, item)
	}
	dns["servers"] = filtered
}

// migrateLegacyInboundSniffFields rewrites the pre-1.11 per-inbound "sniff"/"sniff_timeout"/
// "domain_strategy" fields into the "sniff"/"resolve" route rule actions sing-box now requires
// instead (see migration.md, "Migrate legacy inbound fields to rule actions"). Assigns a tag to
// any affected inbound that doesn't already have one, since the route rules need something to
// target. "sniff_override_destination" has no equivalent in the new rule-action model at all and
// is just dropped - true is not overwhelmingly the common case, and there is no substitute action
// field to move it to.
func migrateLegacyInboundSniffFields(doc map[string]interface{}) {
	inbounds, ok := doc["inbounds"].([]interface{})
	if !ok {
		return
	}
	var newRules []interface{}
	for i, item := range inbounds {
		inbound, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		_, hasSniff := inbound["sniff"]
		_, hasSniffTimeout := inbound["sniff_timeout"]
		_, hasDomainStrategy := inbound["domain_strategy"]
		if !hasSniff && !hasSniffTimeout && !hasDomainStrategy {
			continue
		}

		tag, _ := inbound["tag"].(string)
		if tag == "" {
			tag = fmt.Sprintf("legacy-inbound-%d", i)
			inbound["tag"] = tag
		}

		if strategy, ok := inbound["domain_strategy"]; ok {
			newRules = append(newRules, map[string]interface{}{
				"inbound":  tag,
				"action":   "resolve",
				"strategy": strategy,
			})
			delete(inbound, "domain_strategy")
		}
		if sniff, _ := inbound["sniff"].(bool); sniff {
			rule := map[string]interface{}{
				"inbound": tag,
				"action":  "sniff",
			}
			if timeout, ok := inbound["sniff_timeout"]; ok {
				rule["timeout"] = timeout
			}
			newRules = append(newRules, rule)
		}
		delete(inbound, "sniff")
		delete(inbound, "sniff_timeout")
		delete(inbound, "sniff_override_destination")
	}
	if len(newRules) == 0 {
		return
	}
	route, ok := doc["route"].(map[string]interface{})
	if !ok {
		route = make(map[string]interface{})
		doc["route"] = route
	}
	existingRules, _ := route["rules"].([]interface{})
	route["rules"] = append(newRules, existingRules...)
}

// migrateLegacyDNSOutbounds rewrites the pre-1.13 pattern of a "type": "dns" outbound plus a route
// rule pointing at it via "outbound" into the "action": "hijack-dns" route rule sing-box now
// requires instead (see hiddify-sing-box/docs/migration.md, "Migrate DNS outbound to rule action").
// Older backends (hiddify-manager panels, hand-written configs following older docs) still
// generate the legacy shape, and sing-box hard-rejects any "type": "dns" outbound outright now
// rather than just warning about it - such a config fails to parse at all without this, even
// outside full-config mode, since the deprecated outbound gets rejected during unmarshalling
// regardless of whether anything in the route section actually gets kept.
func migrateLegacyDNSOutbounds(doc map[string]interface{}) {
	outboundsRaw, ok := doc["outbounds"].([]interface{})
	if !ok {
		return
	}
	dnsTags := make(map[string]bool)
	filtered := outboundsRaw[:0]
	for _, item := range outboundsRaw {
		if ob, ok := item.(map[string]interface{}); ok {
			if t, _ := ob["type"].(string); t == "dns" {
				if tag, _ := ob["tag"].(string); tag != "" {
					dnsTags[tag] = true
				}
				continue
			}
		}
		filtered = append(filtered, item)
	}
	if len(dnsTags) == 0 {
		return
	}
	doc["outbounds"] = filtered

	route, ok := doc["route"].(map[string]interface{})
	if !ok {
		return
	}
	rules, ok := route["rules"].([]interface{})
	if !ok {
		return
	}
	for _, item := range rules {
		rule, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if outboundTag, _ := rule["outbound"].(string); dnsTags[outboundTag] {
			delete(rule, "outbound")
			rule["action"] = "hijack-dns"
		}
	}
}

// looksLikeSingboxSchema reports whether a decoded JSON object matches sing-box's config schema
// well enough to be worth unmarshalling as one. Sing-box and Xray-core configs share several
// top-level key names (both use "outbounds", for instance), so the mere presence of an
// "outbounds" key isn't enough to tell them apart - only the shape of the entries is: sing-box
// identifies each outbound's kind with a "type" field, while Xray-core uses "protocol" instead
// (with the real per-protocol settings nested under "settings"/"streamSettings"). A raw Xray-core
// config would otherwise get force-fit through the sing-box unmarshaller here and fail with a
// confusing schema error, instead of falling through to a parser that actually understands it.
func looksLikeSingboxSchema(obj map[string]interface{}) bool {
	outboundsRaw, ok := obj["outbounds"]
	if !ok {
		return true
	}
	outboundsArr, ok := outboundsRaw.([]interface{})
	if !ok {
		return true
	}
	for _, item := range outboundsArr {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		_, hasProtocol := entry["protocol"]
		_, hasType := entry["type"]
		if hasProtocol && !hasType {
			return false
		}
	}
	return true
}

// xrayRealProxyOutbound picks the actual proxy outbound out of a full Xray-core config document's
// "outbounds" array. The xray outbound adapter (hiddify-sing-box/protocol/hiddify/xray/outbound.go)
// wraps exactly one xray-core outbound detour config (protocol/settings/streamSettings) and dials
// straight to its handler, bypassing xray-core's own router - it isn't given a whole document with
// inbounds/routing, so the boilerplate "direct"/"block"/fragment entries every element in this
// format carries alongside the real proxy have to be filtered out here first. "proxy" is the tag
// every observed generator of this format uses for the real entry; fall back to the first entry
// whose protocol isn't one of the known boilerplate ones, in case some panel uses a different tag.
func xrayRealProxyOutbound(entry map[string]interface{}) map[string]interface{} {
	outboundsRaw, ok := entry["outbounds"].([]interface{})
	if !ok {
		return nil
	}
	var fallback map[string]interface{}
	for _, item := range outboundsRaw {
		ob, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if tag, _ := ob["tag"].(string); tag == "proxy" {
			return ob
		}
		if fallback == nil {
			switch protocol, _ := ob["protocol"].(string); protocol {
			case "freedom", "blackhole", "dns", "":
			default:
				fallback = ob
			}
		}
	}
	return fallback
}

// xrayConfigArrayToOutbounds converts a JSON array of complete Xray-core config documents (each
// with its own "outbounds" containing "protocol"-shaped entries, per looksLikeSingboxSchema) into
// one sing-box "xray"-type outbound per element - a format some subscription panels use to offer
// several full "profiles" at once instead of a list of share-links. Elements that aren't full
// Xray-core documents, or whose real proxy outbound can't be identified, are skipped rather than
// aborting the whole array, since a subscription may mix in unrelated entries.
func xrayConfigArrayToOutbounds(arr []interface{}) []option.Outbound {
	var outbounds []option.Outbound
	for i, item := range arr {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasOutbounds := entry["outbounds"]; !hasOutbounds || looksLikeSingboxSchema(entry) {
			continue
		}
		proxyOutbound := xrayRealProxyOutbound(entry)
		if proxyOutbound == nil {
			continue
		}
		tag, _ := entry["remarks"].(string)
		if tag == "" {
			tag = fmt.Sprintf("xray-config-%d", i)
		}
		tag = fmt.Sprintf("%s § %d", tag, i)
		outbounds = append(outbounds, option.Outbound{
			Type: C.TypeXray,
			Tag:  tag,
			Options: &option.XrayOutboundOptions{
				XConfig: &proxyOutbound,
			},
		})
	}
	return outbounds
}

func patchConfigStr(ctx context.Context, content []byte, name string, configOpt *HiddifyOptions) (*option.Options, error) {
	options := option.Options{}
	err := options.UnmarshalJSONContext(ctx, content)

	if err != nil {
		return nil, fmt.Errorf("[SingboxParser] unmarshal error: %w", err)
	}

	return patchConfigOptions(ctx, &options, name, configOpt)
}
func patchConfigOptions(ctx context.Context, options *option.Options, name string, configOpt *HiddifyOptions) (*option.Options, error) {
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[*option.Endpoint](2))
	for _, base := range options.Endpoints {
		out := base
		b.Go(base.Tag, func() (*option.Endpoint, error) {
			err := patchWarp(&out, configOpt, false, nil)
			if err != nil {
				return nil, fmt.Errorf("[Warp] patch warp error: %w", err)
			}
			// options.Outbounds[i] = base
			return &out, nil
		})
	}
	if res, err := b.WaitAndGetResult(); err != nil {
		return nil, err
	} else {
		for i, base := range options.Endpoints {
			options.Endpoints[i] = *res[base.Tag].Value
		}
	}

	// fmt.Printf("%s\n", content)
	return validateResult(ctx, options, name)
}

func validateResult(ctx context.Context, options *option.Options, name string) (*option.Options, error) {
	err := libbox.CheckConfigOptions(options)
	if err != nil {
		return nil, fmt.Errorf("[%s] invalid sing-box config: %w", name, err)
	}
	return options, nil
}
