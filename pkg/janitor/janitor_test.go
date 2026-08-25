package janitor

import (
	"context"
	"io"
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
	k8stesting "k8s.io/client-go/testing"
)

// clusterFixture describes the cluster one run works against: the Resource types
// discovery reports, the namespaces the cluster holds, and the resources in them.
// A namespace a run may judge has to be named in namespaces and supplied as an
// object, the way a real cluster serves it to both clients.
//
// out is where the run's log lines go. A case that does not care leaves it nil
// and the lines are dropped.
type clusterFixture struct {
	types      []ResourceType
	namespaces []string
	objects    []*unstructured.Unstructured
	out        io.Writer
}

// newRunFixture is a Janitor handed fake connections holding the fixture. Because
// New accepts a Cluster, a whole run goes through the interface main uses.
func newRunFixture(t *testing.T, cfg *Config, f clusterFixture) *Janitor {
	t.Helper()

	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, name := range f.namespaces {
		add(name)
	}
	for _, o := range f.objects {
		add(o.GetNamespace())
	}

	held := make([]runtime.Object, 0, len(names))
	for _, name := range names {
		held = append(held, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}

	typed := fake.NewSimpleClientset(held...)
	typed.Discovery().(*fakediscovery.FakeDiscovery).Resources = discoveryFor(f.types)

	listed := make([]runtime.Object, 0, len(f.objects))
	for _, o := range f.objects {
		listed = append(listed, o)
	}

	out := f.out
	if out == nil {
		out = io.Discard
	}

	return New(cfg, Cluster{Typed: typed, Dynamic: dynamicClientFor(f.types, listed...)},
		NewLogger(cfg, out), discardNotifier{})
}

// events reports the Kubernetes events a run recorded, in order. Counting them is
// how a case shows a resource was acted on once rather than twice.
func events(t *testing.T, j *Janitor) []*corev1.Event {
	t.Helper()

	var recorded []*corev1.Event
	for _, action := range j.cluster.Typed.(*fake.Clientset).Actions() {
		create, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetResource().Resource != "events" {
			continue
		}
		event, ok := create.GetObject().(*corev1.Event)
		if !ok {
			t.Fatalf("created events resource is a %T, want an Event", create.GetObject())
		}
		recorded = append(recorded, event)
	}
	return recorded
}

// discoveryFor reports the given types, grouped the way a real API server groups
// them. getResourceTypes asks for the core group first, so a "v1" list is always
// present even when no core-group type is fixtured.
func discoveryFor(types []ResourceType) []*metav1.APIResourceList {
	byGroupVersion := map[string]*metav1.APIResourceList{}
	var order []string

	for _, rt := range types {
		groupVersion := rt.apiVersion()
		list, ok := byGroupVersion[groupVersion]
		if !ok {
			list = &metav1.APIResourceList{GroupVersion: groupVersion}
			byGroupVersion[groupVersion] = list
			order = append(order, groupVersion)
		}
		list.APIResources = append(list.APIResources, metav1.APIResource{
			Name:       rt.Plural,
			Kind:       rt.Kind,
			Namespaced: rt.Namespaced,
			Verbs:      []string{"list", "delete"},
		})
	}

	if _, ok := byGroupVersion["v1"]; !ok {
		byGroupVersion["v1"] = &metav1.APIResourceList{GroupVersion: "v1"}
		order = append(order, "v1")
	}

	lists := make([]*metav1.APIResourceList, 0, len(order))
	for _, groupVersion := range order {
		lists = append(lists, byGroupVersion[groupVersion])
	}
	return lists
}

// dynamicClientFor is a dynamic fake that knows how to list the given types.
func dynamicClientFor(types []ResourceType, objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	kinds := map[schema.GroupVersionResource]string{}
	for _, rt := range types {
		kinds[rt.gvr()] = rt.Kind + "List"
	}

	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objects...)
}

