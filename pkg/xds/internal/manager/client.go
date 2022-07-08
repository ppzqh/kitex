package manager

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	"github.com/cloudwego/kitex/pkg/streaming"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3/aggregateddiscoveryservice"
	v3discovery "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/service"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"google.golang.org/genproto/googleapis/rpc/status"
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
	sc, err := newStreamClient(bCfg.XdsSvrCfg.ServerAddress)
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
		resourceUpdater:    updater,
		refreshInterval:    defaultRefreshInterval,
	}

	cli.run()
	return cli, nil
}

func (c *xdsClient) Subscribe(resourceType xdsresource.ResourceType, resourceName string) {
	c.mu.Lock()
	// New resource type
	if r := c.subscribedResource[resourceType]; r == nil {
		c.subscribedResource[resourceType] = make(map[string]bool)
	}
	// subscribe new resource
	c.subscribedResource[resourceType][resourceName] = true
	c.mu.Unlock()
	// send request for this resource
	c.sendRequest(resourceType, false, "")
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
	c.sendRequest(xdsresource.ListenerType, false, "")
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
	if c.streamClient != nil {
		return c.streamClient, nil
	}
	// reconstruct the stream client
	// TODO: add retry?
	sc, err := newStreamClient(c.config.XdsSvrCfg.ServerAddress)
	if err == nil {
		c.streamClient = sc
	}
	return sc, err
}

func (c *xdsClient) sendRequest(resourceType xdsresource.ResourceType, ack bool, errMsg string) {
	// prepare request
	req := c.prepareRequest(resourceType, ack, errMsg)
	if req == nil {
		return
	}
	// get stream client
	sc, err := c.getStreamClient()
	if err != nil {
		klog.Errorf("[XDS] get stream client failed")
	}
	err = sc.Send(req)
	if err != nil {
		klog.Errorf("[XDS] client, send failed: %s", err)
	}
}

func (c *xdsClient) recv() (resp *v3discovery.DiscoveryResponse, err error) {
	sc, err := c.getStreamClient()
	if err != nil {
		return nil, err
	}
	resp, err = sc.Recv()
	return resp, err
}

func (c *xdsClient) updateMetaAndACK(rsrcType xdsresource.ResourceType, nonce, version string, err error) {
	// update nonce and version
	// update and ACK only if error is nil, or NACK should be sent
	var errMsg string
	c.mu.Lock()
	if err == nil {
		c.versionMap[rsrcType] = version
	} else {
		errMsg = err.Error()
	}
	c.nonceMap[rsrcType] = nonce
	c.mu.Unlock()
	c.sendRequest(rsrcType, true, errMsg)
}

func (c *xdsClient) handleLDS(resp *v3discovery.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalLDS(resp.GetResources())
	c.updateMetaAndACK(xdsresource.ListenerType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return err
	}
	c.mu.Lock()
	for name, v := range res {
		if _, ok := c.subscribedResource[xdsresource.ListenerType][name]; !ok {
			delete(res, name)
			continue
		}
		// subscribe the routeConfig name
		if _, ok := c.subscribedResource[xdsresource.RouteConfigType]; !ok {
			c.subscribedResource[xdsresource.RouteConfigType] = make(map[string]bool)
		}
		c.subscribedResource[xdsresource.RouteConfigType][v.RouteConfigName] = true
	}
	c.mu.Unlock()
	// update to cache
	c.resourceUpdater.UpdateListenerResource(res)
	// send RDS request
	c.sendRequest(xdsresource.RouteConfigType, false, "")
	return nil
}

func (c *xdsClient) handleRDS(resp *v3discovery.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalRDS(resp.GetResources())
	c.updateMetaAndACK(xdsresource.RouteConfigType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return err
	}
	// prepare CDS request
	c.mu.Lock()
	for name, rcfg := range res {
		// only accept the routeConfig that is subscribed
		if _, ok := c.subscribedResource[xdsresource.RouteConfigType][name]; !ok {
			delete(res, name)
			continue
		}

		// subscribe cluster name
		for _, vh := range rcfg.VirtualHosts {
			for _, r := range vh.Routes {
				for _, wc := range r.WeightedClusters {
					if _, ok := c.subscribedResource[xdsresource.ClusterType]; !ok {
						c.subscribedResource[xdsresource.ClusterType] = make(map[string]bool)
					}
					c.subscribedResource[xdsresource.ClusterType][wc.Name] = true
				}
			}
		}
	}
	c.mu.Unlock()
	// update to cache
	c.resourceUpdater.UpdateRouteConfigResource(res)
	// send CDS request
	c.sendRequest(xdsresource.ClusterType, false, "")
	return nil
}

