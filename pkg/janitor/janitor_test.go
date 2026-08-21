package janitor

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// newRunFixture is a Janitor handed fake connections holding the given pods, the
// namespaces they live in, and the discovery a run needs to find them. Because
// New accepts a Cluster, a whole run goes through the interface main uses.
func newRunFixture(t *testing.T, cfg *Config, pods ...*unstructured.Unstructured) *Janitor {
	t.Helper()

	objects := make([]runtime.Object, 0, len(pods))
	var namespaces []runtime.Object
	seen := map[string]bool{}

	for _, p := range pods {
		objects = append(objects, p)

		ns := p.GetNamespace()
		if !seen[ns] {
			seen[ns] = true
			namespaces = append(namespaces,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		}
	}

	typed := fake.NewSimpleClientset(namespaces...)
	typed.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"list", "delete"}},
		},
	}}

	return New(cfg, Cluster{Typed: typed, Dynamic: podDynamicClient(objects...)})
}

// expiredPod carries an expiry annotation in the past, so any run that considers
// it deletes it.
func expiredPod(namespace, name string) *unstructured.Unstructured {
	return podObject(namespace, name, 24*time.Hour, map[string]string{
		ExpiryAnnotation: time.Now().Add(-time.Hour).Format(time.RFC3339),
	})
}

// splitRef splits a "namespace/name" pod reference.
func splitRef(t *testing.T, ref string) (string, string) {
	t.Helper()

	namespace, name, ok := strings.Cut(ref, "/")
	if !ok {
		t.Fatalf("malformed pod reference %q", ref)
	}
	return namespace, name
}

func TestCleanUp(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Config)
		pods      []string
		cancelled bool
		// wantAlive lists the pods that must survive the run; every other pod in
		// pods must be gone.
		wantAlive []string
	}{
		{
			name: "deletes an expired resource",
			pods: []string{"staging/web"},
		},
		{
			name:      "leaves an excluded namespace alone",
			configure: func(c *Config) { c.ExcludeNamespaces = []string{"kube-system"} },
			pods:      []string{"staging/web", "kube-system/coredns"},
			wantAlive: []string{"kube-system/coredns"},
		},
		{
			name:      "leaves an excluded resource type alone",
			configure: func(c *Config) { c.ExcludeResources = []string{"pods"} },
			pods:      []string{"staging/web"},
			wantAlive: []string{"staging/web"},
		},
		{
			name:      "deletes nothing in a dry run",
			configure: func(c *Config) { c.DryRun = true },
			pods:      []string{"staging/web"},
			wantAlive: []string{"staging/web"},
		},
		{
			name:      "stops on a cancelled context",
			pods:      []string{"staging/web"},
			cancelled: true,
			wantAlive: []string{"staging/web"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig()
			if tt.configure != nil {
				tt.configure(cfg)
			}

			pods := make([]*unstructured.Unstructured, 0, len(tt.pods))
			for _, ref := range tt.pods {
				namespace, name := splitRef(t, ref)
				pods = append(pods, expiredPod(namespace, name))
			}

			j := newRunFixture(t, cfg, pods...)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}

			if err := j.CleanUp(ctx); err != nil {
				t.Fatalf("CleanUp() error = %v", err)
			}

			alive := map[string]bool{}
			for _, ref := range tt.wantAlive {
				alive[ref] = true
			}

			for _, ref := range tt.pods {
				namespace, name := splitRef(t, ref)
				if got := podExists(t, j, namespace, name); got != alive[ref] {
					t.Errorf("pod %s exists = %v, want %v", ref, got, alive[ref])
				}
			}
		})
	}
}
