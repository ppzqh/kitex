package xds

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
	v3discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"sync"
	"time"
)

//type XDSClient interface {
//	Subscribe(resourceType ResourceType, resourceName string)
//	Unsubscribe(resourceType ResourceType, resourceName string) // ref: https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol#unsubscribing-from-resources
//}

//type subscribeMeta struct {
//	version string
//	nonce   string
//}

type StreamClient interface {
	v3discovery.AggregatedDiscoveryService_StreamAggregatedResourcesClient
}

func newStreamClient(addr string) (StreamClient, error) {
	dopts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    5 * time.Minute,
			Timeout: 20 * time.Second,
		}),
		grpc.WithInsecure(),
	}
	cc, err := grpc.Dial(addr, dopts...)
	if err != nil {
		return nil, fmt.Errorf("dial error")
	}
	adscc := v3discovery.NewAggregatedDiscoveryServiceClient(cc)
	sc, err := adscc.StreamAggregatedResources(context.Background(), grpc.WaitForReady(true))

	return sc, err
}

type xdsClient struct {
	config             *BootstrapConfig
	mu                 sync.RWMutex
	subscribedResource map[xdsresource.ResourceType]map[string]bool //map[string]bool
	versionMap         map[xdsresource.ResourceType]string
	nonceMap           map[xdsresource.ResourceType]string

	streamClient StreamClient
	updateFunc   ResourceUpdater

	// request queue
	requestQueue []interface{}
	qLock        sync.Mutex

	// channel for stop
	closeCh chan struct{}
}

func newXdsClient(bCfg *BootstrapConfig, updater ResourceUpdater) *xdsClient {
	// build stream client that communicates with the xds server
	sc, err := newStreamClient(bCfg.xdsSvrAddr)
	if err != nil {
		return nil
	}

	sr := make(map[xdsresource.ResourceType]map[string]bool)
	for rt := range xdsresource.ResourceTypeToUrl {
		sr[rt] = make(map[string]bool)
	}

	cli := &xdsClient{
		config:             bCfg,
		streamClient:       sc,
		subscribedResource: sr, //make(map[ResourceType]map[string]bool),
		versionMap:         make(map[xdsresource.ResourceType]string),
		nonceMap:           make(map[xdsresource.ResourceType]string),
		requestQueue:       make([]interface{}, 0),
		updateFunc:         updater,
	}

	cli.run()
	return cli
}

func (c *xdsClient) pushRequest(req interface{}) {
	c.qLock.Lock()
	c.requestQueue = append(c.requestQueue, req)
	c.qLock.Unlock()
}

func (c *xdsClient) popRequest() interface{} {
	c.qLock.Lock()
	defer c.qLock.Unlock()

	if len(c.requestQueue) == 0 {
		return nil
	}
	req := c.requestQueue[0]
	c.requestQueue = c.requestQueue[1:]
	return req
}

func (c *xdsClient) Subscribe(resourceType xdsresource.ResourceType, resourceName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// New resource type
	if r := c.subscribedResource[resourceType]; r == nil {
		c.subscribedResource[resourceType] = make(map[string]bool)
	}

	//// If not subscribe all
	//if resourceName != "*" {
	//	c.subscribedResource[resourceType][resourceName] = true
	//}

	// prepare new request and send to the channel
	// regular 的 ads 要求 req 内包含所有的 resource https://github.com/envoyproxy/go-control-plane/issues/46
	if req := c.prepareRequest(resourceType, false); req != nil {
		c.pushRequest(req)
	}
}

func (c *xdsClient) Unsubscribe(resourceType xdsresource.ResourceType, resourceName string) {
	if _, ok := c.subscribedResource[resourceType]; !ok {
		return
	}

	c.subscribedResource[resourceType][resourceName] = false
}

// refresh all resources
func (c *xdsClient) refresh() {
	// start from cds
	req := c.prepareRequest(xdsresource.ClusterType, false)
	//_ = c.send(req)
	c.pushRequest(req)
}

