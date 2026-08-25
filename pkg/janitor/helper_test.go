package janitor

import (
	"testing"
	"time"
)

func TestParseTTL(t *testing.T) {
	tests := []struct {
		name     string
		ttl      string
		expected time.Duration
		wantErr  bool
	}{
		{
			name:     "valid seconds",
			ttl:      "60s",
			expected: 60 * time.Second,
			wantErr:  false,
		},
		{
			name:     "valid minutes",
			ttl:      "5m",
			expected: 5 * time.Minute,
			wantErr:  false,
		},
		{
			name:     "valid hours",
			ttl:      "24h",
			expected: 24 * time.Hour,
			wantErr:  false,
		},
		{
			name:     "valid days",
			ttl:      "7d",
			expected: 7 * 24 * time.Hour,
			wantErr:  false,
		},
		{
			name:     "valid weeks",
			ttl:      "2w",
			expected: 2 * 7 * 24 * time.Hour,
			wantErr:  false,
		},
		{
			name:     "unlimited TTL",
			ttl:      "forever",
			expected: -1,
			wantErr:  false,
		},
		{
			name:    "invalid format",
			ttl:     "invalid",
			wantErr: true,
		},
		{
			name:    "invalid unit",
			ttl:     "60x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTTL(tt.ttl)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTTL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && got != tt.expected {
				t.Errorf("parseTTL() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseExpiry(t *testing.T) {
	tests := []struct {
		name    string
		expiry  string
		wantErr bool
	}{
		{
			name:    "RFC3339 format",
			expiry:  "2025-07-21T10:30:00Z",
			wantErr: false,
		},
		{
			name:    "RFC3339 format with timezone",
			expiry:  "2025-07-21T10:30:00-07:00",
			wantErr: false,
		},
		{
			name:    "datetime without timezone",
			expiry:  "2025-07-21T10:30",
			wantErr: false,
		},
		{
			name:    "date only format",
			expiry:  "2025-07-21",
			wantErr: false,
		},
		{
			name:    "invalid format",
			expiry:  "invalid-date",
			wantErr: true,
		},
		{
			name:    "empty string",
			expiry:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseExpiry(tt.expiry)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseExpiry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && got.IsZero() {
				t.Errorf("parseExpiry() returned zero time for valid input %q", tt.expiry)
			}
		})
	}
}
