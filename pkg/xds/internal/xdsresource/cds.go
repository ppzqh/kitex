package xdsresource

import (
	"fmt"
	v3clusterpb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
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

type ClusterResource struct {
	DiscoveryType   ClusterDiscoveryType
	LbPolicy        ClusterLbPolicy
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

func UnmarshalCDS(rawResources []*any.Any) (map[string]*ClusterResource, error) {
	ret := make(map[string]*ClusterResource, len(rawResources))
	for _, r := range rawResources {
		c := &v3clusterpb.Cluster{}
		if err := proto.Unmarshal(r.GetValue(), c); err != nil {
			return nil, fmt.Errorf("unmarshal cluster failed: %s", err)
		}
		// inline eds
		inlineEndpoints, err := unmarshalClusterLoadAssignment(c.GetLoadAssignment())
		if err != nil {
			return nil, err
		}
		// TODO: circult breaker and outlier detection
		res := &ClusterResource{
			DiscoveryType:   convertDiscoveryType(c.GetType()),
			LbPolicy:        convertLbPolicy(c.GetLbPolicy()),
			EndpointName:    c.GetEdsClusterConfig().GetServiceName(),
			InlineEndpoints: inlineEndpoints,
		}
		ret[c.GetName()] = res
	}

	return ret, nil
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
