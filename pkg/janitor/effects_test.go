package janitor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

var podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// effectsFixture is a janitor wired to fake clients, holding one pod.
type effectsFixture struct {
	janitor *Janitor
	target  Target
}

func newEffectsFixture(t *testing.T, cfg *Config) effectsFixture {
	t.Helper()

	obj := podObject(time.Hour, map[string]string{TTLAnnotation: "1h"})

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{podGVR: "PodList"},
		obj,
	)

	return effectsFixture{
		janitor: &Janitor{
			client:        fake.NewSimpleClientset(),
			dynamicClient: dyn,
			config:        cfg,
			cache:         make(map[string]interface{}),
		},
		target: mustTarget(t, obj),
	}
}

func (f effectsFixture) events(t *testing.T) []string {
	t.Helper()

	list, err := f.janitor.client.CoreV1().Events("staging").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}

	var reasons []string
	for _, e := range list.Items {
		reasons = append(reasons, e.Reason)
	}
	return reasons
}

func (f effectsFixture) podExists(t *testing.T) bool {
	t.Helper()

	_, err := f.janitor.dynamicClient.Resource(podGVR).Namespace("staging").
		Get(context.Background(), "web", metav1.GetOptions{})
	return err == nil
}

func TestApplyDelete(t *testing.T) {
	f := newEffectsFixture(t, &Config{})

	verdict := Verdict{
		Action:      ActionDelete,
		Deadline:    now.Add(-time.Hour),
		Source:      "annotation " + TTLAnnotation,
		EventReason: "TTLExpired",
		Message:     "Pod staging/web expired and will be deleted",
	}

	if err := f.janitor.Apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 1 || got[0] != "TTLExpired" {
		t.Errorf("event reasons = %v, want [TTLExpired]", got)
	}
	if f.podExists(t) {
		t.Error("pod still exists, want it deleted")
	}
}

func TestApplyNone(t *testing.T) {
	f := newEffectsFixture(t, &Config{})

	if err := f.janitor.Apply(context.Background(), f.target, Verdict{Action: ActionNone}, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 0 {
		t.Errorf("event reasons = %v, want none", got)
	}
	if !f.podExists(t) {
		t.Error("pod was deleted, want it left alone")
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	f := newEffectsFixture(t, &Config{DryRun: true})

	verdict := Verdict{
		Action:      ActionDelete,
		EventReason: "TTLExpired",
		Message:     "Pod staging/web expired and will be deleted",
	}

	if err := f.janitor.Apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 0 {
		t.Errorf("event reasons = %v, want none in dry run", got)
	}
	if !f.podExists(t) {
		t.Error("pod was deleted in dry run, want it left alone")
	}
}

// The event carries the instant the verdict was reached, not the instant the
// write happened.
func TestApplyStampsEventsWithTheDecisionTime(t *testing.T) {
	f := newEffectsFixture(t, &Config{})

	verdict := Verdict{Action: ActionDelete, EventReason: "TTLExpired", Message: "gone"}
	if err := f.janitor.Apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	list, err := f.janitor.client.CoreV1().Events("staging").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("got %d events, want 1", len(list.Items))
	}

	event := list.Items[0]
	if !event.FirstTimestamp.Time.Equal(now) {
		t.Errorf("FirstTimestamp = %v, want %v", event.FirstTimestamp.Time, now)
	}
	if !event.LastTimestamp.Time.Equal(now) {
		t.Errorf("LastTimestamp = %v, want %v", event.LastTimestamp.Time, now)
	}
	if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.UID != "pod-uid" {
		t.Errorf("InvolvedObject = %+v, want Pod/pod-uid", event.InvolvedObject)
	}
}

// webhookServer stands up a webhook endpoint, points WEBHOOK_URL at it, and
// returns the channel the received message lands on.
func webhookServer(t *testing.T) <-chan string {
	t.Helper()

	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("webhook method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)

		var payload WebhookMessage
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("webhook payload is not valid JSON: %v", err)
		}
		received <- payload.Message
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	t.Setenv("WEBHOOK_URL", server.URL)

	return received
}

// awaitWebhook returns the message the webhook received, or fails.
func awaitWebhook(t *testing.T, received <-chan string) string {
	t.Helper()

	select {
	case got := <-received:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called")
		return ""
	}
}

func TestApplyNotify(t *testing.T) {
	received := webhookServer(t)

	f := newEffectsFixture(t, &Config{DeleteNotification: 600})

	verdict := Verdict{
		Action:      ActionNotify,
		Deadline:    now.Add(time.Hour),
		Source:      "annotation " + TTLAnnotation,
		EventReason: "DeleteNotification",
		Message:     "Pod staging/web will be deleted at some point (TTL 1h)",
	}

	if err := f.janitor.Apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 1 || got[0] != "DeleteNotification" {
		t.Errorf("event reasons = %v, want [DeleteNotification]", got)
	}
	if !f.podExists(t) {
		t.Error("pod was deleted on a notify verdict, want it left alone")
	}

	if got := awaitWebhook(t, received); got != verdict.Message {
		t.Errorf("webhook message = %q, want %q", got, verdict.Message)
	}

	// Marks the target so the same run does not notify twice. This is not written
	// back to the cluster, so it does not survive to the next run.
	if !f.target.wasNotified() {
		t.Error("target is not marked notified")
	}
}

func TestApplyNotifyPrefixesContextName(t *testing.T) {
	received := webhookServer(t)
	t.Setenv("CONTEXT_NAME", "prod-eu")

	f := newEffectsFixture(t, &Config{DeleteNotification: 600})

	verdict := Verdict{
		Action:      ActionNotify,
		EventReason: "DeleteNotification",
		Message:     "Pod staging/web will be deleted",
	}

	if err := f.janitor.Apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got, want := awaitWebhook(t, received), "[prod-eu] Pod staging/web will be deleted"; got != want {
		t.Errorf("webhook message = %q, want %q", got, want)
	}
}

func TestApplyWaitsAfterDelete(t *testing.T) {
	f := newEffectsFixture(t, &Config{WaitAfterDelete: 1})

	verdict := Verdict{Action: ActionDelete, EventReason: "TTLExpired", Message: "gone"}

	start := time.Now()
	if err := f.janitor.Apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("Apply() returned after %v, want at least 1s", elapsed)
	}
}
