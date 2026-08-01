//go:build linux

package vpcnet

import (
	"fmt"
	"net"
	"sync"

	"github.com/hamid/hamid-cni/pkg/config"
	"github.com/hamid/hamid-cni/pkg/netutil"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

// Manager maintains per-VPC bridges and VXLAN devices on a node.
type Manager struct {
	cfg    config.AgentConfig
	mu     sync.Mutex
	vteps  map[string]*vpcState // vpc name -> state
	parent netlink.Link
	vtepIP net.IP
}

type vpcState struct {
	bridge netlink.Link
	vxlan  netlink.Link
	vni    int
}

func NewManager(cfg config.AgentConfig) (*Manager, error) {
	m := &Manager{
		cfg:   cfg,
		vteps: make(map[string]*vpcState),
	}
	var err error
	if cfg.UnderlayIface != "" {
		m.parent, err = netlink.LinkByName(cfg.UnderlayIface)
		if err != nil {
			return nil, fmt.Errorf("underlay iface %s: %w", cfg.UnderlayIface, err)
		}
		addrs, err := netlink.AddrList(m.parent, netlink.FAMILY_V4)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if a.IP != nil && !a.IP.IsLoopback() {
				m.vtepIP = a.IP
				break
			}
		}
		if m.vtepIP == nil {
			return nil, fmt.Errorf("no IPv4 on underlay %s", cfg.UnderlayIface)
		}
	} else {
		m.parent, m.vtepIP, err = netutil.DefaultRouteInterface()
		if err != nil {
			return nil, fmt.Errorf("detect underlay: %w", err)
		}
	}
	klog.Infof("underlay interface=%s vtepIP=%s", m.parent.Attrs().Name, m.vtepIP)
	return m, nil
}

func (m *Manager) VTEPIP() net.IP { return m.vtepIP }

// EnsureVPC brings up bridge+VXLAN for a VPC on this node.
func (m *Manager) EnsureVPC(vpcName string, vni int) (*vpcState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if st, ok := m.vteps[vpcName]; ok {
		return st, nil
	}

	brName := netutil.BridgeName(m.cfg.BridgePrefix, vpcName)
	vxName := netutil.VXLANName(m.cfg.VXLANPrefix, vpcName)

	br, err := netutil.EnsureBridge(brName, m.cfg.MTU)
	if err != nil {
		return nil, err
	}
	if err := netutil.EnableProxyARP(brName); err != nil {
		klog.Warningf("proxy_arp on %s: %v", brName, err)
	}

	vx, err := netutil.EnsureVXLAN(vxName, vni, m.vtepIP, m.parent.Attrs().Index, m.cfg.MTU, br)
	if err != nil {
		return nil, err
	}

	st := &vpcState{bridge: br, vxlan: vx, vni: vni}
	m.vteps[vpcName] = st
	klog.Infof("ensured VPC overlay vpc=%s bridge=%s vxlan=%s vni=%d", vpcName, brName, vxName, vni)
	return st, nil
}

// AttachPod enslaves the host veth to the VPC bridge.
func (m *Manager) AttachPod(vpcName string, vni int, hostLink netlink.Link) error {
	st, err := m.EnsureVPC(vpcName, vni)
	if err != nil {
		return err
	}
	return netutil.AttachToBridge(hostLink, st.bridge)
}

// SyncRemote installs FDB + ARP entries for a remote pod, and a flood FDB to the VTEP.
func (m *Manager) SyncRemote(vpcName string, vni int, podIP net.IP, mac net.HardwareAddr, remoteNodeIP net.IP) error {
	st, err := m.EnsureVPC(vpcName, vni)
	if err != nil {
		return err
	}
	// BUM / unknown-unicast flood path to remote VTEP.
	zeroMAC, _ := net.ParseMAC("00:00:00:00:00:00")
	if err := netutil.AddFDB(st.vxlan, zeroMAC, remoteNodeIP); err != nil {
		klog.V(4).Infof("flood fdb to %s: %v", remoteNodeIP, err)
	}
	if err := netutil.AddFDB(st.vxlan, mac, remoteNodeIP); err != nil {
		return fmt.Errorf("fdb: %w", err)
	}
	if err := netutil.AddNeigh(st.bridge, podIP, mac); err != nil {
		return fmt.Errorf("neigh: %w", err)
	}
	return nil
}

// RemoveRemote clears FDB + ARP for a remote pod.
func (m *Manager) RemoveRemote(vpcName string, podIP net.IP, mac net.HardwareAddr, remoteNodeIP net.IP) error {
	m.mu.Lock()
	st, ok := m.vteps[vpcName]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if mac != nil && remoteNodeIP != nil {
		_ = netutil.DelFDB(st.vxlan, mac, remoteNodeIP)
	}
	if podIP != nil {
		_ = netutil.DelNeigh(st.bridge, podIP)
	}
	return nil
}

// BridgeFor returns the bridge link for a VPC if present.
func (m *Manager) BridgeFor(vpcName string) (netlink.Link, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.vteps[vpcName]
	if !ok {
		return nil, false
	}
	return st.bridge, true
}
