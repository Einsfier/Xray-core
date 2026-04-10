package dnscircuit

import (
	"context"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
)

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		s := new(dnsCircuit)
		if err := core.RequireFeatures(ctx, func(r routing.Router, ihm inbound.Manager, ohm outbound.Manager) error {
			return s.Init(ctx, config.(*Config), r, ihm, ohm)
		}); err != nil {
			return nil, err
		}
		return s, nil
	}))
}
