package xdsresource

import (
	"fmt"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"strings"
)

type ListenerResource struct {
	routeConfigName   string
	inlineRouteConfig RouteConfigResource
}

func (res *ListenerResource) RouteConfigName() string {
	return res.routeConfigName
}

func (res *ListenerResource) InlineRouteConfig() RouteConfigResource {
	return res.inlineRouteConfig
}

func UnmarshalLDS(rawResources []*any.Any) map[string]ListenerResource {
	ret := make(map[string]ListenerResource, len(rawResources))

	for _, r := range rawResources {
		lis := &v3listenerpb.Listener{}
		if err := proto.Unmarshal(r.GetValue(), lis); err != nil {
			panic("[xds] LDS Unmarshal error")
		}
		apiLis := &v3httppb.HttpConnectionManager{}
		if err := proto.Unmarshal(lis.GetApiListener().GetApiListener().GetValue(), apiLis); err != nil {
			panic("failed to unmarshal api_listner")
		}
		//fmt.Println("[xds] LDS(routeConfig name):", apiLis.GetRds().RouteConfigName)
		//fmt.Println("[xds] api_listener:", apiLis.String())

		var res ListenerResource
		switch apiLis.RouteSpecifier.(type) {
		case *v3httppb.HttpConnectionManager_Rds:
			res = ListenerResource{routeConfigName: apiLis.GetRds().RouteConfigName}
		case *v3httppb.HttpConnectionManager_RouteConfig:
			var inlineRDS RouteConfigResource
			if apiLis.GetRouteConfig() != nil {
				inlineRDS = unmarshalRouteConfig(apiLis.GetRouteConfig())
			}
			res = ListenerResource{
				routeConfigName:   inlineRDS.Name(),
				inlineRouteConfig: inlineRDS,
			}
		}
		// TODO: check if the name is correct
		fmt.Println("[xds] listener:", lis.String())
		name := parseListenerName(lis.GetName())
		ret[name] = res
	}

	return ret
}

func parseListenerName(name string) string {
	return strings.Split(name, ":")[0]
}