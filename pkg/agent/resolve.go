package agent

import (
	"fmt"

	networkv1alpha1 "github.com/hamid/hamid-cni/api/v1alpha1"
)

// ResolveVPCName returns the VPC for a namespace: annotation wins, else defaultVPC.
func ResolveVPCName(annotation, defaultVPC string) (string, error) {
	if annotation != "" {
		return annotation, nil
	}
	if defaultVPC == "" {
		return "", fmt.Errorf("namespace missing annotation %s and no default VPC configured", networkv1alpha1.VPCAnnotation)
	}
	return defaultVPC, nil
}
