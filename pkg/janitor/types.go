package janitor

import (
    "time"
)



// ResourceContext holds context information for a resource
type ResourceContext struct {
    PVCIsNotMounted    bool
    PVCIsNotReferenced bool
    Cache              map[string]interface{}
}

// TimeUnit represents supported time units for TTL
var TimeUnit = map[string]time.Duration{
    "s": time.Second,
    "m": time.Minute,
    "h": time.Hour,
    "d": 24 * time.Hour,
    "w": 7 * 24 * time.Hour,
}
