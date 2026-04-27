package geodata

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"

	"go4.org/netipx"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
)

// DynamicGeoIPMatcher implements GeoIPMatcher interface for dynamic IP sets.
// It also implements routing.DynamicRuleIP for adding/removing IPs at runtime.
type DynamicGeoIPMatcher struct {
	Name    string
	reverse bool

	builderMu  sync.RWMutex
	ip4Builder *netipx.IPSetBuilder
	ip6Builder *netipx.IPSetBuilder

	ip4 atomic.Pointer[netipx.IPSet]
	ip6 atomic.Pointer[netipx.IPSet]

	connTrackMu sync.RWMutex
	connTrack   map[netip.Addr]*connTrackFromSrc
}

type connTrackFromSrc struct {
	dm  *DynamicGeoIPMatcher
	src netip.Addr

	builderMu  sync.RWMutex
	ip4Builder *netipx.IPSetBuilder
	ip6Builder *netipx.IPSetBuilder

	ip4 atomic.Pointer[netipx.IPSet]
	ip6 atomic.Pointer[netipx.IPSet]
}

func newConnTrackFromSrc(dm *DynamicGeoIPMatcher, src netip.Addr) *connTrackFromSrc {
	return &connTrackFromSrc{
		dm:         dm,
		src:        src,
		ip4Builder: new(netipx.IPSetBuilder),
		ip6Builder: new(netipx.IPSetBuilder),
	}
}

func NewDynamicGeoIPMatcher(name string) (*DynamicGeoIPMatcher, error) {
	dm := &DynamicGeoIPMatcher{
		Name:       name,
		ip4Builder: new(netipx.IPSetBuilder),
		ip6Builder: new(netipx.IPSetBuilder),
		connTrack:  make(map[netip.Addr]*connTrackFromSrc),
	}
	ipSet4, err := dm.ip4Builder.IPSet()
	if err != nil {
		return nil, err
	}
	dm.ip4.Store(ipSet4)
	ipSet6, err := dm.ip6Builder.IPSet()
	if err != nil {
		return nil, err
	}
	dm.ip6.Store(ipSet6)
	return dm, nil
}

// Match implements GeoIPMatcher.
func (m *DynamicGeoIPMatcher) Match(ip net.IP) bool {
	nip, ok := netipx.FromStdIP(ip)
	if !ok {
		return false
	}
	var matched bool
	switch len(ip) {
	case net.IPv4len:
		matched = m.ip4.Load().Contains(nip)
	case net.IPv6len:
		matched = m.ip6.Load().Contains(nip)
	}
	if m.reverse {
		return !matched
	}
	return matched
}

// AnyMatch implements GeoIPMatcher.
func (m *DynamicGeoIPMatcher) AnyMatch(ips []net.IP) bool {
	for _, ip := range ips {
		if m.Match(ip) {
			return true
		}
	}
	return false
}

// Matches implements GeoIPMatcher.
func (m *DynamicGeoIPMatcher) Matches(ips []net.IP) bool {
	if len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !m.Match(ip) {
			return false
		}
	}
	return true
}

// FilterIPs implements GeoIPMatcher.
func (m *DynamicGeoIPMatcher) FilterIPs(ips []net.IP) (matched []net.IP, unmatched []net.IP) {
	for _, ip := range ips {
		if m.Match(ip) {
			matched = append(matched, ip)
		} else {
			unmatched = append(unmatched, ip)
		}
	}
	return
}

// ToggleReverse implements GeoIPMatcher.
func (m *DynamicGeoIPMatcher) ToggleReverse() {
	m.reverse = !m.reverse
}

// SetReverse implements GeoIPMatcher.
func (m *DynamicGeoIPMatcher) SetReverse(reverse bool) {
	m.reverse = reverse
}

// MatchSrc checks if a source IP has conn-track entries matching the destination IP.
func (m *DynamicGeoIPMatcher) MatchSrc(src, dst net.IP) bool {
	srcAddr, ok := netip.AddrFromSlice(src)
	if !ok {
		return false
	}
	srcAddr = srcAddr.Unmap()
	m.connTrackMu.RLock()
	ct, ok := m.connTrack[srcAddr]
	m.connTrackMu.RUnlock()
	if !ok {
		return false
	}
	return ct.match(dst)
}

func (cm *connTrackFromSrc) match(dst net.IP) bool {
	nip, ok := netipx.FromStdIP(dst)
	if !ok {
		return false
	}
	switch len(dst) {
	case net.IPv4len:
		return cm.ip4.Load().Contains(nip)
	case net.IPv6len:
		return cm.ip6.Load().Contains(nip)
	}
	return false
}

func (cm *connTrackFromSrc) updateIPSet(is4Modified, is6Modified bool) {
	if is4Modified {
		ipSet, err := cm.ip4Builder.IPSet()
		if err != nil {
			errors.LogError(context.Background(), fmt.Sprintf("%s err update conn-track %s ip4 set",
				strings.ToLower(cm.dm.Name), cm.src.String()))
		} else {
			cm.ip4.Store(ipSet)
		}
	}
	if is6Modified {
		ipSet, err := cm.ip6Builder.IPSet()
		if err != nil {
			errors.LogError(context.Background(), fmt.Sprintf("%s err update conn-track %s ip6 set",
				strings.ToLower(cm.dm.Name), cm.src.String()))
		} else {
			cm.ip6.Store(ipSet)
		}
	}
}

