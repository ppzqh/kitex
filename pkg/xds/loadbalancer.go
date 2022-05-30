package xds

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/loadbalance"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
)

// Loadbalancer generates pickers for the given service discovery result.
//type Loadbalancer interface {
//	GetPicker(discovery.Result) Picker
//	Name() string // unique key
//}

type XdsLoadbalancer struct {
}

type xdsPicker struct {
	instances discovery.Result
}

func (p *xdsPicker) Next(ctx context.Context, request interface{}) discovery.Instance {
	return p.instances.Instances[0]
}

func (lb *XdsLoadbalancer) GetPicker(e discovery.Result) loadbalance.Picker {
	fmt.Println(e.Instances[0].Address())
	// 1. Get Cluster from xdsResourceManager, which includes lb policy
	// 2. init picker based on the policy
	m := GetXdsResourceManager()
	name := "outbound|9080|v2|reviews.default.svc.cluster.local"
	res := m.Get(xdsresource.ClusterType, name)
	if res == nil {
		panic("[xds loadbalancer] Get resource failed")
	}
	cds, ok := res.(xdsresource.ClusterResource)
	if !ok {
		panic("[xds loadbalancer] CDS cast failed")
	}
	fmt.Println(cds.LbPolicy())

	return &xdsPicker{instances: e}
}

func (lb *XdsLoadbalancer) Name() string {
	return "xdsLoadbalancer"
}