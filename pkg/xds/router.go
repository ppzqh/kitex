package xds

import (
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
	"math/rand"
	"strings"
	"time"
)

const (
	RouterDestinationKey = "route_destination"
)

type RouteConfig struct {
	RPCTimeout  time.Duration
	Destination string
	// TODO: retry policy also in RDS
}

type XdsRouter struct{}

func (r *XdsRouter) Route(info rpcinfo.RPCInfo) (*RouteConfig, error) {
	listenerName := info.To().ServiceName()
	lds, err := GetXdsResourceManager().Get(xdsresource.ListenerType, listenerName)
	if err != nil {
		panic("Get listener failed")
	}
	listener, ok := lds.(*xdsresource.ListenerResource)
	if !ok {
		panic("wrong listener")
	}

	routeConfigName := listener.RouteConfigName
	rds, err := GetXdsResourceManager().Get(xdsresource.RouteConfigType, routeConfigName)
	if err != nil {
		panic("Get route failed")
	}
	routeConfig, ok := rds.(*xdsresource.RouteConfigResource)
	if !ok {
		panic("wrong route config")
	}

	// match the first one
	// TODO: only test
	tags := make(map[string]string)
	path := info.From().Method()

	var matchedRoute xdsresource.Route
	matched := false
	for _, vh := range routeConfig.VirtualHosts {
		// TODO: match the name
		if !strings.Contains(vh.Name, info.To().ServiceName()) {
			continue
		}
		for _, r := range vh.Routes {
			match := r.Match
			if match.Matched(path, tags) {
				matched = true
				matchedRoute = r
				break
			}
		}
	}

	if !matched {
		return nil, kerrors.ErrRoute
	}
	// select cluster
	cluster := selectCluster(matchedRoute)
	return &RouteConfig{
		RPCTimeout:  matchedRoute.Timeout,
		Destination: cluster,
	}, nil
}

func selectCluster(route xdsresource.Route) string {
	// handle weighted cluster
	wcs := route.WeightedClusters
	var cluster string
	if len(wcs) == 1 {
		cluster = wcs[0].Name
	} else {
		defaultTotalWeight := 100
		currWeight := int32(0)
		targetWeight := rand.Int31n(int32(defaultTotalWeight))
		for _, wc := range wcs {
			currWeight += int32(wc.Weight)
			if currWeight >= targetWeight {
				cluster = wc.Name
			}
		}
	}
	return cluster
}
