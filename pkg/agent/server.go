//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	networkv1alpha1 "github.com/hamid/hamid-cni/api/v1alpha1"
	"github.com/hamid/hamid-cni/pkg/agentapi"
	"github.com/hamid/hamid-cni/pkg/config"
	"github.com/hamid/hamid-cni/pkg/ipam"
	"github.com/hamid/hamid-cni/pkg/netutil"
	"github.com/hamid/hamid-cni/pkg/vpcnet"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// Server is the node agent: IPAM, pod datapath, VXLAN peer sync.
type Server struct {
	cfg       config.AgentConfig
	kube      kubernetes.Interface
	restCfg   *rest.Config
	vpcClient *rest.RESTClient
	ipam      *ipam.Allocator
	net       *vpcnet.Manager

	mu       sync.Mutex
	nodeIPs  map[string]net.IP // node name -> underlay IP
}

func NewServer(cfg config.AgentConfig) (*Server, error) {
	restCfg, err := buildRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}
	kube, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	if err := networkv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	crCfg := rest.CopyConfig(restCfg)
	crCfg.GroupVersion = &networkv1alpha1.SchemeGroupVersion
	crCfg.APIPath = "/apis"
	crCfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
	vpcClient, err := rest.RESTClientFor(crCfg)
	if err != nil {
		return nil, err
	}
	alloc, err := ipam.NewAllocator(restCfg)
	if err != nil {
		return nil, err
	}
	netMgr, err := vpcnet.NewManager(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:       cfg,
		kube:      kube,
		restCfg:   restCfg,
		vpcClient: vpcClient,
		ipam:      alloc,
		net:       netMgr,
		nodeIPs:   make(map[string]net.IP),
	}, nil
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func (s *Server) Run(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.cfg.SocketPath), 0755); err != nil {
		return err
	}
	_ = os.Remove(s.cfg.SocketPath)

	ln, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.cfg.SocketPath, 0600); err != nil {
		klog.Warningf("chmod socket: %v", err)
	}
	defer ln.Close()
	defer os.Remove(s.cfg.SocketPath)

	go s.watchNodes(ctx)
	go s.watchAllocations(ctx)
	go s.periodicAllocSync(ctx)

	klog.Infof("hamid-cni agent listening on %s (node=%s)", s.cfg.SocketPath, s.cfg.NodeName)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				klog.Errorf("accept: %v", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	req := &agentapi.Request{}
	if err := dec.Decode(req); err != nil {
		_ = enc.Encode(&agentapi.Response{Error: err.Error(), Code: 1})
		return
	}
	resp := s.dispatch(ctx, req)
	_ = enc.Encode(resp)
}

func (s *Server) dispatch(ctx context.Context, req *agentapi.Request) *agentapi.Response {
	switch req.Command {
	case agentapi.CmdAdd:
		return s.handleAdd(ctx, req)
	case agentapi.CmdDel:
		return s.handleDel(ctx, req)
	case agentapi.CmdCheck:
		return &agentapi.Response{CNIVersion: "1.0.0"}
	case agentapi.CmdVersion:
		return &agentapi.Response{CNIVersion: "1.0.0"}
	default:
		return &agentapi.Response{Error: fmt.Sprintf("unknown command %q", req.Command), Code: 1}
	}
}

