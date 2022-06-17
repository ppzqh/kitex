package xds

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
	"github.com/golang/protobuf/ptypes/any"
	v3discovery "github.com/ppzqh/kitex_xds_api/discoveryv3"
	"github.com/ppzqh/kitex_xds_api/discoveryv3/aggregateddiscoveryservice"
	"sync"
	"time"
)

type StreamClient interface {
	aggregateddiscoveryservice.AggregatedDiscoveryService_StreamAggregatedResourcesClient
	//v3discovery.AggregatedDiscoveryService_StreamAggregatedResourcesClient
}

func newStreamClient(addr string) (StreamClient, error) {
	//dopts := []grpc.DialOption{
	//	grpc.WithKeepaliveParams(keepalive.ClientParameters{
	//		Time:    5 * time.Minute,
	//		Timeout: time.Second,
	//	}),
	//	grpc.WithInsecure(),
	//}
	//cc, err := grpc.Dial(addr, dopts...)
	//if err != nil {
	//	return nil, fmt.Errorf("[XDS] client: dial error")
	//}
	//adscc := v3discovery.NewAggregatedDiscoveryServiceClient(cc)
	//sc, err := adscc.StreamAggregatedResources(context.Background()) //grpc.WaitForReady(true))
	cli := aggregateddiscoveryservice.MustNewClient("xds_server")
	sc, err := cli.StreamAggregatedResources(context.Background())
	return sc, err
}

type xdsClient struct {
	config             *BootstrapConfig
	mu                 sync.RWMutex
	subscribedResource map[xdsresource.ResourceType]map[string]bool
	versionMap         map[xdsresource.ResourceType]string
	nonceMap           map[xdsresource.ResourceType]string

	streamClient    StreamClient
	resourceUpdater ResourceUpdater

	refreshInterval time.Duration
	// request queue
	requestInfoQueue []interface{}
	qLock            sync.Mutex

	// channel for stop
	closeCh chan struct{}
}

func newXdsClient(bCfg *BootstrapConfig, updater ResourceUpdater) (*xdsClient, error) {
	// build stream client that communicates with the xds server
	sc, err := newStreamClient(bCfg.xdsSvrCfg.serverAddress)
	if err != nil {
		return nil, fmt.Errorf("[XDS] client: construct stream client failed, %s", err.Error())
	}

	sr := make(map[xdsresource.ResourceType]map[string]bool)
	for rt := range xdsresource.ResourceTypeToUrl {
		sr[rt] = make(map[string]bool)
	}

	cli := &xdsClient{
		config:             bCfg,
		streamClient:       sc,
		subscribedResource: sr,
		versionMap:         make(map[xdsresource.ResourceType]string),
		nonceMap:           make(map[xdsresource.ResourceType]string),
		requestInfoQueue:   make([]interface{}, 0),
		resourceUpdater:    updater,
		refreshInterval:    defaultRefreshInterval,
	}

	cli.run()
	return cli, nil
}

type requestInfo struct {
	resourceType xdsresource.ResourceType
	ack          bool
}

func (c *xdsClient) pushRequestInfo(req interface{}) {
	c.qLock.Lock()
	c.requestInfoQueue = append(c.requestInfoQueue, req)
	c.qLock.Unlock()
}

func (c *xdsClient) popRequestInfo() interface{} {
	c.qLock.Lock()
	defer c.qLock.Unlock()

	if len(c.requestInfoQueue) == 0 {
		return nil
	}
	req := c.requestInfoQueue[0]
	c.requestInfoQueue = c.requestInfoQueue[1:]
	return req
}

func (c *xdsClient) Subscribe(resourceType xdsresource.ResourceType, resourceName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// New resource type
	if r := c.subscribedResource[resourceType]; r == nil {
		c.subscribedResource[resourceType] = make(map[string]bool)
	}
	// subscribe new resource
	c.subscribedResource[resourceType][resourceName] = true
	ri := &requestInfo{resourceType: resourceType, ack: false}
	c.pushRequestInfo(ri)
}

func (c *xdsClient) Unsubscribe(resourceType xdsresource.ResourceType, resourceName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.subscribedResource[resourceType]; !ok {
		return
	}
	// remove this resource
	delete(c.subscribedResource[resourceType], resourceName)
}

// refresh all resources
func (c *xdsClient) refresh() {
	// TODO: start from cds, why?
	ri := &requestInfo{resourceType: xdsresource.ListenerType, ack: false}
	c.pushRequestInfo(ri)
}

