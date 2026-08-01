package cni

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	cniversion "github.com/containernetworking/cni/pkg/version"
	"github.com/hamid/hamid-cni/pkg/agentapi"
	"github.com/hamid/hamid-cni/pkg/config"
)

func parseArgs(args string) (podName, podNS string) {
	for _, pair := range strings.Split(args, ";") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "K8S_POD_NAME":
			podName = kv[1]
		case "K8S_POD_NAMESPACE":
			podNS = kv[1]
		}
	}
	return
}

func cmdAdd(args *skel.CmdArgs) error {
	conf, err := config.LoadCNINetConf(args.StdinData)
	if err != nil {
		return err
	}
	podName, podNS := parseArgs(args.Args)
	req := &agentapi.Request{
		Command:      agentapi.CmdAdd,
		ContainerID:  args.ContainerID,
		NetNS:        args.Netns,
		IfName:       args.IfName,
		PodName:      podName,
		PodNamespace: podNS,
	}
	resp, err := agentapi.DialAndRoundTrip(conf.SocketPath, req)
	if err != nil {
		return err
	}
	result := &current.Result{CNIVersion: current.ImplementedSpecVersion}
	if resp.CNIVersion != "" {
		result.CNIVersion = resp.CNIVersion
	}
	for _, iface := range resp.Interfaces {
		result.Interfaces = append(result.Interfaces, &current.Interface{
			Name:    iface.Name,
			Mac:     iface.MAC,
			Sandbox: iface.Sandbox,
		})
	}
	for i, ip := range resp.IPs {
		addr, err := cniTypes.ParseCIDR(ip.Address)
		if err != nil {
			return fmt.Errorf("parse address: %w", err)
		}
		ipc := &current.IPConfig{
			Interface: current.Int(1), // container iface index in result
			Address:   *addr,
		}
		if ip.Gateway != "" {
			ipc.Gateway = net.ParseIP(ip.Gateway)
		}
		if i == 0 && len(result.Interfaces) < 2 {
			ipc.Interface = current.Int(0)
		}
		result.IPs = append(result.IPs, ipc)
	}
	return cniTypes.PrintResult(result, conf.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	conf, err := config.LoadCNINetConf(args.StdinData)
	if err != nil {
		return err
	}
	podName, podNS := parseArgs(args.Args)
	req := &agentapi.Request{
		Command:      agentapi.CmdDel,
		ContainerID:  args.ContainerID,
		NetNS:        args.Netns,
		IfName:       args.IfName,
		PodName:      podName,
		PodNamespace: podNS,
	}
	_, err = agentapi.DialAndRoundTrip(conf.SocketPath, req)
	// DEL must be idempotent; ignore agent errors if socket missing during uninstall.
	if err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(os.Stderr, "hamid-cni DEL warning: %v\n", err)
	}
	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	conf, err := config.LoadCNINetConf(args.StdinData)
	if err != nil {
		return err
	}
	podName, podNS := parseArgs(args.Args)
	req := &agentapi.Request{
		Command:      agentapi.CmdCheck,
		ContainerID:  args.ContainerID,
		NetNS:        args.Netns,
		IfName:       args.IfName,
		PodName:      podName,
		PodNamespace: podNS,
	}
	_, err = agentapi.DialAndRoundTrip(conf.SocketPath, req)
	return err
}

// Main is the CNI plugin entrypoint.
func Main() {
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add:   cmdAdd,
		Del:   cmdDel,
		Check: cmdCheck,
	}, cniversion.All, "hamid-cni")
}

// DumpConf is unused helper for debugging.
func DumpConf(b []byte) string {
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	out, _ := json.Marshal(m)
	return string(out)
}
