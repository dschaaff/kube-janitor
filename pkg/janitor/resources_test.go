package janitor

import (
    "testing"
)

func TestGetResourceTypes(t *testing.T) {
    // Skip this test as the fake client doesn't properly support the discovery API
    t.Skip("Skipping as fake client doesn't properly support discovery API")
}