func (c *xdsClient) run() {
	timer := time.NewTicker(c.refreshInterval)

	// sender:
	go func() {
		for {
			select {
			case <-c.closeCh:
				klog.Infof("[XDS] client: stop ads client sender")
				return
			case <-timer.C:
				c.refresh()
				timer.Reset(c.refreshInterval)
			default:
				req := c.popRequestInfo()
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
				klog.Infof("[XDS] client: stop ads client receiver")
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
}

func (c *xdsClient) close() {
	close(c.closeCh)
}

func (c *xdsClient) send(msg interface{}) (err error) {
	ri, ok := msg.(*requestInfo)
	if !ok {
		panic("invalid request info")
	}

	// prepare request
	req := c.prepareRequest(ri.resourceType, ri.ack)
	sc := c.streamClient

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

func (c *xdsClient) handleLDS(rawResources []*any.Any) {
	if rawResources == nil {
		return
	}

	res := xdsresource.UnmarshalLDS(rawResources)
	c.resourceUpdater.UpdateListenerResource(res)
	c.ack(xdsresource.ListenerType)

	c.mu.Lock()
	for _, v := range res {
		// subscribe the routeConfig name
		rn := v.RouteConfigName
		c.subscribedResource[xdsresource.RouteConfigType][rn] = true
	}
	c.mu.Unlock()

	// prepare RDS request
	ri := &requestInfo{resourceType: xdsresource.RouteConfigType, ack: false}
	c.pushRequestInfo(ri)
}

func (c *xdsClient) handleRDS(rawResources []*any.Any) {
	if rawResources == nil {
		return
	}
	res := xdsresource.UnmarshalRDS(rawResources)
	c.resourceUpdater.UpdateRouteConfigResource(res)
	c.ack(xdsresource.RouteConfigType)

	// prepare CDS request
	c.mu.Lock()
	for _, rcfg := range res {
		// subscribe CDS
		for _, vh := range rcfg.VirtualHosts {
			for _, r := range vh.Routes {
				clusterName := r.Cluster
				c.subscribedResource[xdsresource.ClusterType][clusterName] = true
			}
		}
	}
	c.mu.Unlock()
	ri := &requestInfo{resourceType: xdsresource.ClusterType, ack: false}
	c.pushRequestInfo(ri)
}

func (c *xdsClient) handleCDS(rawResources []*any.Any) {
	if rawResources == nil {
		return
	}

	res := xdsresource.UnmarshalCDS(rawResources)
	c.resourceUpdater.UpdateClusterResource(res)
	c.ack(xdsresource.ClusterType)

	// prepare EDS request
	c.mu.Lock()
	// store all inline EDS
	inlineEndpoints := make(map[string]*xdsresource.EndpointsResource)
	for name, v := range res {
		// add inline EDS
		if v.InlineEDS() != nil {
			inlineEndpoints[v.InlineEDS().Name] = v.InlineEDS()
		}
		// subscribe EDS
		if v.EndpointName != "" {
			c.subscribedResource[xdsresource.EndpointsType][v.EndpointName] = true
		} else {
			c.subscribedResource[xdsresource.EndpointsType][name] = true
		}
	}
	// update inline EDS directly
	if len(inlineEndpoints) > 0 {
		c.resourceUpdater.UpdateEndpointsResource(inlineEndpoints)
	}
	c.mu.Unlock()
	ri := &requestInfo{resourceType: xdsresource.EndpointsType, ack: false}
	c.pushRequestInfo(ri)
}

func (c *xdsClient) handleEDS(rawResources []*any.Any) {
	if rawResources == nil {
		return
	}
	res := xdsresource.UnmarshalEDS(rawResources)
	c.resourceUpdater.UpdateEndpointsResource(res)
	c.ack(xdsresource.EndpointsType)
}

func (c *xdsClient) handleResponse(msg interface{}) {
	// check the type of response
	resp, ok := msg.(*v3discovery.DiscoveryResponse)
	if !ok {
		klog.Errorf("[XDS] client: handle response failed, incorrect response")
		return
	}

	version := resp.GetVersionInfo()
	nonce := resp.GetNonce()
	url := resp.GetTypeUrl()
	rsrcType, ok := xdsresource.ResourceUrlToType[url]
	if !ok {
		klog.Errorf("[XDS] client: unknown type of resource, %d", rsrcType)
		return
	}
	// update nonce and version
	c.nonceMap[rsrcType] = nonce
	c.versionMap[rsrcType] = version
	// unmarshal resources
	switch rsrcType {
	case xdsresource.ListenerType:
		c.handleLDS(resp.GetResources())
	case xdsresource.RouteConfigType:
		c.handleRDS(resp.GetResources())
	case xdsresource.ClusterType:
		c.handleCDS(resp.GetResources())
	case xdsresource.EndpointsType:
		c.handleEDS(resp.GetResources())
	}
}

func (c *xdsClient) ack(rsrcType xdsresource.ResourceType) {
	ri := &requestInfo{resourceType: rsrcType, ack: true}
	c.pushRequestInfo(ri)
}

// prepare new request and send to the channel
// regular 的 ads 要求 req 内包含所有的 resource https://github.com/envoyproxy/go-control-plane/issues/46
func (c *xdsClient) prepareRequest(resourceType xdsresource.ResourceType, ack bool) *v3discovery.DiscoveryRequest {
	var (
		version       string
		nonce         string
		resourceNames []string
	)

	c.mu.RLock()
	res := c.subscribedResource[resourceType]
	if res == nil {
		return nil
	}

	// prepare resource name
	resourceNames = make([]string, 0, len(res))
	for name := range res {
		resourceNames = append(resourceNames, name)
	}
	// prepare version
	version = c.versionMap[resourceType]

	if ack {
		// prepare nonce
		nonce = c.nonceMap[resourceType]
	}
	c.mu.RUnlock()

	req := &v3discovery.DiscoveryRequest{
		VersionInfo:   version,
		Node:          c.config.node,
		TypeUrl:       xdsresource.ResourceTypeToUrl[resourceType],
		ResourceNames: resourceNames,
		ResponseNonce: nonce,
	}
	return req
}
