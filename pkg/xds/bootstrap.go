package xds

import envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

type BootstrapConfig struct {
	xdsSvrAddr string
	node       *envoy_config_core_v3.Node
}

func newBootstrapConfig() *BootstrapConfig {
	addr := ":8080"
	n := &envoy_config_core_v3.Node{
		Id: "mesh", // TODO: load id from config file
	}
	cfg := &BootstrapConfig{
		xdsSvrAddr: addr,
		node:       n,
	}
	return cfg
}
