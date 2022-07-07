package mock

import (
	"fmt"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3/aggregateddiscoveryservice"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/service"
	v3discovery "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/service"
	"github.com/cloudwego/kitex/pkg/xds/internal/testutil/resource"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"github.com/cloudwego/kitex/server"
	"net"
	"time"
)

type Server struct {
	xdsSvr *testXDSServer
}

func StartXDSServer(address string) server.Server {
	addr, _ := net.ResolveTCPAddr("tcp", address)
	svr := aggregateddiscoveryservice.NewServer(
		&testXDSServer{
			respCh: make(chan *v3discovery.DiscoveryResponse),
		},
		server.WithServiceAddr(addr),
	)
	go func() {
		_ = svr.Run()
	}()
	time.Sleep(time.Millisecond * 100)
	return svr
}

type testXDSServer struct {
	//reqCh chan interface{}
	respCh chan *v3discovery.DiscoveryResponse
}

func (svr *testXDSServer) StreamAggregatedResources(stream service.AggregatedDiscoveryService_StreamAggregatedResourcesServer) (err error) {
	errCh := make(chan error, 2)
	// receive the request
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			svr.handleRequest(msg)
		}
	}()

	// send the response
	go func() {
		for {
			select {
			case resp := <-svr.respCh:
				err := stream.Send(resp)
				if err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	}
}

func (svr *testXDSServer) handleRequest(msg interface{}) {
	req, ok := msg.(*v3discovery.DiscoveryRequest)
	if !ok {
		// ignore msgs other than DiscoveryRequest
		return
	}
	switch req.TypeUrl {
	case xdsresource.ListenerTypeUrl:
		if req.VersionInfo == "" {
			svr.respCh <- resource.LdsResp1
		}
	case xdsresource.RouteTypeUrl:
		if req.VersionInfo == "" {
			svr.respCh <- resource.RdsResp1
		}
	case xdsresource.ClusterTypeUrl:
		if req.VersionInfo == "" {
			svr.respCh <- resource.CdsResp1
		}
	case xdsresource.EndpointTypeUrl:
		if req.VersionInfo == "" {
			svr.respCh <- resource.EdsResp1
		}
	}
}

func (svr *testXDSServer) DeltaAggregatedResources(stream service.AggregatedDiscoveryService_DeltaAggregatedResourcesServer) (err error) {
	return fmt.Errorf("DeltaAggregatedResources has not been implemented")
}