func (s *Server) handleAdd(ctx context.Context, req *agentapi.Request) *agentapi.Response {
	if req.PodNamespace == "" || req.PodName == "" {
		return errResp("missing pod name/namespace in CNI request")
	}

	vpcName, err := s.resolveVPC(ctx, req.PodNamespace)
	if err != nil {
		return errResp(err.Error())
	}
	vpc, err := s.getVPC(ctx, vpcName)
	if err != nil {
		return errResp(err.Error())
	}

	mac, err := netutil.GenerateMAC()
	if err != nil {
		return errResp(err.Error())
	}

	result, err := s.ipam.Allocate(ctx, vpc, ipam.Request{
		VPC:          vpcName,
		PodNamespace: req.PodNamespace,
		PodName:      req.PodName,
		Node:         s.cfg.NodeName,
		InterfaceID:  req.IfName,
		MAC:          mac.String(),
	})
	if err != nil {
		return errResp(fmt.Sprintf("ipam: %v", err))
	}
	if result.MAC != "" {
		if parsed, err := net.ParseMAC(result.MAC); err == nil {
			mac = parsed
		}
	} else {
		result.MAC = mac.String()
	}

	hostName := netutil.HostVethName(req.ContainerID)
	hostLink, contMAC, err := netutil.SetupVeth(hostName, req.IfName, req.NetNS, s.cfg.MTU, mac)
	if err != nil {
		_ = s.ipam.Release(ctx, vpcName, req.PodNamespace, req.PodName)
		return errResp(fmt.Sprintf("veth: %v", err))
	}
	mac = contMAC

	if err := s.net.AttachPod(vpcName, int(vpc.Spec.VXLANID), hostLink); err != nil {
		_ = netutil.DeleteLinkByName(hostName)
		_ = s.ipam.Release(ctx, vpcName, req.PodNamespace, req.PodName)
		return errResp(fmt.Sprintf("attach: %v", err))
	}

	ipNet := &net.IPNet{IP: result.IP, Mask: result.CIDR.Mask}
	if err := netutil.ConfigureContainerIface(req.NetNS, req.IfName, ipNet, result.Gateway, s.cfg.MTU, mac); err != nil {
		_ = netutil.DeleteLinkByName(hostName)
		_ = s.ipam.Release(ctx, vpcName, req.PodNamespace, req.PodName)
		return errResp(fmt.Sprintf("configure container: %v", err))
	}

	// Persist MAC on allocation for remote FDB sync.
	_ = s.updateAllocMAC(ctx, result.AllocName, mac.String())

	ones, _ := result.CIDR.Mask.Size()
	return &agentapi.Response{
		CNIVersion: "1.0.0",
		Interfaces: []agentapi.Interface{
			{Name: hostName, MAC: hostLink.Attrs().HardwareAddr.String()},
			{Name: req.IfName, MAC: mac.String(), Sandbox: req.NetNS},
		},
		IPs: []agentapi.IPConfig{{
			Address: fmt.Sprintf("%s/%d", result.IP.String(), ones),
			Gateway: result.Gateway.String(),
		}},
	}
}

func (s *Server) handleDel(ctx context.Context, req *agentapi.Request) *agentapi.Response {
	hostName := netutil.HostVethName(req.ContainerID)
	_ = netutil.DeleteLinkByName(hostName)
	if req.PodNamespace != "" && req.PodName != "" {
		if err := s.ipam.Release(ctx, "", req.PodNamespace, req.PodName); err != nil {
			klog.Warningf("release IP for %s/%s: %v", req.PodNamespace, req.PodName, err)
		}
	}
	return &agentapi.Response{CNIVersion: "1.0.0"}
}

func (s *Server) resolveVPC(ctx context.Context, namespace string) (string, error) {
	ns, err := s.kube.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get namespace: %w", err)
	}
	vpc, err := ResolveVPCName(ns.Annotations[networkv1alpha1.VPCAnnotation], s.cfg.DefaultVPC)
	if err != nil {
		return "", fmt.Errorf("namespace %q: %w", namespace, err)
	}
	if ns.Annotations[networkv1alpha1.VPCAnnotation] == "" {
		klog.V(4).Infof("namespace %q has no VPC annotation; using default VPC %q", namespace, vpc)
	}
	return vpc, nil
}

func (s *Server) getVPC(ctx context.Context, name string) (*networkv1alpha1.VPC, error) {
	vpc := &networkv1alpha1.VPC{}
	err := s.vpcClient.Get().Resource("vpcs").Name(name).Do(ctx).Into(vpc)
	if err != nil {
		return nil, fmt.Errorf("get VPC %q: %w", name, err)
	}
	return vpc, nil
}

func (s *Server) updateAllocMAC(ctx context.Context, name, mac string) error {
	alloc := &networkv1alpha1.IPAllocation{}
	if err := s.vpcClient.Get().Resource("ipallocations").Name(name).Do(ctx).Into(alloc); err != nil {
		return err
	}
	alloc.Spec.MAC = mac
	return s.vpcClient.Put().Resource("ipallocations").Name(name).Body(alloc).Do(ctx).Error()
}

func errResp(msg string) *agentapi.Response {
	return &agentapi.Response{Error: msg, Code: 1, CNIVersion: "1.0.0"}
}

