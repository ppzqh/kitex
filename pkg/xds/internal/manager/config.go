package manager

import "time"

const (
	defaultControlPlaneAddress = "istiod.istio-system.svc.cluster.local:8080"
	defaultDNSProxyAddress     = "127.0.0.1:15053"
	defaultDNSTimeout          = time.Millisecond * 100
	defaultRefreshInterval     = 5 * time.Second
	defaultXDSFetchTimeout     = time.Second
	defaultCacheExpireTime     = time.Second * 30
	defaultDumpPath            = "/tmp/xds_resource_manager.json"
)
