package conf

import (
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/dnscircuit"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
)

type DNSCircuitConfig struct {
	InboundTags    StringList `json:"inboundTags"`
	OutboundTags   StringList `json:"outboundTags"`
	BalancerTags   StringList `json:"balancerTags"`
	DNSOutboundTag string     `json:"dnsOutboundTag"`
	InactiveClean  int64      `json:"inactiveClean"`
	OspfSetting    struct {
		IfName  string `json:"ifName"`
		Address string `json:"address"`
	} `json:"ospfSetting"`
	PersistentRoute StringList `json:"persistentRoute"`
}

func (b *DNSCircuitConfig) Build() (proto.Message, error) {
	// ospf validation
	if len(b.OspfSetting.IfName) == 0 {
		return nil, errors.New("OSPF ifName can not be empty")
	}
	_, ipNet, err := net.ParseCIDR(b.OspfSetting.Address)
	if err != nil {
		return nil, errors.New("invalid OSPF address format").Base(err)
	}
	ipStr := strings.SplitN(b.OspfSetting.Address, "/", 2)[0]
	ip := net.ParseAddress(ipStr)
	if ip == nil {
		return nil, errors.New("invalid OSPF listen address: ", ipStr)
	}
	if ip.Family() != net.AddressFamilyIPv4 {
		return nil, errors.New("only IPv4 is supported for OSPF listen address")
	}
	ones, _ := ipNet.Mask.Size()
	if ones < 24 || ones > 32 {
		return nil, errors.New("invalid OSPF listen address mask: only 24-32 is supported")
	}

	// tags validate
	if len(b.OutboundTags) == 0 && len(b.BalancerTags) == 0 {
		return nil, errors.New("outboundTags or balancerTags can not be empty")
	}
	if len(b.DNSOutboundTag) == 0 {
		return nil, errors.New("dnsOutboundTag can not be empty")
	}
	if b.InactiveClean <= 0 {
		b.InactiveClean = 24 * 60 * 60 // default 24 hours
	}

	persistentIPs, err := geodata.ParseIPRules(b.PersistentRoute)
	if err != nil {
		return nil, errors.New("invalid persistent route").Base(err)
	}
	return &dnscircuit.Config{
		InboundTags:     b.InboundTags,
		OutboundTags:    b.OutboundTags,
		BalancerTags:    b.BalancerTags,
		DnsOutboundTag:  b.DNSOutboundTag,
		PersistentRoute: persistentIPs,
		InactiveClean:   b.InactiveClean,
		OspfSetting: &dnscircuit.OSPFInstanceConfig{
			IfName: b.OspfSetting.IfName,
			Address: &geodata.CIDR{
				Ip:     ip.IP(),
				Prefix: uint32(ones),
			},
		},
	}, nil
}
