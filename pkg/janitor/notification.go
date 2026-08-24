package janitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Notifier delivers a Notification to somewhere outside the cluster.
//
// A run is handed one and never builds its own, so a run that configured no
// webhook delivers to nowhere and nothing along the way asks whether a webhook
// was configured.
type Notifier interface {
	Notify(ctx context.Context, message string) error
}

// NewNotifier returns the Notifier the Configuration asks for: the webhook it
// named, or one that delivers nowhere when it named none.
func NewNotifier(cfg *Config) Notifier {
	if cfg.WebhookURL == "" {
		return discardNotifier{}
	}

	return webhookNotifier{
		url:    cfg.WebhookURL,
		client: &http.Client{Timeout: webhookTimeout},
	}
}

// discardNotifier drops every Notification, for a run with nowhere to send one.
type discardNotifier struct{}

func (discardNotifier) Notify(context.Context, string) error { return nil }

// webhookTimeout bounds one delivery. A run judges its Targets one after
// another, so a webhook that accepts the connection and then says nothing would
// otherwise hold up every Target still to come for as long as the process runs.
const webhookTimeout = 10 * time.Second

// webhookNotifier posts a Notification to one URL as JSON.
type webhookNotifier struct {
	url    string
	client *http.Client
}

// drainLimit is how much of a webhook's answer is read before the connection is
// given up on. A webhook answers with a status and little or nothing else, so
// this is far more than a well-behaved one sends.
const drainLimit = 16 << 10

// webhookMessage is the body a webhook receives.
type webhookMessage struct {
	Message string `json:"message"`
}

// Notify posts the message and reports anything but a success status as an
// error. The run's context governs the request, so a webhook that never answers
// does not outlive the run that called it.
func (n webhookNotifier) Notify(ctx context.Context, message string) error {
	data, err := json.Marshal(webhookMessage{Message: message})
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to build webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %v", err)
	}
	defer resp.Body.Close()

	// Drained so the connection goes back to the pool: a run that notifies about
	// many Targets reuses one connection rather than opening one apiece. Bounded,
	// because a webhook that answers and then never stops talking would otherwise
	// hold up every Target still to be judged; past the bound the connection is
	// dropped instead of pooled, which costs a handshake and nothing else.
	_, _ = io.CopyN(io.Discard, resp.Body, drainLimit)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-success status: %s", resp.Status)
	}

	return nil
}
