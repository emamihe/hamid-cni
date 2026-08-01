// Package v1alpha1 contains API Schema definitions for the network v1alpha1 API group.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group   = "network.hamid-cni.io"
	Version = "v1alpha1"

	// VPCAnnotation maps a namespace to a VPC. When absent, the agent uses DefaultVPCName.
	VPCAnnotation = "network.hamid-cni.io/vpc"

	// DefaultVPCLabel marks the cluster default VPC (optional convenience label).
	DefaultVPCLabel = "network.hamid-cni.io/default"

	// DefaultVPCName is the conventional name of the fallback VPC for unannotated namespaces.
	DefaultVPCName = "default"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&VPC{},
		&VPCList{},
		&IPAllocation{},
		&IPAllocationList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
