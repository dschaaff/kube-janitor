package janitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// recordingNotifier keeps what a run delivered, so a case can read it back
// without standing up a server. It is the second adapter the seam exists for.
type recordingNotifier struct {
	messages []string
	err      error
}

// Notify records the attempt before reporting any configured failure, so a case
// about a failing delivery can still show that delivery was attempted.
func (n *recordingNotifier) Notify(_ context.Context, message string) error {
	n.messages = append(n.messages, message)
	return n.err
}

// webhookFor stands up a webhook endpoint and returns a Notifier pointed at it,
// along with the messages it receives.
func webhookFor(t *testing.T, status int) (Notifier, func() []string) {
	t.Helper()

	var received []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("webhook method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var payload webhookMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("webhook payload is not valid JSON: %v", err)
		}
		received = append(received, payload.Message)

		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)

	return NewNotifier(&Config{WebhookURL: server.URL}), func() []string { return received }
}

func TestNewNotifierDeliversToTheConfiguredWebhook(t *testing.T) {
	notifier, received := webhookFor(t, http.StatusOK)

	if err := notifier.Notify(context.Background(), "Pod staging/web will be deleted"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	if got := received(); len(got) != 1 || got[0] != "Pod staging/web will be deleted" {
		t.Errorf("webhook received %q, want the one message", got)
	}
}

// A configuration naming no webhook gets a Notifier that delivers nowhere,
// rather than a nil one every caller would have to check.
func TestNewNotifierDeliversNowhereWithoutAWebhook(t *testing.T) {
	if err := NewNotifier(&Config{}).Notify(context.Background(), "anything"); err != nil {
		t.Errorf("Notify() error = %v, want nil", err)
	}
}

func TestWebhookNotifierReportsAFailureStatus(t *testing.T) {
	notifier, _ := webhookFor(t, http.StatusInternalServerError)

	if err := notifier.Notify(context.Background(), "message"); err == nil {
		t.Error("Notify() error = nil, want the failure status reported")
	}
}

func TestWebhookNotifierReportsAnUnreachableWebhook(t *testing.T) {
	notifier := NewNotifier(&Config{WebhookURL: "not-a-url"})

	if err := notifier.Notify(context.Background(), "message"); err == nil {
		t.Error("Notify() error = nil, want an unreachable webhook reported")
	}
}

// The run's context governs the request, so a shutdown is not held up by a
// webhook that never answers.
func TestWebhookNotifierHonoursTheRunsContext(t *testing.T) {
	notifier, _ := webhookFor(t, http.StatusOK)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := notifier.Notify(ctx, "message"); err == nil {
		t.Error("Notify() error = nil, want the cancelled context reported")
	}
}

// A webhook that answers and then never stops talking must not hold up the
// Targets still to be judged: the answer is read up to a bound and no further.
//
// The request carries a deadline of its own so that a run which does read
// without a bound still ends — failing here rather than hanging the suite.
func TestWebhookNotifierGivesUpOnAnEndlessAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		for r.Context().Err() == nil {
			if _, err := w.Write(make([]byte, 4096)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- NewNotifier(&Config{WebhookURL: server.URL}).Notify(ctx, "message")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Notify() error = %v, want the success status reported", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Notify() has not returned: the answer is being read without a bound")
		<-done
	}
}

// A webhook that accepts the connection and then says nothing is given up on,
// rather than holding up the Targets still to be judged. The bound is injected
// here so the case costs milliseconds; that the real one is set at all is what
// TestNewNotifierBoundsEveryDelivery pins.
func TestWebhookNotifierGivesUpOnASilentWebhook(t *testing.T) {
	stalled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-stalled
	}))
	t.Cleanup(func() { close(stalled); server.Close() })

	notifier := webhookNotifier{url: server.URL, client: &http.Client{Timeout: 50 * time.Millisecond}}

	done := make(chan error, 1)
	go func() { done <- notifier.Notify(context.Background(), "message") }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Notify() error = nil, want the silent webhook given up on")
		}
	case <-time.After(5 * time.Second):
		t.Error("Notify() has not returned: a silent webhook is not bounded")
		<-done
	}
}

// Every delivery a run makes is bounded, so no webhook can stall a run for
// longer than one timeout per Notification.
func TestNewNotifierBoundsEveryDelivery(t *testing.T) {
	notifier, ok := NewNotifier(&Config{WebhookURL: "https://hooks.test/x"}).(webhookNotifier)
	if !ok {
		t.Fatal("NewNotifier() with a webhook configured does not deliver over HTTP")
	}

	if notifier.client.Timeout != webhookTimeout {
		t.Errorf("client timeout = %v, want %v", notifier.client.Timeout, webhookTimeout)
	}
}
