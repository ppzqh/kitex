package xdsresource

import (
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/golang/protobuf/ptypes/any"
)

type RouteAction struct {
}

type RouteConfigResource struct {
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
	//v3routepb.RouteConfiguration{}
	return nil
}
