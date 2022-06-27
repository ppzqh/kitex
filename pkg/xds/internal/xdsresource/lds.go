package xdsresource

import (
	"fmt"
	v3listenerpb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3httppb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	thrift_proxyv3 "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/thrift_proxy/v3"
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
	ThriftProxy = "envoy.filters.network.thrift_proxy"
)

type ListenerResource struct {
	Name              string
	RouteConfigName   string
	InlineRouteConfig *RouteConfigResource
}

func unmarshallListenerFilters(filters []*v3listenerpb.ListenerFilter) {
	for _, f := range filters {
		switch f.GetName() {
		case ThriftProxy:
			unmarshallThriftProxy(f)
		}
	}
}

func unmarshallThriftProxy(filter *v3listenerpb.ListenerFilter) {
	p := &thrift_proxyv3.ThriftProxy{}
	switch cfgType := filter.GetConfigType().(type) {
	case *v3listenerpb.ListenerFilter_TypedConfig:
		if err := proto.Unmarshal(cfgType.TypedConfig.GetValue(), p); err != nil {
			panic("failed to unmarshal thrift proxy")
		}
		fmt.Println("thrift proxy, rds name:", p.GetRouteConfig().GetName())
	}
	routes := p.RouteConfig.GetRoutes()
	for _, r := range routes {
		match := r.GetMatch()
		switch t := match.GetMatchSpecifier().(type) {
		case *thrift_proxyv3.RouteMatch_MethodName:
			_ = t.MethodName
		case *thrift_proxyv3.RouteMatch_ServiceName:
			_ = t.ServiceName
		}
		r.GetRoute()
		//action := r.GetRoute()

	}
}

func unmarshallApiListener(apiListener *v3listenerpb.ApiListener) *ListenerResource {
	if apiListener.GetApiListener() == nil {
		return nil
	}

	apiLis := &v3httppb.HttpConnectionManager{}
	if err := proto.Unmarshal(apiListener.GetApiListener().GetValue(), apiLis); err != nil {
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

	return res
}


func UnmarshalLDS(rawResources []*any.Any) map[string]*ListenerResource {
	ret := make(map[string]*ListenerResource, len(rawResources))

	for _, r := range rawResources {
		lis := &v3listenerpb.Listener{}
		if err := proto.Unmarshal(r.GetValue(), lis); err != nil {
			panic("[xds] LDS Unmarshal error")
		}

		var res *ListenerResource
		// thrift proxy
		if filters := lis.GetListenerFilters(); filters != nil {
			fmt.Println("fliter")
			unmarshallListenerFilters(filters)
		}

		// http listener
		if apiListener := lis.GetApiListener(); apiListener != nil {
			res = unmarshallApiListener(lis.GetApiListener())
			// TODO: check if the Name is correct
			//fmt.Println("[xds] listener:", lis.String())
			name := parseListenerName(lis.GetName())
			res.Name = name
			ret[name] = res
			continue
		}

	}

	return ret
}

func parseListenerName(name string) string {
	fmt.Println(name)
	return strings.Split(name, ":")[0]
}