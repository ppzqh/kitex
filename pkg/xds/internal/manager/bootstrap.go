package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	v3core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/golang/protobuf/jsonpb"
	"io/ioutil"
	"os"
	"strings"
)

var (
	XDSBootstrapFileNameEnv = "GRPC_XDS_BOOTSTRAP"
	POD_NAMESPACE           = "POD_NAMESPACE"
	POD_NAME                = "POD_NAME"
	INSTANCE_IP             = "INSTANCE_IP"
	XDSPROXY_PATH           = "/etc/istio/proxy/XDS"
)

type BootstrapConfig struct {
	node      *v3core.Node
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

// newBootstrapConfig constructs the bootstrapConfig
func newBootstrapConfig() (*BootstrapConfig, error) {
	//XDSBootstrapFileName := os.Getenv(XDSBootstrapFileNameEnv)
	//bootstrapConfig, err := readBootstrap(XDSBootstrapFileName)
	//if err != nil {
	//	return nil, err
	//}
	//processServerAddress(bootstrapConfig)
	//return bootstrapConfig, nil

	namespace := os.Getenv(POD_NAMESPACE)
	if namespace == "" {
		return nil, fmt.Errorf("[XDS] Bootstrap, POD_NAMESPACE is not set in env")
	}
	podName := os.Getenv(POD_NAME)
	if podName == "" {
		return nil, fmt.Errorf("[XDS] Bootstrap, POD_NAME is not set in env")
	}
	podIp := os.Getenv(INSTANCE_IP)
	if podIp == "" {
		return nil, fmt.Errorf("[XDS] Bootstrap, INSTANCE_IP is not set in env")
	}

	return &BootstrapConfig{
		node: &v3core.Node{
			Id: "sidecar~" + podIp + "~" + podName + "." + namespace + "~" + namespace + ".svc.cluster.local",
		},
		xdsSvrCfg: &XDSServerConfig{
			serverAddress: XDSPROXY_PATH, // must be the uds path of xds proxy
		},
	}, nil
}

// UnmarshalJSON unmarshal the json data into the struct.
func (sc *XDSServerConfig) UnmarshalJSON(data []byte) error {
	var server xdsServer
	if err := json.Unmarshal(data, &server); err != nil {
		return fmt.Errorf("[XDS] Bootstrap, Unmarshal field ServerConfig failed during bootstrap: %v", err)
	}
	sc.serverAddress = server.ServerURI
	return nil
}

func processServerAddress(bcfg *BootstrapConfig) {
	svrAddr := bcfg.xdsSvrCfg.serverAddress
	svrAddr = strings.TrimLeft(svrAddr, "unix:")
	// TODO: add some checks when svrAddr is not valid.
	if svrAddr == "" {
		svrAddr = defaultControlPlaneAddress
	}
	bcfg.xdsSvrCfg.serverAddress = svrAddr
}

// readBootstrap read the bootstrap file and unmarshal it to a struct.
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
				return nil, fmt.Errorf("[XDS] Bootstrap, unmarshal node failed: %v", err)
			}
			bootstrapConfig.node = node
		case "xds_servers":
			servers, err := unmarshalServerConfig(v)
			if err != nil {
				return nil, fmt.Errorf("[XDS] Bootstrap, unmarshal xds_server failed: %v", err)
			}
			bootstrapConfig.xdsSvrCfg = servers[0]
		}
	}
	return bootstrapConfig, nil
}

// unmarshalServerConfig unmarshal xds server config to a slice.
func unmarshalServerConfig(data []byte) ([]*XDSServerConfig, error) {
	var servers []*XDSServerConfig
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("[XDS] Bootstrap, failed to unmarshal ServerConfig: %s", err)
	}
	if len(servers) < 1 {
		return nil, fmt.Errorf("[XDS] Bootstrap, no xds server found in bootstrap")
	}
	return servers, nil
}
