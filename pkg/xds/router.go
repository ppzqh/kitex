package xds

import (
	"fmt"
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

func (r *XdsRouter) Route(info rpcinfo.RPCInfo) RouteConfig {
	listenerName := info.To().ServiceName()
	lds := GetXdsResourceManager().Get(xdsresource.ListenerType, listenerName)
	listener, ok := lds.(xdsresource.ListenerResource)
	if !ok {
		panic("wrong listener")
	}

	routeConfigName := listener.RouteConfigName()
	fmt.Println("route config name:", routeConfigName)
	rds := GetXdsResourceManager().Get(xdsresource.RouteConfigType, routeConfigName)
	routeConfig, ok := rds.(xdsresource.RouteConfigResource)
	if !ok {
		panic("wrong route config")
	}

	// match the first one
	// TODO: only test
	tags := make(map[string]string)
	//key, value := "end-user", "jason"
	//tags[key] = value
	path := info.From().Method()

	var matchedRoute xdsresource.Route
	matched := false
	for _, vh := range routeConfig.VirtualHosts() {
		// TODO: match the name
		if !strings.Contains(vh.Name(), info.To().ServiceName()) {
			continue
		}
		for _, r := range vh.Routes() {
			match := r.Match()
			if match.Matched(path, tags) {
				fmt.Printf("[xds router] route matched \n")
				fmt.Println("[xds router] cluster:", r.Cluster())
				matched = true
				matchedRoute = r
				break
			}
		}
	}

	if !matched {
		panic("[xds router] route failed")
	}
	// select cluster
	cluster := selectCluster(matchedRoute)
	return RouteConfig{
		RPCTimeout:  matchedRoute.Timeout(),
		Destination: cluster,
	}
}

func selectCluster(route xdsresource.Route) string {
	// handle weighted cluster
	wcs := route.WeightedClusters()
	var cluster string
	if len(wcs) == 1 {
		cluster = wcs[0].Name()
	} else {
		defaultTotalWeight := 100
		currWeight := int32(0)
		targetWeight := rand.Int31n(int32(defaultTotalWeight))
		for _, wc := range wcs {
			currWeight += int32(wc.Weight())
			if currWeight >= targetWeight {
				cluster = wc.Name()
			}
		}
	}
	return cluster
}
