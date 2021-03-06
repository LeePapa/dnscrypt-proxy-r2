package main

import (
	"strings"
	"unicode"

	"github.com/jedisct1/dlog"
	"github.com/miekg/dns"
)

type ClockEntry struct {
	*EPRing
}

type PluginCloak struct {
	clock_cache    *CloakCache
	ttl            uint32
}

func (plugin *PluginCloak) Init(proxy *Proxy) error {
	dlog.Noticef("loading the set of cloaking rules from [%s]", proxy.cloakFile)
	bin, err := ReadTextFile(proxy.cloakFile)
	if err != nil {
		return err
	}
	plugin.ttl = proxy.cloakTTL
	cloaks := make(map[string]map[string][]*Endpoint)
	for lineNo, line := range strings.Split(string(bin), "\n") {
		line = TrimAndStripInlineComments(line)
		if len(line) == 0 {
			continue
		}
		var target string
		parts := strings.FieldsFunc(line, unicode.IsSpace)
		if len(parts) == 2 {
			line = strings.TrimFunc(parts[0], unicode.IsSpace)
			target = strings.TrimFunc(parts[1], unicode.IsSpace)
		} else if len(parts) > 2 {
			dlog.Errorf("syntax error in cloaking rules at line %d -- Unexpected space character", 1+lineNo)
			continue
		}
		if len(line) == 0 || len(target) == 0 {
			dlog.Errorf("syntax error in cloaking rules at line %d -- Missing name or target", 1+lineNo)
			continue
		}
		line = strings.ToLower(line)
		cloakedName, found := cloaks[line]
		if !found {
			cloakedName = make(map[string][]*Endpoint)
		}
		if ip, err := ResolveEndpoint(target); err == nil {
			if ip.IP.To4() != nil {
				cloakedName["v4"] = append(cloakedName["v4"], ip)
			} else {
				cloakedName["v6"] = append(cloakedName["v6"], ip)
			}
		} else {
			dlog.Errorf("invalid IP address in cloaking rule at line %d", 1+lineNo)
		}
		cloaks[line] = cloakedName
	}
	plugin.clock_cache = NewCloakCache()
	for name,r := range cloaks {
		if len(r["v4"]) > 0 {
			key := *computeCacheKey(false, dns.TypeA, dns.ClassINET, name + ".")
			value := ClockEntry{EPRing:LinkEPRing(r["v4"]...),} 
			plugin.clock_cache.Add(key, value)
		}
		if len(r["v6"]) > 0 {
			key := *computeCacheKey(false, dns.TypeAAAA, dns.ClassINET, name + ".")
			value := ClockEntry{EPRing:LinkEPRing(r["v6"]...),} 
			plugin.clock_cache.Add(key, value)
		}
	}
	return nil
}

func (plugin *PluginCloak) Eval(pluginsState *PluginsState, msg *dns.Msg) error {
	cachedAny, ok := plugin.clock_cache.Get(*pluginsState.hash_key)
	if !ok {
		return nil
	}
	ce := cachedAny.(ClockEntry)
	ip := ce.EPRing.IP
	ce.EPRing = ce.EPRing.Next()
	synth := EmptyResponseFromMessage(msg)
	question := msg.Question[0]
	if question.Qtype == dns.TypeA {
		rr := new(dns.A)
		rr.Hdr = dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: plugin.ttl}
		rr.A = ip
		synth.Answer = []dns.RR{rr}
	} else {
		rr := new(dns.AAAA)
		rr.Hdr = dns.RR_Header{Name: question.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: plugin.ttl}
		rr.AAAA = ip
		synth.Answer = []dns.RR{rr}
	}
	pluginsState.synthResponse = synth
	pluginsState.state = PluginsStateSynth
	pluginsState.returnCode = PluginsReturnCodeCloak
	return nil
}
