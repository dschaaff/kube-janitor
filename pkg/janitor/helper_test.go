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
			got, err := ParseTTL(tt.ttl)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTTL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && got != tt.expected {
				t.Errorf("ParseTTL() = %v, want %v", got, tt.expected)
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
			got, err := ParseExpiry(tt.expiry)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseExpiry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && got.IsZero() {
				t.Errorf("ParseExpiry() returned zero time for valid input %q", tt.expiry)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			name:     "zero duration",
			duration: 0,
			want:     "0s",
		},
		{
			name:     "seconds only",
			duration: 45 * time.Second,
			want:     "45s",
		},
		{
			name:     "minutes and seconds",
			duration: 2*time.Minute + 30*time.Second,
			want:     "2m30s",
		},
		{
			name:     "hours minutes seconds",
			duration: time.Hour + 2*time.Minute + 30*time.Second,
			want:     "1h2m30s",
		},
		{
			name:     "negative duration",
			duration: -(time.Hour + 2*time.Minute + 30*time.Second),
			want:     "-1h2m30s",
		},
		{
			name:     "days",
			duration: 2 * 24 * time.Hour,
			want:     "2d",
		},
		{
			name:     "weeks",
			duration: 2 * 7 * 24 * time.Hour,
			want:     "2w",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatDuration(tt.duration); got != tt.want {
				t.Errorf("FormatDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}
