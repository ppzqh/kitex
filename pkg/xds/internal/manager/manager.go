package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/kitex/pkg/klog"
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
	meta  map[xdsresource.ResourceType]map[string]*xdsresource.ResourceMeta

	// updateChMap maintains the channel for notifying resource update
	updateChMap map[xdsresource.ResourceType]map[string][]chan struct{}
	mu          sync.Mutex

	// TODO: refactor the dump logic
	dumpPath string
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

	m := newXdsResourceManager()
	// Initial xds client
	cli, err := newXdsClient(bootstrapConfig, m)
	if err != nil {
		return nil, err
	}
	m.client = cli

	m.dumpPath = "/tmp/"

	// start the cache cleaner
	go m.cleaner()

	return m, nil
}

func newXdsResourceManager() *xdsResourceManager {
	cache := make(map[xdsresource.ResourceType]map[string]xdsresource.Resource)
	chMap := make(map[xdsresource.ResourceType]map[string][]chan struct{})
	meta := make(map[xdsresource.ResourceType]map[string]*xdsresource.ResourceMeta)
	for rt := range xdsresource.ResourceTypeToUrl {
		cache[rt] = make(map[string]xdsresource.Resource)
		chMap[rt] = make(map[string][]chan struct{})
		meta[rt] = make(map[string]*xdsresource.ResourceMeta)
	}

	return &xdsResourceManager{
		cache:       cache,
		updateChMap: chMap,
		meta:        meta,
	}
}

func (m *xdsResourceManager) getFromCache(resourceType xdsresource.ResourceType, resourceName string) (interface{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, ok := m.cache[resourceType][resourceName]
	if ok {
		// Record the timestamp
		m.meta[resourceType][resourceName] = &xdsresource.ResourceMeta{Timestamp: time.Now()}
	}
	return res, ok
}

func (m *xdsResourceManager) Get(ctx context.Context, resourceType xdsresource.ResourceType, resourceName string) (interface{}, error) {
	// Get from cache
	res, ok := m.getFromCache(resourceType, resourceName)
	if ok {
		return res, nil
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

	// Set fetch timeout
	// TODO: timeout should be specified in the config of xdsResourceManager
	timeout := defaultXDSFetchTimeout
	t := time.NewTimer(timeout)

	select {
	case <-updateCh:
	case <-t.C:
		return nil, fmt.Errorf("[XDS] client, fetch %s resource[%s] failed, timeout %s",
			xdsresource.ResourceTypeToName[resourceType], resourceName, timeout)
	}

	res, _ = m.getFromCache(resourceType, resourceName)
	m.Dump()
	return res, nil
}

func (m *xdsResourceManager) cleaner() {
	maxIdleTime := time.Second * 30
	t := time.NewTicker(maxIdleTime)

	select {
	case <-t.C:
		m.mu.Lock()
		for rt := range m.meta {
			for resourceName, meta := range m.meta[rt] {
				if time.Now().Sub(meta.Timestamp) > maxIdleTime {
					delete(m.meta[rt], resourceName)
					delete(m.cache[rt], resourceName)
					m.client.Unsubscribe(rt, resourceName)
				}
			}
		}
		m.mu.Unlock()
	}
}

func (m *xdsResourceManager) Dump() {
	for t := range xdsresource.ResourceTypeToName {
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
	path := m.dumpPath + xdsresource.ResourceTypeToName[resourceType] + ".json"
	if err := ioutil.WriteFile(path, data, 0o644); err != nil {
		klog.Warnf("dump xds resource failed\n")
	}
}

func (m *xdsResourceManager) Close() {
	// close xds client
	m.client.close()
}

type ResourceUpdater interface {
	UpdateListenerResource(map[string]*xdsresource.ListenerResource)
	UpdateRouteConfigResource(map[string]*xdsresource.RouteConfigResource)
	UpdateClusterResource(map[string]*xdsresource.ClusterResource)
	UpdateEndpointsResource(map[string]*xdsresource.EndpointsResource)
}

func (m *xdsResourceManager) UpdateListenerResource(up map[string]*xdsresource.ListenerResource) {
	m.mu.Lock()
	inlineRDS := make(map[string]*xdsresource.RouteConfigResource)
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
		if res.InlineRouteConfig != nil {
			inlineRDS[res.RouteConfigName] = res.InlineRouteConfig
		}
	}
	m.mu.Unlock()

	// update inlineRDS to the cache
	if len(inlineRDS) != 0 {
		m.UpdateRouteConfigResource(inlineRDS)
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
	inlineEDS := make(map[string]*xdsresource.EndpointsResource)
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
		if res.InlineEndpoints != nil {
			inlineEDS[res.EndpointName] = res.InlineEndpoints
		}
	}
	m.mu.Unlock()
	// update inlineEDS to the cache
	if len(inlineEDS) != 0 {
		m.UpdateEndpointsResource(inlineEDS)
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
