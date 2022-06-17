package xds

import (
	"bytes"
	"encoding/json"
	"fmt"
	envoy_config_core_v3 "github.com/ppzqh/kitex/pkg/xds_gen/github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/golang/protobuf/jsonpb"
	"io/ioutil"
	"os"
)

type BootstrapConfig struct {
	node      *envoy_config_core_v3.Node
	xdsSvrCfg *XDSServerConfig
}

type XDSServerConfig struct {
	serverAddress string
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
		return fmt.Errorf("xds: json.Unmarshal(data) for field ServerConfig failed during bootstrap: %v", err)
	}
	sc.serverAddress = server.ServerURI
	return nil
}

var XDSBootstrapFileNameEnv = "GRPC_XDS_BOOTSTRAP"

func newBootstrapConfig() (*BootstrapConfig, error) {
	XDSBootstrapFileName := os.Getenv(XDSBootstrapFileNameEnv)
	bootstrapConfig := readBootstrap(XDSBootstrapFileName)
	if bootstrapConfig != nil {
		return bootstrapConfig, nil
	}
	err := fmt.Errorf("[XDS] bootstrap")
	return nil, err
}

func readBootstrap(fileName string) *BootstrapConfig {
	b, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil
	}

	var jsonData map[string]json.RawMessage
	if err := json.Unmarshal(b, &jsonData); err != nil {
		panic("xds: Failed to parse bootstrap config")
		return nil
	}
	m := jsonpb.Unmarshaler{AllowUnknownFields: true}

	bootstrapConfig := &BootstrapConfig{}
	for k, v := range jsonData {
		switch k {
		case "node":
			node := &envoy_config_core_v3.Node{}
			if err := m.Unmarshal(bytes.NewReader(v), node); err != nil {
				panic("xds: jsonpb.Unmarshal(%v) for field %q failed during bootstrap")
				return nil
			}
			bootstrapConfig.node = node
		case "xds_servers":
			servers, err := unmarshalJSONServerConfigSlice(v)
			if err != nil {
				panic("xds: xds_server")
				return nil
			}
			bootstrapConfig.xdsSvrCfg = servers[0]
		}
	}
	return bootstrapConfig
}

// TODO: function is copied from grpc?
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
