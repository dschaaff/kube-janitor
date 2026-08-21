# kube-janitor

kube-janitor deletes Kubernetes resources once they are no longer wanted. Each run
considers the resources in a cluster, works out which ones have passed their
deadline, and deletes them — optionally warning first.

## Language

### Resources and decisions

**Target**:
A single Kubernetes resource that the current run is considering, together with
everything needed to judge and act on it.
_Avoid_: Object, item, candidate, subject

**Deadline**:
The moment a Target becomes eligible for deletion. Every Target has at most one,
derived from the first of its expiry annotation, its TTL annotation, or a matching
Rule.
_Avoid_: Expiry time, cutoff, TTL (a TTL produces a Deadline; it is not one)

**Deployment time**:
The moment a Target's lifetime starts counting from when a TTL applies. Taken from
the configured deployment-time annotation when present, and from the resource's
creation timestamp otherwise.
_Avoid_: Start time, age, created at

**Verdict**:
The conclusion about one Target: leave it alone, delete it, or warn that it will be
deleted. A Verdict carries the reason it was reached, so nothing downstream has to
re-derive it.
_Avoid_: Decision, judgement, result, outcome

**Rule**:
A named condition that assigns a TTL to the Targets it matches, for resources that
carry no annotation of their own.
_Avoid_: Policy, matcher, filter

**Resource context**:
Facts about a Target that cannot be read from the resource itself and must be
looked up in the cluster, such as whether a volume claim is mounted. Rules may
test it.
_Avoid_: Metadata, enrichment, extra data

**Cluster**:
The connections one run works through: the typed client for the built-in kinds
Resource context and events need, and the dynamic client for the arbitrary kinds
a run lists and deletes. Both are resolved from one set of credentials.
_Avoid_: Client, clientset, connection pool

**Notification**:
A warning that a Target is about to be deleted, sent ahead of its Deadline.
_Avoid_: Alert, message, event (a Notification may be recorded as a Kubernetes
event, but the two are not the same thing)

### Modules

**Decide**:
The module that produces a Verdict for a Target. It reaches its conclusion from
the Target, the configuration, and the current time — nothing else.
_Avoid_: Evaluate, judge, check

**Apply**:
The module that carries out a Verdict against the cluster: recording an event,
deleting the resource, sending a Notification.
_Avoid_: Execute, enact, perform, handle

**Selector**:
The module that decides which resources a run considers at all, from the
configured resource and namespace include and exclude lists.
_Avoid_: Filter, matcher, scope

**Connect**:
The module that resolves the ambient credentials and returns the Cluster they
grant. A run is handed a Cluster and never builds one, so the whole of a run can
be exercised against fakes.
_Avoid_: Client factory, bootstrap, initialise
