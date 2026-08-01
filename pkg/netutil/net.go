//go:build linux

package netutil

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// GenerateMAC returns a locally-administered unicast MAC.
func GenerateMAC() (net.HardwareAddr, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	buf[0] = (buf[0] | 0x02) & 0xfe
	return net.HardwareAddr(buf), nil
}

// HostVethName returns a host-side veth name derived from container ID.
func HostVethName(containerID string) string {
	const max = 15 // IFNAMSIZ-1
	name := "hv" + containerID
	if len(name) > max {
		name = name[:max]
	}
	return name
}

// BridgeName builds the Linux bridge name for a VPC.
func BridgeName(prefix, vpc string) string {
	name := prefix + "-" + vpc
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}

// VXLANName builds the VXLAN device name for a VPC.
func VXLANName(prefix, vpc string) string {
	name := prefix + "-" + vpc
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}

// EnsureBridge creates a Linux bridge if it does not exist and brings it up.
func EnsureBridge(name string, mtu int) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		if err := netlink.LinkSetUp(link); err != nil {
			return nil, err
		}
		return link, nil
	}
	if _, ok := err.(netlink.LinkNotFoundError); !ok {
		return nil, err
	}

	br := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: name,
			MTU:  mtu,
		},
	}
	if err := netlink.LinkAdd(br); err != nil {
		return nil, fmt.Errorf("add bridge %s: %w", name, err)
	}
	link, err = netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, err
	}
	return link, nil
}

// EnsureVXLAN creates a VXLAN device attached to the given bridge.
// vtepIP is the local underlay IP used as the VTEP source.
func EnsureVXLAN(name string, vni int, vtepIP net.IP, parentIndex, mtu int, bridge netlink.Link) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		if err := netlink.LinkSetUp(link); err != nil {
			return nil, err
		}
		return link, nil
	}
	if _, ok := err.(netlink.LinkNotFoundError); !ok {
		return nil, err
	}

	vx := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name: name,
			MTU:  mtu,
		},
		VxlanId:      vni,
		VtepDevIndex: parentIndex,
		SrcAddr:      vtepIP,
		Port:         4789,
		Learning:     false,
		GBP:          false,
		FlowBased:    false,
	}
	if err := netlink.LinkAdd(vx); err != nil {
		return nil, fmt.Errorf("add vxlan %s: %w", name, err)
	}
	link, err = netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	if err := netlink.LinkSetMaster(link, bridge); err != nil {
		_ = netlink.LinkDel(link)
		return nil, fmt.Errorf("enslave vxlan to bridge: %w", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, err
	}
	return link, nil
}

// DefaultRouteInterface returns the link used by the default IPv4 route and its IP.
func DefaultRouteInterface() (netlink.Link, net.IP, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, nil, err
	}
	var def *netlink.Route
	for i := range routes {
		r := &routes[i]
		if r.Dst == nil || (r.Dst.IP.Equal(net.IPv4zero) && maskLen(r.Dst) == 0) {
			def = r
			break
		}
	}
	if def == nil {
		return nil, nil, fmt.Errorf("no default route found")
	}
	link, err := netlink.LinkByIndex(def.LinkIndex)
	if err != nil {
		return nil, nil, err
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return nil, nil, err
	}
	for _, a := range addrs {
		if a.IP != nil && !a.IP.IsLoopback() {
			return link, a.IP, nil
		}
	}
	return nil, nil, fmt.Errorf("no IPv4 address on underlay %s", link.Attrs().Name)
}

func maskLen(n *net.IPNet) int {
	ones, _ := n.Mask.Size()
	return ones
}

// SetupVeth creates a veth pair, moves peer into netns, then renames it to contName.
// The peer must NOT be created as contName (usually "eth0") in the host netns — that
// collides with the node's own eth0 and fails with "file exists".
func SetupVeth(hostName, contName, netnsPath string, mtu int, mac net.HardwareAddr) (host netlink.Link, contMAC net.HardwareAddr, err error) {
	// Clean up a leftover host veth from a previous failed ADD with the same container ID.
	_ = DeleteLinkByName(hostName)

	peerTemp, err := tempIfaceName("vp")
	if err != nil {
		return nil, nil, err
	}

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: hostName,
			MTU:  mtu,
		},
		PeerName: peerTemp,
	}
	if mac != nil {
		veth.PeerHardwareAddr = mac
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return nil, nil, fmt.Errorf("create veth: %w", err)
	}

	hostLink, err := netlink.LinkByName(hostName)
	if err != nil {
		return nil, nil, err
	}
	peerLink, err := netlink.LinkByName(peerTemp)
	if err != nil {
		_ = netlink.LinkDel(hostLink)
		return nil, nil, err
	}
	contMAC = peerLink.Attrs().HardwareAddr

	nsFile, err := os.Open(netnsPath)
	if err != nil {
		_ = netlink.LinkDel(hostLink)
		return nil, nil, fmt.Errorf("open netns: %w", err)
	}
	defer nsFile.Close()

	if err := netlink.LinkSetNsFd(peerLink, int(nsFile.Fd())); err != nil {
		_ = netlink.LinkDel(hostLink)
		return nil, nil, fmt.Errorf("move peer to netns: %w", err)
	}

	if err := WithNetNS(netnsPath, func() error {
		link, err := netlink.LinkByName(peerTemp)
		if err != nil {
			return fmt.Errorf("lookup peer in netns: %w", err)
		}
		if link.Attrs().Name != contName {
			if err := netlink.LinkSetName(link, contName); err != nil {
				return fmt.Errorf("rename peer to %s: %w", contName, err)
			}
		}
		return nil
	}); err != nil {
		_ = netlink.LinkDel(hostLink)
		return nil, nil, err
	}

	return hostLink, contMAC, nil
}

