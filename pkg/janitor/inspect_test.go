package janitor

import (
	"context"
	"errors"
	"io"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/dschaaff/kube-janitor/pkg/janitor/hooks"
)

// pvcResourceType is the Resource type persistent volume claims are listed as.
var pvcResourceType = ResourceType{
	Version: "v1", Kind: "PersistentVolumeClaim", Plural: "persistentvolumeclaims", Namespaced: true,
}

// newTestInspector is an Inspect over the given cluster contents, reporting
// nowhere. Because it is built from the typed client alone, a case supplies
// nothing else.
func newTestInspector(typed kubernetes.Interface, hook hooks.ResourceContextHook) *inspector {
	cfg := &Config{}
	return newInspector(typed, hook, NewLogger(cfg, io.Discard))
}

// claimVolume is the volume a workload mounts the named claim through.
func claimVolume(claim string) corev1.Volume {
	return corev1.Volume{
		Name: "data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim},
		},
	}
}

// claim is a persistent volume claim by name, in the default namespace.
func claim(name string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	}
}

// TestContextForClaim covers every way a claim can be found in use.
//
// The cases assert the two keys a rule names through _context, because those
// are what the janitor promises: see README.md.
func TestContextForClaim(t *testing.T) {
	tests := []struct {
		name              string
		claim             *corev1.PersistentVolumeClaim
		held              []runtime.Object
		wantNotMounted    bool
		wantNotReferenced bool
	}{
		{
			name:  "mounted by a pod",
			claim: claim("test-pvc"),
			held: []runtime.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
				Spec:       corev1.PodSpec{Volumes: []corev1.Volume{claimVolume("test-pvc")}},
			}},
			wantNotMounted:    false,
			wantNotReferenced: true,
		},
		{
			name:  "referenced by a statefulset's volume claim template",
			claim: claim("data-my-sts-0"),
			held: []runtime.Object{&appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "my-sts", Namespace: "default"},
				Spec: appsv1.StatefulSetSpec{
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
						{ObjectMeta: metav1.ObjectMeta{Name: "data"}},
					},
				},
			}},
			wantNotMounted:    true,
			wantNotReferenced: false,
		},
		{
			name:  "referenced by a deployment",
			claim: claim("test-pvc"),
			held: []runtime.Object{&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "my-deploy", Namespace: "default"},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Volumes: []corev1.Volume{claimVolume("test-pvc")}},
					},
				},
			}},
			wantNotMounted:    true,
			wantNotReferenced: false,
		},
		{
			name:  "referenced by a job",
			claim: claim("test-pvc"),
			held: []runtime.Object{&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "my-job", Namespace: "default"},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Volumes: []corev1.Volume{claimVolume("test-pvc")}},
					},
				},
			}},
			wantNotMounted:    true,
			wantNotReferenced: false,
		},
		{
			name:  "referenced by a cronjob",
			claim: claim("test-pvc"),
			held: []runtime.Object{&batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cronjob", Namespace: "default"},
				Spec: batchv1.CronJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{Volumes: []corev1.Volume{claimVolume("test-pvc")}},
							},
						},
					},
				},
			}},
			wantNotMounted:    true,
			wantNotReferenced: false,
		},
		{
			name:              "used by nothing",
			claim:             claim("unused-pvc"),
			wantNotMounted:    true,
			wantNotReferenced: true,
		},
		{
			name:  "a statefulset whose template names another claim",
			claim: claim("data-my-sts-0"),
			held: []runtime.Object{&appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "other-sts", Namespace: "default"},
				Spec: appsv1.StatefulSetSpec{
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
						{ObjectMeta: metav1.ObjectMeta{Name: "data"}},
					},
				},
			}},
			wantNotMounted:    true,
			wantNotReferenced: true,
		},
		{
			name:  "a pod in another namespace mounting the same name",
			claim: claim("test-pvc"),
			held: []runtime.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "other"},
				Spec:       corev1.PodSpec{Volumes: []corev1.Volume{claimVolume("test-pvc")}},
			}},
			wantNotMounted:    true,
			wantNotReferenced: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			held := append([]runtime.Object{tt.claim}, tt.held...)
			i := newTestInspector(fake.NewSimpleClientset(held...), nil)

			got := i.contextFor(context.Background(), mustTarget(t, tt.claim, pvcResourceType))

			if got[keyPVCIsNotMounted] != tt.wantNotMounted {
				t.Errorf("_context.%s = %v, want %v", keyPVCIsNotMounted,
					got[keyPVCIsNotMounted], tt.wantNotMounted)
			}

			if got[keyPVCIsNotReferenced] != tt.wantNotReferenced {
				t.Errorf("_context.%s = %v, want %v", keyPVCIsNotReferenced,
					got[keyPVCIsNotReferenced], tt.wantNotReferenced)
			}
		})
	}
}

