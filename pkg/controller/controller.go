package controller

import (
	"context"
	"fmt"
	"net"
	"time"

	networkv1alpha1 "github.com/hamid/hamid-cni/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// Controller validates VPC CRDs, enforces unique VNIs, and refreshes allocation counts.
type Controller struct {
	client *rest.RESTClient
}

func New(kubeconfig string) (*Controller, error) {
	var (
		cfg *rest.Config
		err error
	)
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	if err := networkv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	cfg = rest.CopyConfig(cfg)
	cfg.GroupVersion = &networkv1alpha1.SchemeGroupVersion
	cfg.APIPath = "/apis"
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
	rc, err := rest.RESTClientFor(cfg)
	if err != nil {
		return nil, err
	}
	return &Controller{client: rc}, nil
}

func (c *Controller) Run(ctx context.Context) error {
	klog.Info("hamid-cni controller started")
	return wait.PollUntilContextCancel(ctx, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := c.reconcile(ctx); err != nil {
			klog.Errorf("reconcile: %v", err)
		}
		return false, nil // keep looping
	})
}

func (c *Controller) reconcile(ctx context.Context) error {
	vpcs := &networkv1alpha1.VPCList{}
	if err := c.client.Get().Resource("vpcs").Do(ctx).Into(vpcs); err != nil {
		return err
	}
	allocs := &networkv1alpha1.IPAllocationList{}
	if err := c.client.Get().Resource("ipallocations").Do(ctx).Into(allocs); err != nil {
		return err
	}

	vniSeen := map[int32]string{}
	allocCount := map[string]int32{}
	for _, a := range allocs.Items {
		allocCount[a.Spec.VPC]++
	}

	for i := range vpcs.Items {
		vpc := &vpcs.Items[i]
		cond := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Valid",
			Message:            "VPC configuration is valid",
			LastTransitionTime: metav1.Now(),
		}
		if _, _, err := net.ParseCIDR(vpc.Spec.CIDR); err != nil {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "InvalidCIDR"
			cond.Message = err.Error()
		}
		if vpc.Spec.VXLANID < 1 || vpc.Spec.VXLANID > 16777215 {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "InvalidVXLANID"
			cond.Message = "vxlanID must be between 1 and 16777215"
		}
		if other, ok := vniSeen[vpc.Spec.VXLANID]; ok {
			cond.Status = metav1.ConditionFalse
			cond.Reason = "DuplicateVXLANID"
			cond.Message = fmt.Sprintf("vxlanID %d already used by VPC %s", vpc.Spec.VXLANID, other)
		} else {
			vniSeen[vpc.Spec.VXLANID] = vpc.Name
		}
		if vpc.Spec.Gateway != "" {
			if ip := net.ParseIP(vpc.Spec.Gateway); ip == nil {
				cond.Status = metav1.ConditionFalse
				cond.Reason = "InvalidGateway"
				cond.Message = "gateway is not a valid IP"
			} else if _, cidr, err := net.ParseCIDR(vpc.Spec.CIDR); err == nil && !cidr.Contains(ip) {
				cond.Status = metav1.ConditionFalse
				cond.Reason = "GatewayOutOfCIDR"
				cond.Message = "gateway is outside VPC CIDR"
			}
		}

		vpc.Status.Allocated = allocCount[vpc.Name]
		vpc.Status.Conditions = []metav1.Condition{cond}
		if err := c.updateStatus(ctx, vpc); err != nil && !apierrors.IsConflict(err) {
			klog.Warningf("update status %s: %v", vpc.Name, err)
		}
	}

	// GC: remove allocations whose pods are gone is left to CNI DEL;
	// orphan cleanup can be added later.
	return nil
}

func (c *Controller) updateStatus(ctx context.Context, vpc *networkv1alpha1.VPC) error {
	return c.client.Put().
		Resource("vpcs").
		Name(vpc.Name).
		SubResource("status").
		Body(vpc).
		Do(ctx).
		Error()
}
