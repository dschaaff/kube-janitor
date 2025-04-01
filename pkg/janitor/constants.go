package janitor

const (
    // Annotation keys
    TTLAnnotation      = "janitor/ttl"
    ExpiryAnnotation   = "janitor/expires"
    NotifiedAnnotation = "janitor/notified"

    // Special TTL value
    TTLUnlimited = "forever"

    // Default values
    DefaultInterval          = 30
    DefaultExcludeResources = "events,controllerrevisions"
    DefaultExcludeNamespaces = "kube-system"
    DefaultParallelism      = 0 // 0 means use runtime.NumCPU()
)
