package janitor

const (
	// Annotation keys
	TTLAnnotation      = "janitor/ttl"
	ExpiryAnnotation   = "janitor/expires"
	NotifiedAnnotation = "janitor/notified"

	// Special TTL value
	TTLUnlimited = "forever"
)
