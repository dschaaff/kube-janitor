package janitor

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The Resource types fixtures are listed as. ingressResourceType is the one the
// irregular-plural cases turn on.
var (
	podResourceType = ResourceType{
		Version: "v1", Kind: "Pod", Plural: "pods", Namespaced: true,
	}
	deploymentResourceType = ResourceType{
		Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments", Namespaced: true,
	}
	ingressResourceType = ResourceType{
		Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Plural: "ingresses", Namespaced: true,
	}
	networkPolicyResourceType = ResourceType{
		Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy",
		Plural: "networkpolicies", Namespaced: true,
	}
)

// mustTarget builds a Target for tests that drive the functions behind it,
// taking either form a resource arrives in.
func mustTarget(t *testing.T, obj metav1.Object, rt ResourceType) Target {
	t.Helper()

	if u, ok := obj.(*unstructured.Unstructured); ok {
		return newTarget(u, rt)
	}

	target, err := newTypedTarget(obj, rt)
	if err != nil {
		t.Fatalf("newTypedTarget() error = %v", err)
	}
	return target
}

func TestNewTargetFromUnstructured(t *testing.T) {
	created := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"kind":       "Deployment",
		"apiVersion": "apps/v1",
		"metadata": map[string]interface{}{
			"name":      "web",
			"namespace": "staging",
			"uid":       "abc-123",
			"annotations": map[string]interface{}{
				TTLAnnotation: "7d",
			},
			"creationTimestamp": created.Format(time.RFC3339),
		},
	}}

	got := mustTarget(t, obj, deploymentResourceType)

	if got.Kind != "Deployment" {
		t.Errorf("Kind = %q, want %q", got.Kind, "Deployment")
	}
	if got.APIVersion != "apps/v1" {
		t.Errorf("APIVersion = %q, want %q", got.APIVersion, "apps/v1")
	}
	if got.Namespace != "staging" || got.Name != "web" {
		t.Errorf("Namespace/Name = %q/%q, want staging/web", got.Namespace, got.Name)
	}
	if string(got.UID) != "abc-123" {
		t.Errorf("UID = %q, want %q", got.UID, "abc-123")
	}
	if got.Annotations[TTLAnnotation] != "7d" {
		t.Errorf("Annotations[%s] = %q, want %q", TTLAnnotation, got.Annotations[TTLAnnotation], "7d")
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	wantGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	if got.GVR != wantGVR {
		t.Errorf("GVR = %v, want %v", got.GVR, wantGVR)
	}

	// Rules evaluate JMESPath against the raw resource, so it must be carried.
	if got.Raw["kind"] != "Deployment" {
		t.Errorf("Raw[kind] = %v, want Deployment", got.Raw["kind"])
	}
}

func TestNewTargetFromNamespace(t *testing.T) {
	got := mustTarget(t, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
	}, namespaceResourceType)

	// A namespace is its own name, and carries no namespace of its own.
	if got.Kind != "Namespace" {
		t.Errorf("Kind = %q, want Namespace", got.Kind)
	}
	if got.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want v1", got.APIVersion)
	}
	if got.Name != "pr-42" || got.Namespace != "" {
		t.Errorf("Name/Namespace = %q/%q, want pr-42/<empty>", got.Name, got.Namespace)
	}

	wantGVR := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	if got.GVR != wantGVR {
		t.Errorf("GVR = %v, want %v", got.GVR, wantGVR)
	}

	// Namespaces arrive as a typed object, so the raw form is built by round-trip.
	if got.Raw == nil {
		t.Fatal("Raw = nil, want the resource as a map")
	}
	metadata, ok := got.Raw["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("Raw[metadata] = %T, want a map", got.Raw["metadata"])
	}
	if metadata["name"] != "pr-42" {
		t.Errorf("Raw metadata.name = %v, want pr-42", metadata["name"])
	}
}

// The listed Resource type is the only source of the plural, so irregular
// plurals reach the Target intact. Guessing one from the kind is what this
// replaced, and what these cases stop coming back. The wanted values are spelled
// out rather than derived from the type, so that a fault in the derivation
// itself fails here too.
func TestNewTargetCarriesTheListedResourceType(t *testing.T) {
	tests := []struct {
		rt             ResourceType
		wantGVR        schema.GroupVersionResource
		wantAPIVersion string
	}{
		{
			rt: ingressResourceType,
			wantGVR: schema.GroupVersionResource{
				Group: "networking.k8s.io", Version: "v1", Resource: "ingresses",
			},
			wantAPIVersion: "networking.k8s.io/v1",
		},
		{
			rt: networkPolicyResourceType,
			wantGVR: schema.GroupVersionResource{
				Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies",
			},
			wantAPIVersion: "networking.k8s.io/v1",
		},
		{
			// A core-group type: the API version is the bare version, with no
			// leading slash.
			rt:             podResourceType,
			wantGVR:        schema.GroupVersionResource{Version: "v1", Resource: "pods"},
			wantAPIVersion: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.rt.Kind, func(t *testing.T) {
			got := newTarget(&unstructured.Unstructured{Object: map[string]interface{}{
				"metadata": map[string]interface{}{"name": "x", "namespace": "default"},
			}}, tt.rt)

			if got.GVR != tt.wantGVR {
				t.Errorf("GVR = %v, want %v", got.GVR, tt.wantGVR)
			}
			if got.plural() != tt.wantGVR.Resource {
				t.Errorf("plural() = %q, want %q", got.plural(), tt.wantGVR.Resource)
			}
			if got.Kind != tt.rt.Kind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.rt.Kind)
			}
			if got.APIVersion != tt.wantAPIVersion {
				t.Errorf("APIVersion = %q, want %q", got.APIVersion, tt.wantAPIVersion)
			}
		})
	}
}

func TestTargetWasNotified(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{name: "no annotations at all", annotations: nil, want: false},
		{name: "unrelated annotations", annotations: map[string]string{TTLAnnotation: "1d"}, want: false},
		{name: "notified", annotations: map[string]string{NotifiedAnnotation: "yes"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := mustTarget(t, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: tt.annotations},
			}, namespaceResourceType)

			if got := target.wasNotified(); got != tt.want {
				t.Errorf("wasNotified() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTargetDescribe(t *testing.T) {
	target := mustTarget(t,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}, namespaceResourceType)

	if got, want := target.describe(), "Namespace /pr-42"; got != want {
		t.Errorf("describe() = %q, want %q", got, want)
	}
}
