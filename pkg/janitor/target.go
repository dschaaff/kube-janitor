package janitor

import (
	"time"

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

// newTarget builds a target from a resource the dynamic client listed and the
// Resource type it was listed as. Every resource a run considers arrives this
// way, namespaces included. The type is the only source of the kind, API version
// and GVR, so nothing downstream re-derives them and no plural is ever guessed.
// The caller must pass the type it listed the resource under; nothing here can
// check that.
func newTarget(u *unstructured.Unstructured, rt ResourceType) Target {
	return Target{
		Kind:        rt.Kind,
		APIVersion:  rt.apiVersion(),
		Namespace:   u.GetNamespace(),
		Name:        u.GetName(),
		UID:         u.GetUID(),
		Annotations: u.GetAnnotations(),
		CreatedAt:   u.GetCreationTimestamp().Time,
		GVR:         rt.gvr(),
		Raw:         u.Object,
	}
}

// plural is the Resource type's plural: the name this target was listed under,
// and the name the include and exclude lists and a rule's resources list name it
// by.
func (t Target) plural() string {
	return t.GVR.Resource
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
