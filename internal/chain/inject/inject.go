package inject

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/mhsanaei/3x-ui/v3/internal/chain/linkparse"
	"github.com/mhsanaei/3x-ui/v3/internal/chain/xraygen"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Placement decides where managed rules land relative to the operator's rules.
type Placement string

const (
	// AfterBlocks keeps security blocks (private geoip, bittorrent) ahead of the
	// chain rules while still beating catch-all direct rules.
	AfterBlocks Placement = "after_blocks"
	// High puts chain rules right after the api rule.
	High Placement = "high"
	// Low appends chain rules at the end, behind every operator rule.
	Low Placement = "low"
)

// Hop is one link of a node's egress chain; hops[0] is the landing outbound.
type Hop struct {
	Tag      string
	Proxy    *linkparse.Proxy
	External bool
}

// Node is the minimal state the injector needs from the database.
type Node struct {
	Id          int
	Email       string
	InboundTag  string
	OutboundTag string
	Hops        []Hop
	Status      string
}

// Build turns node state into outbound and rule documents. Ordering follows the
// input, which callers sort by node id so output stays deterministic.
func Build(nodes []Node) (outbounds []json.RawMessage, rules []json.RawMessage, err error) {
	seenEmail := make(map[string]struct{}, len(nodes))
	seenTag := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		switch n.Status {
		case "active", "degraded", "provisioning", "failed", "disabled":
		default:
			continue
		}
		if _, dup := seenEmail[n.Email]; dup {
			return nil, nil, fmt.Errorf("duplicate node email: %s", n.Email)
		}
		seenEmail[n.Email] = struct{}{}

		// Anything not actively serving keeps a blackhole rule. Deleting the
		// rule instead would let the client fall back to the default outbound.
		if n.Status != "active" && n.Status != "degraded" {
			rule, err := xraygen.InboundBlackholeRule(n.InboundTag, n.Email)
			if err != nil {
				return nil, nil, err
			}
			rules = append(rules, rule)
			continue
		}
		if len(n.Hops) == 0 || n.Hops[0].Tag != n.OutboundTag {
			return nil, nil, fmt.Errorf("node %d: landing hop does not match outbound tag", n.Id)
		}
		if _, dup := seenTag[n.OutboundTag]; dup {
			return nil, nil, fmt.Errorf("duplicate outbound tag: %s", n.OutboundTag)
		}
		seenTag[n.OutboundTag] = struct{}{}

		hops := make([]xraygen.Hop, 0, len(n.Hops))
		for _, h := range n.Hops {
			gh := xraygen.Hop{Tag: h.Tag, External: h.External}
			if h.Proxy != nil {
				gh.Proxy = *h.Proxy
			}
			hops = append(hops, gh)
		}
		built, err := xraygen.BuildChain(hops)
		if err != nil {
			return nil, nil, fmt.Errorf("node %d: %w", n.Id, err)
		}
		outbounds = append(outbounds, built...)
		rule, err := ruleFor(n)
		if err != nil {
			return nil, nil, err
		}
		rules = append(rules, rule)
	}
	return outbounds, rules, nil
}

func ruleFor(n Node) (json.RawMessage, error) {
	if n.InboundTag == "" {
		return xraygen.UserRule(n.Email, n.OutboundTag)
	}
	return xraygen.InboundUserRule(n.InboundTag, n.Email, n.OutboundTag)
}

// Merge appends generated outbounds and inserts generated rules. It never
// rewrites unrelated entries: non-rule routing keys stay byte-identical.
func Merge(outboundRaw, routingRaw json.RawMessage, outbounds, rules []json.RawMessage, placement Placement) (json.RawMessage, json.RawMessage, error) {
	if len(outbounds) == 0 && len(rules) == 0 {
		return outboundRaw, routingRaw, nil
	}
	ob, existingTags, blackholeTags, err := parseOutbounds(outboundRaw)
	if err != nil {
		return nil, nil, err
	}
	for _, raw := range outbounds {
		tag, err := outboundTag(raw)
		if err != nil {
			return nil, nil, err
		}
		if _, dup := existingTags[tag]; dup {
			return nil, nil, fmt.Errorf("outbound tag already exists: %s", tag)
		}
		existingTags[tag] = struct{}{}
		ob = append(ob, raw)
	}
	if err := validateDialerTargets(ob, outbounds); err != nil {
		return nil, nil, err
	}
	newOutbounds, err := json.Marshal(ob)
	if err != nil {
		return nil, nil, err
	}

	routing, err := parseRouting(routingRaw)
	if err != nil {
		return nil, nil, err
	}
	if placement == "" {
		placement = AfterBlocks
	}
	idx := insertIndex(routing.rules, placement, blackholeTags)
	merged := make([]json.RawMessage, 0, len(routing.rules)+len(rules))
	merged = append(merged, routing.rules[:idx]...)
	merged = append(merged, rules...)
	merged = append(merged, routing.rules[idx:]...)
	routing.rules = merged
	rulesRaw, err := json.Marshal(routing.rules)
	if err != nil {
		return nil, nil, err
	}
	routing.fields["rules"] = rulesRaw
	if _, ok := routing.fields["domainStrategy"]; !ok {
		routing.fields["domainStrategy"] = json.RawMessage(`"AsIs"`)
	}
	newRouting, err := json.Marshal(routing.fields)
	if err != nil {
		return nil, nil, err
	}
	return newOutbounds, newRouting, nil
}

