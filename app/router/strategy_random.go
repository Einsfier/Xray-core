package router

import (
	"context"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/dice"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features"
	"github.com/xtls/xray-core/features/extension"
)

// RandomStrategy represents a random balancing strategy
type RandomStrategy struct {
	FallbackTag string

	ctx         context.Context
	observatory extension.Observatory
	observerTag string
}

func (s *RandomStrategy) InjectContext(ctx context.Context) {
	s.ctx = ctx
	if len(s.FallbackTag) > 0 {
		common.Must(core.RequireFeatures(s.ctx, func(observatory extension.Observatory) error {
			if s.observerTag != "" {
				obs, err := observatory.(features.TaggedFeatures).GetFeaturesByTag(s.observerTag)
				if err != nil {
					return err
				}
				s.observatory = obs.(extension.Observatory)
			} else {
				s.observatory = observatory
			}
			return nil
		}))
	}
}

func (s *RandomStrategy) GetPrincipleTarget(strings []string) []string {
	return strings
}

func (s *RandomStrategy) PickOutbound(candidates []string) string {
	if s.observatory != nil {
		observeReport, err := s.observatory.GetObservation(s.ctx)
		if err == nil {
			aliveTags := make([]string, 0)
			if result, ok := observeReport.(*observatory.ObservationResult); ok {
				status := result.Status
				statusMap := make(map[string]*observatory.OutboundStatus)
				for _, outboundStatus := range status {
					statusMap[outboundStatus.OutboundTag] = outboundStatus
				}
				for _, candidate := range candidates {
					if outboundStatus, found := statusMap[candidate]; found {
						if outboundStatus.Alive {
							aliveTags = append(aliveTags, candidate)
						}
					} else {
						// unfound candidate is considered alive
						aliveTags = append(aliveTags, candidate)
					}
				}
				candidates = aliveTags
			}
		}
	}

	count := len(candidates)
	if count == 0 {
		// goes to fallbackTag
		return ""
	}
	return candidates[dice.Roll(count)]
}
