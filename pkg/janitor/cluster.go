package janitor

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Cluster is the pair of connections a run works through: the typed client for
// the built-in kinds Resource context and events need, and the dynamic client
// for the arbitrary kinds a run lists and deletes. A Janitor is handed one; it
// never builds its own.
type Cluster struct {
	Typed   kubernetes.Interface
	Dynamic dynamic.Interface
}

// Connect resolves the ambient credentials and returns the connections they
// grant: the in-cluster service account when running as a pod, and the
// kubeconfig otherwise. Both clients share one resolved configuration.
func Connect() (Cluster, error) {
	config, err := restConfig()
	if err != nil {
		return Cluster{}, err
	}

	typed, err := kubernetes.NewForConfig(config)
	if err != nil {
		return Cluster{}, fmt.Errorf("failed to create client: %v", err)
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return Cluster{}, fmt.Errorf("failed to create dynamic client: %v", err)
	}

	return Cluster{Typed: typed, Dynamic: dyn}, nil
}

// restConfig prefers the in-cluster service account, and falls back to the
// kubeconfig named by KUBECONFIG or found in the user's home directory.
func restConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		if homeDir, err := os.UserHomeDir(); err == nil {
			kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
		}
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %v (try setting KUBECONFIG environment variable)", err)
	}

	return config, nil
}
