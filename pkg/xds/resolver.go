package xds

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
)

// FOR TEST

type XdsResolver struct {
}

// Target should return a description for the given target that is suitable for being a key for cache.
func (r *XdsResolver) Target(ctx context.Context, target rpcinfo.EndpointInfo) (description string) {
	dest, ok := target.Tag(RouterDestinationKey)
	fmt.Println("[xds resolver] target:", dest)
	if !ok {
		return target.ServiceName()
	}
	return dest
}

// Resolve returns a list of instances for the given description of a target.
func (r *XdsResolver) Resolve(ctx context.Context, desc string) (discovery.Result, error) {
	mng := GetXdsResourceManager()
	fmt.Println("[xds resolver] description:", desc)
	cds := mng.Get(xdsresource.ClusterType, desc)
	if cds == nil {
		panic("[xds resolver] get CDS failed")
	}
	cluster, ok := cds.(xdsresource.ClusterResource)
	if !ok {
		panic("[xds resolver] CDS cast failed")
	}
	fmt.Println("[xds resolver] endpoint:", cluster.EndpointName())

	resource := mng.Get(xdsresource.EndpointsType, desc)
	if resource == nil {
		panic("[xds resolver] get EDS failed")
	}

	cla, ok := resource.(xdsresource.EndpointsResource)
	if !ok {
		panic("[xds resolver] EDS cast failed")
		return discovery.Result{}, kerrors.ErrServiceDiscovery
	}
	fmt.Println("[xds resolver], name:", cla.Name)
	if len(cla.Localities) == 0 {
		panic("no eds")
	}
	for _, l := range cla.Localities {
		for _, e := range l.Endpoints {
			fmt.Println(e.Address())
		}
	}
	eds := cla.Localities[0].Endpoints
	instances := make([]discovery.Instance, len(eds))
	for i, e := range eds {
		instances[i] = e
		fmt.Println(e.Address())
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