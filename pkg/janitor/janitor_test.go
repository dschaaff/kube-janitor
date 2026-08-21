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
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// newRunFixture is a Janitor handed fake connections holding the given resources,
// the namespaces they live in, and the discovery a run needs to find them. Because
// New accepts a Cluster, a whole run goes through the interface main uses.
func newRunFixture(t *testing.T, cfg *Config, rt ResourceType, objects ...*unstructured.Unstructured) *Janitor {
	t.Helper()

	listed := make([]runtime.Object, 0, len(objects))
	var namespaces []runtime.Object
	seen := map[string]bool{}

	for _, o := range objects {
		listed = append(listed, o)

		ns := o.GetNamespace()
		if !seen[ns] {
			seen[ns] = true
			namespaces = append(namespaces,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		}
	}

	typed := fake.NewSimpleClientset(namespaces...)
	typed.Discovery().(*fakediscovery.FakeDiscovery).Resources = discoveryFor(rt)

	return New(cfg, Cluster{Typed: typed, Dynamic: dynamicClientFor(rt, listed...)})
}

// discoveryFor reports the given type, alongside the core group a run always
// asks for first.
func discoveryFor(rt ResourceType) []*metav1.APIResourceList {
	entry := metav1.APIResource{
		Name:       rt.Plural,
		Kind:       rt.Kind,
		Namespaced: rt.Namespaced,
		Verbs:      []string{"list", "delete"},
	}

	if rt.Group == "" {
		return []*metav1.APIResourceList{
			{GroupVersion: "v1", APIResources: []metav1.APIResource{entry}},
		}
	}

	return []*metav1.APIResourceList{
		{GroupVersion: "v1"},
		{GroupVersion: rt.apiVersion(), APIResources: []metav1.APIResource{entry}},
	}
}

// dynamicClientFor is a dynamic fake that knows how to list the given type.
func dynamicClientFor(rt ResourceType, objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{rt.gvr(): rt.Kind + "List"},
		objects...,
	)
}

// expiredObject carries an expiry annotation in the past, so any run that
// considers it deletes it.
func expiredObject(rt ResourceType, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"kind":       rt.Kind,
		"apiVersion": rt.apiVersion(),
		"metadata": map[string]interface{}{
			"name":              name,
			"namespace":         namespace,
			"creationTimestamp": time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			"annotations": map[string]interface{}{
				ExpiryAnnotation: time.Now().Add(-time.Hour).Format(time.RFC3339),
			},
		},
	}}
}

// resourceExists reports whether the janitor's cluster still holds the resource.
func resourceExists(t *testing.T, j *Janitor, rt ResourceType, namespace, name string) bool {
	t.Helper()

	_, err := j.cluster.Dynamic.Resource(rt.gvr()).Namespace(namespace).
		Get(context.Background(), name, metav1.GetOptions{})
	return err == nil
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
				pods = append(pods, expiredObject(podResourceType, namespace, name))
			}

			j := newRunFixture(t, cfg, podResourceType, pods...)

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
				if got := resourceExists(t, j, podResourceType, namespace, name); got != alive[ref] {
					t.Errorf("pod %s exists = %v, want %v", ref, got, alive[ref])
				}
			}
		})
	}
}

// A resource whose plural is not its kind plus "s" has to survive discovery,
// filtering, rule matching and deletion with that plural intact. Guessing it
// produced "ingresss", which the include list rejected and the API server would
// not have recognised.
func TestCleanUpDeletesAnIrregularlyPluralisedResource(t *testing.T) {
	ingresses := ResourceType{
		Group: "networking.k8s.io", Version: "v1",
		Kind: "Ingress", Plural: "ingresses", Namespaced: true,
	}

	cfg := NewConfig()
	cfg.IncludeResources = []string{"ingresses"}

	j := newRunFixture(t, cfg, ingresses, expiredObject(ingresses, "staging", "web"))

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	if resourceExists(t, j, ingresses, "staging", "web") {
		t.Error("ingress staging/web still exists, want it deleted")
	}
}
