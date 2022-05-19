package xdsresource

import (
	"fmt"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3matcherpb "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"time"
)

type RouteConfigResource struct {
	name string
	vhs  []VirtualHost
}

func (res *RouteConfigResource) Name() string {
	return res.name
}

func (res *RouteConfigResource) VirtualHosts() []VirtualHost {
	return res.vhs
}

type VirtualHost struct {
	name   string
	routes []Route
}

func (vh *VirtualHost) Routes() []Route {
	return vh.routes
}

type HeaderMatchType int

const (
	Prefix HeaderMatchType = iota
	Exact
	Suffix
	Contains
)

type Route struct {
	match   RouteMatch
	cluster string
	timeout time.Duration
}

func (r *Route) Match() RouteMatch {
	return r.match
}

type RouteMatch struct {
	prefix        string
	path          string
	caseSensitive bool
	headers       []RouteMatchHeader
}

type RouteMatchHeader struct {
	name        string
	value       string
	matcherType HeaderMatchType
}

func (rm *RouteMatch) Matched(tags map[string]string) bool {
	for _, h := range rm.headers {
		switch h.matcherType {
		case Exact:
			if v, ok := tags[h.name]; !ok || v != h.value {
				return false
			}
		}
	}
	return true
}

func unmarshalRouteConfig(routeConfig *v3routepb.RouteConfiguration) RouteConfigResource {
	vhs := routeConfig.GetVirtualHosts()
	virtualHosts := make([]VirtualHost, len(vhs))
	for i := 0; i < len(virtualHosts); i++ {
		rs := vhs[i].GetRoutes()
		routes := make([]Route, len(rs))
		for j := 0; j < len(rs); j++ {
			route := Route{}
			// match
			// 1. different path
			// 2. store all headers
			routeMatch := RouteMatch{}
			match := rs[j].GetMatch()
			pathSpecifier := match.GetPathSpecifier()
			switch p := pathSpecifier.(type) {
			case *v3routepb.RouteMatch_Prefix:
				routeMatch.prefix = p.Prefix
			case *v3routepb.RouteMatch_Path:
				routeMatch.prefix = p.Path
			}
			// route header
			hs := match.GetHeaders()
			headers := make([]RouteMatchHeader, len(hs))
			for k := 0; k < len(hs); k++ {
				headerSpecifier := hs[k].GetHeaderMatchSpecifier()
				var value string
				var matcherType HeaderMatchType
				switch ht := headerSpecifier.(type) {
				// new string match
				case *v3routepb.HeaderMatcher_StringMatch:
					switch pattern := ht.StringMatch.GetMatchPattern().(type) {
					case *v3matcherpb.StringMatcher_Exact:
						value = pattern.Exact
						matcherType = Exact
					case *v3matcherpb.StringMatcher_Prefix:
						value = pattern.Prefix
						matcherType = Prefix
					case *v3matcherpb.StringMatcher_Suffix:
						value = pattern.Suffix
						matcherType = Suffix
					case *v3matcherpb.StringMatcher_Contains:
						value = pattern.Contains
						matcherType = Contains
					}
				// old match
				case *v3routepb.HeaderMatcher_ExactMatch:
					value = ht.ExactMatch
					matcherType = Exact
				case *v3routepb.HeaderMatcher_PrefixMatch:
					value = ht.PrefixMatch
					matcherType = Prefix
				case *v3routepb.HeaderMatcher_SuffixMatch:
					value = ht.SuffixMatch
					matcherType = Suffix
				case *v3routepb.HeaderMatcher_ContainsMatch:
					value = ht.ContainsMatch
					matcherType = Contains
				}
				headers[k] = RouteMatchHeader{
					name:        hs[k].GetName(),
					value:       value,
					matcherType: matcherType,
				}
				fmt.Printf("[xds] Route, name: %s, value: %s, type %d \n", headers[k].name, headers[k].value, headers[k].matcherType)
			}
			// action
			action := rs[j].GetAction()
			switch a := action.(type) {
			case *v3routepb.Route_Route:
				route.cluster = a.Route.GetCluster()
				route.timeout = a.Route.GetTimeout().AsDuration()
			}
			routes[j] = route
		}
		virtualHost := VirtualHost{
			name:   vhs[i].GetName(),
			routes: routes,
		}
		virtualHosts[i] = virtualHost
	}

	return RouteConfigResource{
		name: routeConfig.GetName(),
		vhs:  virtualHosts,
	}
}

func UnmarshalRDS(rawResources []*any.Any) map[string]RouteConfigResource {
	ret := make(map[string]RouteConfigResource, len(rawResources))

	for _, r := range rawResources {
		rcfg := &v3routepb.RouteConfiguration{}
		if err := proto.Unmarshal(r.GetValue(), rcfg); err != nil {
			panic("[xds] RDS Unmarshal error")
		}
		ret[rcfg.GetName()] = unmarshalRouteConfig(rcfg)
	}

	return ret
}
