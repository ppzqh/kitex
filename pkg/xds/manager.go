package xds

import (
	"fmt"
	"github.com/cloudwego/kitex/pkg/xds/xdsresource"
	"sync"
)

var (
	manager *xdsResourceManager
	once    sync.Once
)

type xdsResourceManager struct {
	client *xdsClient
	cache  *xdsResourceCache
}

func GetXdsResourceManager() *xdsResourceManager {
	once.Do(func() {
		fmt.Println("[xds] initial xds resource manager")
		manager = newXdsResourceManager()
	})
	if manager == nil {
		panic("xds manager not init")
	}
	return manager
}

func newXdsResourceManager() *xdsResourceManager {
	// TODO: load ADS configuration from bootstrap.yaml, which should be passed into the xdsClient for initiation
	cache := newXdsResourceCache()
	cli := newXdsClient(newBootstrapConfig(), cache)
	m := &xdsResourceManager{
		client: cli,
		cache:  cache,
	}
	return m
}

func (m *xdsResourceManager) Get(resourceType xdsresource.ResourceType, resourceName string) interface{} {
	return m.cache.Get(resourceType, resourceName)
}

func (m *xdsResourceManager) GetAll(resourceType xdsresource.ResourceType) interface{} {
	return m.cache.GetAll(resourceType)
}

func (m *xdsResourceManager) Subscribe(resourceType xdsresource.ResourceType, resourceName string) error {
	// subscribe this resource
	m.client.Subscribe(resourceType, resourceName)
	return nil
}

// TODO: dump into local file
func (m *xdsResourceManager) Dump() interface{} {
	return nil
}

type xdsResourceCache struct {
	cache map[xdsresource.ResourceType]map[string]xdsresource.Resource // store all resources
	// TODO: determine if splitting the cache
	//edsCache map[string]xdsresource.EndpointsResource
	mu sync.RWMutex
}

func newXdsResourceCache() *xdsResourceCache {
	c := make(map[xdsresource.ResourceType]map[string]xdsresource.Resource)
	for t, _ := range xdsresource.ResourceTypeToUrl {
		c[t] = make(map[string]xdsresource.Resource)
	}
	return &xdsResourceCache{
		cache: c,
	}
}

func (c *xdsResourceCache) Get(resourceType xdsresource.ResourceType, resourceName string) interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if r := c.cache[resourceType]; r != nil {
		return r[resourceName]
	}
	return nil
}

func (c *xdsResourceCache) GetAll(resourceType xdsresource.ResourceType) interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if r := c.cache[resourceType]; r != nil {
		return r
	}
	return nil
}

//func (c *xdsResourceCache) HandleUpdate(resourceType xdsresource.ResourceType, update map[string]interface{}) {
//	c.mu.Lock()
//	defer c.mu.Unlock()
//
//
//	for k, v := range update {
//		c.cache[resourceType][k] = v
//	}
//}

type ResourceUpdater interface {
	UpdateListenerResource(map[string]xdsresource.ListenerResource)
	UpdateRouteConfigResource(map[string]xdsresource.RouteConfigResource)
	UpdateClusterResource(map[string]xdsresource.ClusterResource)
	UpdateEndpointsResource(map[string]xdsresource.EndpointsResource)
}

func (c *xdsResourceCache) UpdateListenerResource(up map[string]xdsresource.ListenerResource) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range up {
		c.cache[xdsresource.ListenerType][k] = v
	}
}

func (c *xdsResourceCache) UpdateRouteConfigResource(up map[string]xdsresource.RouteConfigResource) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range up {
		c.cache[xdsresource.RouteConfigType][k] = v
	}
}

func (c *xdsResourceCache) UpdateClusterResource(up map[string]xdsresource.ClusterResource) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range up {
		c.cache[xdsresource.ClusterType][k] = v
	}
}

func (c *xdsResourceCache) UpdateEndpointsResource(up map[string]xdsresource.EndpointsResource) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range up {
		c.cache[xdsresource.EndpointsType][k] = v
	}
}
