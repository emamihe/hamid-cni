//go:build linux

package netutil

import (
	"fmt"
	"runtime"

	"github.com/vishvananda/netns"
)

// WithNetNS executes fn inside the network namespace at path.
func WithNetNS(path string, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origns, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current netns: %w", err)
	}
	defer origns.Close()

	ns, err := netns.GetFromPath(path)
	if err != nil {
		return fmt.Errorf("get netns %s: %w", path, err)
	}
	defer ns.Close()

	if err := netns.Set(ns); err != nil {
		return fmt.Errorf("set netns: %w", err)
	}
	defer func() { _ = netns.Set(origns) }()

	return fn()
}
