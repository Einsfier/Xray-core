package conf

import (
	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/proxy/httphealthcheck"
)

type HttpHealthCheckConfig struct {
	Timeout uint32 `json:"timeout"`
}

func (c *HttpHealthCheckConfig) Build() (proto.Message, error) {
	if c.Timeout <= 0 {
		c.Timeout = 3
	}
	return &httphealthcheck.HealthServerConfig{
		Timeout: c.Timeout,
	}, nil
}
