package xds

import (
	"encoding/json"
	"fmt"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
	"io/ioutil"
	"sync"
	"time"
)

var (
	manager *xdsResourceManager
	once    sync.Once
)

type xdsResourceManager struct {
	// client communicates with the control plane
	client *xdsClient
	// cache stores all the resources
	cache map[xdsresource.ResourceType]map[string]xdsresource.Resource
	// updateCbMap maintains the callback functions of resource update
	updateCbMap map[xdsresource.ResourceType]map[string][]func()
	mu          sync.Mutex

	// TODO: refactor the dump logic
	dumpPath string
}

func GetXdsResourceManager() *xdsResourceManager {
	var err error
	once.Do(func() {
		manager, err = newXdsResourceManager()
	})
	if err != nil {
		panic("xds manager not init")
	}
	return manager
}

func newXdsResourceManager() (*xdsResourceManager, error) {
	// TODO: load ADS configuration from bootstrap.yaml, which should be passed into the xdsClient for initiation
	up := make(map[xdsresource.ResourceType]map[string][]func())
	for rt := range xdsresource.ResourceTypeToUrl {
		up[rt] = make(map[string][]func())
	}

	m := &xdsResourceManager{
		cache:       newXdsResourceCache(),
		updateCbMap: up,
	}
	// Construct bootstrap config
	bootstrapConfig, err := newBootstrapConfig()
	if err != nil {
		return nil, err
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

func (m *xdsResourceManager) Get(resourceType xdsresource.ResourceType, resourceName string) (interface{}, error) {
	// Get from cache
	if r, ok := m.cache[resourceType][resourceName]; ok {
		return r, nil
	}

	// Fetch resource via client and wait for the update
	m.mu.Lock()
	m.client.Subscribe(resourceType, resourceName)
	// Setup callback function for this resource
	cbs := m.updateCbMap[resourceType][resourceName]
	if cbs == nil {
		cbs = make([]func(), 0)
	}
	updateCh := make(chan struct{})
	cb := func() {
		close(updateCh)
	}
	cbs = append(cbs, cb)
	m.updateCbMap[resourceType][resourceName] = cbs
	m.mu.Unlock()

	// Set timeout
	// TODO: timeout should be specified in the config of xdsResourceManager
	timeout := time.Second
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

// TODO: dump into local file
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
	for k := range m.updateCbMap {
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
		if cbs, exist := m.updateCbMap[xdsresource.ListenerType][name]; exist {
			for _, cb := range cbs {
				if cb != nil {
					cb()
				}
			}
			m.updateCbMap[xdsresource.ListenerType] = nil
		}
	}
}

func (m *xdsResourceManager) UpdateRouteConfigResource(up map[string]*xdsresource.RouteConfigResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.RouteConfigType][name] = res
		if cbs, exist := m.updateCbMap[xdsresource.RouteConfigType][name]; exist {
			for _, cb := range cbs {
				if cb != nil {
					cb()
				}
			}
			m.updateCbMap[xdsresource.RouteConfigType] = nil
		}
	}
}

func (m *xdsResourceManager) UpdateClusterResource(up map[string]*xdsresource.ClusterResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.ClusterType][name] = res
		if cbs, exist := m.updateCbMap[xdsresource.ClusterType][name]; exist {
			for _, cb := range cbs {
				if cb != nil {
					cb()
				}
			}
			m.updateCbMap[xdsresource.ClusterType] = nil
		}
	}
}

func (m *xdsResourceManager) UpdateEndpointsResource(up map[string]*xdsresource.EndpointsResource) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, res := range up {
		m.cache[xdsresource.EndpointsType][name] = res
		if cbs, exist := m.updateCbMap[xdsresource.EndpointsType][name]; exist {
			for _, cb := range cbs {
				if cb != nil {
					cb()
				}
			}
			m.updateCbMap[xdsresource.EndpointsType] = nil
		}
	}
}
