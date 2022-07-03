package xdsresource

import (
	"fmt"
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
	Weight int32
}

type Route struct {
	Match            *RouteMatch
	WeightedClusters []*weightedCluster
	Timeout          time.Duration
}

type RouteMatch struct {
	Path          string
	CaseSensitive bool
}

func (rm *RouteMatch) Matched(path string, tags map[string]string) bool {
	return rm.Path == "" || rm.Path == path
}

func unmarshalRoutes(rs []*v3routepb.Route) ([]*Route, error) {
	routes := make([]*Route, len(rs))
	for i := 0; i < len(rs); i++ {
		route := &Route{}
		routeMatch := &RouteMatch{}
		match := rs[i].GetMatch()
		if match == nil {
			return nil, fmt.Errorf("no match in route %s", rs[i].GetName())
		}
		pathSpecifier := match.GetPathSpecifier()
		// only support exact match for path
		switch p := pathSpecifier.(type) {
		case *v3routepb.RouteMatch_Path:
			routeMatch.Path = p.Path
			//default:
			//	return nil, fmt.Errorf("only support path match")
		}
		route.Match = routeMatch
		// action
		action := rs[i].GetAction()
		if action == nil {
			return nil, fmt.Errorf("no action in route %s", rs[i].GetName())
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
						Weight: int32(wc.GetWeight().GetValue()),
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
	if len(vhs) == 0 {
		return nil, fmt.Errorf("no virtual host in route config")
	}
	virtualHosts := make([]*VirtualHost, len(vhs))
	for i := 0; i < len(vhs); i++ {
		rs := vhs[i].GetRoutes()
		routes, err := unmarshalRoutes(rs)
		if err != nil {
			return nil, fmt.Errorf("processing route in virtual host %s failed: %s", vhs[i].GetName(), err)
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
	ret := make(map[string]*RouteConfigResource, len(rawResources))

	for _, r := range rawResources {
		rcfg := &v3routepb.RouteConfiguration{}
		if err := proto.Unmarshal(r.GetValue(), rcfg); err != nil {
			return nil, fmt.Errorf("unmarshal failed: %s", err)
		}
		res, err := unmarshalRouteConfig(rcfg)
		if err != nil {
			return nil, fmt.Errorf("unmarshal route config %s failed: %s", rcfg.GetName(), err)
		}
		ret[rcfg.GetName()] = res
	}

	return ret, nil
}
