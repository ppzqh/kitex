package xdsresource

import (
	"encoding/json"
	"github.com/cloudwego/kitex/pkg/utils"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"net"
	"strconv"
)

type Endpoint struct {
	Addr   net.Addr
	Weight int
	Meta   map[string]string // Tag in discovery.instance.tag
}

func (e *Endpoint) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Addr   string            `json:"addr"`
		Weight int               `json:"weight"`
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
	Weight    int
}

type EndpointsResource struct {
	Name       string
	Localities []*Locality
}

func unmarshalClusterLoadAssignment(cla *v3endpointpb.ClusterLoadAssignment) *EndpointsResource {
	localities := make([]*Locality, len(cla.GetEndpoints()))
	for idx1, leps := range cla.GetEndpoints() {
		eps := make([]*Endpoint, len(leps.GetLbEndpoints()))
		for idx2, ep := range leps.GetLbEndpoints() {
			addr := ep.GetEndpoint().GetAddress().GetSocketAddress()
			weight := ep.GetLoadBalancingWeight()
			eps[idx2] = &Endpoint{
				Addr: utils.NewNetAddr("tcp",
					net.JoinHostPort(addr.GetAddress(), strconv.Itoa(int(addr.GetPortValue())))),
				Weight: int(weight.GetValue()),
				Meta:   nil,
			}
			// TODO: add healthcheck
		}
		localities[idx1] = &Locality{
			Endpoints: eps,
		}
	}
	return &EndpointsResource{
		Name:       cla.GetClusterName(),
		Localities: localities,
	}
}

func UnmarshalEDS(rawResources []*any.Any) map[string]*EndpointsResource {
	ret := make(map[string]*EndpointsResource, len(rawResources))

	for _, r := range rawResources {
		cla := &v3endpointpb.ClusterLoadAssignment{}
		if err := proto.Unmarshal(r.GetValue(), cla); err != nil {
			panic("Unmarshal error")
		}
		cluster := unmarshalClusterLoadAssignment(cla)
		ret[cla.GetClusterName()] = cluster
	}
	return ret
}
