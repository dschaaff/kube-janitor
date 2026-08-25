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

// inClusterCredentials names the credentials a run gets for being a pod.
const inClusterCredentials = "the in-cluster service account"

// Connect resolves the ambient credentials and returns the connections they
// grant, together with the name of the credentials it used: the in-cluster
// service account when running as a pod, and the kubeconfig otherwise. Both
// clients share one resolved configuration.
//
// The name is what a run says it connected with. Connect is the only thing
// that knows the order the credentials are looked for in, so nothing else has
// to work that order out again to talk about it — and nothing else can get it
// wrong.
func Connect() (Cluster, string, error) {
	config, credentials, err := restConfig()
	if err != nil {
		return Cluster{}, "", err
	}

	typed, err := kubernetes.NewForConfig(config)
	if err != nil {
		return Cluster{}, "", fmt.Errorf("failed to create client: %v", err)
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return Cluster{}, "", fmt.Errorf("failed to create dynamic client: %v", err)
	}

	return Cluster{Typed: typed, Dynamic: dyn}, credentials, nil
}

// restConfig prefers the in-cluster service account, and falls back to the
// kubeconfig named by KUBECONFIG or found in the user's home directory.
func restConfig() (*rest.Config, string, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, inClusterCredentials, nil
	}

	// A home directory that cannot be resolved is not itself the failure worth
	// reporting: it just leaves nowhere to look, which the empty path below
	// turns into the one error a run gets.
	home, _ := os.UserHomeDir()
	path, credentials := kubeconfig(os.Getenv("KUBECONFIG"), home)

	config, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create config: %v (try setting KUBECONFIG environment variable)", err)
	}

	return config, credentials, nil
}

// kubeconfig names the file a run reads its credentials from when it is not
// running as a pod, and words what it found for the run to report.
//
// An empty home leaves both empty rather than guessing at a path: a run with
// nowhere to look should fail saying so, not saying that a file it was never
// going to find is missing. Nothing reports the empty wording, because an
// empty path never builds a configuration to report about.
func kubeconfig(fromEnv, home string) (path, credentials string) {
	if fromEnv != "" {
		return fromEnv, "kubeconfig " + fromEnv + ", named by KUBECONFIG"
	}

	if home == "" {
		return "", ""
	}

	path = filepath.Join(home, ".kube", "config")

	return path, "kubeconfig " + path
}