// expiringObject carries an expiry annotation at the given offset from now, so a
// run judges it without any rule being configured. A negative offset has passed,
// so the run deletes it; a positive one has not, so at most the run warns.
func expiringObject(rt ResourceType, namespace, name string, in time.Duration) *unstructured.Unstructured {
	object := resourceObject(rt, namespace, name)
	metadata := object.Object["metadata"].(map[string]interface{})
	metadata["creationTimestamp"] = time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	metadata["annotations"] = map[string]interface{}{
		ExpiryAnnotation: time.Now().Add(in).Format(time.RFC3339),
	}
	return object
}

// expiredObject is the common case: an expiry an hour past.
func expiredObject(rt ResourceType, namespace, name string) *unstructured.Unstructured {
	return expiringObject(rt, namespace, name, -time.Hour)
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
			cfg := newConfig()
			if tt.configure != nil {
				tt.configure(cfg)
			}

			pods := make([]*unstructured.Unstructured, 0, len(tt.pods))
			for _, ref := range tt.pods {
				namespace, name := splitRef(t, ref)
				pods = append(pods, expiredObject(podResourceType, namespace, name))
			}

			j := newRunFixture(t, cfg, clusterFixture{types: []ResourceType{podResourceType}, objects: pods})

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

// A run reports what it did through the Logger it was handed, so a process that
// builds one Logger sees the whole of its own output. Nothing in the janitor
// reaches for standard error on its own.
func TestCleanUpReportsThroughTheLoggerItWasGiven(t *testing.T) {
	var said strings.Builder

	cfg := newConfig()
	cfg.LogFormat = "%(levelname)s %(message)s"

	j := newRunFixture(t, cfg, clusterFixture{
		types:   []ResourceType{podResourceType},
		objects: []*unstructured.Unstructured{expiredObject(podResourceType, "staging", "web")},
		out:     &said,
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	for _, want := range []string{"INFO Pod staging/web", "INFO Clean up run completed", "pods-deleted=1"} {
		if !strings.Contains(said.String(), want) {
			t.Errorf("the run never said %q. It said:\n%s", want, said.String())
		}
	}
}

// A resource whose plural is not its kind plus "s" has to survive discovery,
// filtering, rule matching and deletion with that plural intact. Guessing it
// produced "ingresss", which the include list rejected and the API server would
// not have recognised.
func TestCleanUpDeletesAnIrregularlyPluralisedResource(t *testing.T) {
	cfg := newConfig()
	cfg.IncludeResources = []string{"ingresses"}

	j := newRunFixture(t, cfg, clusterFixture{
		types:   []ResourceType{ingressResourceType},
		objects: []*unstructured.Unstructured{expiredObject(ingressResourceType, "staging", "web")},
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	if resourceExists(t, j, ingressResourceType, "staging", "web") {
		t.Error("ingress staging/web still exists, want it deleted")
	}
}

// A namespace used to be reached twice in one run: once through the pass that
// handled namespaces on their own, and once as a discovered cluster-scoped type.
// A verdict that deletes hid it, because the first reach removed the resource
// before the second listed it. A verdict that only warns did not: it recorded two
// events and sent two notifications. The Selector yields one Listing per Resource
// type, so there is no second reach to make.
func TestCleanUpActsOnANamespaceOnce(t *testing.T) {
	cfg := newConfig()
	cfg.IncludeClusterResources = true
	cfg.DeleteNotification = int(time.Hour.Seconds())

	j := newRunFixture(t, cfg, clusterFixture{
		types:      []ResourceType{namespaceResourceType},
		namespaces: []string{"pr-42"},
		objects: []*unstructured.Unstructured{
			expiringObject(namespaceResourceType, "", "pr-42", 30*time.Minute),
		},
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	recorded := events(t, j)
	if len(recorded) != 1 {
		t.Fatalf("recorded %d events, want 1", len(recorded))
	}
	if recorded[0].Reason != "DeleteNotification" {
		t.Errorf("event reason = %q, want DeleteNotification", recorded[0].Reason)
	}
}

// Namespaces are the one cluster-scoped type a run handles without
// --include-cluster-resources: see the flag's entry in the README.
func TestCleanUpDeletesANamespaceWithoutClusterResources(t *testing.T) {
	cfg := newConfig()

	j := newRunFixture(t, cfg, clusterFixture{
		types:      []ResourceType{namespaceResourceType},
		namespaces: []string{"pr-42"},
		objects: []*unstructured.Unstructured{
			expiredObject(namespaceResourceType, "", "pr-42"),
		},
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	if resourceExists(t, j, namespaceResourceType, "", "pr-42") {
		t.Error("namespace pr-42 still exists, want it deleted")
	}
}

// A namespace is excluded by name, the same lists that exclude the resources
// inside it.
func TestCleanUpLeavesAnExcludedNamespaceAlone(t *testing.T) {
	cfg := newConfig()

	j := newRunFixture(t, cfg, clusterFixture{
		types:      []ResourceType{namespaceResourceType},
		namespaces: []string{"pr-42", "kube-system"},
		objects: []*unstructured.Unstructured{
			expiredObject(namespaceResourceType, "", "pr-42"),
			expiredObject(namespaceResourceType, "", "kube-system"),
		},
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	if resourceExists(t, j, namespaceResourceType, "", "pr-42") {
		t.Error("namespace pr-42 still exists, want it deleted")
	}
	if !resourceExists(t, j, namespaceResourceType, "", "kube-system") {
		t.Error("namespace kube-system was deleted, want it left alone")
	}
}

// The plan is built from one read of the namespace list, however many Resource
// types a run considers. It used to be read once per type, plus once more for
// the pass that handled namespaces on their own.
//
// This counts the planning read only. A run that judges namespaces lists them
// again through the dynamic client, because that is the path every resource it
// judges arrives by.
func TestCleanUpReadsTheNamespaceListOnceToPlan(t *testing.T) {
	cfg := newConfig()

	j := newRunFixture(t, cfg, clusterFixture{
		types:      []ResourceType{podResourceType, deploymentResourceType, ingressResourceType},
		namespaces: []string{"staging", "prod"},
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	lists := 0
	for _, action := range j.cluster.Typed.(*fake.Clientset).Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "namespaces" {
			lists++
		}
	}

	if lists != 1 {
		t.Errorf("listed namespaces %d times, want 1", lists)
	}
}

// TestCleanUpJudgesOnFactsInspectLookedUp covers the wiring a run reaches its
// Resource context through: New builds the Inspect, and handleResource hands
// Decide a way to reach it that Decide only calls once it gets as far as the
// rules.
//
// A hook supplies the fact, because that needs nothing seeded in the cluster.
// The rule is what proves the fact arrived: nothing else would delete this pod.
func TestCleanUpJudgesOnFactsInspectLookedUp(t *testing.T) {
	asked := 0

	cfg := newConfig()
	cfg.IncludeResources = []string{"pods"}
	cfg.Rules = mustRules(t, Rule{
		ID: "reap-it", Resources: []string{"pods"}, JMESPath: "_context.reap", TTL: "1h",
	})
	cfg.ResourceContextHook = func(resource interface{}, cache map[string]interface{}) map[string]interface{} {
		asked++
		return map[string]interface{}{"reap": true}
	}

	j := newRunFixture(t, cfg, clusterFixture{
		types:   []ResourceType{podResourceType},
		objects: []*unstructured.Unstructured{resourceObject(podResourceType, "default", "web")},
	})

	if err := j.CleanUp(context.Background()); err != nil {
		t.Fatalf("CleanUp() error = %v", err)
	}

	if asked != 1 {
		t.Errorf("the run looked its Resource context up %d times, want 1", asked)
	}

	if resourceExists(t, j, podResourceType, "default", "web") {
		t.Error("pod default/web still exists, want the rule to have deleted it")
	}
}
