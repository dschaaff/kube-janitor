package janitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aKubeconfig writes a kubeconfig naming one unreachable server and returns its
// path. Nothing dials it: building the connections only has to parse the file.
func aKubeconfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	const content = `apiVersion: v1
kind: Config
current-context: test
clusters:
- name: test
  cluster:
    server: https://kubernetes.invalid
contexts:
- name: test
  context:
    cluster: test
    user: test
users:
- name: test
  user:
    token: not-a-real-token
`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}

	return path
}

// outsideACluster makes the in-cluster service account unavailable, so a case
// about the kubeconfig holds even when the tests themselves run in a pod.
func outsideACluster(t *testing.T) {
	t.Helper()

	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
}

func TestConnectNamesTheCredentialsItUsed(t *testing.T) {
	outsideACluster(t)

	path := aKubeconfig(t)
	t.Setenv("KUBECONFIG", path)

	cluster, credentials, err := Connect()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if cluster.Typed == nil || cluster.Dynamic == nil {
		t.Errorf("connected without both clients: %+v", cluster)
	}

	if !strings.Contains(credentials, path) || !strings.Contains(credentials, "KUBECONFIG") {
		t.Errorf("credentials %q name neither %q nor KUBECONFIG", credentials, path)
	}
}

func TestConnectReportsCredentialsItCannotResolve(t *testing.T) {
	outsideACluster(t)

	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))

	if _, _, err := Connect(); err == nil {
		t.Fatal("connected with a kubeconfig that is not there")
	} else if !strings.Contains(err.Error(), "KUBECONFIG") {
		t.Errorf("error %q does not say which variable to set", err)
	}
}

// nowhereToLook empties every variable a home directory is resolved from, so
// the run has no kubeconfig to fall back to on any platform.
func nowhereToLook(t *testing.T) {
	t.Helper()

	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
}

func TestConnectReportsHavingNowhereToLook(t *testing.T) {
	outsideACluster(t)
	nowhereToLook(t)

	_, credentials, err := Connect()
	if err == nil {
		t.Fatal("connected with nowhere to look for credentials")
	}

	if !strings.Contains(err.Error(), "KUBECONFIG") {
		t.Errorf("error %q does not say which variable to set", err)
	}

	// The empty wording kubeconfig leaves behind must never reach a Log line:
	// a run that did not connect has nothing to say it connected with.
	if credentials != "" {
		t.Errorf("credentials %q reported for a connection that was never made", credentials)
	}
}

func TestKubeconfigResolution(t *testing.T) {
	tests := []struct {
		name        string
		fromEnv     string
		home        string
		wantPath    string
		credentials []string
	}{
		{
			name:        "KUBECONFIG names one",
			fromEnv:     "/etc/kube/config",
			home:        "/home/janitor",
			wantPath:    "/etc/kube/config",
			credentials: []string{"/etc/kube/config", "KUBECONFIG"},
		},
		{
			name:        "the home directory holds the only one",
			home:        "/home/janitor",
			wantPath:    "/home/janitor/.kube/config",
			credentials: []string{"/home/janitor/.kube/config"},
		},
		{
			name:     "there is nowhere to look",
			wantPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, credentials := kubeconfig(tt.fromEnv, tt.home)

			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}

			for _, want := range tt.credentials {
				if !strings.Contains(credentials, want) {
					t.Errorf("credentials %q do not name %q", credentials, want)
				}
			}

			if tt.wantPath == "" && credentials != "" {
				t.Errorf("credentials %q worded for a path that was never found", credentials)
			}
		})
	}
}

func TestKubeconfigFromTheHomeDirectoryDoesNotClaimKUBECONFIG(t *testing.T) {
	_, credentials := kubeconfig("", "/home/janitor")

	if strings.Contains(credentials, "KUBECONFIG") {
		t.Errorf("credentials %q credit KUBECONFIG, which named nothing", credentials)
	}
}
