package janitor

import (
	"fmt"
	"time"
)

// Action is what a Verdict concludes should happen to a Target.
type Action int

const (
	// ActionNone leaves the target alone.
	ActionNone Action = iota
	// ActionDelete deletes the target.
	ActionDelete
	// ActionNotify warns that the target will be deleted.
	ActionNotify
)

// Wording that never varies, folded by the compiler rather than formatted per
// resource.
const (
	sourceExpiryAnnotation = "annotation " + ExpiryAnnotation
	sourceTTLAnnotation    = "annotation " + TTLAnnotation
	expiryDetail           = "annotation " + ExpiryAnnotation + " is set"
)

func (a Action) String() string {
	switch a {
	case ActionDelete:
		return "delete"
	case ActionNotify:
		return "notify"
	default:
		return "none"
	}
}

// Verdict is the conclusion about one Target.
type Verdict struct {
	Action Action

	// Deadline is the moment the target becomes eligible for deletion. Zero when
	// no source supplied one.
	Deadline time.Time

	// Source names where the Deadline came from, for logging.
	Source string

	// EventReason is recorded as the Reason of the Kubernetes event.
	EventReason string

	// Message describes the action in full, without the context name prefix that
	// notifications carry.
	Message string
}

// Decide works out what should happen to a target.
//
// A target has at most one Deadline, taken from the first source that supplies
// one: the expiry annotation, then the TTL annotation, then the first matching
// rule. See docs/adr/0001-deadline-precedence.md.
//
// resourceContext is only called if rule evaluation is reached, because building
// it costs several cluster reads. It may be nil.
func Decide(t Target, cfg *Config, now time.Time, resourceContext func() map[string]interface{}) (Verdict, error) {
	if expiry, ok := t.Annotations[ExpiryAnnotation]; ok {
		return decideFromExpiry(t, cfg, now, expiry)
	}

	if ttl, ok := t.Annotations[TTLAnnotation]; ok {
		return decideFromTTL(t, cfg, now, ttl)
	}

	return decideFromRules(t, cfg, now, resourceContext)
}

func decideFromExpiry(t Target, cfg *Config, now time.Time, expiry string) (Verdict, error) {
	deadline, err := ParseExpiry(expiry)
	if err != nil {
		return Verdict{}, fmt.Errorf("invalid expiry value: %v", err)
	}

	return conclude(t, cfg, now, deadlineSource{
		deadline:    deadline,
		source:      sourceExpiryAnnotation,
		eventReason: "ExpiryTimeReached",
		// The expiry message quotes the annotation value, not the parsed deadline.
		expiredOn: expiry,
	}), nil
}

func decideFromTTL(t Target, cfg *Config, now time.Time, ttl string) (Verdict, error) {
	lifetime, err := ParseTTL(ttl)
	if err != nil {
		return Verdict{}, fmt.Errorf("invalid TTL value: %v", err)
	}

	// An unlimited TTL supplies no deadline, and stops rules being considered.
	if lifetime < 0 {
		return Verdict{Source: sourceTTLAnnotation}, nil
	}

	from := deploymentTime(t, cfg)

	return conclude(t, cfg, now, deadlineSource{
		deadline:    from.Add(lifetime),
		source:      sourceTTLAnnotation,
		eventReason: "TTLExpired",
		from:        from,
		ttl:         ttl,
	}), nil
}

func decideFromRules(t Target, cfg *Config, now time.Time, resourceContext func() map[string]interface{}) (Verdict, error) {
	if len(cfg.Rules) == 0 {
		return Verdict{}, nil
	}

	var context map[string]interface{}
	if resourceContext != nil {
		context = resourceContext()
	}

	for _, rule := range cfg.Rules {
		if !rule.Matches(t, context) {
			continue
		}

		lifetime, err := ParseTTL(rule.TTL)
		if err != nil {
			return Verdict{}, fmt.Errorf("invalid TTL in rule %s: %v", rule.ID, err)
		}

		// A matching rule with an unlimited TTL is passed over, and rules after it
		// still get their chance.
		if lifetime < 0 {
			continue
		}

		from := deploymentTime(t, cfg)

		return conclude(t, cfg, now, deadlineSource{
			deadline:    from.Add(lifetime),
			source:      "rule " + rule.ID,
			eventReason: "RuleTTLExpired",
			from:        from,
			ttl:         rule.TTL,
			ruleID:      rule.ID,
		}), nil
	}

	return Verdict{}, nil
}

// deadlineSource is one resolved deadline and the ingredients for its wording.
type deadlineSource struct {
	deadline    time.Time
	source      string
	eventReason string

	// from and ttl describe a relative deadline, and ruleID names the rule that
	// set it. An empty ttl means the deadline came from the expiry annotation,
	// whose wording quotes the raw annotation value held in expiredOn.
	from      time.Time
	ttl       string
	ruleID    string
	expiredOn string
}

// detail is the parenthesised fragment both messages quote.
func (s deadlineSource) detail() string {
	switch {
	case s.ruleID != "":
		return fmt.Sprintf("rule %s, TTL %s from %s", s.ruleID, s.ttl, s.from.Format(time.RFC3339))
	case s.ttl != "":
		return fmt.Sprintf("TTL %s from %s", s.ttl, s.from.Format(time.RFC3339))
	default:
		return expiryDetail
	}
}

// expiredAt is what the delete message says the target expired on.
func (s deadlineSource) expiredAt() string {
	if s.expiredOn != "" {
		return s.expiredOn
	}
	return s.deadline.Format(time.RFC3339)
}

// conclude turns a resolved deadline into a verdict by comparing it to now.
//
// Wording is built only for a verdict that acts, so the common case of a
// deadline still in the future formats nothing.
func conclude(t Target, cfg *Config, now time.Time, s deadlineSource) Verdict {
	if now.After(s.deadline) {
		return Verdict{
			Action:      ActionDelete,
			Deadline:    s.deadline,
			Source:      s.source,
			EventReason: s.eventReason,
			Message: fmt.Sprintf("%s expired on %s and will be deleted (%s)",
				t.describe(), s.expiredAt(), s.detail()),
		}
	}

	if cfg.DeleteNotification > 0 && !t.wasNotified() {
		notifyFrom := s.deadline.Add(-time.Duration(cfg.DeleteNotification) * time.Second)
		if now.After(notifyFrom) {
			return Verdict{
				Action:      ActionNotify,
				Deadline:    s.deadline,
				Source:      s.source,
				EventReason: "DeleteNotification",
				Message: fmt.Sprintf("%s will be deleted at %s (%s)",
					t.describe(), s.deadline.Format(time.RFC3339), s.detail()),
			}
		}
	}

	return Verdict{Deadline: s.deadline, Source: s.source}
}

// deploymentTime returns the moment a target's lifetime counts from: the
// configured deployment time annotation when present and parseable, and the
// creation timestamp otherwise.
func deploymentTime(t Target, cfg *Config) time.Time {
	if cfg.DeploymentTimeAnnotation != "" {
		if raw, ok := t.Annotations[cfg.DeploymentTimeAnnotation]; ok {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				return parsed
			}
		}
	}

	return t.CreatedAt
}
