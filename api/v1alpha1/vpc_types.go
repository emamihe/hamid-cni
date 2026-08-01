package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VPCSpec defines the desired state of a VPC.
type VPCSpec struct {
	// CIDR is the IPv4 address range for this VPC (e.g. "10.0.0.0/16").
	// Different VPCs may use overlapping CIDRs; isolation is provided by VXLAN VNI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`
	CIDR string `json:"cidr"`

	// VXLANID is the VXLAN Network Identifier (VNI) used to isolate this VPC's overlay.
	// Must be unique across VPCs in the cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=16777215
	VXLANID int32 `json:"vxlanID"`

	// Gateway is an optional gateway IP within the CIDR used as the default
	// route for pods. If empty, the first usable host address in the CIDR is used.
	// +optional
	Gateway string `json:"gateway,omitempty"`

	// ExcludeIPs are addresses within the CIDR that must never be allocated to pods.
	// +optional
	ExcludeIPs []string `json:"excludeIPs,omitempty"`
}

// VPCStatus defines the observed state of a VPC.
type VPCStatus struct {
	// Allocated count of IPs currently claimed from this VPC.
	// +optional
	Allocated int32 `json:"allocated,omitempty"`

	// Conditions represent the latest available observations of the VPC.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=vpc
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="CIDR",type=string,JSONPath=`.spec.cidr`
// +kubebuilder:printcolumn:name="VXLAN",type=integer,JSONPath=`.spec.vxlanID`
// +kubebuilder:printcolumn:name="Allocated",type=integer,JSONPath=`.status.allocated`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// VPC is the Schema for the vpcs API. Each VPC owns an IP range and a VXLAN VNI.
// Namespaces opt into a VPC via the annotation network.hamid-cni.io/vpc=<name>.
type VPC struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VPCSpec   `json:"spec,omitempty"`
	Status VPCStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VPCList contains a list of VPC.
type VPCList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPC `json:"items"`
}
