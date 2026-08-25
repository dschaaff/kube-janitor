package janitor

import (
	"context"
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/dschaaff/kube-janitor/pkg/janitor/hooks"
)

// pvcPlural is the plural persistent volume claims are listed through.
const pvcPlural = "persistentvolumeclaims"

// isPersistentVolumeClaims reports whether a group and plural name the built-in
// PersistentVolumeClaim type. Matching on the plural alone would also catch a
// custom resource that happens to be called "persistentvolumeclaims" in a group
// of its own, which has none of a claim's meaning and would cost five listings
// to find that out.
func isPersistentVolumeClaims(group, plural string) bool {
	return group == "" && plural == pvcPlural
}

// The keys a Resource context carries for a claim. Rules name them through
// _context, so they are part of what the janitor promises: see README.md.
const (
	keyPVCIsNotMounted    = "pvc_is_not_mounted"
	keyPVCIsNotReferenced = "pvc_is_not_referenced"
)

// inspector looks up a Target's Resource context: the facts about it that
// cannot be read from the resource itself.
//
// It is handed the typed client alone, because the kinds it reads are all
// built-in ones.
type inspector struct {
	typed kubernetes.Interface
	hook  hooks.ResourceContextHook
	log   *Logger

	// cache is whatever the hook wants to carry from one Target to the next. It
	// is built with the Janitor and never emptied, so a process that runs on an
	// interval carries it across every run, not just the first — which is what
	// a hook answering out of it answers the same thing forever.
	//
	// Nothing else reads it, and a run judges its Targets one after another, so
	// it needs no guard.
	cache map[string]interface{}
}

func newInspector(typed kubernetes.Interface, hook hooks.ResourceContextHook, log *Logger) *inspector {
	return &inspector{
		typed: typed,
		hook:  hook,
		log:   log,
		cache: make(map[string]interface{}),
	}
}

// contextFor looks up the facts a rule may test about the target.
//
// It answers with the facts alone. A lookup it cannot make is reported here and
// leaves that fact out, so a rule testing it simply does not match — which is
// why nothing that asks has an error to handle. Leaving the fact out rather
// than guessing at it matters: a claim whose workloads could not be listed is
// not a claim known to be unreferenced, and saying so would delete it.
func (i *inspector) contextFor(ctx context.Context, t Target) map[string]interface{} {
	facts := make(map[string]interface{})

	if isPersistentVolumeClaims(t.GVR.Group, t.plural()) {
		mounted, referenced, err := i.claimFacts(ctx, t)
		if err != nil {
			i.log.Warnf("failed to get context for %s: %v", t.describe(), err)
		} else {
			facts[keyPVCIsNotMounted] = !mounted
			facts[keyPVCIsNotReferenced] = !referenced
		}
	}

	if i.hook != nil {
		for k, v := range i.hook(t.Raw, i.cache) {
			facts[k] = v
		}
	}

	return facts
}

// claimFacts reports whether the claim is mounted by a pod and whether a
// workload references it. The two are asked independently: a claim can be
// mounted by a pod no workload owns, and referenced by a workload that has no
// pod running.
func (i *inspector) claimFacts(ctx context.Context, t Target) (mounted, referenced bool, err error) {
	mounted, err = i.mountedByPod(ctx, t.Namespace, t.Name)
	if err != nil {
		return false, false, err
	}

	referenced, err = i.referencedByWorkload(ctx, t.Namespace, t.Name)
	if err != nil {
		return false, false, err
	}

	return mounted, referenced, nil
}

func (i *inspector) mountedByPod(ctx context.Context, namespace, claim string) (bool, error) {
	pods, err := i.typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list pods: %v", err)
	}

	for _, pod := range pods.Items {
		if claimsVolume(pod.Spec.Volumes, claim) {
			i.log.Debugf("PVC %s/%s is mounted by pod %s", namespace, claim, pod.Name)
			return true, nil
		}
	}

	return false, nil
}

// referencedByWorkload asks each workload kind in turn and stops at the first
// that references the claim. A kind that cannot be listed stops the lookup
// rather than counting as no reference.
func (i *inspector) referencedByWorkload(ctx context.Context, namespace, claim string) (bool, error) {
	checks := []func(context.Context, string, string) (bool, error){
		i.referencedByStatefulSet,
		i.referencedByDeployment,
		i.referencedByJob,
		i.referencedByCronJob,
	}

	for _, check := range checks {
		referenced, err := check(ctx, namespace, claim)
		if err != nil {
			return false, err
		}
		if referenced {
			return true, nil
		}
	}

	return false, nil
}

// referencedByStatefulSet matches the names a StatefulSet's volume claim
// templates give the claims it creates: "<template>-<statefulset>-<ordinal>".
// The claims themselves are not owned by the set, so the name is all there is
// to go on.
func (i *inspector) referencedByStatefulSet(ctx context.Context, namespace, claim string) (bool, error) {
	sets, err := i.typed.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list statefulsets: %v", err)
	}

	for _, set := range sets.Items {
		for _, template := range set.Spec.VolumeClaimTemplates {
			pattern := fmt.Sprintf("^%s-%s-[0-9]+$",
				regexp.QuoteMeta(template.Name), regexp.QuoteMeta(set.Name))

			matched, err := regexp.MatchString(pattern, claim)
			if err != nil {
				return false, fmt.Errorf("failed to match claim name against %s: %v", set.Name, err)
			}

			if matched {
				i.log.Debugf("PVC %s/%s is referenced by StatefulSet %s", namespace, claim, set.Name)
				return true, nil
			}
		}
	}

	return false, nil
}

func (i *inspector) referencedByDeployment(ctx context.Context, namespace, claim string) (bool, error) {
	deployments, err := i.typed.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list deployments: %v", err)
	}

	for _, deployment := range deployments.Items {
		if claimsVolume(deployment.Spec.Template.Spec.Volumes, claim) {
			i.log.Debugf("PVC %s/%s is referenced by Deployment %s", namespace, claim, deployment.Name)
			return true, nil
		}
	}

	return false, nil
}

func (i *inspector) referencedByJob(ctx context.Context, namespace, claim string) (bool, error) {
	jobs, err := i.typed.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list jobs: %v", err)
	}

	for _, job := range jobs.Items {
		if claimsVolume(job.Spec.Template.Spec.Volumes, claim) {
			i.log.Debugf("PVC %s/%s is referenced by Job %s", namespace, claim, job.Name)
			return true, nil
		}
	}

	return false, nil
}

func (i *inspector) referencedByCronJob(ctx context.Context, namespace, claim string) (bool, error) {
	cronJobs, err := i.typed.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list cronjobs: %v", err)
	}

	for _, cronJob := range cronJobs.Items {
		if claimsVolume(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes, claim) {
			i.log.Debugf("PVC %s/%s is referenced by CronJob %s", namespace, claim, cronJob.Name)
			return true, nil
		}
	}

	return false, nil
}

// claimsVolume reports whether any of the volumes is backed by the named claim.
func claimsVolume(volumes []corev1.Volume, claim string) bool {
	for _, volume := range volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == claim {
			return true
		}
	}

	return false
}
