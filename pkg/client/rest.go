package client

import (
	networkv1alpha1 "github.com/hamid/hamid-cni/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

// NewNetworkRESTClient returns a REST client for network.hamid-cni.io/v1alpha1.
func NewNetworkRESTClient(cfg *rest.Config) (*rest.RESTClient, error) {
	scheme := runtime.NewScheme()
	if err := networkv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	codecs := serializer.NewCodecFactory(scheme)
	crCfg := rest.CopyConfig(cfg)
	crCfg.GroupVersion = &networkv1alpha1.SchemeGroupVersion
	crCfg.APIPath = "/apis"
	crCfg.NegotiatedSerializer = codecs.WithoutConversion()
	crCfg.UserAgent = rest.DefaultKubernetesUserAgent()
	return rest.RESTClientFor(crCfg)
}
