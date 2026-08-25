package janitor

import (
	"time"
)

// ResourceContext holds context information for a resource
type ResourceContext struct {
	PVCIsNotMounted    bool
	PVCIsNotReferenced bool
}

// timeUnit represents supported time units for TTL
var timeUnit = map[string]time.Duration{
	"s": time.Second,
	"m": time.Minute,
	"h": time.Hour,
	"d": 24 * time.Hour,
	"w": 7 * 24 * time.Hour,
}
