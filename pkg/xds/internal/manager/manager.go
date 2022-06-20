package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/xds/internal/api/discoveryv3/aggregateddiscoveryservice"
	"github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"
	"io/ioutil"
	"sync"
	"time"
)

type xdsResourceManager struct {
	// client communicates with the control plane
	client *xdsClient
	// cache stores all the resources
	cache map[xdsresource.ResourceType]map[string]xdsresource.Resource
	// updateChMap maintains the channel for notifying resource update
	updateChMap map[xdsresource.ResourceType]map[string][]chan struct{}
	mu          sync.Mutex

	// TODO: refactor the dump logic
	dumpPath string
}

func newStreamClient(addr string) (StreamClient, error) {
	cli, err := aggregateddiscoveryservice.NewClient(addr, client.WithHostPorts(addr))
	if err != nil {
		panic(err)
	}
	sc, err := cli.StreamAggregatedResources(context.Background())
	return sc, err
}

func NewXDSResourceManager(bootstrapConfig *BootstrapConfig) (*xdsResourceManager, error) {
	// load bootstrap config
	var err error
	if bootstrapConfig == nil {
		bootstrapConfig, err = newBootstrapConfig()
		if err != nil {
			return nil, err
		}
	}

	up := make(map[xdsresource.ResourceType]map[string][]chan struct{})
	for rt := range xdsresource.ResourceTypeToUrl {
		up[rt] = make(map[string][]chan struct{})
	}

	m := &xdsResourceManager{
		cache:       newXdsResourceCache(),
		updateChMap: up,
	}
	// Initial xds client
	cli, err := newXdsClient(bootstrapConfig, m)
	if err != nil {
		return nil, err
	}
	m.client = cli

	m.dumpPath = "/tmp/"
	return m, nil
}

func newXdsResourceCache() map[xdsresource.ResourceType]map[string]xdsresource.Resource {
	c := make(map[xdsresource.ResourceType]map[string]xdsresource.Resource)
	for t, _ := range xdsresource.ResourceTypeToUrl {
		c[t] = make(map[string]xdsresource.Resource)
	}
	return c
}

//func (m *xdsResourceManager) GetListener(resourceName string) (interface{}, error) {
//	res, err := m.Get(xdsresource.ListenerType, resourceName)
//	if err != nil {
//		panic("Get failed")
//	}
//	return res
//}
//
//func (m *xdsResourceManager) GetRoute(resourceName string) (interface{}, error) {
//	res, err := m.Get(xdsresource.RouteConfigType, resourceName)
//	if err != nil {
//		panic("Get failed")
//	}
//	return res
//}
//
//func (m *xdsResourceManager) GetCluster(resourceName string) (interface{}, error) {
//	res, err := m.Get(xdsresource.ClusterType, resourceName)
//	if err != nil {
//		panic("Get failed")
//	}
//	return res
//}
//func (m *xdsResourceManager) GetEndpoint(resourceName string) (interface{}, error) {
//	res, err := m.Get(xdsresource.EndpointsType, resourceName)
//	if err != nil {
//		panic("Get failed")
//	}
//	return res
//}

func (m *xdsResourceManager) Get(resourceType xdsresource.ResourceType, resourceName string) (interface{}, error) {
	// Get from cache
	if r, ok := m.cache[resourceType][resourceName]; ok {
		return r, nil
	}

	// Fetch resource via client and wait for the update
	m.mu.Lock()
	// Setup channel for this resource
	chs := m.updateChMap[resourceType][resourceName]
	if len(chs) == 0 {
		// only send one request for this resource
		m.client.Subscribe(resourceType, resourceName)
	}
	updateCh := make(chan struct{})
	chs = append(chs, updateCh)
	m.updateChMap[resourceType][resourceName] = chs
	m.mu.Unlock()

	// Set timeout
	// TODO: timeout should be specified in the config of xdsResourceManager
	timeout := defaultXDSFetchTimeout
	t := time.NewTimer(timeout)
	var err error
	select {
	case <-updateCh:
	case <-t.C:
		err = fmt.Errorf("[XDS] client: fetch failed, timeout")
	}
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	res := m.cache[resourceType][resourceName]
	m.mu.Unlock()
	return res, nil
}

func (m *xdsResourceManager) Subscribe(resourceType xdsresource.ResourceType, resourceName string) {
	// subscribe this resource
	m.client.Subscribe(resourceType, resourceName)
}

func (m *xdsResourceManager) Dump() {
	for t := range xdsresource.ResourceUrlToName {
		m.DumpOne(t)
	}
}

func (m *xdsResourceManager) DumpOne(resourceType xdsresource.ResourceType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res := m.cache[resourceType]
	data, err := json.MarshalIndent(res, "", "    ")
	if err != nil {
		panic(err)
	}
	path := m.dumpPath + xdsresource.ResourceUrlToName[resourceType] + ".json"
	if err := ioutil.WriteFile(path, data, 0o644); err != nil {
		panic("dump error")
	}
}

func (m *xdsResourceManager) Close() {
	// close xds client
	m.client.close()
	// clear all cache
	for k := range m.cache {
		delete(m.cache, k)
	}
	// clear the updateCb
	for k := range m.updateChMap {
		delete(m.cache, k)
	}
}

type ResourceUpdater interface {
	UpdateListenerResource(map[string]*xdsresource.ListenerResource)
	UpdateRouteConfigResource(map[string]*xdsresource.RouteConfigResource)
	UpdateClusterResource(map[string]*xdsresource.ClusterResource)
	UpdateEndpointsResource(map[string]*xdsresource.EndpointsResource)
}

func (m *xdsResourceManager) UpdateListenerResource(up map[string]*xdsresource.ListenerResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.ListenerType][name] = res
		if chs, exist := m.updateChMap[xdsresource.ListenerType][name]; exist {
			for _, ch := range chs {
				if ch != nil {
					close(ch)
				}
			}
			m.updateChMap[xdsresource.ListenerType][name] = m.updateChMap[xdsresource.ListenerType][name][0:0]
		}
	}
}

func (m *xdsResourceManager) UpdateRouteConfigResource(up map[string]*xdsresource.RouteConfigResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.RouteConfigType][name] = res
		if chs, exist := m.updateChMap[xdsresource.RouteConfigType][name]; exist {
			for _, ch := range chs {
				if ch != nil {
					close(ch)
				}
			}
			m.updateChMap[xdsresource.RouteConfigType][name] = m.updateChMap[xdsresource.RouteConfigType][name][0:0]
		}
	}
}

func (m *xdsResourceManager) UpdateClusterResource(up map[string]*xdsresource.ClusterResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.ClusterType][name] = res
		if chs, exist := m.updateChMap[xdsresource.ClusterType][name]; exist {
			for _, ch := range chs {
				if ch != nil {
					close(ch)
				}
			}
			m.updateChMap[xdsresource.ClusterType][name] = m.updateChMap[xdsresource.ClusterType][name][0:0]
		}
	}
}

func (m *xdsResourceManager) UpdateEndpointsResource(up map[string]*xdsresource.EndpointsResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.EndpointsType][name] = res
		if chs, exist := m.updateChMap[xdsresource.EndpointsType][name]; exist {
			for _, ch := range chs {
				if ch != nil {
					close(ch)
				}
			}
			m.updateChMap[xdsresource.EndpointsType][name] = m.updateChMap[xdsresource.EndpointsType][name][0:0]
		}
	}
}
