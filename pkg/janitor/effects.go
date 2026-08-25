package janitor

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// apply carries out a Verdict against the cluster. It is the only place that
// writes: everything that decides what should happen lives behind Decide.
//
// now is the same instant the verdict was reached, so a resource's event
// timestamps line up with the decision that produced them.
func (j *Janitor) apply(ctx context.Context, t Target, v verdict, now time.Time) error {
	switch v.Action {
	case actionDelete:
		if err := j.createEvent(ctx, t, v.Message, v.EventReason, now); err != nil {
			return err
		}
		return j.deleteResource(ctx, t)

	case actionNotify:
		return j.notify(ctx, t, v, now)
	}

	return nil
}

// notify warns that a target is about to be deleted.
func (j *Janitor) notify(ctx context.Context, t Target, v verdict, now time.Time) error {
	if j.config.DryRun {
		j.log.Infof("**DRY-RUN**: Would send delete notification for %s", t.describe())
		j.log.Debugf("Notification: %s", v.Message)
		return nil
	}

	if err := j.createEvent(ctx, t, v.Message, v.EventReason, now); err != nil {
		return err
	}

	// A Notification that could not be delivered is worth reporting but is not
	// worth stopping the run for: the event recording the same warning is
	// already in the cluster.
	if err := j.notifier.Notify(ctx, v.Message); err != nil {
		j.log.Warnf("Failed to deliver notification for %s: %v", t.describe(), err)
	}

	// Flags the target as notified for the rest of this run only. Nothing writes
	// the annotation back to the cluster, so notifications re-fire on the next
	// run. Fixing that needs the "patch" verb, which deploy/rbac.yaml does not
	// grant.
	if t.Annotations != nil {
		t.Annotations[NotifiedAnnotation] = "yes"
	}

	return nil
}

// createEvent records a Kubernetes event against the target.
func (j *Janitor) createEvent(ctx context.Context, t Target, message, reason string, now time.Time) error {
	if j.config.DryRun {
		j.log.Infof("**DRY-RUN**: Would create event: %s", message)
		return nil
	}

	// Cluster-scoped resources have no namespace of their own to hold the event.
	eventNamespace := t.Namespace
	if eventNamespace == "" {
		eventNamespace = "default"
	}

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kube-janitor-",
			Namespace:    eventNamespace,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: t.APIVersion,
			Kind:       t.Kind,
			Name:       t.Name,
			Namespace:  t.Namespace,
			UID:        t.UID,
		},
		Reason:         reason,
		Message:        message,
		FirstTimestamp: metav1.NewTime(now),
		LastTimestamp:  metav1.NewTime(now),
		Count:          1,
		Type:           "Normal",
		Source: corev1.EventSource{
			Component: "kube-janitor",
		},
	}

	if _, err := j.cluster.Typed.CoreV1().Events(eventNamespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create event: %v", err)
	}

	return nil
}

func (j *Janitor) deleteResource(ctx context.Context, t Target) error {
	if j.config.DryRun {
		j.log.Infof("**DRY-RUN**: Would delete %s", t.describe())
		j.log.Debugf("Resource would be deleted with propagation policy: Background")
		return nil
	}

	policy := metav1.DeletePropagationBackground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &policy}

	var err error
	if t.Namespace != "" {
		j.log.Infof("Deleting namespaced resource %s/%s", t.Namespace, t.Name)
		err = j.cluster.Dynamic.Resource(t.GVR).Namespace(t.Namespace).Delete(ctx, t.Name, deleteOptions)
	} else {
		j.log.Infof("Deleting cluster-scoped resource %s", t.Name)
		err = j.cluster.Dynamic.Resource(t.GVR).Delete(ctx, t.Name, deleteOptions)
	}
	if err != nil {
		return fmt.Errorf("failed to delete resource: %v", err)
	}

	if j.config.WaitAfterDelete > 0 {
		j.log.Infof("Waiting %d seconds after delete", j.config.WaitAfterDelete)
		time.Sleep(time.Duration(j.config.WaitAfterDelete) * time.Second)
	}

	return nil
}
