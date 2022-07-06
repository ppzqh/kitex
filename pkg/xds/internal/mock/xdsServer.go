package mock

import (
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3/aggregateddiscoveryservice"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/service"
	"github.com/cloudwego/kitex/server"
	"net"
)

type Server struct {
	xdsSvr *xdsServer
}

func StartServer(address string) {
	errCh := make(chan error)
	addr, _ := net.ResolveTCPAddr("tcp", address)
	svr := aggregateddiscoveryservice.NewServer(&xdsServer{},
		server.WithServiceAddr(addr),
	)
	go func() {
		errCh <- svr.Run()
	}()
	//select {
	//case err := <-errCh:
	//	fmt.Printf("xds server error: %v\n", err)
	//	return
	//}
}

type xdsServer struct {

}

func (svr *xdsServer) StreamAggregatedResources(stream service.AggregatedDiscoveryService_StreamAggregatedResourcesServer) (err error) {
	return nil
}

func (svr *xdsServer) DeltaAggregatedResources(stream service.AggregatedDiscoveryService_DeltaAggregatedResourcesServer) (err error) {
	return nil
}

