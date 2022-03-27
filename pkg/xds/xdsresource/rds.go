package xdsresource

import (
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/golang/protobuf/ptypes/any"
)

type RouteConfigResource struct {
}

func unmarshalRoute(routeConfig *v3routepb.RouteConfiguration) {
	//vhs := routeConfig.GetVirtualHosts()
	//for _, vh := range vhs {
	//	name := vh.GetName()
	//	routes := vh.GetRoutes()
	//	matcher := vh.GetMatcher()
	//}
}

func UnmarshalRDS(rawResources []*any.Any) map[string]RouteConfigResource {
	return nil
}
