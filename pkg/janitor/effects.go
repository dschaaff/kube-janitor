package janitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Apply carries out a Verdict against the cluster. It is the only place that
// writes: everything that decides what should happen lives behind Decide.
//
// now is the same instant the verdict was reached, so a resource's event
// timestamps line up with the decision that produced them.
func (j *Janitor) Apply(ctx context.Context, t Target, v Verdict, now time.Time) error {
	switch v.Action {
	case ActionDelete:
		if err := j.createEvent(ctx, t, v.Message, v.EventReason, now); err != nil {
			return err
		}
		return j.deleteResource(ctx, t)

	case ActionNotify:
		return j.notify(ctx, t, v, now)
	}

	return nil
}

// notify warns that a target is about to be deleted.
func (j *Janitor) notify(ctx context.Context, t Target, v Verdict, now time.Time) error {
	if j.config.DryRun {
		log.Printf("**DRY-RUN**: Would send delete notification for %s", t.describe())
		j.debugLog("Notification: %s", v.Message)
		return nil
	}

	message := v.Message
	if contextName := os.Getenv("CONTEXT_NAME"); contextName != "" {
		message = "[" + contextName + "] " + message
	}

	if err := j.createEvent(ctx, t, message, v.EventReason, now); err != nil {
		return err
	}

	if err := SendWebhookNotification(message); err != nil {
		log.Printf("Failed to send webhook notification: %v", err)
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
		log.Printf("**DRY-RUN**: Would create event: %s", message)
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
		log.Printf("**DRY-RUN**: Would delete %s", t.describe())
		j.debugLog("Resource would be deleted with propagation policy: Background")
		return nil
	}

	policy := metav1.DeletePropagationBackground
	deleteOptions := metav1.DeleteOptions{PropagationPolicy: &policy}

	var err error
	if t.Namespace != "" {
		j.infoLog("Deleting namespaced resource %s/%s", t.Namespace, t.Name)
		err = j.cluster.Dynamic.Resource(t.GVR).Namespace(t.Namespace).Delete(ctx, t.Name, deleteOptions)
	} else {
		j.infoLog("Deleting cluster-scoped resource %s", t.Name)
		err = j.cluster.Dynamic.Resource(t.GVR).Delete(ctx, t.Name, deleteOptions)
	}
	if err != nil {
		return fmt.Errorf("failed to delete resource: %v", err)
	}

	if j.config.WaitAfterDelete > 0 {
		j.infoLog("Waiting %d seconds after delete", j.config.WaitAfterDelete)
		time.Sleep(time.Duration(j.config.WaitAfterDelete) * time.Second)
	}

	return nil
}

// SendWebhookNotification sends a notification to a webhook
func SendWebhookNotification(message string) error {
	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		return nil
	}

	payload := WebhookMessage{
		Message: message,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %v", err)
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to send webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-success status: %s", resp.Status)
	}

	return nil
}
