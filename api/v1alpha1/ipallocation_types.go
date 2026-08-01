package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IPAllocationSpec describes an IP claimed from a VPC for a pod.
type IPAllocationSpec struct {
	// VPC is the name of the cluster-scoped VPC this allocation belongs to.
	// +kubebuilder:validation:Required
	VPC string `json:"vpc"`

	// PodNamespace is the namespace of the pod.
	// +kubebuilder:validation:Required
	PodNamespace string `json:"podNamespace"`

	// PodName is the name of the pod.
	// +kubebuilder:validation:Required
	PodName string `json:"podName"`

	// Node is the node where the pod is scheduled.
	// +kubebuilder:validation:Required
	Node string `json:"node"`

	// IP is the allocated IPv4 address (without prefix length).
	// +kubebuilder:validation:Required
	IP string `json:"ip"`

	// MAC is the MAC address assigned to the pod interface.
	// +optional
	MAC string `json:"mac,omitempty"`

	// InterfaceID is the container interface name (e.g. eth0).
	// +optional
	InterfaceID string `json:"interfaceID,omitempty"`
}

// IPAllocationStatus is the observed state of an allocation.
type IPAllocationStatus struct {
	// Phase is Pending, Allocated, or Released.
	// +optional
	Phase string `json:"phase,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=ipalloc
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VPC",type=string,JSONPath=`.spec.vpc`
// +kubebuilder:printcolumn:name="IP",type=string,JSONPath=`.spec.ip`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.spec.podNamespace`+`/`+`.spec.podName`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.node`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// IPAllocation records a pod IP claimed from a VPC. Names are typically
// "<namespace>_<pod>" so they are unique cluster-wide.
type IPAllocation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IPAllocationSpec   `json:"spec,omitempty"`
	Status IPAllocationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IPAllocationList contains a list of IPAllocation.
type IPAllocationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IPAllocation `json:"items"`
}

const (
	IPAllocationPhaseAllocated = "Allocated"
	IPAllocationPhaseReleased  = "Released"
)
