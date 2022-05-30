package xdsresource

import (
	"fmt"
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"strconv"
)

type ClusterDiscoveryType int

const (
	ClusterDiscoveryTypeEDS = iota
	ClusterDiscoveryTypeLogicalDNS
	ClusterDiscoveryTypeStatic
)

type ClusterLbPolicy int

const (
	ClusterLbRoundRobin = iota
	ClusterLbRingHash
)

type CircuitBreakerConfig struct {
}

type ClusterResource struct {
	name           string
	discoveryType  ClusterDiscoveryType
	lbPolicy       ClusterLbPolicy
	circuitBreaker CircuitBreakerConfig

	endpointName    string
	inlineEndpoints EndpointsResource
}

func (r *ClusterResource) Name() string {
	return r.name
}

func (r *ClusterResource) ClusterType() string {
	return strconv.Itoa(int(r.discoveryType))
}

func (r *ClusterResource) LbPolicy() string {
	switch r.lbPolicy {
	case ClusterLbRoundRobin:
		return "roundrobin"
	case ClusterLbRingHash:
		return "ringhash"
	}
	return ""
}

func (r *ClusterResource) EndpointName() string {
	return r.endpointName
}

func (r *ClusterResource) InlineEDS() EndpointsResource {
	return r.inlineEndpoints
}

func UnmarshalCDS(rawResources []*any.Any) map[string]ClusterResource {
	ret := make(map[string]ClusterResource, len(rawResources))
	fmt.Println("[xds] cluster resource, length:", len(rawResources))
	for _, r := range rawResources {
		c := &v3clusterpb.Cluster{}
		if err := proto.Unmarshal(r.GetValue(), c); err != nil {
			panic("Unmarshal error")
		}
		// TODO:
		// circult breaker and outlier detection
		res := ClusterResource{
			name:            c.GetName(),
			discoveryType:   convertDiscoveryType(c.GetType()),
			lbPolicy:        convertLbPolicy(c.GetLbPolicy()),
			endpointName:    c.GetEdsClusterConfig().GetServiceName(),
			inlineEndpoints: unmarshalClusterLoadAssignment(c.GetLoadAssignment()),
		}
		fmt.Println("[xds] cluster resource name:", c.GetName())
		//fmt.Println("[xds] cluster discovery type:", c.GetType())
		//fmt.Println("[xds] cluster eds name:", c.GetEdsClusterConfig().GetServiceName())
		ret[c.GetName()] = res
	}

	return ret
}

func convertDiscoveryType(t v3clusterpb.Cluster_DiscoveryType) ClusterDiscoveryType {
	switch t {
	case v3clusterpb.Cluster_EDS:
		return ClusterDiscoveryTypeEDS
	case v3clusterpb.Cluster_LOGICAL_DNS:
		return ClusterDiscoveryTypeLogicalDNS
	case v3clusterpb.Cluster_STATIC:
		return ClusterDiscoveryTypeStatic
	}

	return ClusterDiscoveryTypeEDS
}

func convertLbPolicy(lb v3clusterpb.Cluster_LbPolicy) ClusterLbPolicy {
	switch lb {
	case v3clusterpb.Cluster_ROUND_ROBIN:
		return ClusterLbRoundRobin
	case v3clusterpb.Cluster_RING_HASH:
		return ClusterLbRingHash
	}

	return ClusterLbRoundRobin
}
