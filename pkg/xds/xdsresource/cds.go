package xdsresource

import (
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
	Name          string
	DiscoveryType  ClusterDiscoveryType
	LbPolicy       ClusterLbPolicy
	CircuitBreaker CircuitBreakerConfig

	EndpointName    string
	InlineEndpoints *EndpointsResource
}

func (r *ClusterResource) ClusterType() string {
	return strconv.Itoa(int(r.DiscoveryType))
}

func (r *ClusterResource) GetLbPolicy() string {
	switch r.LbPolicy {
	case ClusterLbRoundRobin:
		return "roundrobin"
	case ClusterLbRingHash:
		return "ringhash"
	}
	return ""
}

func (r *ClusterResource) InlineEDS() *EndpointsResource {
	return r.InlineEndpoints
}

func UnmarshalCDS(rawResources []*any.Any) map[string]*ClusterResource {
	ret := make(map[string]*ClusterResource, len(rawResources))
	for _, r := range rawResources {
		c := &v3clusterpb.Cluster{}
		if err := proto.Unmarshal(r.GetValue(), c); err != nil {
			panic("Unmarshal error")
		}
		// TODO: circult breaker and outlier detection
		res := &ClusterResource{
			Name:            c.GetName(),
			DiscoveryType:   convertDiscoveryType(c.GetType()),
			LbPolicy:        convertLbPolicy(c.GetLbPolicy()),
			EndpointName:    c.GetEdsClusterConfig().GetServiceName(),
			InlineEndpoints: unmarshalClusterLoadAssignment(c.GetLoadAssignment()),
		}
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
