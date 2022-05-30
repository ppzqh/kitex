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

func (vh *VirtualHost) Name() string {
	return vh.name
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
	Present
)

type weightedCluster struct {
	name   string
	weight uint32
}

func (c *weightedCluster) Name() string {
	return c.name
}

func (c *weightedCluster) Weight() uint32 {
	return c.weight
}

type Route struct {
	match            RouteMatch
	cluster          string
	weightedClusters []weightedCluster
	timeout          time.Duration
}

func (r *Route) Match() RouteMatch {
	return r.match
}

func (r *Route) Cluster() string {
	return r.cluster
}

func (r *Route) WeightedClusters() []weightedCluster {
	return r.weightedClusters
}

func (r *Route) Timeout() time.Duration {
	return r.timeout
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
	present     bool
	matcherType HeaderMatchType
}

func (rm *RouteMatch) Matched(path string, tags map[string]string) bool {
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
	for i := 0; i < len(vhs); i++ {
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
				var header RouteMatchHeader
				header.name = hs[k].GetName()

				headerSpecifier := hs[k].GetHeaderMatchSpecifier()
				switch ht := headerSpecifier.(type) {
				// new string match
				case *v3routepb.HeaderMatcher_StringMatch:
					switch pattern := ht.StringMatch.GetMatchPattern().(type) {
					case *v3matcherpb.StringMatcher_Exact:
						header.value = pattern.Exact
						header.matcherType = Exact
					case *v3matcherpb.StringMatcher_Prefix:
						header.value = pattern.Prefix
						header.matcherType = Prefix
					case *v3matcherpb.StringMatcher_Suffix:
						header.value = pattern.Suffix
						header.matcherType = Suffix
					case *v3matcherpb.StringMatcher_Contains:
						header.value = pattern.Contains
						header.matcherType = Contains
						// TODO:
						// case *v3matcherpb.StringMatcher_SafeRegex:
					}
				// old match
				case *v3routepb.HeaderMatcher_ExactMatch:
					header.value = ht.ExactMatch
					header.matcherType = Exact
				case *v3routepb.HeaderMatcher_PrefixMatch:
					header.value = ht.PrefixMatch
					header.matcherType = Prefix
				case *v3routepb.HeaderMatcher_SuffixMatch:
					header.value = ht.SuffixMatch
					header.matcherType = Suffix
				case *v3routepb.HeaderMatcher_ContainsMatch:
					header.value = ht.ContainsMatch
					header.matcherType = Contains
				case *v3routepb.HeaderMatcher_PresentMatch:
					header.matcherType = Present
					header.present = ht.PresentMatch
				}
				headers[k] = header
				fmt.Printf("[xds] Route, name: %s, value: %s, type %d \n", headers[k].name, headers[k].value, headers[k].matcherType)
			}
			// action
			action := rs[j].GetAction()
			switch a := action.(type) {
			case *v3routepb.Route_Route:
				switch cs := a.Route.GetClusterSpecifier().(type) {
				case *v3routepb.RouteAction_Cluster:
					route.cluster = cs.Cluster
					route.weightedClusters = []weightedCluster{
						{name: cs.Cluster, weight: 1},
					}
				case *v3routepb.RouteAction_WeightedClusters:
					wcs := cs.WeightedClusters
					clusters := make([]weightedCluster, len(wcs.Clusters))
					for i, wc := range wcs.GetClusters() {
						clusters[i] = weightedCluster{
							name:   wc.GetName(),
							weight: wc.GetWeight().GetValue(),
						}
					}
				}
				a.Route.GetWeightedClusters()
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
		res := unmarshalRouteConfig(rcfg)
		ret[rcfg.GetName()] = res
		fmt.Println("[xds client] route name:", res.Name())
	}

	return ret
}
