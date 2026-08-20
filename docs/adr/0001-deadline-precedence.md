# A Target has at most one Deadline, and the first source wins

A resource can carry both `janitor/expires` and `janitor/ttl` while also matching a
Rule. Until now all of them were evaluated: the TTL path ran, then the expiry path
ran on the same resource, so a resource with two past deadlines produced two events,
two delete calls (the second failing) and a doubled counter. We decided a Target has
at most one Deadline, resolved from the first source that supplies one in the order
`janitor/expires` → `janitor/ttl` → matching Rule.

## Considered Options

Evaluating every source and deleting on the **earliest** deadline was the main
alternative. It is arguably more correct — the resource is genuinely eligible at the
earliest of its deadlines — but it means resolving every source on every resource,
including looking up Resource context for rules that a plain annotation would have
short-circuited. Returning a list of verdicts and applying all of them was rejected
because it preserves the double-delete.

Ordering expiry ahead of TTL reflects specificity: an absolute date written on one
object is a more deliberate statement than a relative TTL, and both are more
deliberate than a Rule that matches many resources at once.

## Consequences

The README describes the two annotations as an "or" and states no precedence, so no
documented behaviour changes. Resources carrying two past deadlines are now deleted
once rather than twice, and the spurious second delete error disappears from the
logs. A resource whose `janitor/expires` has not yet passed no longer has its TTL
considered at all — previously the TTL could delete it first.
