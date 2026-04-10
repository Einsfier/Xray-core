package dnscircuit

import (
	"context"
	"fmt"
	"strings"

	"github.com/xtls/xray-core/app/dnscircuit/ospf"
	xrayerrors "github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
)

func (s *dnsCircuit) initPersistentRoute() {
	var allCIDRs []net.IPNet
	defer func() {
		if len(allCIDRs) > 0 {
			s.ospf.AnnounceASBRRoute(allCIDRs)
		}
	}()
	s.ob.obDestStatRw.Lock()
	defer s.ob.obDestStatRw.Unlock()
	for _, rule := range s.persistentRoute {
		var cidrs []*geodata.CIDR
		geoIPdesc := func() string { return "" }
		switch w := rule.Value.(type) {
		case *geodata.IPRule_Custom:
			cidrs = []*geodata.CIDR{w.Custom.Cidr}
		case *geodata.IPRule_Geoip:
			cidrRules := w.Geoip
			geoIPdesc = func() string {
				if len(cidrRules.Code) > 0 {
					if cidrRules.ReverseMatch {
						return "geoip:!" + strings.ToLower(cidrRules.Code)
					}
					return "geoip:" + strings.ToLower(cidrRules.Code)
				}
				return ""
			}
			if cidrRules.ReverseMatch {
				xrayerrors.LogWarning(context.Background(), fmt.Sprintf("ignored %s persistent route: inverse match is not supported", geoIPdesc()))
				continue
			}
			var err error
			cidrs, err = geodata.LoadIP(cidrRules.File, cidrRules.Code)
			if err != nil {
				xrayerrors.LogWarning(context.Background(), fmt.Sprintf("failed to load %s persistent route: %v", geoIPdesc(), err))
				continue
			}
		}

		for _, cidr := range cidrs {
			ip := net.IPAddress(cidr.Ip)
			if ip.Family() != net.AddressFamilyIPv4 {
				xrayerrors.LogWarning(context.Background(), fmt.Sprintf("ignored non-IPv4 persistent route: %s CIDR %s/%d",
					geoIPdesc(), ip.String(), cidr.Prefix))
				continue
			}
			mask := net.CIDRMask(int(cidr.Prefix), 32)
			k := kFromIPAndMask(cidr.Ip, mask)
			s.ob.obDestStat[k] = &obDestMeta{
				isPersistent: true,
			}
			ospf.LogImportant("adding persistent route: %s CIDR %s/%d",
				geoIPdesc(), ip.String(), cidr.Prefix)
			allCIDRs = append(allCIDRs, net.IPNet{
				IP:   ip.IP(),
				Mask: mask,
			})
		}
	}
}