// TestContextForLeavesOutFactsItCouldNotLookUp keeps a failed lookup from
// reading as a claim nothing uses. A rule testing the key does not match, which
// is what stops the claim being deleted on the strength of a listing that never
// happened.
func TestContextForLeavesOutFactsItCouldNotLookUp(t *testing.T) {
	for _, listed := range []string{"pods", "statefulsets", "deployments", "jobs", "cronjobs"} {
		t.Run(listed+" cannot be listed", func(t *testing.T) {
			typed := fake.NewSimpleClientset(claim("test-pvc"))
			typed.PrependReactor("list", listed,
				func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("the API server said no")
				})

			i := newTestInspector(typed, nil)

			got := i.contextFor(context.Background(),
				mustTarget(t, claim("test-pvc"), pvcResourceType))

			if _, ok := got[keyPVCIsNotMounted]; ok {
				t.Errorf("_context.%s = %v, want the fact left out",
					keyPVCIsNotMounted, got[keyPVCIsNotMounted])
			}

			if _, ok := got[keyPVCIsNotReferenced]; ok {
				t.Errorf("_context.%s = %v, want the fact left out",
					keyPVCIsNotReferenced, got[keyPVCIsNotReferenced])
			}
		})
	}
}

// TestContextForLooksUpNothingForOtherTypes keeps the five listings for the one
// type they say anything about.
func TestContextForLooksUpNothingForOtherTypes(t *testing.T) {
	typed := fake.NewSimpleClientset()

	var listed []string
	typed.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		listed = append(listed, action.GetResource().Resource)
		return false, nil, nil
	})

	i := newTestInspector(typed, nil)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}
	got := i.contextFor(context.Background(), mustTarget(t, pod, podResourceType))

	if len(got) != 0 {
		t.Errorf("contextFor() = %v, want no facts", got)
	}

	if len(listed) != 0 {
		t.Errorf("contextFor() listed %v, want nothing read", listed)
	}
}

// TestContextForRunsTheHook covers the facts a Configuration's hook supplies.
func TestContextForRunsTheHook(t *testing.T) {
	hook := func(resource interface{}, cache map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"test_value": "test"}
	}

	i := newTestInspector(fake.NewSimpleClientset(), hook)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}
	got := i.contextFor(context.Background(), mustTarget(t, pod, podResourceType))

	if got["test_value"] != "test" {
		t.Errorf("_context.test_value = %v, want %q", got["test_value"], "test")
	}
}

// TestContextForCarriesTheHookCacheAcrossTargets covers the cache being the
// run's, not one Target's: a hook that answers once answers the same for every
// Target after it.
func TestContextForCarriesTheHookCacheAcrossTargets(t *testing.T) {
	calls := 0
	hook := func(resource interface{}, cache map[string]interface{}) map[string]interface{} {
		if held, ok := cache["rolled"]; ok {
			return map[string]interface{}{"rolled": held}
		}

		calls++
		cache["rolled"] = calls

		return map[string]interface{}{"rolled": calls}
	}

	i := newTestInspector(fake.NewSimpleClientset(), hook)

	for _, name := range []string{"first", "second"} {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}

		got := i.contextFor(context.Background(), mustTarget(t, pod, podResourceType))
		if got["rolled"] != 1 {
			t.Errorf("_context.rolled for %s = %v, want 1", name, got["rolled"])
		}
	}

	if calls != 1 {
		t.Errorf("the hook worked %d times, want 1", calls)
	}
}

// TestContextForIgnoresAClaimLookalike keeps a custom resource that happens to
// be called "persistentvolumeclaims" in a group of its own from being read as
// the built-in type.
func TestContextForIgnoresAClaimLookalike(t *testing.T) {
	lookalike := ResourceType{
		Group: "example.com", Version: "v1", Kind: "PersistentVolumeClaim",
		Plural: pvcPlural, Namespaced: true,
	}

	typed := fake.NewSimpleClientset()

	listed := false
	typed.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		listed = true
		return false, nil, nil
	})

	i := newTestInspector(typed, nil)

	got := i.contextFor(context.Background(),
		mustTarget(t, claim("test-pvc"), lookalike))

	if len(got) != 0 {
		t.Errorf("contextFor() = %v, want no facts", got)
	}

	if listed {
		t.Error("contextFor() read the cluster for a type that is not the built-in claim")
	}
}
