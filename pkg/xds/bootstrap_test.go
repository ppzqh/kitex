package xds

import (
	"github.com/cloudwego/kitex/internal/test"
	"testing"
)

func Test_newBootstrapConfig(t *testing.T) {
	config, _ := newBootstrapConfig()
	test.Assert(t, config.xdsSvrCfg != nil)
}
