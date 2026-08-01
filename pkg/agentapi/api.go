package agentapi

import (
	"encoding/json"
	"fmt"
	"net"
)

// Protocol spoken over the Unix domain socket between CNI plugin and node agent.

type Command string

const (
	CmdAdd  Command = "ADD"
	CmdDel  Command = "DEL"
	CmdCheck Command = "CHECK"
	CmdVersion Command = "VERSION"
)

type Request struct {
	Command     Command `json:"command"`
	ContainerID string  `json:"containerID"`
	NetNS       string  `json:"netns"`
	IfName      string  `json:"ifName"`
	// K8S_POD_NAME / K8S_POD_NAMESPACE / K8S_POD_INFRA_CONTAINER_ID from CNI_ARGS
	PodName      string `json:"podName"`
	PodNamespace string `json:"podNamespace"`
}

type IPConfig struct {
	Address string `json:"address"` // CIDR
	Gateway string `json:"gateway"`
}

type Interface struct {
	Name    string `json:"name"`
	MAC     string `json:"mac,omitempty"`
	Sandbox string `json:"sandbox,omitempty"`
}

type Response struct {
	CNIVersion string      `json:"cniVersion"`
	Interfaces []Interface `json:"interfaces,omitempty"`
	IPs        []IPConfig  `json:"ips,omitempty"`
	Error      string      `json:"error,omitempty"`
	Code       int         `json:"code,omitempty"`
}

func Encode(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func DecodeRequest(b []byte) (*Request, error) {
	r := &Request{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	return r, nil
}

func DecodeResponse(b []byte) (*Response, error) {
	r := &Response{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	return r, nil
}

// DialAndRoundTrip sends one request and reads one response over a Unix socket.
func DialAndRoundTrip(socketPath string, req *Request) (*Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial agent %s: %w", socketPath, err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	if err := enc.Encode(req); err != nil {
		return nil, err
	}
	resp := &Response{}
	if err := dec.Decode(resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}