// tempIfaceName returns a short unique interface name with the given prefix.
func tempIfaceName(prefix string) (string, error) {
	entropy := make([]byte, 4)
	if _, err := rand.Read(entropy); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s%x", prefix, entropy)
	if len(name) > 15 {
		name = name[:15]
	}
	return name, nil
}

// ConfigureContainerIface sets IP, MAC, routes inside the pod netns.
func ConfigureContainerIface(netnsPath, ifName string, ipNet *net.IPNet, gateway net.IP, mtu int, mac net.HardwareAddr) error {
	return WithNetNS(netnsPath, func() error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return err
		}
		if mac != nil {
			if err := netlink.LinkSetHardwareAddr(link, mac); err != nil {
				return err
			}
		}
		if err := netlink.LinkSetMTU(link, mtu); err != nil {
			return err
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return err
		}
		addr := &netlink.Addr{IPNet: ipNet}
		if err := netlink.AddrAdd(link, addr); err != nil && !os.IsExist(err) {
			// EEXIST is fine on retry
			if errno, ok := err.(syscall.Errno); !ok || errno != unix.EEXIST {
				return fmt.Errorf("addr add: %w", err)
			}
		}
		// Default route via gateway (on-link for L2 VPC).
		route := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Dst:       nil,
			Gw:        gateway,
			Scope:     netlink.SCOPE_UNIVERSE,
		}
		if err := netlink.RouteReplace(route); err != nil {
			// Fall back to on-link /32 style if gateway route fails
			onlink := &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst: &net.IPNet{
					IP:   gateway,
					Mask: net.CIDRMask(32, 32),
				},
				Scope: netlink.SCOPE_LINK,
			}
			_ = netlink.RouteReplace(onlink)
			route.Gw = gateway
			if err2 := netlink.RouteReplace(route); err2 != nil {
				return fmt.Errorf("default route: %w (fallback: %v)", err, err2)
			}
		}
		return nil
	})
}

// AttachToBridge enslaves a host link to a bridge and brings it up.
func AttachToBridge(link, bridge netlink.Link) error {
	if err := netlink.LinkSetMaster(link, bridge); err != nil {
		return err
	}
	return netlink.LinkSetUp(link)
}

// DeleteLinkByName removes a link if present.
func DeleteLinkByName(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil
		}
		return err
	}
	return netlink.LinkDel(link)
}

// AddFDB adds a static bridge FDB entry pointing a MAC at a remote VTEP.
func AddFDB(vxlan netlink.Link, mac net.HardwareAddr, remoteIP net.IP) error {
	return netlink.NeighSet(&netlink.Neigh{
		LinkIndex:    vxlan.Attrs().Index,
		State:        netlink.NUD_PERMANENT,
		HardwareAddr: mac,
		IP:           remoteIP,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
	})
}

// DelFDB removes a static FDB entry.
func DelFDB(vxlan netlink.Link, mac net.HardwareAddr, remoteIP net.IP) error {
	err := netlink.NeighDel(&netlink.Neigh{
		LinkIndex:    vxlan.Attrs().Index,
		HardwareAddr: mac,
		IP:           remoteIP,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
	})
	if err != nil && err != syscall.ENOENT && err != unix.ENOENT {
		return err
	}
	return nil
}

// AddNeigh adds a permanent ARP neigh entry on the bridge for a remote pod.
func AddNeigh(bridge netlink.Link, ip net.IP, mac net.HardwareAddr) error {
	return netlink.NeighSet(&netlink.Neigh{
		LinkIndex:    bridge.Attrs().Index,
		State:        netlink.NUD_PERMANENT,
		IP:           ip,
		HardwareAddr: mac,
		Family:       unix.AF_INET,
	})
}

// DelNeigh removes an ARP neigh entry.
func DelNeigh(bridge netlink.Link, ip net.IP) error {
	err := netlink.NeighDel(&netlink.Neigh{
		LinkIndex: bridge.Attrs().Index,
		IP:        ip,
		Family:    unix.AF_INET,
	})
	if err != nil && err != syscall.ENOENT && err != unix.ENOENT {
		return err
	}
	return nil
}

// EnableProxyARP turns on proxy_arp for a bridge so pods can reach remotes.
func EnableProxyARP(bridgeName string) error {
	path := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/proxy_arp", bridgeName)
	return os.WriteFile(path, []byte("1\n"), 0644)
}
