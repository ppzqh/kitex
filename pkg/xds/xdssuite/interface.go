package xdssuite

import "github.com/cloudwego/kitex/pkg/xds/internal/xdsresource"

type XDSResourceManager interface {
	//GetListener(string) (interface{}, error) //*xdsresource.ListenerResource
	//GetRoute(string) (interface{}, error) //*xdsresource.RouteConfigResource
	//GetCluster(string) (interface{}, error) //*xdsresource.ClusterResource
	//GetEndpoint(string) (interface{}, error) // *xdsresource.EndpointsResource
	Get(resourceType xdsresource.ResourceType, resourceName string) (interface{}, error)
}

var (
	manager XDSResourceManager
	newXDSResourceManager func() (XDSResourceManager, error)
)

func BuildXDSResourceManager(f func() (XDSResourceManager, error)) {
	newXDSResourceManager = f
	m, err := newXDSResourceManager()
	if err != nil {
		panic(err.Error())
	}
	manager = m
}

func getXdsResourceManager() (XDSResourceManager, error) {
	if manager != nil {
		return manager, nil
	}

	m, err := newXDSResourceManager()
	manager = m
	return manager, err
}