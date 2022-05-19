package xds

import (
	"fmt"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/pkg/serviceinfo"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
	"time"
)

type routeConfig struct {
	// clusterName
	// timeout
	// 现阶段无法动态调整的 config:
	// lb（可能可以自定义一个 xdsLoadBalancer，可以执行多种lb策略）
	// retry policy
	// rate limit
}

type xdsTimeoutProvider struct {
	serviceInfo serviceinfo.ServiceInfo
}

type timeoutConfig struct {
	rpcTimeout        time.Duration
	connectTimeout    time.Duration
	readWriteTimeout  time.Duration
}

func (c *timeoutConfig) RPCTimeout() time.Duration {
	return c.rpcTimeout
}

func (c *timeoutConfig) ConnectTimeout() time.Duration {
	return c.connectTimeout
}

func (c *timeoutConfig) ReadWriteTimeout() time.Duration {
	return c.readWriteTimeout
}


// implement rpcinfo.TimeoutProvider
func (r *xdsTimeoutProvider) Timeouts(ri rpcinfo.RPCInfo) rpcinfo.Timeouts {
	cfg := ri.Config()

	// timeout config in RouteConfig
	// 1. Get RouteConfig from xdsResourceManager
	// 2. match the route with ri (rpcinfo) and get timeout config
	// 3. set timeout
	name := ""
	res := GetXdsResourceManager().Get(xdsresource.RouteConfigType, name)
	_, ok := res.(xdsresource.RouteConfigResource)
	if !ok {
		panic("wrong route")
	}

	config := &timeoutConfig{
		rpcTimeout: cfg.RPCTimeout(),
		connectTimeout: cfg.ConnectTimeout(),
		readWriteTimeout: cfg.ReadWriteTimeout(),
	}

	return config
}

func XDSRouterTest() {
	tags := make(map[string]string)
	key, value := "end-user", "jason"
	tags[key] = value

	resourceName := "outbound|9080||reviews.default.svc.cluster.local"
	res := GetXdsResourceManager().Get(xdsresource.RouteConfigType, resourceName)
	rds, ok := res.(xdsresource.RouteConfigResource)
	if !ok {
		panic("wrong route")
	}
	// match the first one
	for _, vh := range rds.VirtualHosts() {

		for _, r := range vh.Routes() {
			match := r.Match()
			if match.Matched(tags) {
				fmt.Printf("[!!!!XDS TEST!!!!]: route matched \n")
				break
			}
		}
	}
}

