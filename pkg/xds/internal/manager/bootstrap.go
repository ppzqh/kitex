package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	v3core "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/golang/protobuf/jsonpb"
	"io/ioutil"
	"os"
	"strings"
)

type BootstrapConfig struct {
	Node      *v3core.Node
	XdsSvrCfg *XDSServerConfig
}

type XDSServerConfig struct {
	ServerAddress string
}

type xdsServer struct {
	ServerURI      string         `json:"server_uri"`
	ChannelCreds   []channelCreds `json:"channel_creds"`
	ServerFeatures []string       `json:"server_features"`
}

type channelCreds struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

// UnmarshalJSON takes the json data (a server) and unmarshals it to the struct.
func (sc *XDSServerConfig) UnmarshalJSON(data []byte) error {
	var server xdsServer
	if err := json.Unmarshal(data, &server); err != nil {
		return fmt.Errorf("[XDS Bootstrap] Unmarshal field ServerConfig failed during bootstrap: %v", err)
	}
	sc.ServerAddress = server.ServerURI
	return nil
}

var XDSBootstrapFileNameEnv = "GRPC_XDS_BOOTSTRAP"

func newBootstrapConfig() (*BootstrapConfig, error) {
	XDSBootstrapFileName := os.Getenv(XDSBootstrapFileNameEnv)
	bootstrapConfig, err := readBootstrap(XDSBootstrapFileName)
	if err != nil {
		return nil, err
	}
	processServerAddress(bootstrapConfig)
	return bootstrapConfig, nil
}

func processServerAddress(bcfg *BootstrapConfig) {
	svrAddr := bcfg.XdsSvrCfg.ServerAddress
	svrAddr = strings.TrimLeft(svrAddr, "unix:")
	bcfg.XdsSvrCfg.ServerAddress = svrAddr
}

// TODO: function is copied from grpc?
func readBootstrap(fileName string) (*BootstrapConfig, error) {
	b, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	var jsonData map[string]json.RawMessage
	if err := json.Unmarshal(b, &jsonData); err != nil {
		return nil, err
	}
	m := jsonpb.Unmarshaler{AllowUnknownFields: true}

	bootstrapConfig := &BootstrapConfig{}
	for k, v := range jsonData {
		switch k {
		case "node":
			node := &v3core.Node{}
			if err := m.Unmarshal(bytes.NewReader(v), node); err != nil {
				return nil, fmt.Errorf("[XDS Bootstrap] unmarshal node failed: %v", err)
			}
			bootstrapConfig.Node = node
		case "xds_servers":
			servers, err := unmarshalJSONServerConfigSlice(v)
			if err != nil {
				return nil, fmt.Errorf("[XDS Bootstrap] unmarshal xds_server failed: %v", err)
			}
			bootstrapConfig.XdsSvrCfg = servers[0]
		}
	}
	return bootstrapConfig, nil
}

// unmarshalJSONServerConfigSlice unmarshals JSON to a slice.
func unmarshalJSONServerConfigSlice(data []byte) ([]*XDSServerConfig, error) {
	var servers []*XDSServerConfig
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to []*ServerConfig: %v", err)
	}
	if len(servers) < 1 {
		return nil, fmt.Errorf("no management server found in JSON")
	}
	return servers, nil
}
