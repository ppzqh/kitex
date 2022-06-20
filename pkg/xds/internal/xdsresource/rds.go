package xdsresource

import (
	v3routepb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3matcherpb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"time"
)

type RouteConfigResource struct {
	Name         string
	VirtualHosts []*VirtualHost
}

type VirtualHost struct {
	Name   string
	Routes []Route
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
	Name   string
	Weight uint32
}

type Route struct {
	Match            RouteMatch
	Cluster          string
	WeightedClusters []weightedCluster
	Timeout          time.Duration
}

type RouteMatch struct {
	Prefix        string
	Path          string
	CaseSensitive bool
	Headers       []RouteMatchHeader
}

type RouteMatchHeader struct {
	Name      string
	Value     string
	Present   bool
	MatchType HeaderMatchType
}

func (rm *RouteMatch) Matched(path string, tags map[string]string) bool {
	for _, h := range rm.Headers {
		switch h.MatchType {
		case Exact:
			if v, ok := tags[h.Name]; !ok || v != h.Value {
				return false
			}
		}
	}
	return true
}

func unmarshalRouteConfig(routeConfig *v3routepb.RouteConfiguration) *RouteConfigResource {
	vhs := routeConfig.GetVirtualHosts()
	virtualHosts := make([]*VirtualHost, len(vhs))
	for i := 0; i < len(vhs); i++ {
		rs := vhs[i].GetRoutes()
		routes := make([]Route, len(rs))
		for j := 0; j < len(rs); j++ {
			route := Route{}
			// match
			// 1. different Path
			// 2. store all Headers
			routeMatch := RouteMatch{}
			match := rs[j].GetMatch()
			pathSpecifier := match.GetPathSpecifier()
			switch p := pathSpecifier.(type) {
			case *v3routepb.RouteMatch_Prefix:
				routeMatch.Prefix = p.Prefix
			case *v3routepb.RouteMatch_Path:
				routeMatch.Prefix = p.Path
			}
			// route header
			hs := match.GetHeaders()
			headers := make([]RouteMatchHeader, len(hs))
			for k := 0; k < len(hs); k++ {
				var header RouteMatchHeader
				header.Name = hs[k].GetName()

				headerSpecifier := hs[k].GetHeaderMatchSpecifier()
				switch ht := headerSpecifier.(type) {
				// new string match
				case *v3routepb.HeaderMatcher_StringMatch:
					switch pattern := ht.StringMatch.GetMatchPattern().(type) {
					case *v3matcherpb.StringMatcher_Exact:
						header.Value = pattern.Exact
						header.MatchType = Exact
					case *v3matcherpb.StringMatcher_Prefix:
						header.Value = pattern.Prefix
						header.MatchType = Prefix
					case *v3matcherpb.StringMatcher_Suffix:
						header.Value = pattern.Suffix
						header.MatchType = Suffix
					case *v3matcherpb.StringMatcher_Contains:
						header.Value = pattern.Contains
						header.MatchType = Contains
						// TODO:
						// case *v3matcherpb.StringMatcher_SafeRegex:
					}
				// old match
				case *v3routepb.HeaderMatcher_ExactMatch:
					header.Value = ht.ExactMatch
					header.MatchType = Exact
				case *v3routepb.HeaderMatcher_PrefixMatch:
					header.Value = ht.PrefixMatch
					header.MatchType = Prefix
				case *v3routepb.HeaderMatcher_SuffixMatch:
					header.Value = ht.SuffixMatch
					header.MatchType = Suffix
				case *v3routepb.HeaderMatcher_ContainsMatch:
					header.Value = ht.ContainsMatch
					header.MatchType = Contains
				case *v3routepb.HeaderMatcher_PresentMatch:
					header.MatchType = Present
					header.Present = ht.PresentMatch
				}
				headers[k] = header
			}
			// action
			action := rs[j].GetAction()
			switch a := action.(type) {
			case *v3routepb.Route_Route:
				switch cs := a.Route.GetClusterSpecifier().(type) {
				case *v3routepb.RouteAction_Cluster:
					route.Cluster = cs.Cluster
					route.WeightedClusters = []weightedCluster{
						{Name: cs.Cluster, Weight: 1},
					}
				case *v3routepb.RouteAction_WeightedClusters:
					wcs := cs.WeightedClusters
					clusters := make([]weightedCluster, len(wcs.Clusters))
					for i, wc := range wcs.GetClusters() {
						clusters[i] = weightedCluster{
							Name:   wc.GetName(),
							Weight: wc.GetWeight().GetValue(),
						}
					}
				}
				a.Route.GetWeightedClusters()
				route.Timeout = a.Route.GetTimeout().AsDuration()
			}
			routes[j] = route
		}
		virtualHost := &VirtualHost{
			Name:   vhs[i].GetName(),
			Routes: routes,
		}
		virtualHosts[i] = virtualHost
	}

	return &RouteConfigResource{
		Name:         routeConfig.GetName(),
		VirtualHosts: virtualHosts,
	}
}

func UnmarshalRDS(rawResources []*any.Any) map[string]*RouteConfigResource {
	ret := make(map[string]*RouteConfigResource, len(rawResources))

	for _, r := range rawResources {
		rcfg := &v3routepb.RouteConfiguration{}
		if err := proto.Unmarshal(r.GetValue(), rcfg); err != nil {
			panic("[xds] RDS Unmarshal error")
		}
		res := unmarshalRouteConfig(rcfg)
		ret[rcfg.GetName()] = res
	}

	return ret
}