func (cm *connTrackFromSrc) addIPNet(ipNets ...net.IPNet) {
	var is4Modified, is6Modified bool
	cm.builderMu.Lock()
	defer cm.builderMu.Unlock()
	defer func() {
		cm.updateIPSet(is4Modified, is6Modified)
	}()
	for _, ipNet := range ipNets {
		addr, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		ones, _ := ipNet.Mask.Size()
		prefix := netip.PrefixFrom(addr, ones)
		if prefix.Bits() == -1 {
			continue
		}
		switch {
		case addr.Is4():
			cm.ip4Builder.AddPrefix(prefix)
			is4Modified = true
		case addr.Is6():
			cm.ip6Builder.AddPrefix(prefix)
			is6Modified = true
		}
	}
}

func (cm *connTrackFromSrc) removeIPNet(ipNets ...net.IPNet) {
	var is4Modified, is6Modified bool
	cm.builderMu.Lock()
	defer cm.builderMu.Unlock()
	defer func() {
		cm.updateIPSet(is4Modified, is6Modified)
	}()
	for _, ipNet := range ipNets {
		addr, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		ones, _ := ipNet.Mask.Size()
		prefix := netip.PrefixFrom(addr, ones)
		if prefix.Bits() == -1 {
			continue
		}
		switch {
		case addr.Is4():
			cm.ip4Builder.RemovePrefix(prefix)
			is4Modified = true
		case addr.Is6():
			cm.ip6Builder.RemovePrefix(prefix)
			is6Modified = true
		}
	}
}

// AddIPNetConnTrack implements routing.DynamicRuleIP.
func (m *DynamicGeoIPMatcher) AddIPNetConnTrack(src net.IP, dsts ...net.IPNet) {
	srcAddr, ok := netip.AddrFromSlice(src)
	if !ok {
		return
	}
	srcAddr = srcAddr.Unmap()
	m.connTrackMu.Lock()
	ct, ok := m.connTrack[srcAddr]
	if !ok {
		ct = newConnTrackFromSrc(m, srcAddr)
		m.connTrack[srcAddr] = ct
	}
	m.connTrackMu.Unlock()
	ct.addIPNet(dsts...)
}

// RemoveIPNetConnTrack implements routing.DynamicRuleIP.
func (m *DynamicGeoIPMatcher) RemoveIPNetConnTrack(src net.IP, dsts ...net.IPNet) {
	srcAddr, ok := netip.AddrFromSlice(src)
	if !ok {
		return
	}
	srcAddr = srcAddr.Unmap()
	m.connTrackMu.RLock()
	ct, ok := m.connTrack[srcAddr]
	m.connTrackMu.RUnlock()
	if !ok {
		return
	}
	ct.removeIPNet(dsts...)
}

func (m *DynamicGeoIPMatcher) updateIPSet(is4Modified, is6Modified bool) {
	if is4Modified {
		ipSet, err := m.ip4Builder.IPSet()
		if err != nil {
			return
		}
		m.ip4.Store(ipSet)
	}
	if is6Modified {
		ipSet, err := m.ip6Builder.IPSet()
		if err != nil {
			return
		}
		m.ip6.Store(ipSet)
	}
}

func (m *DynamicGeoIPMatcher) DynamicRuleName() string {
	return m.Name
}

// AddIPNet implements routing.DynamicRuleIP.
func (m *DynamicGeoIPMatcher) AddIPNet(ipNets ...net.IPNet) {
	var is4Modified, is6Modified bool

	m.builderMu.Lock()
	defer m.builderMu.Unlock()
	defer func() {
		m.updateIPSet(is4Modified, is6Modified)
	}()

	for _, ipNet := range ipNets {
		addr, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		ones, _ := ipNet.Mask.Size()
		prefix := netip.PrefixFrom(addr, ones)
		if prefix.Bits() == -1 {
			continue
		}
		switch {
		case addr.Is4():
			m.ip4Builder.AddPrefix(prefix)
			is4Modified = true
		case addr.Is6():
			m.ip6Builder.AddPrefix(prefix)
			is6Modified = true
		}
	}
}

// RemoveIPNet implements routing.DynamicRuleIP.
func (m *DynamicGeoIPMatcher) RemoveIPNet(ipNets ...net.IPNet) {
	var is4Modified, is6Modified bool

	m.builderMu.Lock()
	defer m.builderMu.Unlock()
	defer func() {
		m.updateIPSet(is4Modified, is6Modified)
	}()

	for _, ipNet := range ipNets {
		addr, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		ones, _ := ipNet.Mask.Size()
		prefix := netip.PrefixFrom(addr, ones)
		if prefix.Bits() == -1 {
			continue
		}
		switch {
		case addr.Is4():
			m.ip4Builder.RemovePrefix(prefix)
			is4Modified = true
		case addr.Is6():
			m.ip6Builder.RemovePrefix(prefix)
			is6Modified = true
		}
	}
}

// Global dynamic GeoIP matcher registry
var (
	dynamicGeoIPRegistryMu sync.RWMutex
	dynamicGeoIPRegistry   = make(map[string]*DynamicGeoIPMatcher)
)

func GetOrCreateDynamicGeoIPMatcher(name string) (*DynamicGeoIPMatcher, error) {
	name = strings.ToUpper(name)
	dynamicGeoIPRegistryMu.Lock()
	defer dynamicGeoIPRegistryMu.Unlock()
	if m, ok := dynamicGeoIPRegistry[name]; ok {
		return m, nil
	}
	m, err := NewDynamicGeoIPMatcher(name)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic GeoIP matcher %s: %w", name, err)
	}
	dynamicGeoIPRegistry[name] = m
	return m, nil
}

func GetDynamicGeoIPMatcher(name string) *DynamicGeoIPMatcher {
	name = strings.ToUpper(name)
	dynamicGeoIPRegistryMu.RLock()
	defer dynamicGeoIPRegistryMu.RUnlock()
	return dynamicGeoIPRegistry[name]
}
