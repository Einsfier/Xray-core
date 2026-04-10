package router

import (
	"strings"

	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/features/routing"
)

// RouteB is an implementation of routing.RouteB.
type RouteB struct {
	*Route
	balancerTag string
}

func (r *RouteB) GetBalancerTag() string {
	return r.balancerTag
}

// PickRouteB implements routing.RouterB.
func (r *Router) PickRouteB(ctx routing.Context) (routing.RouteB, error) {
	rule, ctx, err := r.pickRouteInternal(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := rule.GetTag()
	if err != nil {
		return nil, err
	}
	return &RouteB{Route: &Route{
		Context:     ctx,
		outboundTag: tag,
	}, balancerTag: rule.BTag}, nil
}

// GetDynamicRuleIP implements routing.RouterWithDynamicRule.
func (r *Router) GetDynamicRuleIP(ruleName string) routing.DynamicRuleIP {
	ruleName = strings.ToUpper(ruleName)
	dm := geodata.GetDynamicGeoIPMatcher(ruleName)
	if dm == nil {
		return nil
	}
	return dm
}