func (c *xdsClient) handleCDS(resp *v3discovery.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalCDS(resp.GetResources())
	c.updateMetaAndACK(xdsresource.ClusterType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return fmt.Errorf("handle cluster failed: %s", err)
	}
	// prepare EDS request
	c.mu.Lock()
	// store all inline EDS
	for name, v := range res {
		if _, ok := c.subscribedResource[xdsresource.ClusterType][name]; !ok {
			delete(res, name)
			continue
		}
		// subscribe endpoint name
		if v.EndpointName != "" {
			c.subscribedResource[xdsresource.EndpointsType][v.EndpointName] = true
		} else {
			c.subscribedResource[xdsresource.EndpointsType][name] = true
		}
	}
	c.mu.Unlock()
	// update to cache
	c.resourceUpdater.UpdateClusterResource(res)
	// send EDS request
	c.sendRequest(xdsresource.EndpointsType, false, "")
	return nil
}

func (c *xdsClient) handleEDS(resp *v3discovery.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalEDS(resp.GetResources())
	c.updateMetaAndACK(xdsresource.EndpointsType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return fmt.Errorf("handle endpoint failed: %s", err)
	}
	for name := range res {
		if _, ok := c.subscribedResource[xdsresource.EndpointsType][name]; !ok {
			delete(res, name)
			continue
		}
	}
	// update to cache
	c.resourceUpdater.UpdateEndpointsResource(res)
	return nil
}

func (c *xdsClient) handleResponse(msg interface{}) error {
	// check the type of response
	resp, ok := msg.(*v3discovery.DiscoveryResponse)
	if !ok {
		return fmt.Errorf("invalid discovery response")
	}
	url := resp.GetTypeUrl()
	rsrcType, ok := xdsresource.ResourceUrlToType[url]
	if !ok {
		return fmt.Errorf("unknown type of resource, url: %s", url)
	}
	/*
		handle different resources:
		unmarshal resources and ack, update to cache if no error
	*/
	var err error
	switch rsrcType {
	case xdsresource.ListenerType:
		err = c.handleLDS(resp)
	case xdsresource.RouteConfigType:
		err = c.handleRDS(resp)
	case xdsresource.ClusterType:
		err = c.handleCDS(resp)
	case xdsresource.EndpointsType:
		err = c.handleEDS(resp)
	}
	return err
}

// prepare new request and send to the channel
// resourceNames should include all the subscribed resources of the specified type
func (c *xdsClient) prepareRequest(resourceType xdsresource.ResourceType, ack bool, errMsg string) *v3discovery.DiscoveryRequest {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if _, ok := c.subscribedResource[resourceType]; !ok {
		return nil
	}

	var (
		version       string
		nonce         string
		resourceNames []string
	)
	res, ok := c.subscribedResource[resourceType]
	if !ok || len(res) == 0 {
		return nil
	}
	// prepare resource name
	resourceNames = make([]string, 0, len(res))
	for name := range res {
		resourceNames = append(resourceNames, name)
	}
	// prepare version and nonce
	version = c.versionMap[resourceType]
	nonce = c.nonceMap[resourceType]
	req := &v3discovery.DiscoveryRequest{
		VersionInfo:   version,
		Node:          c.config.Node,
		TypeUrl:       xdsresource.ResourceTypeToUrl[resourceType],
		ResourceNames: resourceNames,
		ResponseNonce: nonce,
	}

	// NACK with error message
	if ack && errMsg != "" {
		req.ErrorDetail = &status.Status{
			Code: int32(codes.InvalidArgument), Message: errMsg,
		}
	}
	return req
}
