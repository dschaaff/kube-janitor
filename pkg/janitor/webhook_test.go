package janitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestSendWebhookNotification(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		webhookURL string
		serverResp int
		wantErr    bool
	}{
		{
			name:       "successful notification",
			message:    "test message",
			webhookURL: "http://webhook.test",
			serverResp: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "no webhook URL configured",
			message:    "test message",
			webhookURL: "",
			serverResp: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "webhook server error",
			message:    "test message",
			webhookURL: "http://webhook.test",
			serverResp: http.StatusInternalServerError,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request
				if r.Method != http.MethodPost {
					t.Errorf("Expected POST request, got %s", r.Method)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
				}

				// Verify payload
				var payload WebhookMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("Failed to decode request body: %v", err)
				}
				if payload.Message != tt.message {
					t.Errorf("Expected message %q, got %q", tt.message, payload.Message)
				}

				w.WriteHeader(tt.serverResp)
			}))
			defer server.Close()

			// Set webhook URL in environment
			if tt.webhookURL != "" {
				oldURL := os.Getenv("WEBHOOK_URL")
				os.Setenv("WEBHOOK_URL", server.URL)
				defer os.Setenv("WEBHOOK_URL", oldURL)
			}

			// Test webhook notification
			err := SendWebhookNotification(tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("SendWebhookNotification() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWebhookNotificationWithInvalidURL(t *testing.T) {
	oldURL := os.Getenv("WEBHOOK_URL")
	os.Setenv("WEBHOOK_URL", "invalid-url")
	defer os.Setenv("WEBHOOK_URL", oldURL)

	err := SendWebhookNotification("test message")
	if err == nil {
		t.Error("Expected error for invalid webhook URL, got nil")
	}
}
