package xdsresource

import (
	"encoding/json"
	"fmt"
	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/utils"
	v3endpointpb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"net"
	"strconv"
)

type Endpoint struct {
	Addr   net.Addr
	Weight uint32
	Meta   map[string]string // Tag in discovery.instance.tag
}

func (e *Endpoint) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Addr   string            `json:"addr"`
		Weight uint32               `json:"weight"`
		Meta   map[string]string `json:"meta,omitempty"`
	}{
		Addr:   e.Addr.String(),
		Weight: e.Weight,
		Meta:   e.Meta,
	})
}

func (e *Endpoint) Tag(key string) (value string, exist bool) {
	value, exist = e.Meta[key]
	return
}

type Locality struct {
	Endpoints []*Endpoint
	Weight    uint32
}

type EndpointsResource struct {
	Localities []*Locality
}

func parseClusterLoadAssignment(cla *v3endpointpb.ClusterLoadAssignment) (*EndpointsResource, error) {
	if cla == nil || len(cla.GetEndpoints()) == 0 {
		return nil, nil
	}
	localities := make([]*Locality, len(cla.GetEndpoints()))
	for idx1, leps := range cla.GetEndpoints() {
		eps := make([]*Endpoint, len(leps.GetLbEndpoints()))
		for idx2, ep := range leps.GetLbEndpoints() {
			addr := ep.GetEndpoint().GetAddress().GetSocketAddress()
			weight := ep.GetLoadBalancingWeight()
			eps[idx2] = &Endpoint{
				Addr: utils.NewNetAddr("tcp",
					net.JoinHostPort(addr.GetAddress(), strconv.Itoa(int(addr.GetPortValue())))),
				Weight: weight.GetValue(),
				Meta:   nil,
			}
			// TODO: add healthcheck
		}
		localities[idx1] = &Locality{
			Endpoints: eps,
		}
	}
	return &EndpointsResource{
		Localities: localities,
	}, nil
}

func UnmarshalEDS(rawResources []*any.Any) (map[string]*EndpointsResource, error) {
	if rawResources == nil {
		return nil, fmt.Errorf("empty endpoint resource")
	}

	ret := make(map[string]*EndpointsResource, len(rawResources))
	for _, r := range rawResources {
		if r.GetTypeUrl() != EndpointTypeUrl {
			klog.Errorf("invalid endpoint resource type: %s", r.GetTypeUrl())
			continue
		}
		cla := &v3endpointpb.ClusterLoadAssignment{}
		if err := proto.Unmarshal(r.GetValue(), cla); err != nil {
			klog.Errorf("unmarshal ClusterLoadAssignment failed, error=%s\n", err)
			continue
		}
		endpoints, err := parseClusterLoadAssignment(cla)
		if err != nil {
			klog.Errorf("parse ClusterLoadAssignment %s failed: %s", cla.GetClusterName(), err)
			continue
		}
		ret[cla.GetClusterName()] = endpoints
	}
	return ret, nil
}
