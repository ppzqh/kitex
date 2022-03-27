package xdsresource

import (
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
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

type ClusterResource struct {
	name            string
	discoveryType   ClusterDiscoveryType
	lbPolicy        ClusterLbPolicy
	//inlineEndpoints *EndpointsResource
}

func UnmarshalCDS(rawResources []*any.Any) map[string]ClusterResource {
	ret := make(map[string]ClusterResource, len(rawResources))
	for _, r := range rawResources {
		c := &v3clusterpb.Cluster{}
		if err := proto.Unmarshal(r.GetValue(), c); err != nil {
			panic("Unmarshal error")
		}
		// TODO:
		//cb := c.GetCircuitBreakers()
		//connTimeout := c.GetConnectTimeout()
		res := ClusterResource{
			name:            c.GetName(),
			discoveryType:   convertDiscoveryType(c.GetType()),
			lbPolicy:        convertLbPolicy(c.GetLbPolicy()),
			// TODO: cds may include endpoint info
			//inlineEndpoints: unmarshalClusterLoadAssignment(c.GetLoadAssignment()),
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
