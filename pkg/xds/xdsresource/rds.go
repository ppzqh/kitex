package xdsresource

import (
	"fmt"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
)

type RouteAction struct {
}

type RouteConfigResource struct {
	name string
}

func (res *RouteConfigResource) Name() string {
	return res.name
}

type Route struct {
	name string
	path string
}

func unmarshalRoute(routeConfig *v3routepb.RouteConfiguration) {
	vhs := routeConfig.GetVirtualHosts()
	for _, vh := range vhs {
		//name := vh.GetName()
		routes := vh.GetRoutes()
		for _, r := range routes {
			ret := Route{name: r.GetName()}
			// match
			match := r.GetMatch()
			pathSpecifier := match.GetPathSpecifier()
			switch p := pathSpecifier.(type) {
			case *v3routepb.RouteMatch_Path:
				ret.path = p.Path
			}
			// action
			action := r.GetAction()
			switch a := action.(type) {
			case *v3routepb.Route_Route:
				a.Route.GetTimeout()
				a.Route.GetCluster()
			}
		}
	}
}

func UnmarshalRDS(rawResources []*any.Any) map[string]RouteConfigResource {
	ret := make(map[string]RouteConfigResource, len(rawResources))

	for _, r := range rawResources {
		rcfg := &v3routepb.RouteConfiguration{}
		if err := proto.Unmarshal(r.GetValue(), rcfg); err != nil {
			panic("[xds] RDS Unmarshal error")
		}
		// TODO: how to store?
		name := rcfg.GetName()
		fmt.Println("[xds] routeConfig's name:", name)
		for _, vh := range rcfg.GetVirtualHosts() {
			fmt.Println("[xds] virtual host name:", vh.GetName())
			routes := vh.GetRoutes()
			for _, rs := range routes {
				fmt.Println("[xds] route:", rs.GetRoute().String())
			}
		}
		ret[name] = RouteConfigResource{
			name: name,
		}
	}
	return ret
}
