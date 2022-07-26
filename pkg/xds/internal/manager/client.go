package manager

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3/aggregateddiscoveryservice"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"google.golang.org/genproto/googleapis/rpc/status"
	"strings"
	"sync"
	"time"
)

type ADSClient = aggregateddiscoveryservice.Client
type ADSStream = aggregateddiscoveryservice.AggregatedDiscoveryService_StreamAggregatedResourcesClient

// xdsClient communicates with the control plane to perform xds resource discovery.
// It maintains the connection and all the resources being watched.
// It processes the responses from the xds server and update the resources to the cache in xdsResourceManager.
type xdsClient struct {
	// config is the bootstrap config read from the config file.
	config *BootstrapConfig

	// watchedResource is the map of resources that are watched by the client.
	// every discovery request will contain all the resources of one type in the map.
	watchedResource map[xdsresource.ResourceType]map[string]bool

	// cipResolver is used to resolve the clusterIP.
	// listenerName (we use fqdn for listener name) to clusterIP.
	// "kitex-server.default.svc.cluster.local" -> "10.0.0.1"
	cipResolver ClusterIPResolver

	// versionMap stores the versions of different resource type.
	versionMap map[xdsresource.ResourceType]string
	// nonceMap stores the nonce of the recent response.
	nonceMap map[xdsresource.ResourceType]string

	// adsClient is a kitex client using grpc protocol that can communicate with xds server.
	adsClient        ADSClient
	adsStream        ADSStream
	streamClientLock sync.Mutex

	// resourceUpdater is used to update the resource update to the cache.
	resourceUpdater *xdsResourceManager

	// refreshInterval is the interval of refreshing the resources.
	refreshInterval time.Duration

	// channel for stop
	closeCh chan struct{}

	mu sync.RWMutex
}

func newNdsResolver() *ndsResolver {
	return &ndsResolver{
		mu:          sync.Mutex{},
		lookupTable: make(map[string][]string),
	}
}

type ndsResolver struct {
	lookupTable map[string][]string
	mu          sync.Mutex
}

func (r *ndsResolver) lookupHost(host string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ips, ok := r.lookupTable[host]; ok {
		return ips, nil
	}
	// TODO: send nds request
	return nil, nil
}

