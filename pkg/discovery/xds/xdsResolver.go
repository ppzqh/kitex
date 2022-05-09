package xds

import (
	"context"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/xds"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
)

// FOR TEST

type XdsResolver struct {
}

// Target should return a description for the given target that is suitable for being a key for cache.
func (r *XdsResolver) Target(ctx context.Context, target rpcinfo.EndpointInfo) (description string) {
	// 1. routeConfig (RDS)
	// 2. cluster (CDS)
	return target.ServiceName()
}

// Resolve returns a list of instances for the given description of a target.
func (r *XdsResolver) Resolve(ctx context.Context, desc string) (discovery.Result, error) {
	mng := xds.GetXdsResourceManager()

	resource := mng.Get(xdsresource.EndpointsType, desc)
	cla, ok := resource.(xdsresource.EndpointsResource)
	if !ok {
		return discovery.Result{}, kerrors.ErrServiceDiscovery
	}
	eds := cla.Localities[0].Endpoints
	instances := make([]discovery.Instance, len(eds))
	for i, e := range eds {
		instances[i] = e
	}
	res := discovery.Result{
		Cacheable: false,
		CacheKey: "",
		Instances: instances,
	}
	return res, nil
}


func (r *XdsResolver) Diff(cacheKey string, prev, next discovery.Result) (discovery.Change, bool) {
	return discovery.Change{}, false
}

// Name returns the name of the resolver.
func (r *XdsResolver) Name() string {
	return "xdsResolver"
}