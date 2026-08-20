package janitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// Target is a single Kubernetes resource that the current run is considering,
// together with everything needed to judge and act on it.
type Target struct {
	Kind        string
	APIVersion  string
	Namespace   string
	Name        string
	UID         types.UID
	Annotations map[string]string
	CreatedAt   time.Time

	// Raw is the resource as a map, for JMESPath evaluation by rules.
	Raw map[string]interface{}

	// GVR is the resource this target is deleted through.
	GVR schema.GroupVersionResource
}

// newTarget resolves a resource's kind, GVR and raw representation once, so that
// nothing downstream has to assert on the concrete type again.
func newTarget(obj metav1.Object) (Target, error) {
	t := Target{
		Namespace:   obj.GetNamespace(),
		Name:        obj.GetName(),
		UID:         obj.GetUID(),
		Annotations: obj.GetAnnotations(),
		CreatedAt:   obj.GetCreationTimestamp().Time,
	}

	switch o := obj.(type) {
	case *unstructured.Unstructured:
		gvk := o.GroupVersionKind()
		t.Kind = o.GetKind()
		t.APIVersion = o.GetAPIVersion()
		t.Raw = o.Object
		t.GVR = schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: pluralize(gvk.Kind),
		}
	case *corev1.Namespace:
		t.Kind = "Namespace"
		t.APIVersion = "v1"
		t.GVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	default:
		t.Kind = "Unknown"
		t.APIVersion = "v1"
		t.GVR = schema.GroupVersionResource{Version: "v1", Resource: pluralize("Unknown")}
	}

	if t.Raw == nil {
		raw, err := toMap(obj)
		if err != nil {
			return Target{}, err
		}
		t.Raw = raw
	}

	return t, nil
}

// pluralize derives a plural resource name from a kind.
//
// This is wrong for irregular plurals: Ingress becomes "ingresss" and
// NetworkPolicy becomes "networkpolicys". It reproduces what the code it replaced
// did. The fix is to carry the GVR that listed the resource down to here instead
// of guessing, which means building the target at list time.
func pluralize(kind string) string {
	return strings.ToLower(kind) + "s"
}

// toMap converts a Kubernetes object to a map for JMESPath evaluation.
func toMap(obj metav1.Object) (map[string]interface{}, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal object: %v", err)
	}

	return result, nil
}

// describe renders the target the way log messages and events refer to it.
func (t Target) describe() string {
	return fmt.Sprintf("%s %s/%s", t.Kind, t.Namespace, t.Name)
}

// wasNotified reports whether a delete notification was already sent.
func (t Target) wasNotified() bool {
	_, notified := t.Annotations[NotifiedAnnotation]
	return notified
}
