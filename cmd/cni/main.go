package main

import (
	"os"

	"github.com/hamid/hamid-cni/pkg/cni"
)

func main() {
	// When invoked as a CNI plugin from /opt/cni/bin/hamid-cni
	cni.Main()
	os.Exit(0)
}
