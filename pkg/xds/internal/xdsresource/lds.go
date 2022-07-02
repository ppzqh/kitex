package xdsresource

import (
	"fmt"
	v3listenerpb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3httppb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"strings"
)

// Network filter names
// github.com/envoyproxy/go-control-plane/pkg/wellknown
const (
	// HTTPConnectionManager network filter
	HTTPConnectionManager = "envoy.filters.network.http_connection_manager"
	// ThriftProxy network filter
	// ThriftProxy = "envoy.filters.network.thrift_proxy" // Has been removed from Istio
)

type ListenerResource struct {
	RouteConfigName   string
	InlineRouteConfig *RouteConfigResource
}

func unmarshallApiListener(apiListener *v3listenerpb.ApiListener) (*ListenerResource, error) {
	if apiListener.GetApiListener() == nil {
		return nil, fmt.Errorf("apiListener is empty")
	}
	// unmarshal listener
	apiLis := &v3httppb.HttpConnectionManager{}
	if err := proto.Unmarshal(apiListener.GetApiListener().GetValue(), apiLis); err != nil {
		return nil, fmt.Errorf("unmarshal api listener failed: %s", err)
	}
	// convert listener
	// 1. RDS
	// 2. inline route config
	var res *ListenerResource
	switch apiLis.RouteSpecifier.(type) {
	case *v3httppb.HttpConnectionManager_Rds:
		if apiLis.GetRds() == nil {
			return nil, fmt.Errorf("no Rds in the apiListener")
		}
		if apiLis.GetRds().GetRouteConfigName() == "" {
			return nil, fmt.Errorf("no route config name")
		}
		res = &ListenerResource{RouteConfigName: apiLis.GetRds().GetRouteConfigName()}
	case *v3httppb.HttpConnectionManager_RouteConfig:
		if rcfg := apiLis.GetRouteConfig(); rcfg == nil {
			return nil, fmt.Errorf("no inline route config")
		} else {
			inlineRouteConfig, err := unmarshalRouteConfig(rcfg)
			if err != nil {
				return nil, err
			}
			res = &ListenerResource{
				RouteConfigName:   apiLis.GetRouteConfig().GetName(),
				InlineRouteConfig: inlineRouteConfig,
			}
		}
	}
	return res, nil
}

func UnmarshalLDS(rawResources []*any.Any) (map[string]*ListenerResource, error) {
	ret := make(map[string]*ListenerResource, len(rawResources))

	for _, r := range rawResources {
		lis := &v3listenerpb.Listener{}
		if err := proto.Unmarshal(r.GetValue(), lis); err != nil {
			return nil, fmt.Errorf("unmarshal listener failed: %s", err)
		}
		// http listener
		if apiListener := lis.GetApiListener(); apiListener != nil {
			res, err := unmarshallApiListener(apiListener)
			if err != nil {
				return nil, fmt.Errorf("unmarshal listener %s failed: %s", lis.GetName(), err)
			}
			ret[lis.GetName()] = res
		}
	}

	return ret, nil
}

func parseListenerName(name string) string {
	fmt.Println(name)
	return strings.Split(name, ":")[0]
}