func (c *xdsClient) run() {
	refreshInterval := 1 * time.Second
	timer := time.NewTicker(refreshInterval)

	// sender:
	go func() {
		for {
			select {
			case <-c.closeCh:
				klog.Infof("[xds] stop ads client sender")
				return
			case <-timer.C:
				c.refresh()
				timer.Reset(refreshInterval)
			default:
				req := c.popRequest()
				if req == nil {
					continue
				}
				err := c.send(req)
				if err != nil {
					panic(err)
				}
			}
		}
	}()

	// receiver
	go func() {
		for {
			select {
			case <-c.closeCh:
				klog.Infof("[xds] stop ads client receiver")
				return
			default:
			}
			resp, err := c.recv()
			if err != nil {
				panic(err)
			}
			c.handleResponse(resp)
		}
	}()

	// Test warmup: send request with name as "*"
	warmup := func() {
		req := &v3discovery.DiscoveryRequest{
			VersionInfo:   "",
			Node:          c.config.node,
			TypeUrl:       xdsresource.ResourceTypeToUrl[xdsresource.ClusterType],
			ResourceNames: []string{"*"},
			ResponseNonce: "",
		} //c.prepareRequest(ClusterType, false)
		c.pushRequest(req)
	}
	warmup()
}

func (c *xdsClient) close() {
	close(c.closeCh)
}

func (c *xdsClient) send(msg interface{}) (err error) {
	req, ok := msg.(*v3discovery.DiscoveryRequest)
	if !ok {
		panic("invalid request")
	}
	sc := c.streamClient
	if err != nil {
		return err
	}
	err = sc.Send(req)
	if err != nil {
		panic(err)
	}
	return err

}

func (c *xdsClient) recv() (resp *v3discovery.DiscoveryResponse, err error) {
	sc := c.streamClient
	resp, err = sc.Recv()
	return resp, err
}

//func (c *xdsClient) updateSubscription(rsrcType ResourceType, names []string) {
//	c.mu.Lock()
//	defer c.mu.Unlock()
//	for _, name := range names {
//		c.subscribedResource[rsrcType][name] = true
//	}
//}

func (c *xdsClient) handleCDS(rawResources []*any.Any) {
	if rawResources == nil {
		return
	}

	res := xdsresource.UnmarshalCDS(rawResources)
	c.updateFunc.UpdateClusterResource(res)
	// TODO: ACK
	c.ack(xdsresource.ClusterType)

	// prepare EDS request
	c.mu.Lock()
	for name := range res {
		c.subscribedResource[xdsresource.EndpointsType][name] = true
	}
	c.mu.Unlock()
	if req := c.prepareRequest(xdsresource.EndpointsType, false); req != nil {
		c.pushRequest(req)
	}
}

func (c *xdsClient) handleEDS(rawResources []*any.Any) {
	if rawResources == nil {
		return
	}
	res := xdsresource.UnmarshalEDS(rawResources)
	c.updateFunc.UpdateEndpointsResource(res)
	//c.ack(EndpointsType)

	// send next request
	//c.prepareRequest(EndpointsType, false)
}

func (c *xdsClient) handleResponse(msg interface{}) {
	// check the type of response
	resp, ok := msg.(*v3discovery.DiscoveryResponse)
	if !ok {
		return
	}

	version := resp.GetVersionInfo()
	nonce := resp.GetNonce()
	url := resp.GetTypeUrl()
	rsrcType, ok := xdsresource.ResourceUrlToType[url]
	if !ok {
		panic("[xds]: Unknown type of xds response")
	}
	// update nonce and version
	c.nonceMap[rsrcType] = nonce
	c.versionMap[rsrcType] = version

	// unmarshal resources
	switch rsrcType {
	case xdsresource.ListenerType:
	case xdsresource.RouteConfigType:
	case xdsresource.ClusterType:
		c.handleCDS(resp.GetResources())
	case xdsresource.EndpointsType:
		c.handleEDS(resp.GetResources())
	}
}

func (c *xdsClient) ack(rsrcType xdsresource.ResourceType) {
	ackMsg := c.prepareRequest(rsrcType, true)
	_ = c.send(ackMsg)
}

// prepare new request and send to the channel
// regular 的 ads 要求 req 内包含所有的 resource https://github.com/envoyproxy/go-control-plane/issues/46
func (c *xdsClient) prepareRequest(resourceType xdsresource.ResourceType, ack bool) *v3discovery.DiscoveryRequest {
	var (
		version       string
		nonce         string
		resourceNames []string
	)

	r := c.subscribedResource[resourceType]
	if r == nil {
		return nil
	}
	// prepare resources
	resourceNames = make([]string, len(r))
	i := 0
	for name, inUse := range r {
		if inUse {
			resourceNames[i] = name
			i++
		}
	}
	// prepare version
	version = c.versionMap[resourceType]

	if ack {
		// prepare nonce
		nonce = c.nonceMap[resourceType]
	}

	req := &v3discovery.DiscoveryRequest{
		VersionInfo:   version,
		Node:          c.config.node,
		TypeUrl:       xdsresource.ResourceTypeToUrl[resourceType],
		ResourceNames: resourceNames,
		ResponseNonce: nonce,
	}
	return req
}
