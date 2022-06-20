package xdsresource

import (
	"github.com/golang/protobuf/ptypes/any"
	v3listenerpb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3httppb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/proto"
	"strings"
)

type ListenerResource struct {
	Name              string
	RouteConfigName   string
	InlineRouteConfig *RouteConfigResource
}

func UnmarshalLDS(rawResources []*any.Any) map[string]*ListenerResource {
	ret := make(map[string]*ListenerResource, len(rawResources))

	for _, r := range rawResources {
		lis := &v3listenerpb.Listener{}
		if err := proto.Unmarshal(r.GetValue(), lis); err != nil {
			panic("[xds] LDS Unmarshal error")
		}
		apiLis := &v3httppb.HttpConnectionManager{}
		if err := proto.Unmarshal(lis.GetApiListener().GetApiListener().GetValue(), apiLis); err != nil {
			panic("failed to unmarshal api_listner")
		}

		var res *ListenerResource
		switch apiLis.RouteSpecifier.(type) {
		case *v3httppb.HttpConnectionManager_Rds:
			res = &ListenerResource{RouteConfigName: apiLis.GetRds().RouteConfigName}
		case *v3httppb.HttpConnectionManager_RouteConfig:
			var inlineRDS *RouteConfigResource
			if apiLis.GetRouteConfig() != nil {
				inlineRDS = unmarshalRouteConfig(apiLis.GetRouteConfig())
			}
			res = &ListenerResource{
				RouteConfigName:   inlineRDS.Name,
				InlineRouteConfig: inlineRDS,
			}
		}
		// TODO: check if the Name is correct
		//fmt.Println("[xds] listener:", lis.String())
		name := parseListenerName(lis.GetName())
		res.Name = name
		ret[name] = res
	}

	return ret
}

func parseListenerName(name string) string {
	return strings.Split(name, ":")[0]
}