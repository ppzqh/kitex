package xdsresource

import (
	"fmt"
	"github.com/cloudwego/kitex/pkg/klog"
	v3routepb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"time"
)

type RouteConfigResource struct {
	VirtualHosts []*VirtualHost
}

type VirtualHost struct {
	Name   string
	Routes []*Route
}

type weightedCluster struct {
	Name   string
	Weight uint32
}

type Route struct {
	Match            *RouteMatch
	WeightedClusters []*weightedCluster
	Timeout          time.Duration
}

type RouteMatch struct {
	Path   string
	Prefix string
}

func (rm *RouteMatch) Matched(path string, tags map[string]string) bool {
	if rm.Path != "" {
		return rm.Path == path
	}
	return rm.Prefix == "/"
}

func unmarshalRoutes(rs []*v3routepb.Route) ([]*Route, error) {
	routes := make([]*Route, len(rs))
	for i := 0; i < len(rs); i++ {
		route := &Route{}
		routeMatch := &RouteMatch{}
		match := rs[i].GetMatch()
		if match == nil {
			klog.Errorf("no match in route %s\n", rs[i].GetName())
			continue
		}
		pathSpecifier := match.GetPathSpecifier()
		// only support exact match for path
		switch p := pathSpecifier.(type) {
		// default route if virtual service is not configured
		case *v3routepb.RouteMatch_Prefix:
			routeMatch.Prefix = p.Prefix
		case *v3routepb.RouteMatch_Path:
			routeMatch.Path = p.Path
			//default:
			//	return nil, fmt.Errorf("only support path match")
		}
		route.Match = routeMatch
		// action
		action := rs[i].GetAction()
		if action == nil {
			klog.Errorf("no action in route %s\n", rs[i].GetName())
		}
		switch a := action.(type) {
		case *v3routepb.Route_Route:
			switch cs := a.Route.GetClusterSpecifier().(type) {
			case *v3routepb.RouteAction_Cluster:
				route.WeightedClusters = []*weightedCluster{
					{Name: cs.Cluster, Weight: 1},
				}
			case *v3routepb.RouteAction_WeightedClusters:
				wcs := cs.WeightedClusters
				clusters := make([]*weightedCluster, len(wcs.Clusters))
				for i, wc := range wcs.GetClusters() {
					clusters[i] = &weightedCluster{
						Name:   wc.GetName(),
						Weight: wc.GetWeight().GetValue(),
					}
				}
				route.WeightedClusters = clusters
			}
			route.Timeout = a.Route.GetTimeout().AsDuration()
		}
		routes[i] = route
	}
	return routes, nil
}

func unmarshalRouteConfig(routeConfig *v3routepb.RouteConfiguration) (*RouteConfigResource, error) {
	vhs := routeConfig.GetVirtualHosts()
	virtualHosts := make([]*VirtualHost, len(vhs))
	for i := 0; i < len(vhs); i++ {
		rs := vhs[i].GetRoutes()
		routes, err := unmarshalRoutes(rs)
		if err != nil {
			klog.Errorf("processing route in virtual host %s failed: %s\n", vhs[i].GetName(), err)
			continue
		}
		virtualHost := &VirtualHost{
			Name:   vhs[i].GetName(),
			Routes: routes,
		}
		virtualHosts[i] = virtualHost
	}
	return &RouteConfigResource{
		VirtualHosts: virtualHosts,
	}, nil
}

func UnmarshalRDS(rawResources []*any.Any) (map[string]*RouteConfigResource, error) {
	if rawResources == nil {
		return nil, fmt.Errorf("empty route config resource")
	}

	ret := make(map[string]*RouteConfigResource, len(rawResources))
	for _, r := range rawResources {
		if r.GetTypeUrl() != RouteTypeUrl {
			klog.Errorf("invalid route config resource type: %s\n", r.GetTypeUrl())
			continue
		}
		rcfg := &v3routepb.RouteConfiguration{}
		if err := proto.Unmarshal(r.GetValue(), rcfg); err != nil {
			klog.Errorf("unmarshal failed: %s\n", err)
			continue
		}
		res, err := unmarshalRouteConfig(rcfg)
		if err != nil {
			return nil, fmt.Errorf("unmarshal route config %s failed: %s", rcfg.GetName(), err)
		}
		ret[rcfg.GetName()] = res
	}

	return ret, nil
}
