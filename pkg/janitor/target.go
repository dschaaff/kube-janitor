package janitor

import (
	"encoding/json"
	"fmt"
	"time"

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

// newTarget builds a target from a listed resource and the Resource type it was
// listed as. The type is the only source of the kind, API version and GVR, so
// nothing downstream re-derives them and no plural is ever guessed.
func newTarget(obj metav1.Object, rt ResourceType) (Target, error) {
	t := Target{
		Kind:        rt.Kind,
		APIVersion:  rt.apiVersion(),
		Namespace:   obj.GetNamespace(),
		Name:        obj.GetName(),
		UID:         obj.GetUID(),
		Annotations: obj.GetAnnotations(),
		CreatedAt:   obj.GetCreationTimestamp().Time,
		GVR:         rt.gvr(),
	}

	// Everything listed through the dynamic client already carries its raw form.
	// Namespaces arrive from the typed client and have to be converted.
	if u, ok := obj.(*unstructured.Unstructured); ok {
		t.Raw = u.Object
		return t, nil
	}

	raw, err := toMap(obj)
	if err != nil {
		return Target{}, err
	}
	t.Raw = raw

	return t, nil
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
// Concatenated rather than formatted: it is called for every resource on every
// run, including for log lines that are discarded.
func (t Target) describe() string {
	return t.Kind + " " + t.Namespace + "/" + t.Name
}

// wasNotified reports whether a delete notification was already sent.
func (t Target) wasNotified() bool {
	_, notified := t.Annotations[NotifiedAnnotation]
	return notified
}
