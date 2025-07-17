package janitor

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var (
	ttlPattern      = regexp.MustCompile(`^(\d+)([smhdw])$`)
	dateTimeFormats = []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02",
	}
)

// ParseTTL parses a TTL string into duration
func ParseTTL(ttl string) (time.Duration, error) {
	if ttl == TTLUnlimited {
		return -1, nil
	}

	matches := ttlPattern.FindStringSubmatch(ttl)
	if matches == nil {
		return 0, fmt.Errorf("TTL value %q does not match format (e.g. 60s, 5m, 8h, 7d, 2w)", ttl)
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, err
	}

	unit, exists := TimeUnit[matches[2]]
	if !exists {
		return 0, fmt.Errorf("unknown time unit %q for TTL %q", matches[2], ttl)
	}

	return time.Duration(value) * unit, nil
}

// ParseExpiry parses an expiry timestamp string
func ParseExpiry(expiry string) (time.Time, error) {
	for _, format := range dateTimeFormats {
		if t, err := time.Parse(format, expiry); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expiry value %q does not match any supported format", expiry)
}

// FormatDuration formats a duration in a human-readable format
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "-" + FormatDuration(-d)
	}

	var result string

	// Order matters: largest to smallest
	units := []struct {
		d time.Duration
		s string
	}{
		{7 * 24 * time.Hour, "w"},
		{24 * time.Hour, "d"},
		{time.Hour, "h"},
		{time.Minute, "m"},
		{time.Second, "s"},
	}

	remaining := d
	for _, unit := range units {
		if value := remaining / unit.d; value > 0 {
			result += fmt.Sprintf("%d%s", value, unit.s)
			remaining = remaining % unit.d
		}
	}

	if result == "" {
		return "0s"
	}
	return result
}
