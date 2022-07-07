package manager

import (
	"github.com/cloudwego/kitex/internal/test"
	envoy_config_core_v3 "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3discovery "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/service"
	"github.com/cloudwego/kitex/pkg/xds/internal/manager/mock"
	"github.com/cloudwego/kitex/pkg/xds/internal/testutil/resource"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"reflect"
	"testing"
)

type mockStreamClient struct {
	StreamClient
}

func (sc *mockStreamClient) Send(*v3discovery.DiscoveryRequest) error {
	return nil
}

func (sc *mockStreamClient) Recv() (*v3discovery.DiscoveryResponse, error) {
	select {}
}

type mockResourceUpdater struct {
	ResourceUpdater
}
func (m *mockResourceUpdater) UpdateListenerResource(map[string]*xdsresource.ListenerResource) {}
func (m *mockResourceUpdater) UpdateRouteConfigResource(map[string]*xdsresource.RouteConfigResource) {}
func (m *mockResourceUpdater) UpdateClusterResource(map[string]*xdsresource.ClusterResource)     {}
func (m *mockResourceUpdater) UpdateEndpointsResource(map[string]*xdsresource.EndpointsResource) {}

func Test_newXdsClient(t *testing.T) {
	address := ":8888"
	svr := mock.StartXDSServer(address)
	defer func() {
		if svr != nil {
			_ = svr.Stop()
		}
	}()
	_, err := newXdsClient(
		&BootstrapConfig{
			Node:      &envoy_config_core_v3.Node{},
			XdsSvrCfg: &XDSServerConfig{ServerAddress: address},
		},
		nil,
	)
	test.Assert(t, err == nil)
}

func Test_xdsClient_prepareRequest(t *testing.T) {
	c := &xdsClient{
		config: XdsBootstrapConfig,
		subscribedResource: map[xdsresource.ResourceType]map[string]bool{
			xdsresource.RouteConfigType: {},
			xdsresource.ClusterType:     {"cluster1": true},
		},
	}
	type args struct {
		resourceType xdsresource.ResourceType
		ack          bool
		errMsg       string
	}
	tests := []struct {
		name string
		args args
		want *v3discovery.DiscoveryRequest
	}{
		{
			name: "unknown resource type",
			args: args{
				resourceType: -1,
				ack:          false,
				errMsg:       "",
			},
			want: nil,
		},
		{
			name: "resource type not in the subscribed list",
			args: args{
				resourceType: xdsresource.ListenerType,
				ack:          false,
				errMsg:       "",
			},
			want: nil,
		},
		{
			name: "resource list is empty",
			args: args{
				resourceType: xdsresource.RouteConfigType,
				ack:          false,
				errMsg:       "",
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.prepareRequest(tt.args.resourceType, tt.args.ack, tt.args.errMsg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("prepareRequest() = %v, want %v", got, tt.want)
			}
		})
	}

	var req *v3discovery.DiscoveryRequest
	// ack = false
	req = c.prepareRequest(xdsresource.ClusterType, false, "")
	test.Assert(t, req != nil)
	test.Assert(t, req.ResourceNames[0] == "cluster1")
	test.Assert(t, req.ErrorDetail == nil)
	// ack = true, errMsg = ""
	req = c.prepareRequest(xdsresource.ClusterType, true, "")
	test.Assert(t, req != nil)
	test.Assert(t, req.ErrorDetail == nil)
	// ack = true, errMsg = "error"
	req = c.prepareRequest(xdsresource.ClusterType, true, "error")
	test.Assert(t, req != nil)
	test.Assert(t, req.ErrorDetail.Message == "error")
}

func Test_xdsClient_handleResponse(t *testing.T) {
	c := &xdsClient{
		config: &BootstrapConfig{
			Node:      NodeProto,
			XdsSvrCfg: XdsServerConfig,
		},
		streamClient:       &mockStreamClient{},
		subscribedResource: make(map[xdsresource.ResourceType]map[string]bool),
		versionMap:         make(map[xdsresource.ResourceType]string),
		nonceMap:           make(map[xdsresource.ResourceType]string),
		resourceUpdater:    &mockResourceUpdater{},
		refreshInterval:    defaultRefreshInterval,
	}
	// handle the response that includes resources that are not in the subscribed list
	err := c.handleResponse(resource.LdsResp1)
	test.Assert(t, err == nil)
	test.Assert(t, c.versionMap[xdsresource.ListenerType] == resource.ListenerVersion)
	test.Assert(t, c.nonceMap[xdsresource.ListenerType] == resource.ListenerNonce)
	test.Assert(t, c.subscribedResource[xdsresource.RouteConfigType][resource.RouteConfigName] == false)
	c.subscribedResource[xdsresource.ListenerType] = map[string]bool{
		resource.ListenerName: true,
	}
	err = c.handleResponse(resource.LdsResp1)
	test.Assert(t, c.subscribedResource[xdsresource.RouteConfigType][resource.RouteConfigName] == true)
}
