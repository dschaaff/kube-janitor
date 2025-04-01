package janitor

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
)

// WebhookMessage represents a message to be sent to a webhook
type WebhookMessage struct {
    Message string `json:"message"`
}

// WebhookClient interface for webhook notifications
type WebhookClient interface {
    Send(message WebhookMessage) error
}

// DefaultWebhookClient implements WebhookClient
type DefaultWebhookClient struct {
    URL string
}

func (c *DefaultWebhookClient) Send(message WebhookMessage) error {
    if c.URL == "" {
        return nil
    }

    jsonData, err := json.Marshal(message)
    if err != nil {
        return fmt.Errorf("failed to marshal webhook message: %v", err)
    }

    resp, err := http.Post(c.URL, "application/json", bytes.NewBuffer(jsonData))
    if err != nil {
        return fmt.Errorf("failed to send webhook request: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 300 {
        return fmt.Errorf("webhook request failed with status %d", resp.StatusCode)
    }

    return nil
}
