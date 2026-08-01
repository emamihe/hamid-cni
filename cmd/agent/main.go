//go:build linux

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/hamid/hamid-cni/pkg/agent"
	"github.com/hamid/hamid-cni/pkg/config"
	"github.com/hamid/hamid-cni/pkg/version"
	"k8s.io/klog/v2"
)

func main() {
	klog.InitFlags(nil)
	cfg := config.DefaultAgentConfig()
	flag.StringVar(&cfg.NodeName, "node-name", cfg.NodeName, "Kubernetes node name")
	flag.StringVar(&cfg.SocketPath, "socket", cfg.SocketPath, "Unix socket path for CNI plugin")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", "", "Path to kubeconfig (out-of-cluster)")
	flag.StringVar(&cfg.UnderlayIface, "underlay-iface", "", "Underlay interface for VXLAN VTEP (default: auto)")
	flag.IntVar(&cfg.MTU, "mtu", cfg.MTU, "MTU for bridge/veth/vxlan")
	flag.StringVar(&cfg.BridgePrefix, "bridge-prefix", cfg.BridgePrefix, "Bridge name prefix")
	flag.StringVar(&cfg.VXLANPrefix, "vxlan-prefix", cfg.VXLANPrefix, "VXLAN device name prefix")
	flag.StringVar(&cfg.DefaultVPC, "default-vpc", cfg.DefaultVPC, "VPC used when a namespace has no network.hamid-cni.io/vpc annotation")
	flag.Parse()

	if cfg.NodeName == "" {
		klog.Fatal("node-name is required (set NODE_NAME or --node-name)")
	}

	klog.Infof("hamid-cni agent %s starting (default-vpc=%s)", version.Version, cfg.DefaultVPC)
	srv, err := agent.NewServer(cfg)
	if err != nil {
		klog.Fatalf("init agent: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Run(ctx); err != nil {
		klog.Fatalf("agent exited: %v", err)
	}
	os.Exit(0)
}
