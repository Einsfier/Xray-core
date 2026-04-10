package taggedfeatures

import (
	"context"
	"reflect"
	"sync"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/features"
)

// Holder manages multiple features indexed by tag.
type Holder struct {
	access     *sync.RWMutex
	features   map[string]features.Feature
	memberType reflect.Type
	ctx        context.Context
}

// NewHolder creates a new Holder with the given member type.
func NewHolder(ctx context.Context, memberType interface{}) *Holder {
	return &Holder{
		ctx:        ctx,
		access:     &sync.RWMutex{},
		features:   make(map[string]features.Feature),
		memberType: reflect.TypeOf(memberType),
	}
}

func (h *Holder) GetFeaturesByTag(tag string) (features.Feature, error) {
	h.access.RLock()
	defer h.access.RUnlock()
	feature, ok := h.features[tag]
	if !ok {
		return nil, errors.New("unable to find feature with tag: ", tag)
	}
	return feature, nil
}

func (h *Holder) AddFeaturesByTag(tag string, feature features.Feature) error {
	h.access.Lock()
	defer h.access.Unlock()
	featureType := reflect.TypeOf(feature.Type())
	if !featureType.AssignableTo(h.memberType) {
		return errors.New("feature is not assignable to the base type")
	}
	h.features[tag] = feature
	return nil
}

func (h *Holder) GetFeaturesTag() []string {
	h.access.RLock()
	defer h.access.RUnlock()
	var ret []string
	for key := range h.features {
		ret = append(ret, key)
	}
	return ret
}

func (h *Holder) Start() error {
	h.access.Lock()
	defer h.access.Unlock()
	var startTasks []func() error
	for _, v := range h.features {
		startTasks = append(startTasks, v.Start)
	}
	return task.Run(h.ctx, startTasks...)
}

func (h *Holder) Close() error {
	h.access.Lock()
	defer h.access.Unlock()
	var closeTasks []func() error
	for _, v := range h.features {
		closeTasks = append(closeTasks, v.Close)
	}
	return task.Run(h.ctx, closeTasks...)
}

// NewHolderFromConfig creates a Holder from a Config, instantiating each feature.
func NewHolderFromConfig(ctx context.Context, config *Config, memberType interface{}) (features.TaggedFeatures, error) {
	holder := NewHolder(ctx, memberType)
	for k, v := range config.Features {
		instance, err := v.GetInstance()
		if err != nil {
			return nil, errors.New("unable to get instance for tag: ", k).Base(err)
		}
		obj, err := common.CreateObject(ctx, instance)
		if err != nil {
			return nil, errors.New("unable to create object for tag: ", k).Base(err)
		}
		feat, ok := obj.(features.Feature)
		if !ok {
			return nil, errors.New("not a feature: ", k)
		}
		if err := holder.AddFeaturesByTag(k, feat); err != nil {
			return nil, errors.New("unable to add feature: ", k).Base(err)
		}
	}
	return holder, nil
}
