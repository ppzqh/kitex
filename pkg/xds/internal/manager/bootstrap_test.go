package manager

import (
	"github.com/cloudwego/kitex/internal/test"
	"testing"
)

func Test_newBootstrapConfig(t *testing.T) {
	config, err := newBootstrapConfig()
	test.Assert(t, err == nil)
	test.Assert(t, config.xdsSvrCfg != nil)
}
