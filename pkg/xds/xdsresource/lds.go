package xdsresource

import (
	"fmt"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
)

type ListenerResource struct {
	name string
}

func (res *ListenerResource) Name() string {
	return res.name
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

		fmt.Println("[xds] LDS(routeConfig name):", apiLis.GetRds().RouteConfigName)
		fmt.Println("[xds] api_listener:", apiLis.String())
		// name
		name := apiLis.GetRds().RouteConfigName
		// inline RDS
		if apiLis.GetRouteConfig() != nil {
			fmt.Println("[xds] LDS Inline RDS", apiLis.GetRouteConfig().String())
		}
		ret[name] = ListenerResource{
			name: name,
		}
	}

	return ret
}
