package manager

import (
	"github.com/cloudwego/kitex/internal/test"
	envoy_config_core_v3 "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"

	//envoy_config_core_v3 "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/cloudwego/kitex/pkg/xds/internal/mock"
	"testing"
)

func Test_newXdsClient(t *testing.T) {
	address := ":8888"
	mock.StartServer(address)
	cli, err := newXdsClient(
		&BootstrapConfig{
			node: &envoy_config_core_v3.Node{},
			xdsSvrCfg: &XDSServerConfig{serverAddress: address},
		},
		nil,
	)
	test.Assert(t, err == nil)
	cli.Subscribe(xdsresource.ListenerType, "*")
}