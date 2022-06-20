package xdssuite

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/loadbalance"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
)

// Loadbalancer generates pickers for the given service discovery result.
//type Loadbalancer interface {
//	GetPicker(discovery.Result) Picker
//	Name() string // unique key
//}

type XDSLoadBalancer struct {
	//map[string]loadbalance.Picker
}

type xdsPicker struct {
	instances discovery.Result
}

func (p *xdsPicker) Next(ctx context.Context, request interface{}) discovery.Instance {
	return p.instances.Instances[0]
}

func (lb *XDSLoadBalancer) GetPicker(e discovery.Result) loadbalance.Picker {
	fmt.Println(e.Instances[0].Address())
	// 1. Get Cluster from xdsResourceManager, which includes lb policy
	// 2. init picker based on the policy
	name := "outbound|9080|v2|reviews.default.svc.cluster.local"
	m, err := getXdsResourceManager()
	if err != nil {
		return nil
	}
	res, err := m.Get(xdsresource.ClusterType, name)
	if err != nil {
		return nil
	}
	cds, ok := res.(xdsresource.ClusterResource)
	if !ok {
		panic("[xds loadbalancer] CDS cast failed")
	}
	fmt.Println(cds.LbPolicy)

	return &xdsPicker{instances: e}
}

func (lb *XDSLoadBalancer) Name() string {
	return "xdsLoadbalancer"
}