func (s *Server) watchNodes(ctx context.Context) {
	for {
		if err := s.watchNodesOnce(ctx); err != nil {
			klog.Errorf("node watch: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (s *Server) watchNodesOnce(ctx context.Context) error {
	list, err := s.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		s.rememberNode(&list.Items[i])
	}
	w, err := s.kube.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{ResourceVersion: list.ResourceVersion})
	if err != nil {
		return err
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.ResultChan():
			if !ok {
				return fmt.Errorf("node watch closed")
			}
			node, ok := ev.Object.(*corev1.Node)
			if !ok {
				continue
			}
			switch ev.Type {
			case watch.Added, watch.Modified:
				s.rememberNode(node)
			case watch.Deleted:
				s.mu.Lock()
				delete(s.nodeIPs, node.Name)
				s.mu.Unlock()
			}
		}
	}
}

func (s *Server) rememberNode(node *corev1.Node) {
	var ip net.IP
	for _, a := range node.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			ip = net.ParseIP(a.Address)
			break
		}
	}
	if ip == nil {
		return
	}
	s.mu.Lock()
	s.nodeIPs[node.Name] = ip
	s.mu.Unlock()
}

func (s *Server) nodeIP(name string) net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nodeIPs[name]
}

func (s *Server) watchAllocations(ctx context.Context) {
	for {
		if err := s.watchAllocationsOnce(ctx); err != nil {
			klog.Errorf("allocation watch: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (s *Server) periodicAllocSync(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			list := &networkv1alpha1.IPAllocationList{}
			if err := s.vpcClient.Get().Resource("ipallocations").Do(ctx).Into(list); err != nil {
				klog.Warningf("periodic alloc list: %v", err)
				continue
			}
			for i := range list.Items {
				s.syncAllocation(ctx, &list.Items[i], false)
			}
		}
	}
}

func (s *Server) watchAllocationsOnce(ctx context.Context) error {
	list := &networkv1alpha1.IPAllocationList{}
	if err := s.vpcClient.Get().Resource("ipallocations").Do(ctx).Into(list); err != nil {
		return err
	}
	for i := range list.Items {
		s.syncAllocation(ctx, &list.Items[i], false)
	}
	opts := metav1.ListOptions{ResourceVersion: list.ResourceVersion}
	w, err := s.vpcClient.Get().
		Resource("ipallocations").
		VersionedParams(&opts, metav1.ParameterCodec).
		Watch(ctx)
	if err != nil {
		return err
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.ResultChan():
			if !ok {
				return fmt.Errorf("allocation watch closed")
			}
			alloc, ok := ev.Object.(*networkv1alpha1.IPAllocation)
			if !ok {
				continue
			}
			switch ev.Type {
			case watch.Added, watch.Modified:
				s.syncAllocation(ctx, alloc, false)
			case watch.Deleted:
				s.syncAllocation(ctx, alloc, true)
			}
		}
	}
}

func (s *Server) syncAllocation(ctx context.Context, alloc *networkv1alpha1.IPAllocation, deleted bool) {
	if alloc.Spec.Node == "" || alloc.Spec.Node == s.cfg.NodeName {
		return // local pods are on the bridge via veth; no FDB needed
	}
	if alloc.Spec.MAC == "" || alloc.Spec.IP == "" {
		return
	}
	mac, err := net.ParseMAC(alloc.Spec.MAC)
	if err != nil {
		return
	}
	podIP := net.ParseIP(alloc.Spec.IP)
	remote := s.nodeIP(alloc.Spec.Node)
	if remote == nil {
		klog.V(4).Infof("no underlay IP yet for node %s", alloc.Spec.Node)
		return
	}
	vpc, err := s.getVPC(ctx, alloc.Spec.VPC)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Warningf("sync alloc get vpc: %v", err)
		}
		return
	}
	if deleted {
		_ = s.net.RemoveRemote(alloc.Spec.VPC, podIP, mac, remote)
		return
	}
	if err := s.net.SyncRemote(alloc.Spec.VPC, int(vpc.Spec.VXLANID), podIP, mac, remote); err != nil {
		klog.Warningf("sync remote %s/%s: %v", alloc.Spec.PodNamespace, alloc.Spec.PodName, err)
	}
}

// Ensure namespace annotation helper used by examples — not required at runtime.
func Sanitize(s string) string { return strings.TrimSpace(s) }
