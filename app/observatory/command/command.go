package command

import (
	"context"

	"google.golang.org/grpc"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features"
	"github.com/xtls/xray-core/features/extension"
)

type service struct {
	UnimplementedObservatoryServiceServer
	v *core.Instance

	observatory extension.Observatory
}

func (s *service) GetOutboundStatus(ctx context.Context, request *GetOutboundStatusRequest) (*GetOutboundStatusResponse, error) {
	if s.observatory == nil {
		return nil, errors.New("observatory is not enabled")
	}

	merged := make(map[string]*observatory.OutboundStatus)

	// Collect from default observer first.
	if resp, err := s.observatory.GetObservation(ctx); err == nil {
		for _, status := range resp.(*observatory.ObservationResult).GetStatus() {
			merged[status.GetOutboundTag()] = status
		}
	}

	// If multi observer, iterate all sub-observers and merge; later overwrites earlier.
	if lister, ok := s.observatory.(features.TaggedFeatures); ok {
		if tags := lister.GetFeaturesTag(); len(tags) > 0 {
			for _, tag := range tags {
				feat, err := lister.GetFeaturesByTag(tag)
				if err != nil {
					continue
				}
				resp, err := feat.(extension.Observatory).GetObservation(ctx)
				if err != nil {
					continue
				}
				for _, status := range resp.(*observatory.ObservationResult).GetStatus() {
					merged[status.GetOutboundTag()] = status
				}
			}
		}
	}

	result := &observatory.ObservationResult{}
	for _, status := range merged {
		if request.Tag == "" || status.GetOutboundTag() == request.Tag {
			result.Status = append(result.Status, status)
		}
	}
	return &GetOutboundStatusResponse{Status: result}, nil
}

func (s *service) Register(server *grpc.Server) {
	RegisterObservatoryServiceServer(server, s)

	// For compatibility with v2ray gRPC clients
	vCoreDesc := ObservatoryService_ServiceDesc
	vCoreDesc.ServiceName = "v2ray.core.app.observatory.command.ObservatoryService"
	server.RegisterService(&vCoreDesc, s)
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, cfg interface{}) (interface{}, error) {
		s := core.MustFromContext(ctx)
		sv := &service{v: s}
		err := s.RequireFeatures(func(Observatory extension.Observatory) {
			sv.observatory = Observatory
		}, false)
		if err != nil {
			return nil, err
		}
		return sv, nil
	}))
}
