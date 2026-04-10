package dns

import (
	"context"
	"strings"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/routing"
)

// ResolvableContext is an implementation of routing.Context, with domain resolving capability.
type ResolvableContext struct {
	routing.Context
	dnsClient dns.Client
	cacheIPs  []net.IP
	hasError  bool
}

// GetTargetIPs overrides original routing.Context's implementation.
func (ctx *ResolvableContext) GetTargetIPs() []net.IP {
	if len(ctx.cacheIPs) > 0 {
		return ctx.cacheIPs
	}

	if ctx.hasError {
		return nil
	}

	if domain := ctx.GetTargetDomain(); len(domain) != 0 {
		ips, _, err := ctx.dnsClient.LookupIP(domain, dns.IPOption{
			IPv4Enable: true,
			IPv6Enable: true,
			FakeEnable: false,
		})
		if err == nil {
			ctx.cacheIPs = ips
			return ips
		}
		errors.LogInfoInner(context.Background(), err, "resolve ip for ", domain)
	}

	if ips := ctx.Context.GetTargetIPs(); len(ips) != 0 {
		ctx.cacheIPs = ips
		return ips
	}

	ctx.hasError = true
	return nil
}

// ContextWithDNSClient creates a new routing context with domain resolving capability.
// Resolved domain IPs can be retrieved by GetTargetIPs().
func ContextWithDNSClient(ctx routing.Context, client dns.Client) routing.Context {
	return &ResolvableContext{Context: ctx, dnsClient: client}
}

// RoutingContextWithSkipDynamicRule is an optional feature for routing context, to
// control the behavior of whether skip certain dynamic rule while rule matching.
// By default, all rules are checked.
type RoutingContextWithSkipDynamicRule interface {
	routing.Context
	GetSkipDynamicRuleIP(ruleName string) bool
	GetSkipDynamicRuleDomain(ruleName string) bool
}

type SkipDynamicRuleContext struct {
	routing.Context
	skipRuleNameIP     string
	skipRuleNameDomain string // TODO: not used yet
}

func (ctx *SkipDynamicRuleContext) GetSkipDynamicRuleIP(ruleName string) bool {
	ruleName = strings.ToUpper(ruleName)
	if ctx.skipRuleNameIP == ruleName {
		return true
	}
	if sCtx, ok := ctx.Context.(RoutingContextWithSkipDynamicRule); ok {
		return sCtx.GetSkipDynamicRuleIP(ruleName)
	}
	return false
}

func (ctx *SkipDynamicRuleContext) GetSkipDynamicRuleDomain(ruleName string) bool {
	ruleName = strings.ToUpper(ruleName)
	if ctx.skipRuleNameDomain == ruleName {
		return true
	}
	if sCtx, ok := ctx.Context.(RoutingContextWithSkipDynamicRule); ok {
		return sCtx.GetSkipDynamicRuleDomain(ruleName)
	}
	return false
}

func ContextWithSkippingDynamicRule(ctx routing.Context, ruleNameIP, ruleNameDomain string) routing.Context {
	return &SkipDynamicRuleContext{
		Context:            ctx,
		skipRuleNameIP:     strings.ToUpper(ruleNameIP),
		skipRuleNameDomain: ruleNameDomain,
	}
}
