package dnscircuit

import (
	"context"
	"fmt"
	"time"

	"github.com/xtls/xray-core/app/dnscircuit/ospf"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy"
)

type dnsCircuit struct {
	dynRouter          routing.RouterWithDynamicRule
	expectTags         map[string]bool
	expectBalancerTags map[string]bool

	inboundTags []string
	ihm         inbound.Manager
	obIns       []proxy.ActivityObservableInbound
	dnsOutTag   string
	ohm         outbound.Manager
	obDNSOut    proxy.ObservableDNSOutBound

	ospfIfName    string
	ospfIfAddr    net.IPNet
	ospfRtId      string
	ospf          *ospf.Router
	inactiveClean time.Duration

	ob              *observer
	persistentRoute []*geodata.IPRule
}

const (
	inBoundObserver     = "dnscircuit"
	outboundDNSObserver = "dnscircuit"
)

func (s *dnsCircuit) Init(ctx context.Context, c *Config, r routing.Router, ihm inbound.Manager, ohm outbound.Manager) (err error) {
	// outbound matching tags
	s.expectTags = make(map[string]bool, len(c.GetOutboundTags()))
	for _, tag := range c.GetOutboundTags() {
		s.expectTags[tag] = true
	}
	s.expectBalancerTags = make(map[string]bool, len(c.GetBalancerTags()))
	for _, tag := range c.GetBalancerTags() {
		s.expectBalancerTags[tag] = true
	}
	// confirm router capable
	dynRouter, ok := r.(routing.RouterWithDynamicRule)
	if !ok {
		return newError("router is not capable for dynamic rule")
	}
	s.dynRouter = dynRouter

	// ospf init
	ospfAddr := c.GetOspfSetting().GetAddress()
	leadingOnes := ospfAddr.GetPrefix()
	ifIP := ospfAddr.GetIp()
	ifMask := net.CIDRMask(int(leadingOnes), 32)
	s.ospfIfAddr = net.IPNet{
		IP:   ifIP,
		Mask: ifMask,
	}
	rt, err := ospf.NewRouter(c.GetOspfSetting().GetIfName(), &s.ospfIfAddr, net.IP(ifIP).String())
	if err != nil {
		return newError("err init ospf router instance: ", err)
	}
	s.ospf = rt
	s.inactiveClean = time.Duration(c.GetInactiveClean()) * time.Second

	// all other fields
	s.persistentRoute = c.GetPersistentRoute()
	s.ohm = ohm
	s.ihm = ihm
	s.inboundTags = c.GetInboundTags()
	s.dnsOutTag = c.GetDnsOutboundTag()

	return nil
}

func (s *dnsCircuit) Type() interface{} {
	return (*dnsCircuit)(nil)
}

// Start implements common.Runnable.
func (s *dnsCircuit) Start() error {
	if err := s.initObservableInbounds(); err != nil {
		return err
	}
	if err := s.initObservableDNSOutbounds(); err != nil {
		return err
	}
	if err := s.initObserver(); err != nil {
		return err
	}
	s.ospf.Start()
	s.initPersistentRoute()
	return nil
}

func (s *dnsCircuit) initObservableInbounds() error {
	for _, tag := range s.inboundTags {
		h, err := s.ihm.GetHandler(context.TODO(), tag)
		if err != nil {
			return newError(fmt.Sprintf("failed to get inbound %q handler: %v", tag, err))
		}
		gh, ok := h.(proxy.GetInbound)
		if !ok {
			return newError(fmt.Sprintf("inbound handler %q is not a proxy.GetInbound", tag))
		}
		obIn, ok := gh.GetInbound().(proxy.ActivityObservableInbound)
		if !ok {
			return newError(fmt.Sprintf("inbound handler %q does not have a proxy.ActivityObservableInbound", tag))
		}
		obIn.RegisterActivityObserver(inBoundObserver, s.observeInboundOnRequest, s.observeInboundOnResponse)
		s.obIns = append(s.obIns, obIn)
	}
	return nil
}

func (s *dnsCircuit) initObservableDNSOutbounds() error {
	h := s.ohm.GetHandler(s.dnsOutTag)
	if h == nil {
		return newError(fmt.Sprintf("can not get outbound handler %q", s.dnsOutTag))
	}
	gh, ok := h.(proxy.GetOutbound)
	if !ok {
		return newError(fmt.Sprintf("outbound handler %q is not a proxy.GetOutbound", s.dnsOutTag))
	}
	obDNSOut, ok := gh.GetOutbound().(proxy.ObservableDNSOutBound)
	if !ok {
		return newError(fmt.Sprintf("outbound handler %q is not a proxy.ObservableDNSOutBound", s.dnsOutTag))
	}
	obDNSOut.RegisterDNSOutBoundObserver(outboundDNSObserver, s.observeDNSOutBound)
	s.obDNSOut = obDNSOut
	return nil
}

// Close implements common.Closable.
func (s *dnsCircuit) Close() error {
	for _, ob := range s.obIns {
		ob.UnregisterActivityObserver(inBoundObserver)
	}
	if s.obDNSOut != nil {
		s.obDNSOut.UnregisterDNSOutBoundObserver(outboundDNSObserver)
	}
	s.ob.stop()
	return s.ospf.Close()
}
