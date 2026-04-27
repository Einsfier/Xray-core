package geodata

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
)

type IPRegistry struct {
	mu           sync.Mutex
	ipsetFactory *IPSetFactory
	matchers     []*DynamicIPMatcher
}

func (r *IPRegistry) BuildIPMatcher(rules []*IPRule) (IPMatcher, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, err := buildOptimizedIPMatcher(r.ipsetFactory, rules)
	if err != nil {
		return nil, err
	}

	d := NewDynamicIPMatcher(rules, m)
	r.matchers = append(r.matchers, d)
	return d, nil
}

func (r *IPRegistry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	errors.LogInfo(context.Background(), "reloading GeoIP data for ", len(r.matchers), " IP matcher(s)")

	factory := newIPSetFactory()
	type reloadEntry struct {
		dynamic *DynamicIPMatcher
		matcher IPMatcher
	}
	reloaded := make([]reloadEntry, len(r.matchers))
	for i, d := range r.matchers {
		m, err := buildOptimizedIPMatcher(factory, d.rules)
		if err != nil {
			errors.LogErrorInner(context.Background(), err, "failed to reload GeoIP data for IP matcher ", i)
			return err
		}
		reloaded[i] = reloadEntry{dynamic: d, matcher: m}
	}
	for _, entry := range reloaded {
		entry.dynamic.Reload(entry.matcher)
	}
	r.ipsetFactory = factory
	errors.LogInfo(context.Background(), "reloaded GeoIP data for ", len(r.matchers), " IP matcher(s)")
	return nil
}

func newIPRegistry() *IPRegistry {
	return &IPRegistry{
		ipsetFactory: newIPSetFactory(),
	}
}

var IPReg = newIPRegistry()

type ipMatcherState struct {
	matcher IPMatcher
}

type DynamicIPMatcher struct {
	rules      []*IPRule
	state      atomic.Pointer[ipMatcherState]
	mu         sync.Mutex
	reverse    bool
	reverseSet bool
}

// Match implements IPMatcher.
func (d *DynamicIPMatcher) Match(ip net.IP) bool {
	return d.state.Load().matcher.Match(ip)
}

// AnyMatch implements IPMatcher.
func (d *DynamicIPMatcher) AnyMatch(ips []net.IP) bool {
	return d.state.Load().matcher.AnyMatch(ips)
}

// Matches implements IPMatcher.
func (d *DynamicIPMatcher) Matches(ips []net.IP) bool {
	return d.state.Load().matcher.Matches(ips)
}

// FilterIPs implements IPMatcher.
func (d *DynamicIPMatcher) FilterIPs(ips []net.IP) (matched []net.IP, unmatched []net.IP) {
	return d.state.Load().matcher.FilterIPs(ips)
}

func (d *DynamicIPMatcher) DynamicRuleName() string {
	if dm, ok := d.state.Load().matcher.(routing.DynamicRuleIP); ok {
		return dm.DynamicRuleName()
	}
	return ""
}

func (d *DynamicIPMatcher) MatchSrc(src, dst net.IP) bool {
	if dm, ok := d.state.Load().matcher.(routing.DynamicRuleIP); ok {
		return dm.MatchSrc(src, dst)
	}
	return false
}

func (d *DynamicIPMatcher) AddIPNet(ipNets ...net.IPNet) {
	if dm, ok := d.state.Load().matcher.(routing.DynamicRuleIP); ok {
		dm.AddIPNet(ipNets...)
	}
}
func (d *DynamicIPMatcher) RemoveIPNet(ipNets ...net.IPNet) {
	if dm, ok := d.state.Load().matcher.(routing.DynamicRuleIP); ok {
		dm.RemoveIPNet(ipNets...)
	}
}
func (d *DynamicIPMatcher) AddIPNetConnTrack(src net.IP, dsts ...net.IPNet) {
	if dm, ok := d.state.Load().matcher.(routing.DynamicRuleIP); ok {
		dm.AddIPNetConnTrack(src, dsts...)
	}
}
func (d *DynamicIPMatcher) RemoveIPNetConnTrack(src net.IP, dsts ...net.IPNet) {
	if dm, ok := d.state.Load().matcher.(routing.DynamicRuleIP); ok {
		dm.RemoveIPNetConnTrack(src, dsts...)
	}
}

// ToggleReverse implements IPMatcher.
func (d *DynamicIPMatcher) ToggleReverse() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.reverse = !d.reverse
	d.state.Load().matcher.ToggleReverse()
}

// SetReverse implements IPMatcher.
func (d *DynamicIPMatcher) SetReverse(reverse bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.reverse = reverse
	d.reverseSet = true
	d.state.Load().matcher.SetReverse(reverse)
}

func (d *DynamicIPMatcher) Reload(newMatcher IPMatcher) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.reverseSet {
		newMatcher.SetReverse(d.reverse)
	} else if d.reverse {
		newMatcher.ToggleReverse()
	}
	d.state.Store(&ipMatcherState{matcher: newMatcher})
}

func NewDynamicIPMatcher(rules []*IPRule, matcher IPMatcher) *DynamicIPMatcher {
	d := &DynamicIPMatcher{rules: rules}
	d.Reload(matcher)
	return d
}
