package xds

import (
	"github.com/cloudwego/kitex/pkg/xds/internal/manager"
	"github.com/cloudwego/kitex/pkg/xds/xdssuite"
)

func newManager() (xdssuite.XDSResourceManager, error) {
	return manager.NewXDSResourceManager(nil)
}
func Init() {
	xdssuite.BuildXDSResourceManager(newManager)
	// TODO: add some init process to subscribe xds resource.
	// Load ENV to get the resourceName on server-side?
}
