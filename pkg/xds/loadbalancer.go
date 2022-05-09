package xds

import (
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/loadbalance"
)

// Loadbalancer generates pickers for the given service discovery result.
//type Loadbalancer interface {
//	GetPicker(discovery.Result) Picker
//	Name() string // unique key
//}

type XdsLoadbalancer struct {
}

func (lb *XdsLoadbalancer) GetPicker(discovery.Result) loadbalance.Picker {
	// 1. Get Cluster from xdsResourceManager, which includes lb policy
	// 2. init picker based on the policy

	return nil
}

func (lb *XdsLoadbalancer) Name() string {
	return "xdsLoadbalancer"
}