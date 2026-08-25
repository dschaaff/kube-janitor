package janitor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// effectsFixture is a janitor wired to fake clients and a recording Notifier,
// holding one pod.
type effectsFixture struct {
	janitor  *Janitor
	target   Target
	notifier *recordingNotifier
	logs     *bytes.Buffer
}

func newEffectsFixture(t *testing.T, cfg *Config) effectsFixture {
	t.Helper()

	notifier := &recordingNotifier{}
	logs := &bytes.Buffer{}
	obj := podObject("staging", "web", time.Hour, map[string]string{TTLAnnotation: "1h"})

	return effectsFixture{
		janitor: New(cfg, Cluster{
			Typed:   fake.NewSimpleClientset(),
			Dynamic: dynamicClientFor([]ResourceType{podResourceType}, obj),
		}, NewLogger(cfg, logs), notifier),
		target:   mustTarget(t, obj, podResourceType),
		notifier: notifier,
		logs:     logs,
	}
}

// exists reports whether the fixture's pod is still in the cluster.
func (f effectsFixture) exists(t *testing.T) bool {
	t.Helper()

	return resourceExists(t, f.janitor, podResourceType, f.target.Namespace, f.target.Name)
}

func (f effectsFixture) events(t *testing.T) []string {
	t.Helper()

	list, err := f.janitor.cluster.Typed.CoreV1().Events("staging").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}

	var reasons []string
	for _, e := range list.Items {
		reasons = append(reasons, e.Reason)
	}
	return reasons
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

	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 1 || got[0] != "TTLExpired" {
		t.Errorf("event reasons = %v, want [TTLExpired]", got)
	}
	if f.exists(t) {
		t.Error("pod still exists, want it deleted")
	}
}

func TestApplyNone(t *testing.T) {
	f := newEffectsFixture(t, &Config{})

	if err := f.janitor.apply(context.Background(), f.target, Verdict{Action: ActionNone}, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 0 {
		t.Errorf("event reasons = %v, want none", got)
	}
	if !f.exists(t) {
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

	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 0 {
		t.Errorf("event reasons = %v, want none in dry run", got)
	}
	if !f.exists(t) {
		t.Error("pod was deleted in dry run, want it left alone")
	}
}

// The event carries the instant the verdict was reached, not the instant the
// write happened.
func TestApplyStampsEventsWithTheDecisionTime(t *testing.T) {
	f := newEffectsFixture(t, &Config{})

	verdict := Verdict{Action: ActionDelete, EventReason: "TTLExpired", Message: "gone"}
	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	list, err := f.janitor.cluster.Typed.CoreV1().Events("staging").List(context.Background(), metav1.ListOptions{})
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
	if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.UID != f.target.UID {
		t.Errorf("InvolvedObject = %+v, want Pod/%s", event.InvolvedObject, f.target.UID)
	}
}

func TestApplyNotify(t *testing.T) {
	f := newEffectsFixture(t, &Config{DeleteNotification: 600})

	verdict := Verdict{
		Action:      ActionNotify,
		Deadline:    now.Add(time.Hour),
		Source:      "annotation " + TTLAnnotation,
		EventReason: "DeleteNotification",
		Message:     "Pod staging/web will be deleted at some point (TTL 1h)",
	}

	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if got := f.events(t); len(got) != 1 || got[0] != "DeleteNotification" {
		t.Errorf("event reasons = %v, want [DeleteNotification]", got)
	}
	if !f.exists(t) {
		t.Error("pod was deleted on a notify verdict, want it left alone")
	}

	if got := f.notifier.messages; len(got) != 1 || got[0] != verdict.Message {
		t.Errorf("delivered = %q, want [%q]", got, verdict.Message)
	}

	// Marks the target so the same run does not notify twice. This is not written
	// back to the cluster, so it does not survive to the next run.
	if !f.target.wasNotified() {
		t.Error("target is not marked notified")
	}
}

// Apply reports the wording the Verdict carries, to both the event and the
// delivery, without rewording either.
func TestApplyNotifyReportsOneWording(t *testing.T) {
	f := newEffectsFixture(t, &Config{DeleteNotification: 600})

	verdict := Verdict{
		Action:      ActionNotify,
		EventReason: "DeleteNotification",
		Message:     "[prod-eu] Pod staging/web will be deleted",
	}

	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if got := f.notifier.messages; len(got) != 1 || got[0] != verdict.Message {
		t.Errorf("delivered = %q, want [%q]", got, verdict.Message)
	}

	recorded := events(t, f.janitor)
	if len(recorded) != 1 || recorded[0].Message != verdict.Message {
		t.Fatalf("events = %v, want one saying %q", recorded, verdict.Message)
	}
}

// A webhook that will not take a Notification is reported, but the run carries
// on: the event recording the same warning is already in the cluster.
func TestApplyNotifySurvivesDeliveryFailure(t *testing.T) {
	f := newEffectsFixture(t, &Config{DeleteNotification: 600})
	f.notifier.err = errors.New("webhook is down")

	verdict := Verdict{
		Action:      ActionNotify,
		EventReason: "DeleteNotification",
		Message:     "Pod staging/web will be deleted",
	}

	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v, want the run to carry on", err)
	}

	if got := f.notifier.messages; len(got) != 1 {
		t.Errorf("delivery attempts = %d, want 1", len(got))
	}
	if got := f.events(t); len(got) != 1 || got[0] != "DeleteNotification" {
		t.Errorf("event reasons = %v, want [DeleteNotification]", got)
	}
	if !f.target.wasNotified() {
		t.Error("target is not marked notified")
	}
	if !strings.Contains(f.logs.String(), "webhook is down") {
		t.Errorf("run reported %q, want the failed delivery reported", f.logs.String())
	}
}

// Nothing leaves the process in a dry run, whatever the Notifier would do.
func TestApplyNotifyDeliversNothingInDryRun(t *testing.T) {
	f := newEffectsFixture(t, &Config{DeleteNotification: 600, DryRun: true})

	verdict := Verdict{
		Action:      ActionNotify,
		EventReason: "DeleteNotification",
		Message:     "Pod staging/web will be deleted",
	}

	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if got := f.notifier.messages; len(got) != 0 {
		t.Errorf("delivered = %q, want nothing in a dry run", got)
	}
	if got := f.events(t); len(got) != 0 {
		t.Errorf("event reasons = %v, want none in a dry run", got)
	}
}

func TestApplyWaitsAfterDelete(t *testing.T) {
	f := newEffectsFixture(t, &Config{WaitAfterDelete: 1})

	verdict := Verdict{Action: ActionDelete, EventReason: "TTLExpired", Message: "gone"}

	start := time.Now()
	if err := f.janitor.apply(context.Background(), f.target, verdict, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("apply() returned after %v, want at least 1s", elapsed)
	}
}
