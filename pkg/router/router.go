package router

import (
	"github.com/cloudwego/kitex/pkg/rpcinfo"
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

//func (r *RouteConfig) Timeout() time.Duration {
//	return r.rpcTimeout
//}
//
//func (r *RouteConfig) Destination() string {
//	return r.destination
//}

type Router interface {
	Route(info rpcinfo.RPCInfo) RouteConfig
}
