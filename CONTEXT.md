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

**Resource type**:
A kind as the cluster's discovery reports it, carrying the plural a Target is
listed and deleted through. A Target is built from the type it was listed as, so
the plural is never derived from the kind.
_Avoid_: Kind (one field of a Resource type), GVR, plural

**Listing**:
One list a run makes: every resource of a single Resource type, in one namespace,
or across the cluster for a type that has no namespace. A run reaches a Target
through exactly one Listing, which is what stops a resource being judged twice.
_Avoid_: Query, scan, batch, page

**Verdict**:
The conclusion about one Target: leave it alone, delete it, or warn that it will be
deleted. A Verdict carries the reason it was reached, so nothing downstream has to
re-derive it.
_Avoid_: Decision, judgement, result, outcome

**Rule**:
A named condition that assigns a TTL to the Targets it matches, for resources that
carry no annotation of their own.
_Avoid_: Policy, matcher, filter

**Configuration**:
The settings one process runs under: what it considers, what it does when a
Deadline passes, and the Rules it judges by. It is settled once, before the first
run, and never changes while the process lives.
_Avoid_: Options, settings, flags (flags are one of the things a Configuration is
built from, not what it is)

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

**Log line**:
One thing a run says it did, carrying a level — whether it is a diagnostic, the
ordinary course of the run, something worked around, or something that could not
be done. The Configuration names the shape a line is written in and the levels
worth writing; nothing that emits one decides whether it is wanted.
_Avoid_: Message (a Notification's wording is a message; a Log line is not),
output, trace

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
configured resource and namespace include and exclude lists. It plans the run's
Listings, naming each one once, and is the only thing that judges scope.
Namespaces are the one kind it considers without cluster resources being
included.
_Avoid_: Filter, matcher, scope

**Connect**:
The module that resolves the ambient credentials and returns the Cluster they
grant. A run is handed a Cluster and never builds one, so the whole of a run can
be exercised against fakes.
_Avoid_: Client factory, bootstrap, initialise

**Load**:
The module that resolves the process's arguments and environment into a
Configuration. What it hands back is complete, so nothing downstream has a
half-built Configuration to finish. It is to the Configuration what Connect is
to the Cluster.
_Avoid_: Parse, init, setup
