package multiobservatory

import (
	"context"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/taggedfeatures"
	"github.com/xtls/xray-core/features"
	"github.com/xtls/xray-core/features/extension"
	"google.golang.org/protobuf/proto"
)

type Observer struct {
	features.TaggedFeatures
	config *Config
	ctx    context.Context
}

func (o *Observer) GetObservation(ctx context.Context) (proto.Message, error) {
	feat, err := o.GetFeaturesByTag("")
	if err != nil {
		return nil, errors.New("cannot get default observatory").Base(err)
	}
	return feat.(extension.Observatory).GetObservation(ctx)
}

func (o *Observer) Type() interface{} {
	return extension.ObservatoryType()
}

func New(ctx context.Context, config *Config) (*Observer, error) {
	holder, err := taggedfeatures.NewHolderFromConfig(ctx, config.Holders, extension.ObservatoryType())
	if err != nil {
		return nil, err
	}
	return &Observer{config: config, ctx: ctx, TaggedFeatures: holder}, nil
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return New(ctx, config.(*Config))
	}))
}
