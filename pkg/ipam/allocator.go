package ipam

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	networkv1alpha1 "github.com/hamid/hamid-cni/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

// Allocator allocates and releases pod IPs from VPC CIDRs using IPAllocation CRDs.
// Allocation object names are derived from VPC+IP so concurrent node agents cannot
// claim the same address (AlreadyExists forces retry with the next free IP).
type Allocator struct {
	client *rest.RESTClient
	mu     sync.Mutex
}

func NewAllocator(cfg *rest.Config) (*Allocator, error) {
	scheme := runtime.NewScheme()
	if err := networkv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	cfg = rest.CopyConfig(cfg)
	cfg.GroupVersion = &networkv1alpha1.SchemeGroupVersion
	cfg.APIPath = "/apis"
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
	if cfg.UserAgent == "" {
		cfg.UserAgent = rest.DefaultKubernetesUserAgent()
	}
	rc, err := rest.RESTClientFor(cfg)
	if err != nil {
		return nil, err
	}
	return &Allocator{client: rc}, nil
}

// Request describes a pod needing an IP.
type Request struct {
	VPC          string
	PodNamespace string
	PodName      string
	Node         string
	InterfaceID  string
	MAC          string
}

// Result is a successful allocation.
type Result struct {
	IP        net.IP
	PrefixLen int
	Gateway   net.IP
	MAC       string
	VXLANID   int32
	CIDR      *net.IPNet
	AllocName string
}

func podKey(ns, pod string) string {
	return ns + "/" + pod
}

func ipAllocName(vpc string, ip net.IP) string {
	n := strings.ToLower(vpc + "-" + strings.ReplaceAll(ip.String(), ".", "-"))
	n = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, n)
	if len(n) > 63 {
		n = n[:63]
	}
	return strings.Trim(n, "-")
}

// Allocate finds or creates an IPAllocation for the pod within the VPC CIDR.
func (a *Allocator) Allocate(ctx context.Context, vpc *networkv1alpha1.VPC, req Request) (*Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	_, cidr, err := net.ParseCIDR(vpc.Spec.CIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid VPC CIDR %q: %w", vpc.Spec.CIDR, err)
	}
	gateway := gatewayIP(vpc, cidr)
	ones, _ := cidr.Mask.Size()

	if existing, err := a.findByPod(ctx, req.VPC, req.PodNamespace, req.PodName); err == nil && existing != nil {
		ip := net.ParseIP(existing.Spec.IP)
		if req.MAC != "" && existing.Spec.MAC != req.MAC {
			existing.Spec.MAC = req.MAC
			existing.Spec.Node = req.Node
			_ = a.updateAllocation(ctx, existing)
		}
		return &Result{
			IP:        ip,
			PrefixLen: ones,
			Gateway:   gateway,
			MAC:       existing.Spec.MAC,
			VXLANID:   vpc.Spec.VXLANID,
			CIDR:      cidr,
			AllocName: existing.Name,
		}, nil
	}

	used, err := a.usedIPs(ctx, req.VPC)
	if err != nil {
		return nil, err
	}
	for _, ex := range vpc.Spec.ExcludeIPs {
		if ip := net.ParseIP(ex); ip != nil {
			used[ip.String()] = true
		}
	}
	used[gateway.String()] = true
	used[cidr.IP.String()] = true
	if bcast := broadcast(cidr); bcast != nil {
		used[bcast.String()] = true
	}

	// Retry loop handles races across node agents.
	for attempt := 0; attempt < 64; attempt++ {
		ip, err := nextIP(cidr, used)
		if err != nil {
			return nil, err
		}
		name := ipAllocName(req.VPC, ip)
		alloc := &networkv1alpha1.IPAllocation{
			TypeMeta: metav1.TypeMeta{
				APIVersion: networkv1alpha1.SchemeGroupVersion.String(),
				Kind:       "IPAllocation",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"network.hamid-cni.io/vpc":         req.VPC,
					"network.hamid-cni.io/node":        sanitizeLabel(req.Node),
					"network.hamid-cni.io/pod-namespace": sanitizeLabel(req.PodNamespace),
					"network.hamid-cni.io/pod-name":     sanitizeLabel(req.PodName),
				},
				Annotations: map[string]string{
					"network.hamid-cni.io/pod": podKey(req.PodNamespace, req.PodName),
				},
			},
			Spec: networkv1alpha1.IPAllocationSpec{
				VPC:          req.VPC,
				PodNamespace: req.PodNamespace,
				PodName:      req.PodName,
				Node:         req.Node,
				IP:           ip.String(),
				MAC:          req.MAC,
				InterfaceID:  req.InterfaceID,
			},
			Status: networkv1alpha1.IPAllocationStatus{
				Phase: networkv1alpha1.IPAllocationPhaseAllocated,
			},
		}
		if err := a.createAllocation(ctx, alloc); err != nil {
			if apierrors.IsAlreadyExists(err) {
				used[ip.String()] = true
				continue
			}
			return nil, err
		}
		return &Result{
			IP:        ip,
			PrefixLen: ones,
			Gateway:   gateway,
			MAC:       req.MAC,
			VXLANID:   vpc.Spec.VXLANID,
			CIDR:      cidr,
			AllocName: name,
		}, nil
	}
	return nil, fmt.Errorf("failed to allocate IP after retries in VPC %s", req.VPC)
}

