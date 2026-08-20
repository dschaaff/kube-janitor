package janitor

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// mustTarget builds a Target for tests that drive the functions behind it.
func mustTarget(t *testing.T, obj metav1.Object) Target {
	t.Helper()

	target, err := newTarget(obj)
	if err != nil {
		t.Fatalf("newTarget() error = %v", err)
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

	got := mustTarget(t, obj)

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
	})

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

func TestNewTargetFromUnrecognisedType(t *testing.T) {
	got := mustTarget(t, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "one-off", Namespace: "default"},
	})

	if got.Kind != "Unknown" {
		t.Errorf("Kind = %q, want Unknown", got.Kind)
	}
	if got.Raw == nil {
		t.Error("Raw = nil, want the resource as a map")
	}
}

// Pins behaviour that is known to be wrong, so that carrying the listed GVR down
// to the target has to change it deliberately rather than by accident.
func TestNewTargetGuessesPluralsAndGetsIrregularsWrong(t *testing.T) {
	for kind, want := range map[string]string{
		"Deployment":    "deployments",
		"Ingress":       "ingresss",       // should be "ingresses"
		"NetworkPolicy": "networkpolicys", // should be "networkpolicies"
	} {
		obj := &unstructured.Unstructured{Object: map[string]interface{}{
			"kind":       kind,
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"name": "x", "namespace": "default"},
		}}

		if got := mustTarget(t, obj).GVR.Resource; got != want {
			t.Errorf("kind %s: GVR.Resource = %q, want %q", kind, got, want)
		}
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
			})

			if got := target.wasNotified(); got != tt.want {
				t.Errorf("wasNotified() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTargetDescribe(t *testing.T) {
	target := mustTarget(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}})

	if got, want := target.describe(), "Namespace /pr-42"; got != want {
		t.Errorf("describe() = %q, want %q", got, want)
	}
}
