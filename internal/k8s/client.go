package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Config struct {
	KubeconfigPath string
	MasterURL      string
}

// NewClient returns a standard kubernetes.Interface (in-cluster or local).
func NewClient(cfg Config) (kubernetes.Interface, error) {
	restConfig, err := buildRESTConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load k8s config: %w", err)
	}

	return kubernetes.NewForConfig(restConfig)
}

func buildRESTConfig(cfg Config) (*rest.Config, error) {
	// 1. Explicit path
	if cfg.KubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags(cfg.MasterURL, cfg.KubeconfigPath)
	}

	// 2. In-cluster (container/pod)
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	// 3. Fallback to ~/.kube/config
	home := homedir.HomeDir()
	if home == "" {
		return nil, fmt.Errorf("could not locate home directory for kubeconfig")
	}
	defaultPath := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(defaultPath); err != nil {
		return nil, fmt.Errorf("kubeconfig not found at %s: %w", defaultPath, err)
	}

	return clientcmd.BuildConfigFromFlags(cfg.MasterURL, defaultPath)
}
