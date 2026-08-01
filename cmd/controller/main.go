package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/hamid/hamid-cni/pkg/controller"
	"github.com/hamid/hamid-cni/pkg/version"
	"k8s.io/klog/v2"
)

func main() {
	klog.InitFlags(nil)
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig (out-of-cluster)")
	flag.Parse()

	klog.Infof("hamid-cni controller %s starting", version.Version)
	c, err := controller.New(*kubeconfig)
	if err != nil {
		klog.Fatalf("init controller: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := c.Run(ctx); err != nil {
		klog.Fatalf("controller exited: %v", err)
	}
	os.Exit(0)
}
