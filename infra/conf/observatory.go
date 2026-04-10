package conf

import (
	"encoding/json"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/observatory/multiobservatory"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/taggedfeatures"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
)

type ObservatoryConfig struct {
	SubjectSelector   []string          `json:"subjectSelector"`
	ProbeURL          string            `json:"probeURL"`
	ProbeInterval     duration.Duration `json:"probeInterval"`
	EnableConcurrency bool              `json:"enableConcurrency"`
}

func (o *ObservatoryConfig) Build() (proto.Message, error) {
	return &observatory.Config{SubjectSelector: o.SubjectSelector, ProbeUrl: o.ProbeURL, ProbeInterval: int64(o.ProbeInterval), EnableConcurrency: o.EnableConcurrency}, nil
}

type BurstObservatoryConfig struct {
	SubjectSelector []string `json:"subjectSelector"`
	// health check settings
	HealthCheck *healthCheckSettings `json:"pingConfig,omitempty"`
}

func (b BurstObservatoryConfig) Build() (proto.Message, error) {
	if b.HealthCheck == nil {
		return nil, errors.New("BurstObservatory requires a valid pingConfig")
	}
	if result, err := b.HealthCheck.Build(); err == nil {
		return &burst.Config{SubjectSelector: b.SubjectSelector, PingConfig: result.(*burst.HealthPingConfig)}, nil
	} else {
		return nil, err
	}
}

type MultiObservatoryItem struct {
	MemberType string          `json:"type"`
	Tag        string          `json:"tag"`
	Value      json.RawMessage `json:"settings"`
}

type MultiObservatoryConfig struct {
	Observers []MultiObservatoryItem `json:"observers"`
}

func (o *MultiObservatoryConfig) Build() (proto.Message, error) {
	ret := &multiobservatory.Config{
		Holders: &taggedfeatures.Config{
			Features: make(map[string]*serial.TypedMessage),
		},
	}
	for _, v := range o.Observers {
		switch v.MemberType {
		case "burst":
			var burstCfg BurstObservatoryConfig
			if err := json.Unmarshal(v.Value, &burstCfg); err != nil {
				return nil, err
			}
			pb, err := burstCfg.Build()
			if err != nil {
				return nil, err
			}
			ret.Holders.Features[v.Tag] = serial.ToTypedMessage(pb)
		case "default":
			fallthrough
		default:
			var obsCfg ObservatoryConfig
			if err := json.Unmarshal(v.Value, &obsCfg); err != nil {
				return nil, err
			}
			pb, err := obsCfg.Build()
			if err != nil {
				return nil, err
			}
			ret.Holders.Features[v.Tag] = serial.ToTypedMessage(pb)
		}
	}
	return ret, nil
}