func (r *ndsResolver) updateLookupTable(up map[string][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookupTable = up
}

// newADSClient constructs a new stream client that communicates with the xds server
func newADSClient(addr string) (ADSClient, error) {
	cli, err := aggregateddiscoveryservice.NewClient("xds_servers",
		client.WithHostPorts(addr),
	)
	if err != nil {
		return nil, fmt.Errorf("[XDS] client: construct stream client failed, %s", err.Error())
	}
	return cli, nil
}

// newXDSClient constructs a new xdsClient, which is used to get xds resources from the xds server.
func newXdsClient(bCfg *BootstrapConfig, updater *xdsResourceManager) (*xdsClient, error) {
	// build stream client that communicates with the xds server
	ac, err := newADSClient(bCfg.xdsSvrCfg.serverAddress)
	if err != nil {
		return nil, fmt.Errorf("[XDS] client: construct stream client failed, %s", err.Error())
	}

	cli := &xdsClient{
		config:          bCfg,
		adsClient:       ac,
		watchedResource: make(map[xdsresource.ResourceType]map[string]bool),
		cipResolver:     newNdsResolver(), //newClusterIPResolver(),
		versionMap:      make(map[xdsresource.ResourceType]string),
		nonceMap:        make(map[xdsresource.ResourceType]string),
		resourceUpdater: updater,
		refreshInterval: defaultRefreshInterval,
		closeCh:         make(chan struct{}),
	}
	cli.run()
	return cli, nil
}

// Watch adds a resource to the watch map and send a discovery request.
func (c *xdsClient) Watch(rType xdsresource.ResourceType, rName string) {
	c.mu.Lock()
	// New resource type
	if r := c.watchedResource[rType]; r == nil {
		c.watchedResource[rType] = make(map[string]bool)
	}
	// subscribe new resource
	c.watchedResource[rType][rName] = true
	c.mu.Unlock()
	// send request for this resource
	c.sendRequest(rType, false, "")
}

// RemoveWatch removes a resource from the watch map.
func (c *xdsClient) RemoveWatch(rType xdsresource.ResourceType, rName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.watchedResource[rType]; !ok {
		return
	}
	// remove this resource
	delete(c.watchedResource[rType], rName)
}

// refresh all resources
func (c *xdsClient) refresh() {
	for rt := range c.watchedResource {
		c.sendRequest(rt, false, "")
	}
}

// sender refresh the resources periodically
func (c *xdsClient) sender() {
	timer := time.NewTicker(c.refreshInterval)

	// sender
	go func() {
		defer func() {
			if err := recover(); err != nil {
				klog.Errorf("[XDS] client: run sender panic, error=%s", err)
			}
		}()

		for {
			select {
			case <-c.closeCh:
				klog.Infof("[XDS] client: stop ads client sender")
				return
			case <-timer.C:
				//c.refresh()
				timer.Reset(c.refreshInterval)
			}
		}
	}()
}

// receiver receives and handle response from the xds server.
// xds server may proactively push the update.
func (c *xdsClient) receiver() {
	// receiver
	go func() {
		defer func() {
			if err := recover(); err != nil {
				klog.Errorf("[XDS] client: run receiver panic, error=%s", err)
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
				klog.Errorf("[XDS] client, receive failed, error=%s", err)
				// TODO: reconnect?
				continue
			}
			err = c.handleResponse(resp)
			if err != nil {
				klog.Errorf("[XDS] client, handle response failed, error=%s", err)
			}
		}
	}()
}

func (c *xdsClient) run() {
	// two goroutines for sender and receiver
	c.sender()
	c.receiver()
}

// close the xdsClient
func (c *xdsClient) close() {
	close(c.closeCh)
	c.adsStream.Close()
}

// getStreamClient returns the adsClient of xdsClient
func (c *xdsClient) getStreamClient() (ADSStream, error) {
	c.streamClientLock.Lock()
	defer c.streamClientLock.Unlock()
	// get stream client
	if c.adsStream != nil {
		return c.adsStream, nil
	}
	// reconnect stream
	as, err := c.adsClient.StreamAggregatedResources(context.Background())
	if err == nil {
		c.adsStream = as
	}
	return c.adsStream, err
}

// sendRequest prepares the requests and sends to the xds server
func (c *xdsClient) sendRequest(rType xdsresource.ResourceType, ack bool, errMsg string) {
	// prepare request
	req := c.prepareRequest(rType, ack, errMsg)
	if req == nil {
		return
	}
	// get stream client
	sc, err := c.getStreamClient()
	if err != nil {
		klog.Errorf("[XDS] client, get stream client failed, error=%s", err)
		return
	}
	err = sc.Send(req)
	if err != nil {
		klog.Errorf("[XDS] client, send failed, error=%s", err)
	}
}

// recv uses stream client to receive the response from the xds server
func (c *xdsClient) recv() (resp *discoveryv3.DiscoveryResponse, err error) {
	sc, err := c.getStreamClient()
	if err != nil {
		return nil, err
	}
	resp, err = sc.Recv()
	return resp, err
}

// updateAndACK update versionMap, nonceMap and send ack to xds server
func (c *xdsClient) updateAndACK(rType xdsresource.ResourceType, nonce, version string, err error) {
	// update nonce and version
	// update and ACK only if error is nil, or NACK should be sent
	var errMsg string
	c.mu.Lock()
	if err == nil {
		c.versionMap[rType] = version
	} else {
		errMsg = err.Error()
	}
	c.nonceMap[rType] = nonce
	c.mu.Unlock()
	c.sendRequest(rType, true, errMsg)
}

func (c *xdsClient) warmup() {
	// watch the NameTable when init the xds client
	c.Watch(xdsresource.NameTableType, "")
	// TODO: maybe need to watch the listener
}

// getListenerName returns the listener name in this format: ${clusterIP}_${port}
// lookup the clusterIP using the cipResolver and return the listenerName
func (c *xdsClient) getListenerName(rName string) (string, error) {
	tmp := strings.Split(rName, ":")
	if len(tmp) < 2 {
		return "", fmt.Errorf("invalid listener name: %s", rName)
	}
	addr, port := tmp[0], tmp[1]
	cip, err := c.cipResolver.lookupHost(addr)
	if err == nil && len(cip) > 0 {
		clusterIPPort := cip[0] + "_" + port
		return clusterIPPort, nil
	}
	return "", err
}

// handleLDS handles the lds response
func (c *xdsClient) handleLDS(resp *discoveryv3.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalLDS(resp.GetResources())
	filteredRes := make(map[string]*xdsresource.ListenerResource)
	// returned listener name is in the format of ${clusterIP}_${port}
	// which should be converted into to the listener name, in the form of ${fqdn}_${port}, watched by the xds client.
	for n := range c.watchedResource[xdsresource.ListenerType] {
		cip, err := c.getListenerName(n)
		if err != nil || cip == "" {
			klog.Warnf("[XDS] client, get cluster ip of resource[%s] failed, error=%s", n, err)
			continue
		}
		if ln, ok := res[cip]; ok {
			filteredRes[n] = ln
		}
	}
	c.updateAndACK(xdsresource.ListenerType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return err
	}

	//c.mu.Lock()
	//for name, v := range res {
	//	if _, ok := c.watchedResource[xdsresource.ListenerType][name]; !ok {
	//		delete(res, name)
	//		continue
	//	}
	//	// subscribe the routeConfig name
	//	if _, ok := c.watchedResource[xdsresource.RouteConfigType]; !ok {
	//		c.watchedResource[xdsresource.RouteConfigType] = make(map[string]bool)
	//	}
	//	c.watchedResource[xdsresource.RouteConfigType][v.RouteConfigName] = true
	//}
	//c.mu.Unlock()
	// update to cache
	c.resourceUpdater.UpdateListenerResource(filteredRes, resp.GetVersionInfo())
	return nil
}

// handleRDS handles the rds response
func (c *xdsClient) handleRDS(resp *discoveryv3.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalRDS(resp.GetResources())
	c.updateAndACK(xdsresource.RouteConfigType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return err
	}

	// prepare CDS request
	//c.mu.Lock()
	//for name, rcfg := range res {
	//	// only accept the routeConfig that is subscribed
	//	if _, ok := c.watchedResource[xdsresource.RouteConfigType][name]; !ok {
	//		delete(res, name)
	//		continue
	//	}
	//
	//	// subscribe cluster name
	//	for _, vh := range rcfg.VirtualHosts {
	//		for _, r := range vh.Routes {
	//			for _, wc := range r.WeightedClusters {
	//				if _, ok := c.watchedResource[xdsresource.ClusterType]; !ok {
	//					c.watchedResource[xdsresource.ClusterType] = make(map[string]bool)
	//				}
	//				c.watchedResource[xdsresource.ClusterType][wc.Name] = true
	//			}
	//		}
	//	}
	//}
	//c.mu.Unlock()
	// update to cache
	c.resourceUpdater.UpdateRouteConfigResource(res, resp.GetVersionInfo())
	return nil
}

// handleCDS handles the cds response
func (c *xdsClient) handleCDS(resp *discoveryv3.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalCDS(resp.GetResources())
	c.updateAndACK(xdsresource.ClusterType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return fmt.Errorf("handle cluster failed: %s", err)
	}
	// prepare EDS request
	c.mu.Lock()
	// store all inline EDS
	for name, v := range res {
		if _, ok := c.watchedResource[xdsresource.ClusterType][name]; !ok {
			delete(res, name)
			continue
		}
		if _, ok := c.watchedResource[xdsresource.EndpointsType]; !ok {
			c.watchedResource[xdsresource.EndpointsType] = make(map[string]bool)
		}
		// subscribe endpoint name
		if v.EndpointName != "" {
			c.watchedResource[xdsresource.EndpointsType][v.EndpointName] = true
		} else {
			c.watchedResource[xdsresource.EndpointsType][name] = true
		}
	}
	c.mu.Unlock()
	// update to cache
	c.resourceUpdater.UpdateClusterResource(res, resp.GetVersionInfo())
	return nil
}

// handleEDS handles the eds response
func (c *xdsClient) handleEDS(resp *discoveryv3.DiscoveryResponse) error {
	res, err := xdsresource.UnmarshalEDS(resp.GetResources())
	c.updateAndACK(xdsresource.EndpointsType, resp.GetNonce(), resp.GetVersionInfo(), err)
	if err != nil {
		return fmt.Errorf("handle endpoint failed: %s", err)
	}
	for name := range res {
		if _, ok := c.watchedResource[xdsresource.EndpointsType][name]; !ok {
			delete(res, name)
			continue
		}
	}
	// update to cache
	c.resourceUpdater.UpdateEndpointsResource(res, resp.GetVersionInfo())
	return nil
}

func (c *xdsClient) handleNDS(resp *discoveryv3.DiscoveryResponse) error {
	nt, err := xdsresource.UnmarshalNDS(resp.GetResources())
	c.updateAndACK(xdsresource.NameTableType, resp.GetNonce(), resp.GetVersionInfo(), err)
	c.cipResolver.updateLookupTable(nt.NameTable)
	return err
}

// handleResponse handles the response from xDS server
func (c *xdsClient) handleResponse(msg interface{}) error {
	// check the type of response
	resp, ok := msg.(*discoveryv3.DiscoveryResponse)
	if !ok {
		return fmt.Errorf("invalid discovery response")
	}
	url := resp.GetTypeUrl()
	rType, ok := xdsresource.ResourceUrlToType[url]
	if !ok {
		return fmt.Errorf("unknown type of resource, url: %s", url)
	}
	if _, ok := c.watchedResource[rType]; !ok {
		c.watchedResource[rType] = make(map[string]bool)
	}
	/*
		handle different resources:
		unmarshal resources and ack, update to cache if no error
	*/
	var err error
	switch rType {
	case xdsresource.ListenerType:
		err = c.handleLDS(resp)
	case xdsresource.RouteConfigType:
		err = c.handleRDS(resp)
	case xdsresource.ClusterType:
		err = c.handleCDS(resp)
	case xdsresource.EndpointsType:
		err = c.handleEDS(resp)
	case xdsresource.NameTableType:
		err = c.handleNDS(resp)
	}
	return err
}

// prepareRequest prepares a new request for the specified resource type
// ResourceNames should include all the subscribed resources of the specified type
func (c *xdsClient) prepareRequest(rType xdsresource.ResourceType, ack bool, errMsg string) *discoveryv3.DiscoveryRequest {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var (
		version string
		nonce   string
		rNames  []string
	)
	res, ok := c.watchedResource[rType]
	if !ok || len(res) == 0 {
		return nil
	}
	// prepare resource name
	rNames = make([]string, 0, len(res))
	for name := range res {
		rNames = append(rNames, name)
	}
	// prepare version and nonce
	version = c.versionMap[rType]
	nonce = c.nonceMap[rType]
	req := &discoveryv3.DiscoveryRequest{
		VersionInfo:   version,
		Node:          c.config.node,
		TypeUrl:       xdsresource.ResourceTypeToUrl[rType],
		ResourceNames: rNames,
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
