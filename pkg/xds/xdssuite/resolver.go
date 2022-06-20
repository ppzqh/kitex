package xdssuite

import (
	"context"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
)

// FOR TEST

type XDSResolver struct {
}

// Target should return a description for the given target that is suitable for being a key for cache.
func (r *XDSResolver) Target(ctx context.Context, target rpcinfo.EndpointInfo) (description string) {
	dest, ok := target.Tag(RouterDestinationKey)
	if !ok {
		return target.ServiceName()
	}
	return dest
}

// Resolve returns a list of instances for the given description of a target.
func (r *XDSResolver) Resolve(ctx context.Context, desc string) (discovery.Result, error) {
	resource, err := manager.Get(xdsresource.EndpointsType, desc)
	if err != nil {
		return discovery.Result{}, kerrors.ErrServiceDiscovery.WithCause(err)
	}

	cla, ok := resource.(*xdsresource.EndpointsResource)
	if !ok {
		return discovery.Result{}, kerrors.ErrServiceDiscovery.WithCause(err)
	}
	if len(cla.Localities) == 0 {
		return discovery.Result{}, kerrors.ErrServiceDiscovery.WithCause(err)
	}
	eds := cla.Localities[0].Endpoints
	instances := make([]discovery.Instance, len(eds))
	for i, e := range eds {
		instances[i] = discovery.NewInstance(e.Addr.Network(), e.Addr.String(), e.Weight, e.Meta)
	}
	res := discovery.Result{
		Cacheable: false,
		CacheKey:  "",
		Instances: instances,
	}
	return res, nil
}

func (r *XDSResolver) Diff(cacheKey string, prev, next discovery.Result) (discovery.Change, bool) {
	return discovery.Change{}, false
}

// Name returns the name of the resolver.
func (r *XDSResolver) Name() string {
	return "xdsResolver"
}
