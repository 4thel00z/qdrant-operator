package helm

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// restGetter serves Helm's Kubernetes client from a *rest.Config, so in-cluster
// configuration works without a kubeconfig file.
type restGetter struct {
	config    *rest.Config
	namespace string
}

func newRESTGetter(config *rest.Config, namespace string) *restGetter {
	return &restGetter{config: rest.CopyConfig(config), namespace: namespace}
}

func (g *restGetter) ToRESTConfig() (*rest.Config, error) {
	return rest.CopyConfig(g.config), nil
}

func (g *restGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	client, err := discovery.NewDiscoveryClientForConfig(rest.CopyConfig(g.config))
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(client), nil
}

func (g *restGetter) ToRESTMapper() (meta.RESTMapper, error) {
	client, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(client), nil
}

func (g *restGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return restClientConfig{config: g.config, namespace: g.namespace}
}

type restClientConfig struct {
	config    *rest.Config
	namespace string
}

func (c restClientConfig) RawConfig() (clientcmdapi.Config, error) {
	return *clientcmdapi.NewConfig(), nil
}

func (c restClientConfig) ClientConfig() (*rest.Config, error) {
	return rest.CopyConfig(c.config), nil
}

func (c restClientConfig) Namespace() (string, bool, error) {
	return c.namespace, true, nil
}

func (c restClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return &clientcmd.ClientConfigLoadingRules{}
}