// Release deletes the IPAllocation for a pod.
func (a *Allocator) Release(ctx context.Context, vpcHint, podNamespace, podName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	alloc, err := a.findByPod(ctx, vpcHint, podNamespace, podName)
	if err != nil {
		return err
	}
	if alloc == nil {
		// Fallback: scan all
		alloc, err = a.findByPod(ctx, "", podNamespace, podName)
		if err != nil || alloc == nil {
			return nil
		}
	}
	err = a.client.Delete().
		Resource("ipallocations").
		Name(alloc.Name).
		Do(ctx).
		Error()
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (a *Allocator) findByPod(ctx context.Context, vpc, ns, pod string) (*networkv1alpha1.IPAllocation, error) {
	list := &networkv1alpha1.IPAllocationList{}
	err := a.client.Get().Resource("ipallocations").Do(ctx).Into(list)
	if err != nil {
		return nil, err
	}
	key := podKey(ns, pod)
	for i := range list.Items {
		item := &list.Items[i]
		if vpc != "" && item.Spec.VPC != vpc {
			continue
		}
		if item.Spec.PodNamespace == ns && item.Spec.PodName == pod {
			return item, nil
		}
		if item.Annotations["network.hamid-cni.io/pod"] == key {
			return item, nil
		}
	}
	return nil, nil
}

func (a *Allocator) createAllocation(ctx context.Context, alloc *networkv1alpha1.IPAllocation) error {
	result := &networkv1alpha1.IPAllocation{}
	return a.client.Post().
		Resource("ipallocations").
		Body(alloc).
		Do(ctx).
		Into(result)
}

func (a *Allocator) updateAllocation(ctx context.Context, alloc *networkv1alpha1.IPAllocation) error {
	return a.client.Put().
		Resource("ipallocations").
		Name(alloc.Name).
		Body(alloc).
		Do(ctx).
		Error()
}

func (a *Allocator) usedIPs(ctx context.Context, vpc string) (map[string]bool, error) {
	list := &networkv1alpha1.IPAllocationList{}
	err := a.client.Get().
		Resource("ipallocations").
		Do(ctx).
		Into(list)
	if err != nil {
		return nil, err
	}
	used := make(map[string]bool)
	for _, item := range list.Items {
		if item.Spec.VPC == vpc && item.Spec.IP != "" {
			used[item.Spec.IP] = true
		}
	}
	return used, nil
}

// ListByVPC returns all allocations for a VPC.
func (a *Allocator) ListByVPC(ctx context.Context, vpc string) ([]networkv1alpha1.IPAllocation, error) {
	list := &networkv1alpha1.IPAllocationList{}
	err := a.client.Get().
		Resource("ipallocations").
		Do(ctx).
		Into(list)
	if err != nil {
		return nil, err
	}
	var out []networkv1alpha1.IPAllocation
	for _, item := range list.Items {
		if item.Spec.VPC == vpc {
			out = append(out, item)
		}
	}
	return out, nil
}

func gatewayIP(vpc *networkv1alpha1.VPC, cidr *net.IPNet) net.IP {
	if vpc.Spec.Gateway != "" {
		if ip := net.ParseIP(vpc.Spec.Gateway); ip != nil {
			return ip.To4()
		}
	}
	ip := make(net.IP, len(cidr.IP.To4()))
	copy(ip, cidr.IP.To4())
	ip[3]++
	return ip
}

func nextIP(cidr *net.IPNet, used map[string]bool) (net.IP, error) {
	ip := make(net.IP, 4)
	copy(ip, cidr.IP.To4())
	for {
		incIP(ip)
		if !cidr.Contains(ip) {
			return nil, fmt.Errorf("no free IPs in %s", cidr.String())
		}
		if !used[ip.String()] {
			out := make(net.IP, 4)
			copy(out, ip)
			return out, nil
		}
	}
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func broadcast(cidr *net.IPNet) net.IP {
	ip := cidr.IP.To4()
	if ip == nil {
		return nil
	}
	mask := net.IP(cidr.Mask).To4()
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}

func sanitizeLabel(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			return r
		}
		return '-'
	}, s)
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.Trim(s, "-.")
}
