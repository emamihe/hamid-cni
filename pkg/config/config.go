package config

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	DefaultSocketPath   = "/var/run/hamid-cni/hamid.sock"
	DefaultCNIBinDir    = "/opt/cni/bin"
	DefaultCNIConfDir   = "/etc/cni/net.d"
	DefaultCNIConfName  = "10-hamid-cni.conflist"
	DefaultBridgePrefix = "hv"
	DefaultVXLANPrefix  = "vx"
	DefaultMTU          = 1450
	DefaultVPCName      = "default"
)

// CNINetConf is the CNI configuration passed by kubelet / the conflist.
type CNINetConf struct {
	CNIVersion   string `json:"cniVersion"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	SocketPath   string `json:"socketPath,omitempty"`
	MTU          int    `json:"mtu,omitempty"`
	RuntimeConfig map[string]interface{} `json:"runtimeConfig,omitempty"`
	RawPrevResult map[string]interface{} `json:"prevResult,omitempty"`
}

// AgentConfig is the node-agent configuration (file or flags).
type AgentConfig struct {
	NodeName     string `json:"nodeName"`
	SocketPath   string `json:"socketPath"`
	Kubeconfig   string `json:"kubeconfig,omitempty"`
	MTU          int    `json:"mtu"`
	BridgePrefix string `json:"bridgePrefix"`
	VXLANPrefix  string `json:"vxlanPrefix"`
	// UnderlayIface is the host interface used as VXLAN VTEP parent.
	// Empty means auto-detect from the default route.
	UnderlayIface string `json:"underlayIface,omitempty"`
	// DefaultVPC is used when a namespace lacks the VPC annotation.
	DefaultVPC string `json:"defaultVPC,omitempty"`
}

func LoadCNINetConf(stdin []byte) (*CNINetConf, error) {
	conf := &CNINetConf{}
	if err := json.Unmarshal(stdin, conf); err != nil {
		return nil, fmt.Errorf("parse CNI conf: %w", err)
	}
	if conf.SocketPath == "" {
		conf.SocketPath = DefaultSocketPath
	}
	if conf.MTU == 0 {
		conf.MTU = DefaultMTU
	}
	return conf, nil
}

func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		NodeName:     os.Getenv("NODE_NAME"),
		SocketPath:   DefaultSocketPath,
		MTU:          DefaultMTU,
		BridgePrefix: DefaultBridgePrefix,
		VXLANPrefix:  DefaultVXLANPrefix,
		DefaultVPC:   DefaultVPCName,
	}
}
