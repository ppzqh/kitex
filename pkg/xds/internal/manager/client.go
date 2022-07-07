package manager

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/streaming"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3/aggregateddiscoveryservice"
	v3discovery "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/service"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"github.com/golang/protobuf/ptypes/any"
	"sync"
	"time"
)

type StreamClient interface {
	streaming.Stream
	Send(*v3discovery.DiscoveryRequest) error
	Recv() (*v3discovery.DiscoveryResponse, error)
}

type xdsClient struct {
	config             *BootstrapConfig
	mu                 sync.RWMutex
	subscribedResource map[xdsresource.ResourceType]map[string]bool
	versionMap         map[xdsresource.ResourceType]string
	nonceMap           map[xdsresource.ResourceType]string

	streamClient     StreamClient
	streamClientLock sync.Mutex
	resourceUpdater  ResourceUpdater

	refreshInterval time.Duration
	// request queue
	requestInfoQueue []interface{}
	qLock            sync.Mutex

	// channel for stop
	closeCh chan struct{}
}

func newStreamClient(addr string) (StreamClient, error) {
	cli, err := aggregateddiscoveryservice.NewClient(addr, client.WithHostPorts(addr))
	if err != nil {
		panic(err)
	}
	sc, err := cli.StreamAggregatedResources(context.Background())
	return sc, err
}

func newXdsClient(bCfg *BootstrapConfig, updater ResourceUpdater) (*xdsClient, error) {
	// build stream client that communicates with the xds server
	sc, err := newStreamClient(bCfg.xdsSvrCfg.serverAddress)
	if err != nil {
		return nil, fmt.Errorf("[XDS] client: construct stream client failed, %s", err.Error())
	}
	// subscribed resource map
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
	ri := &requestInfo{resourceType: xdsresource.ListenerType, ack: false}
	c.pushRequestInfo(ri)
}

func (c *xdsClient) sender() {
	timer := time.NewTicker(c.refreshInterval)

	// sender
	go func() {
		defer func() {
			if err := recover(); err == nil {
				klog.Errorf("[XDS] client: run sender panic")
			}
		}()

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
}

func (c *xdsClient) receiver() {
	// receiver
	go func() {
		defer func() {
			if err := recover(); err == nil {
				klog.Errorf("[XDS] client: run receiver panic")
			}
		}()

		for {
			select {
			case <-c.closeCh:
				klog.Infof("[XDS] client: stop ads client receiver")
				return
			default:
			}
			resp, err := c.recv()
			if err != nil {
				klog.Errorf("[XDS] client, receive failed: %s", err)
			}
			err = c.handleResponse(resp)
			if err != nil {
				klog.Errorf("[XDS] client, handle response failed, error: %s", err)
			}
		}
	}()
}

func (c *xdsClient) run() {
	// two goroutines for sender and receiver
	c.sender()
	c.receiver()
}

func (c *xdsClient) close() {
	close(c.closeCh)
	c.streamClient.Close()
}

func (c *xdsClient) getStreamClient() (StreamClient, error) {
	c.streamClientLock.Lock()
	defer c.streamClientLock.Unlock()

	// get stream client
	var sc StreamClient
	sc = c.streamClient
	if sc != nil {
		return sc, nil
	}
	// construct stream client
	sc, err := newStreamClient(c.config.xdsSvrCfg.serverAddress)
	if err != nil {
		return nil, err
	}
	c.streamClient = sc
	return sc, err
}

func (c *xdsClient) send(msg interface{}) (err error) {
	ri, ok := msg.(*requestInfo)
	if !ok {
		panic("invalid request info")
	}

	// prepare request
	req := c.prepareRequest(ri.resourceType, ri.ack)
	// get stream client
	sc, err := c.getStreamClient()
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
	sc, err := c.getStreamClient()
	if err != nil {
		return nil, err
	}
	resp, err = sc.Recv()
	return resp, err
}

func (c *xdsClient) handleLDS(rawResources []*any.Any) error {
	res, err := xdsresource.UnmarshalLDS(rawResources)
	if err != nil {
		return err
	}
	c.resourceUpdater.UpdateListenerResource(res)

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
	return nil
}

func (c *xdsClient) handleRDS(rawResources []*any.Any) error {
	res, err := xdsresource.UnmarshalRDS(rawResources)
	if err != nil {
		return err
	}
	c.resourceUpdater.UpdateRouteConfigResource(res)
	// prepare CDS request
	c.mu.Lock()
	for rn, rcfg := range res {
		// only accept the routeConfig that is subscribed
		if _, ok := c.subscribedResource[xdsresource.RouteConfigType][rn]; ok {
			// subscribe CDS
			for _, vh := range rcfg.VirtualHosts {
				for _, r := range vh.Routes {
					for _, wc := range r.WeightedClusters {
						c.subscribedResource[xdsresource.ClusterType][wc.Name] = true
					}
				}
			}
		}
	}
	c.mu.Unlock()
	ri := &requestInfo{resourceType: xdsresource.ClusterType, ack: false}
	c.pushRequestInfo(ri)
	return nil
}

func (c *xdsClient) handleCDS(rawResources []*any.Any) error {
	res, err := xdsresource.UnmarshalCDS(rawResources)
	if err != nil {
		return fmt.Errorf("handle cluster failed: %s", err)
	}
	c.resourceUpdater.UpdateClusterResource(res)
	// prepare EDS request
	c.mu.Lock()
	// store all inline EDS
	for name, v := range res {
		// subscribe EDS
		if v.EndpointName != "" {
			c.subscribedResource[xdsresource.EndpointsType][v.EndpointName] = true
		} else {
			c.subscribedResource[xdsresource.EndpointsType][name] = true
		}
	}
	c.mu.Unlock()
	ri := &requestInfo{resourceType: xdsresource.EndpointsType, ack: false}
	c.pushRequestInfo(ri)
	return nil
}

func (c *xdsClient) handleEDS(rawResources []*any.Any) error {
	res, err := xdsresource.UnmarshalEDS(rawResources)
	if err != nil {
		return fmt.Errorf("handle endpoint failed: %s", err)
	}
	c.resourceUpdater.UpdateEndpointsResource(res)
	return nil
}

func (c *xdsClient) handleResponse(msg interface{}) error {
	// check the type of response
	resp, ok := msg.(*v3discovery.DiscoveryResponse)
	if !ok {
		return fmt.Errorf("invalid discovery response")
	}
	// get version and nonce
	version := resp.GetVersionInfo()
	nonce := resp.GetNonce()
	url := resp.GetTypeUrl()
	rsrcType, ok := xdsresource.ResourceUrlToType[url]
	if !ok {
		return fmt.Errorf("unknown type of resource, url: %s", url)
	}

	// update nonce and version
	// TODO: update if the handle function doesn't return error, or NACK should be sent
	c.mu.Lock()
	c.nonceMap[rsrcType] = nonce
	c.versionMap[rsrcType] = version
	c.mu.Unlock()

	// handle different resources
	// unmarshal resources and update to cache
	var err error
	switch rsrcType {
	case xdsresource.ListenerType:
		err = c.handleLDS(resp.GetResources())
	case xdsresource.RouteConfigType:
		err = c.handleRDS(resp.GetResources())
	case xdsresource.ClusterType:
		err = c.handleCDS(resp.GetResources())
	case xdsresource.EndpointsType:
		err = c.handleEDS(resp.GetResources())
	}
	// ACK/NACK
	c.ack(rsrcType, err)
	return err
}

// TODO: add error msg
func (c *xdsClient) ack(rsrcType xdsresource.ResourceType, err error) {
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
