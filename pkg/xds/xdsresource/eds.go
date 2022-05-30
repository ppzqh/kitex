package xdsresource

import (
	"fmt"
	"github.com/cloudwego/kitex/pkg/utils"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/proto"
	"net"
	"strconv"
)

// Endpoint implements the Instance interface in pkg/discovery
type Endpoint struct {
	addr   net.Addr
	weight int
	meta   map[string]string // Tag in discovery.instance.tag
}

func (e *Endpoint) Address() net.Addr {
	return e.addr
}

func (e *Endpoint) Weight() int {
	return e.weight
}

func (e *Endpoint) Tag(key string) (value string, exist bool) {
	value, exist = e.meta[key]
	return
}

func (e *Endpoint) Meta() map[string]string {
	return e.meta
}

type Locality struct {
	Endpoints []*Endpoint
	weight    int
}

type EndpointsResource struct {
	Name       string
	Localities []*Locality
}

func unmarshalClusterLoadAssignment(cla *v3endpointpb.ClusterLoadAssignment) EndpointsResource {
	localities := make([]*Locality, len(cla.GetEndpoints()))
	for idx1, leps := range cla.GetEndpoints() {
		eps := make([]*Endpoint, len(leps.GetLbEndpoints()))
		for idx2, ep := range leps.GetLbEndpoints() {
			addr := ep.GetEndpoint().GetAddress().GetSocketAddress()
			weight := ep.GetLoadBalancingWeight()
			eps[idx2] = &Endpoint{
				addr: utils.NewNetAddr("tcp",
					net.JoinHostPort(addr.GetAddress(), strconv.Itoa(int(addr.GetPortValue())))),
				weight: int(weight.GetValue()),
				meta:   nil,
			}
			// TODO: add healthcheck
			// healthcheck := ep.GetEndpoint().HealthCheckConfig
			//fmt.Println("[xds] endpoint: ", eps[idx2].Address())
		}
		localities[idx1] = &Locality{
			Endpoints: eps,
		}
	}
	return EndpointsResource{
		Name:       cla.GetClusterName(),
		Localities: localities,
	}
}

func UnmarshalEDS(rawResources []*any.Any) map[string]EndpointsResource {
	ret := make(map[string]EndpointsResource, len(rawResources))

	for _, r := range rawResources {
		cla := &v3endpointpb.ClusterLoadAssignment{}
		if err := proto.Unmarshal(r.GetValue(), cla); err != nil {
			panic("Unmarshal error")
		}
		cluster := unmarshalClusterLoadAssignment(cla)
		fmt.Println("[xds client] edsCluster's name:", cla.GetClusterName())
		ret[cla.GetClusterName()] = cluster
	}
	return ret
}
