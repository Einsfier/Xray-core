package dnscircuit

import (
	"strings"

	"github.com/xtls/xray-core/common/net"
)

func PrettyPrintIPNet(ipNets ...net.IPNet) string {
	buf := new(strings.Builder)
	for i, ipNet := range ipNets {
		if i != 0 {
			buf.WriteString(" ")
		}
		buf.WriteString(ipNet.String())
	}
	return buf.String()
}
