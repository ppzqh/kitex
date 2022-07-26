package xdssuite

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"github.com/cloudwego/kitex/transport"
	"math/rand"
	"time"
)

const (
	RouterClusterKey         = "XDS_Route_Picked_Cluster"
	defaultTotalWeight int32 = 100
)

// RouteResult stores the result of route
type RouteResult struct {
	RPCTimeout    time.Duration
	ClusterPicked string
	// TODO: retry policy also in RDS
}

type XDSRouter struct{}

// Route routes the rpc call to a cluster based on the RPCInfo
func (r *XDSRouter) Route(ctx context.Context, ri rpcinfo.RPCInfo) (*RouteResult, error) {
	//rc, err := getRouteConfig(ctx, ri)
	//if err != nil {
	//	return nil, kerrors.ErrXDSRoute.WithCause(err)
	//}
	//matchedRoute := matchRoute(ri, rc)

	route, err := getRoute(ctx, ri)
	// no matched route
	if err != nil {
		return nil, kerrors.ErrXDSRoute.WithCause(fmt.Errorf("no matched route for service %s", ri.To().ServiceName()))
	}
	// pick cluster from the matched route
	cluster := pickCluster(route)
	if cluster == "" {
		return nil, kerrors.ErrXDSRoute.WithCause(fmt.Errorf("no cluster selected"))
	}
	return &RouteResult{
		RPCTimeout:    route.Timeout,
		ClusterPicked: cluster,
	}, nil
}

// getRouteConfig gets the route config from xdsResourceManager
func getRoute(ctx context.Context, ri rpcinfo.RPCInfo) (*xdsresource.Route, error) {
	// TODO: 拼接 ri.Invocation().ServiceName()

	ri.To().ServiceName()
	m, err := getXdsResourceManager()
	if err != nil {
		return nil, err
	}
	// use serviceName as the listener name
	ln := ri.To().ServiceName()
	lds, err := m.Get(ctx, xdsresource.ListenerType, ln)
	if err != nil {
		return nil, fmt.Errorf("get listener failed: %v", err)
	}
	lis := lds.(*xdsresource.ListenerResource)

	var thrift, http *xdsresource.NetworkFilter
	for _, f := range lis.NetworkFilters {
		if f.FilterType == xdsresource.NetworkFilterTypeHTTP {
			http = f
		}
		if f.FilterType == xdsresource.NetworkFilterTypeThrift {
			thrift = f
		}
	}
	// match thrift route first, only inline route is supported
	if ri.Config().TransportProtocol() != transport.GRPC {
		if thrift != nil && thrift.InlineRouteConfig != nil {
			r := matchThriftRoute(ri, thrift.InlineRouteConfig)
			if r != nil {
				return r, nil
			}
		}
	}
	if http == nil {
		return nil, fmt.Errorf("no http filter found in listener %s", ln)
	}
	// inline route config
	if http.InlineRouteConfig != nil {
		r := matchHTTPRoute(ri, http.InlineRouteConfig)
		if r != nil {
			return r, nil
		}
	}
	// Get the route config
	rds, err := m.Get(ctx, xdsresource.RouteConfigType, http.RouteConfigName)
	if err != nil {
		return nil, fmt.Errorf("get route failed: %v", err)
	}
	rcfg := rds.(*xdsresource.RouteConfigResource)
	r := matchHTTPRoute(ri, rcfg)
	if r != nil {
		return r, nil
	}
	return nil, fmt.Errorf("no matched route")
}

// matchRoute matches one route in the provided routeConfig based on information in RPCInfo
func matchRoute(ri rpcinfo.RPCInfo, routeConfig *xdsresource.RouteConfigResource) *xdsresource.Route {
	// If using GRPC, match the HTTP route
	if ri.Config().TransportProtocol() == transport.GRPC {
		return matchHTTPRoute(ri, routeConfig)
	}
	// Or, match thrift route first
	r := matchThriftRoute(ri, routeConfig)
	if r != nil {
		return r
	}
	// fallback to http route if thrift route is not configured
	return matchHTTPRoute(ri, routeConfig)
}

// matchHTTPRoute matches one http route
func matchHTTPRoute(ri rpcinfo.RPCInfo, routeConfig *xdsresource.RouteConfigResource) *xdsresource.Route {
	if rcfg := routeConfig.HttpRouteConfig; rcfg != nil {
		for _, vh := range rcfg.VirtualHosts {
			// skip the domain match

			// use the first matched route
			for _, r := range vh.Routes {
				if routeMatched(ri.To(), r) {
					return r
				}
			}
		}
	}
	return nil
}

// matchThriftRoute matches one thrift route
func matchThriftRoute(ri rpcinfo.RPCInfo, routeConfig *xdsresource.RouteConfigResource) *xdsresource.Route {
	if rcfg := routeConfig.ThriftRouteConfig; rcfg != nil {
		for _, r := range rcfg.Routes {
			if routeMatched(ri.To(), r) {
				return r
			}
		}
	}
	return nil
}

// routeMatched checks if the route matches the info provided in the RPCInfo
func routeMatched(to rpcinfo.EndpointInfo, r *xdsresource.Route) bool {
	method := to.Method()
	if r.Match != nil && r.Match.MatchPath(method) {
		//return r
		tagMatched := true
		for mk, mv := range r.Match.GetTags() {
			if v, ok := to.Tag(mk); !ok || v != mv {
				tagMatched = false
				break
			}
		}
		if tagMatched {
			return true
		}
	}
	return false
}

// pickCluster selects cluster based on the weight
func pickCluster(route *xdsresource.Route) string {
	// handle weighted cluster
	wcs := route.WeightedClusters
	if len(wcs) == 0 {
		return ""
	}
	if len(wcs) == 1 {
		return wcs[0].Name
	}
	currWeight := uint32(0)
	targetWeight := uint32(rand.Int31n(defaultTotalWeight))
	for _, wc := range wcs {
		currWeight += wc.Weight
		if currWeight >= targetWeight {
			return wc.Name
		}
	}
	// total weight is less than target weight
	return ""
}