// MergeConfig applies Merge to a generated config in place.
func MergeConfig(cfg *xray.Config, outbounds, rules []json.RawMessage, placement Placement) error {
	ob, rt, err := Merge(json.RawMessage(cfg.OutboundConfigs), json.RawMessage(cfg.RouterConfig), outbounds, rules, placement)
	if err != nil {
		return err
	}
	cfg.OutboundConfigs = json_util.RawMessage(ob)
	cfg.RouterConfig = json_util.RawMessage(rt)
	return nil
}

type routingDoc struct {
	fields map[string]json.RawMessage
	rules  []json.RawMessage
}

func parseRouting(raw json.RawMessage) (routingDoc, error) {
	doc := routingDoc{fields: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &doc.fields); err != nil {
			return doc, fmt.Errorf("routing section: %w", err)
		}
	}
	if r, ok := doc.fields["rules"]; ok {
		if err := json.Unmarshal(r, &doc.rules); err != nil {
			return doc, fmt.Errorf("routing rules: %w", err)
		}
	}
	return doc, nil
}

// parseOutbounds returns the existing outbounds, their tags, and the tags whose
// protocol is blackhole (the rules that must stay ahead of chain routing).
func parseOutbounds(raw json.RawMessage) ([]json.RawMessage, map[string]struct{}, map[string]struct{}, error) {
	tags := map[string]struct{}{}
	blackholes := map[string]struct{}{}
	var list []json.RawMessage
	if len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, nil, nil, fmt.Errorf("outbounds section: %w", err)
		}
	}
	if len(list) == 0 {
		list = []json.RawMessage{
			json.RawMessage(`{"protocol":"freedom","settings":{},"tag":"direct"}`),
			json.RawMessage(`{"protocol":"blackhole","settings":{},"tag":"blocked"}`),
		}
	}
	for _, item := range list {
		var probe struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			return nil, nil, nil, fmt.Errorf("outbound entry: %w", err)
		}
		if probe.Tag != "" {
			tags[probe.Tag] = struct{}{}
		}
		if probe.Protocol == "blackhole" && probe.Tag != "" {
			blackholes[probe.Tag] = struct{}{}
		}
	}
	return list, tags, blackholes, nil
}

// validateDialerTargets keeps a generated hop from referencing a tag that the
// final outbound list does not contain: the core refuses to start in that case.
func validateDialerTargets(all []json.RawMessage, generated []json.RawMessage) error {
	tags := make(map[string]struct{}, len(all))
	var probes []struct {
		Tag    string `json:"tag"`
		Stream struct {
			Sockopt struct {
				DialerProxy string `json:"dialerProxy"`
			} `json:"sockopt"`
		} `json:"streamSettings"`
	}
	for _, raw := range all {
		var t struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(raw, &t); err == nil && t.Tag != "" {
			tags[t.Tag] = struct{}{}
		}
	}
	for _, raw := range generated {
		var p struct {
			Tag    string `json:"tag"`
			Stream struct {
				Sockopt struct {
					DialerProxy string `json:"dialerProxy"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.Stream.Sockopt.DialerProxy == "" {
			continue
		}
		if _, ok := tags[p.Stream.Sockopt.DialerProxy]; !ok {
			return fmt.Errorf("hop %s: dialerProxy %s not found", p.Tag, p.Stream.Sockopt.DialerProxy)
		}
	}
	_ = probes
	return nil
}

func outboundTag(raw json.RawMessage) (string, error) {
	var probe struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", err
	}
	if err := xraygen.ValidateTag(probe.Tag); err != nil {
		return "", err
	}
	return probe.Tag, nil
}

type ruleProbe struct {
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
}

// insertIndex resolves the splice point. AfterBlocks is the safe default: the
// core matches top-down, so chain rules must follow the block rules or a
// customer could reach private ranges through their proxy.
func insertIndex(rules []json.RawMessage, placement Placement, blackholes map[string]struct{}) int {
	apiIdx, blockIdx := -1, -1
	for i, raw := range rules {
		var r ruleProbe
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		if r.OutboundTag == "api" && contains(r.InboundTag, "api") {
			apiIdx = i
		}
		if _, isBlock := blackholes[r.OutboundTag]; isBlock || r.OutboundTag == xraygen.TagBlocked {
			blockIdx = i
		}
	}
	switch placement {
	case High:
		if apiIdx >= 0 {
			return apiIdx + 1
		}
		return 0
	case Low:
		return len(rules)
	default:
		start := 0
		if apiIdx >= 0 {
			start = apiIdx + 1
		}
		if blockIdx >= 0 && blockIdx+1 > start {
			start = blockIdx + 1
		}
		return start
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
