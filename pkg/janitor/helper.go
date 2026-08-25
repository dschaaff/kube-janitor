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

// parseTTL parses a TTL string into duration
func parseTTL(ttl string) (time.Duration, error) {
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

	unit, exists := timeUnit[matches[2]]
	if !exists {
		return 0, fmt.Errorf("unknown time unit %q for TTL %q", matches[2], ttl)
	}

	return time.Duration(value) * unit, nil
}

// parseExpiry parses an expiry timestamp string
func parseExpiry(expiry string) (time.Time, error) {
	for _, format := range dateTimeFormats {
		if t, err := time.Parse(format, expiry); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expiry value %q does not match any supported format", expiry)
}
